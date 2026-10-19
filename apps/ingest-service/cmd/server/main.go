package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	commonkafka "github.com/sentinel/go-common/kafka"

	"github.com/sentinel/ingest-service/internal/config"
	"github.com/sentinel/ingest-service/internal/density"
	"github.com/sentinel/ingest-service/internal/dlq"
	"github.com/sentinel/ingest-service/internal/drivers"
	"github.com/sentinel/ingest-service/internal/feed"
	"github.com/sentinel/ingest-service/internal/health"
	kafkaproducer "github.com/sentinel/ingest-service/internal/kafka"
	"github.com/sentinel/ingest-service/internal/lease"
	"github.com/sentinel/ingest-service/internal/metrics"
	"github.com/sentinel/ingest-service/internal/mgmt"
	"github.com/sentinel/ingest-service/internal/outbound"
	"github.com/sentinel/ingest-service/internal/publish"
	"github.com/sentinel/ingest-service/internal/registry"
	_ "github.com/sentinel/ingest-service/internal/source/all" // registers transports, decoders and drivers
	"github.com/sentinel/ingest-service/internal/source/driver/stream"
)

// The coordinator must keep telling the supervisor which replicas listen;
// without it a listening connection would never find a home.
var _ feed.ListenerLister = (*lease.Coordinator)(nil)

