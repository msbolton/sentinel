package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	commonkafka "github.com/sentinel/go-common/kafka"
	"github.com/sentinel/go-common/terrain"

	"github.com/sentinel/ingest-service/internal/config"
	"github.com/sentinel/ingest-service/internal/density"
	"github.com/sentinel/ingest-service/internal/dlq"
	"github.com/sentinel/ingest-service/internal/drivers"
	"github.com/sentinel/ingest-service/internal/emplacement"
	"github.com/sentinel/ingest-service/internal/feed"
	"github.com/sentinel/ingest-service/internal/health"
	"github.com/sentinel/ingest-service/internal/ingesttoken"
	kafkaproducer "github.com/sentinel/ingest-service/internal/kafka"
	"github.com/sentinel/ingest-service/internal/lease"
	"github.com/sentinel/ingest-service/internal/metrics"
	"github.com/sentinel/ingest-service/internal/mgmt"
	"github.com/sentinel/ingest-service/internal/outbound"
	"github.com/sentinel/ingest-service/internal/publish"
	"github.com/sentinel/ingest-service/internal/registry"
	_ "github.com/sentinel/ingest-service/internal/source/all" // registers transports, decoders and drivers
	"github.com/sentinel/ingest-service/internal/source/driver"
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
	for _, w := range cfg.Warnings() {
		logger.Warn(w)
	}
	// Parsed with the rest of the configuration, before anything starts: a bad
	// value must not stop the service once feeds have taken their leases.
	reserved, err := cfg.ReservedPortSet()
	if err != nil {
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
		zap.Int("tls_reload_sec", cfg.TLSReloadSec),
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

	// Emplacements: the store behind the management API, and the cache the
	// drivers that report as emplacements read.
	empStore := emplacement.NewPGStore(reg.Pool())
	empCache := emplacement.NewCache(empStore, reg.Pool(), logger)
	go empCache.Run(ctx)

	// Ingest tokens: the store behind the token routes, and the cache the
	// grpc driver authenticates senders with.
	tokStore := ingesttoken.NewPGStore(reg.Pool())
	tokCache := ingesttoken.NewCache(tokStore, reg.Pool(), logger)
	tokDone := make(chan struct{})
	go func() {
		defer close(tokDone)
		tokCache.Run(ctx)
	}()

	// Terrain: ground heights for the drivers that turn directions into
	// positions. On only when a cache folder is set; a service that cannot be
	// built is fatal, since the operator asked for terrain. It is left a nil
	// interface otherwise, which is what drivers test for.
	var terrainSvc driver.Terrain
	if cfg.TerrainEnabled() {
		svc, err := terrain.New(terrain.Config{
			SourceURL:     cfg.TerrainSourceURL,
			CacheDir:      cfg.TerrainCacheDir,
			MaxCacheBytes: int64(cfg.TerrainCacheMaxMB) << 20,
			FetchTimeout:  cfg.TerrainFetchTimeout,
			// A detection waits TerrainWait; a fetch older than that turns
			// callers away, so a hung fetch stalls a stream about once.
			JoinWindow: cfg.TerrainWait,
			Logger:     logger.Named("terrain"),
		})
		if err != nil {
			logger.Fatal("failed to start the terrain service", zap.Error(err))
		}
		terrainSvc = svc
		promReg.MustRegister(metrics.NewTerrainCollector(svc.Stats))
		logger.Info("terrain enabled", zap.String("cache_dir", cfg.TerrainCacheDir), zap.String("source", cfg.TerrainSourceURL),
			zap.Int("cache_max_mb", cfg.TerrainCacheMaxMB), zap.Duration("wait", cfg.TerrainWait), zap.Float64("max_range_m", cfg.TerrainMaxRangeM))
	}

	// Supervisor: runs the feeds this replica owns and reports status.
	supOpts := feed.Options{
		ReplicaID:         cfg.ReplicaID,
		RetryMin:          cfg.FeedRetryMin,
		RetryMax:          cfg.FeedRetryMax,
		StaleWarn:         cfg.FeedStaleWarn,
		StaleCritical:     cfg.FeedStaleCritical,
		RawMax:            cfg.RawPayloadMaxBytes,
		Listener:          cfg.Listener,
		ListenerHost:      cfg.ListenerHost,
		ListenerShared:    cfg.ListenerShared,
		LeaseTick:         cfg.LeaseTick,
		RebalanceAfter:    cfg.LeaseRebalance,
		SecretPolicy:      secretPolicy,
		SecretReloadEvery: time.Duration(cfg.TLSReloadSec) * time.Second,
		Shapes:            outbound.NewShapeStore(reg.Pool()),
		Emplacements:      empCache,
		Tokens:            tokCache,
		EmplacementWriter: empStore,
		Terrain:           terrainSvc,
		TerrainWait:       cfg.TerrainWait,
		TerrainMaxRangeM:  cfg.TerrainMaxRangeM,
		Density:           density.New(producer, commonkafka.TopicDensityEvents).For,
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
	closeDeadLetters := serveDeadLetters(mgmtHandler, cfg, logger)
	defer closeDeadLetters(dlq.Deadline) // a no-op once shutdown has closed it
	mgmtHandler.StaleAfter = 3 * cfg.LeaseTick * 2
	if cfg.LeaseEnabled {
		mgmtHandler.StaleAfter = cfg.LeaseTTL
	}
	mgmtHandler.Reserved = reserved
	mgmtHandler.Tokens = tokStore
	mgmtHandler.UseSecretPolicy(secretPolicy)
	mgmtHandler.RegisterRoutes(mux, mgmt.Auth(mgmt.AuthOptions{ServiceToken: cfg.ServiceToken}))
	empHandler := emplacement.NewHandler(empStore, feedCheck(reg, catalog), logger)
	empHandler.Liveness = livenessSource(reg, mgmtHandler.StaleAfter, time.Now)
	empHandler.RegisterRoutes(mux, mgmt.Auth(mgmt.AuthOptions{ServiceToken: cfg.ServiceToken}))

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
	// supervisor stops drivers → token cache flushes → publisher drains → HTTP shutdown → producer
	// flushes → dead-letter reader closes.
	logger.Info("initiating graceful shutdown", zap.Duration("timeout", cfg.ShutdownTimeout))
	deadline := time.Now().Add(cfg.ShutdownTimeout)
	healthHandler.SetDraining(true)
	cancel()
	select {
	case <-supervisorDone:
	case <-time.After(time.Until(deadline)):
		logger.Warn("feed supervisor did not stop in time")
	}
	// The token cache writes its last lastUsedAt batch (up to 5 s) once the
	// context is cancelled; wait for it before the pool closes.
	select {
	case <-tokDone:
	case <-time.After(max(time.Until(deadline), 0)):
		logger.Warn("ingest token cache did not finish its last write in time")
	}
	// With Kafka unreachable nothing drains: the shards cannot hand their
	// observations over and a flush delivers none. So the publisher waits only
	// until the deadline and then gives up on what it still holds (and logs how
	// much), the HTTP server gets what is left of the deadline (at most 5 s), and
	// the producer's one flush happens in its close, for the time then left but
	// at least a second, and the dead-letter reader's close after it has the same
	// floor. The process thus ends at most about two seconds after the deadline,
	// inside the grace period the orchestrator allows.
	publisher.CloseWithin(time.Until(deadline))

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), min(5*time.Second, max(time.Until(deadline), 0)))
	defer shutdownCancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		logger.Error("http server shutdown error", zap.Error(err))
	}

	// The producer's deferred Close flushes for 10 s whatever the deadline; closing
	// it here makes that one a no-op.
	producer.CloseWithin(max(time.Until(deadline), time.Second))

	// The dead-letter reader's consumer is closed last, with the time that is
	// left but at least a second: a read stuck in librdkafka must not hold the
	// process past its deadline. The deferred stop is then a no-op.
	closeDeadLetters(max(time.Until(deadline), time.Second))

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
func serveDeadLetters(h *mgmt.Handler, cfg *config.Config, logger *zap.Logger) (closeWithin func(time.Duration)) {
	if cfg.DLQPeekMaxScan <= 0 {
		logger.Info("dead-letter viewing is off", zap.Int("dlq_peek_max_scan", cfg.DLQPeekMaxScan))
		return func(time.Duration) {}
	}
	reader := dlq.NewKafkaReader(cfg.KafkaBrokers, cfg.KafkaDeadLetterTopic, cfg.DLQPeekMaxScan, logger)
	h.DeadLetters = reader
	return once(reader.CloseWithin)
}

