package main

import (
	"context"
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

	"github.com/sentinel/ingest-service/internal/config"
	"github.com/sentinel/ingest-service/internal/drivers"
	"github.com/sentinel/ingest-service/internal/feed"
	"github.com/sentinel/ingest-service/internal/health"
	kafkaproducer "github.com/sentinel/ingest-service/internal/kafka"
	"github.com/sentinel/ingest-service/internal/lease"
	"github.com/sentinel/ingest-service/internal/metrics"
	"github.com/sentinel/ingest-service/internal/mgmt"
	"github.com/sentinel/ingest-service/internal/publish"
	"github.com/sentinel/ingest-service/internal/registry"
	_ "github.com/sentinel/ingest-service/internal/source/all" // registers transports, decoders and drivers
	"github.com/sentinel/ingest-service/internal/source/driver/stream"
)

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
		zap.Int("publisher_shards", cfg.PublisherShards),
		zap.Int("frame_buffer", cfg.FrameBuffer),
	)
	stream.FrameBuffer = cfg.FrameBuffer

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
		LeaseTick:      cfg.LeaseTick,
		RebalanceAfter: cfg.LeaseRebalance,
	}
	if cfg.LeaseEnabled {
		supOpts.Leaser = lease.New(reg.Pool(), cfg.ReplicaID, lease.Options{TTL: cfg.LeaseTTL})
		supOpts.Preferred = lease.Preferred
		logger.Info("feed leases enabled", zap.Duration("ttl", cfg.LeaseTTL), zap.Duration("tick", cfg.LeaseTick), zap.Duration("rebalance_after", cfg.LeaseRebalance))
	} else {
		logger.Warn("LEASE_ENABLED=false: this replica runs every enabled feed")
	}
	sup := feed.New(reg, publisher, logger, m, supOpts)
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
	mgmtHandler.StaleAfter = 3 * cfg.LeaseTick * 2
	if cfg.LeaseEnabled {
		mgmtHandler.StaleAfter = cfg.LeaseTTL
	}
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
	publisher.Close()
	flushMs := int(time.Until(deadline) / time.Millisecond)
	if flushMs < 1000 {
		flushMs = 1000
	}
	if remaining := producer.Flush(flushMs); remaining > 0 {
		logger.Warn("kafka producer still has unflushed messages", zap.Int("remaining", remaining))
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		logger.Error("http server shutdown error", zap.Error(err))
	}

	logger.Info("sentinel ingest service shut down gracefully")
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