func main() {
	logger, err := newLogger()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to initialize logger: %v\n", err)
		os.Exit(1)
	}
	defer logger.Sync()

	logger.Info("starting sentinel ingest service")

	cfg := config.Load()
	if err := cfg.Validate(); err != nil {
		logger.Fatal("invalid configuration", zap.Error(err))
	}
	if cfg.ServiceToken == "" {
		logger.Warn("INGEST_SERVICE_TOKEN is not set; the management API accepts unauthenticated callers (development only)")
	}
	logger.Info("configuration loaded",
		zap.String("kafka_brokers", cfg.KafkaBrokers),
		zap.String("observation_topic", cfg.KafkaObservationTopic),
		zap.String("http_addr", cfg.HTTPAddr),
		zap.String("replica_id", cfg.ReplicaID),
		zap.Bool("listener", cfg.Listener),
		zap.Bool("listener_shared", cfg.ListenerShared),
		zap.Int("publisher_shards", cfg.PublisherShards),
		zap.Int("frame_buffer", cfg.FrameBuffer),
		zap.Strings("secret_env_allow", cfg.SecretEnvAllow),
		zap.Strings("secret_file_dirs", cfg.SecretFileDirs),
	)
	stream.FrameBuffer = cfg.FrameBuffer
	secretPolicy := cfg.SecretPolicy()
	if !secretPolicy.Unrestricted() {
		stream.FileDirs = append([]string{}, secretPolicy.FileDirs...) // non-nil: restricted
	}

	promReg := prometheus.NewRegistry()
	promReg.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	m := metrics.New(promReg)

	producer, err := kafkaproducer.NewProducer(cfg.KafkaBrokers, logger, m)
	if err != nil {
		logger.Fatal("failed to create kafka producer", zap.Error(err))
	}
	defer producer.Close()

	// Observations: protobuf to observations.raw, dead letters to observations.dlq.
	publisher := publish.New(producer, m, logger, publish.Options{
		Topic:           cfg.KafkaObservationTopic,
		DeadLetterTopic: cfg.KafkaDeadLetterTopic,
		Shards:          cfg.PublisherShards,
		Buffer:          cfg.ObservationBuffer,
		DLQRatePerSec:   float64(cfg.DLQRatePerSec),
		ReplicaID:       cfg.ReplicaID,
	})
	publisher.Start()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Feed registry: desired state in Postgres, shared by every replica.
	reg, err := registry.Open(ctx, cfg.DatabaseURL, logger)
	if err != nil {
		logger.Fatal("failed to connect to postgres for the feed registry", zap.Error(err))
	}
	defer reg.Close()
	// db/schema is applied outside the service. Refuse to run against one older
	// than this code: a missing column otherwise fails quietly (the listener
	// replica never registers, for one).
	schemaCtx, schemaCancel := context.WithTimeout(ctx, 10*time.Second)
	err = reg.CheckSchema(schemaCtx)
	schemaCancel()
	if err != nil {
		logger.Fatal(schemaCheckMessage(err), zap.Error(err))
	}
	if err := reg.EnsureBuiltins(ctx, cfg.FeedsBootstrapEnabled); err != nil {
		logger.Fatal("failed to ensure built-in feeds", zap.Error(err))
	}

	catalog := drivers.Default()

	// Supervisor: runs the feeds this replica owns and reports status.
	supOpts := feed.Options{
		ReplicaID:      cfg.ReplicaID,
		RetryMin:       cfg.FeedRetryMin,
		RetryMax:       cfg.FeedRetryMax,
		StaleWarn:      cfg.FeedStaleWarn,
		StaleCritical:  cfg.FeedStaleCritical,
		RawMax:         cfg.RawPayloadMaxBytes,
		Listener:       cfg.Listener,
		ListenerHost:   cfg.ListenerHost,
		ListenerShared: cfg.ListenerShared,
		LeaseTick:      cfg.LeaseTick,
		RebalanceAfter: cfg.LeaseRebalance,
		SecretPolicy:   secretPolicy,
		Shapes:         outbound.NewShapeStore(reg.Pool()),
		Density:        density.New(producer, commonkafka.TopicDensityEvents).For,
		ConsumerFactory: func(groupID, clientID string) (outbound.Consumer, error) {
			return commonkafka.NewBatchConsumer(cfg.KafkaBrokers, groupID, clientID, "latest", commonkafka.BatchOptions{MaxMessages: 200, MaxWait: 200 * time.Millisecond, MaxRetries: 1}, logger.With(zap.String("group", groupID)))
		},
	}
	if cfg.LeaseEnabled {
		supOpts.Leaser = lease.New(reg.Pool(), cfg.ReplicaID, lease.Options{TTL: cfg.LeaseTTL, Listener: cfg.Listener, ListenerHost: cfg.ListenerHost, ListenerShared: cfg.ListenerShared})
		supOpts.Preferred = lease.Preferred
		logger.Info("feed leases enabled", zap.Duration("ttl", cfg.LeaseTTL), zap.Duration("tick", cfg.LeaseTick), zap.Duration("rebalance_after", cfg.LeaseRebalance))
	} else {
		logger.Warn("LEASE_ENABLED=false: this replica runs every enabled feed")
	}
	sup := feed.New(reg, publisher, logger, m, supOpts)
	// The listeners' counters, connections and senders live in their drivers'
	// health detail; this reads them when Prometheus scrapes.
	promReg.MustRegister(metrics.NewListenerCollector(sup))
	supervisorDone := make(chan struct{})
	go func() {
		defer close(supervisorDone)
		sup.Run(ctx)
	}()

	// HTTP: health and metrics (open) plus the management API (guarded).
	mux := http.NewServeMux()
	healthHandler := health.NewHandler(producer, promReg, logger)
	healthHandler.RegisterRoutes(mux)
	mgmtHandler := mgmt.NewHandler(reg, catalog, sup, logger)
	stopDeadLetters := serveDeadLetters(mgmtHandler, cfg, logger)
	defer stopDeadLetters()
	mgmtHandler.StaleAfter = 3 * cfg.LeaseTick * 2
	if cfg.LeaseEnabled {
		mgmtHandler.StaleAfter = cfg.LeaseTTL
	}
	reserved, err := registry.ParsePortSet(cfg.ReservedPorts)
	if err != nil {
		logger.Fatal("INGEST_RESERVED_PORTS", zap.Error(err))
	}
	mgmtHandler.Reserved = reserved
	mgmtHandler.UseSecretPolicy(secretPolicy)
	mgmtHandler.RegisterRoutes(mux, mgmt.Auth(mgmt.AuthOptions{ServiceToken: cfg.ServiceToken}))

	httpServer := &http.Server{
		Addr:         cfg.HTTPAddr,
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}
	httpErr := make(chan error, 1)
	go func() {
		logger.Info("http server starting", zap.String("addr", cfg.HTTPAddr))
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			httpErr <- err
		}
	}()

	logger.Info("sentinel ingest service started successfully", zap.Strings("drivers", driverIDs(catalog)))

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	select {
	case sig := <-sigCh:
		logger.Info("shutdown signal received", zap.String("signal", sig.String()))
	case err := <-httpErr:
		logger.Error("http server failed", zap.Error(err))
	}

	// Graceful shutdown, bounded by SHUTDOWN_TIMEOUT_SEC: readiness 503 →
	// supervisor stops drivers → publisher drains → producer flushes →
	// HTTP shutdown → close.
	logger.Info("initiating graceful shutdown", zap.Duration("timeout", cfg.ShutdownTimeout))
	deadline := time.Now().Add(cfg.ShutdownTimeout)
	healthHandler.SetDraining(true)
	cancel()
	select {
	case <-supervisorDone:
	case <-time.After(time.Until(deadline)):
		logger.Warn("feed supervisor did not stop in time")
	}
	// With Kafka unreachable nothing drains: the shards cannot hand their
	// observations over and a flush delivers none. So the publisher waits only
	// until the deadline and then gives up on what it still holds (and logs how
	// much), the HTTP server gets what is left of the deadline (at most 5 s), and
	// the producer's one flush happens in its close, for the time then left but
	// at least a second. The process thus ends at most about a second after
	// the deadline, inside the grace period the orchestrator allows.
	publisher.CloseWithin(time.Until(deadline))

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), min(5*time.Second, max(time.Until(deadline), 0)))
	defer shutdownCancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		logger.Error("http server shutdown error", zap.Error(err))
	}

	// The producer's deferred Close flushes for 10 s whatever the deadline; closing
	// it here makes that one a no-op.
	producer.CloseWithin(max(time.Until(deadline), time.Second))

	logger.Info("sentinel ingest service shut down gracefully")
}