// once makes a close that takes a time bound run a single time: the first call
// does the work with its bound, and the later ones return at once. The explicit
// close at the end of shutdown thereby disarms the deferred one, so that a read
// stuck in the consumer, which made the first give up, is not waited on again
// past the orchestrator's grace period.
func once(f func(time.Duration)) func(time.Duration) {
	var o sync.Once
	return func(d time.Duration) { o.Do(func() { f(d) }) }
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

// feedCheck looks a connection up for an emplacement binding: whether it
// exists, takes data in (its effective direction, after config.direction
// narrows a bidirectional driver) and which driver it runs.
func feedCheck(reg *registry.Store, catalog *drivers.Catalog) emplacement.FeedCheck {
	return func(ctx context.Context, id string) (emplacement.FeedInfo, error) {
		f, err := reg.Get(ctx, id)
		if errors.Is(err, registry.ErrNotFound) {
			return emplacement.FeedInfo{}, nil
		}
		if err != nil {
			return emplacement.FeedInfo{}, err
		}
		info, ok := catalog.Get(f.Driver)
		if !ok {
			return emplacement.FeedInfo{Exists: true, Driver: f.Driver}, nil
		}
		return emplacement.FeedInfo{Exists: true, Inbound: f.Direction(info.Dir()).Inbound(), Driver: f.Driver}, nil
	}
}

// feedLister is the part of the registry livenessSource reads.
type feedLister interface {
	List(ctx context.Context) ([]registry.Feed, error)
}

// livenessSource reads each connection's emplacement heartbeats from its
// status row, which the replica that runs it rewrites every few seconds. A
// row older than staleAfter belongs to a replica that went silent and is not
// believed, so the emplacements' own rows are read instead.
func livenessSource(reg feedLister, staleAfter time.Duration, now func() time.Time) emplacement.LivenessSource {
	return func(ctx context.Context) (map[string]emplacement.FeedLiveness, error) {
		feeds, err := reg.List(ctx)
		if err != nil {
			return nil, err
		}
		out := make(map[string]emplacement.FeedLiveness, len(feeds))
		for _, f := range feeds {
			fl := emplacement.FeedLiveness{Driver: f.Driver}
			if st := f.Status; st != nil && st.Owner != "" && st.UpdatedAt != nil && now().Sub(*st.UpdatedAt) <= staleAfter {
				if list, ok := emplacement.LivenessFromDetail(st.Detail); ok {
					fl.Emplacements = list
				}
			}
			out[f.ID] = fl
		}
		return out, nil
	}
}