// schemaCheckMessage is the fatal log message for a CheckSchema error. Only a
// *registry.SchemaError means the schema is older than this code. Any other
// error (a timeout, a permissions error, a dropped connection) means the check
// did not finish, and calling the schema out of date would send the operator
// to apply db/schema when the cause is elsewhere.
func schemaCheckMessage(err error) string {
	if errors.As(err, new(*registry.SchemaError)) {
		return "database schema is out of date"
	}
	return "checking the database schema failed"
}

// serveDeadLetters gives the management API a reader of observations.dlq for
// GET /feeds/{id}/deadletters, and returns what releases it. The reader makes
// its own Kafka consumer when the first request comes, so a Kafka that is down
// at startup does not stop the service. With DLQ_PEEK_MAX_SCAN=0 it gives the
// API none: the endpoint then answers 501, and nothing is created.
func serveDeadLetters(h *mgmt.Handler, cfg *config.Config, logger *zap.Logger) (stop func()) {
	if cfg.DLQPeekMaxScan <= 0 {
		logger.Info("dead-letter viewing is off", zap.Int("dlq_peek_max_scan", cfg.DLQPeekMaxScan))
		return func() {}
	}
	reader := dlq.NewKafkaReader(cfg.KafkaBrokers, cfg.KafkaDeadLetterTopic, cfg.DLQPeekMaxScan, logger)
	h.DeadLetters = reader
	return reader.Close
}

func driverIDs(c *drivers.Catalog) []string {
	var ids []string
	for _, d := range c.List() {
		ids = append(ids, d.ID)
	}
	return ids
}

// newLogger creates a production-ready structured logger with JSON output.
func newLogger() (*zap.Logger, error) {
	level := zapcore.InfoLevel
	if lvl := os.Getenv("LOG_LEVEL"); lvl != "" {
		if err := level.UnmarshalText([]byte(lvl)); err != nil {
			return nil, fmt.Errorf("invalid LOG_LEVEL %q: %w", lvl, err)
		}
	}

	cfg := zap.Config{
		Level:       zap.NewAtomicLevelAt(level),
		Development: false,
		Encoding:    "json",
		EncoderConfig: zapcore.EncoderConfig{
			TimeKey:        "ts",
			LevelKey:       "level",
			NameKey:        "logger",
			CallerKey:      "caller",
			MessageKey:     "msg",
			StacktraceKey:  "stacktrace",
			LineEnding:     zapcore.DefaultLineEnding,
			EncodeLevel:    zapcore.LowercaseLevelEncoder,
			EncodeTime:     zapcore.ISO8601TimeEncoder,
			EncodeDuration: zapcore.MillisDurationEncoder,
			EncodeCaller:   zapcore.ShortCallerEncoder,
		},
		OutputPaths:      []string{"stdout"},
		ErrorOutputPaths: []string{"stderr"},
	}

	return cfg.Build()
}
