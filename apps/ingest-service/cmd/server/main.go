package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/sentinel/ingest-service/internal/config"
	"github.com/sentinel/ingest-service/internal/drivers"
	"github.com/sentinel/ingest-service/internal/health"
	"github.com/sentinel/ingest-service/internal/ingest"
	kafkaproducer "github.com/sentinel/ingest-service/internal/kafka"
	"github.com/sentinel/ingest-service/internal/metrics"
	"github.com/sentinel/ingest-service/internal/mgmt"
	"github.com/sentinel/ingest-service/internal/registry"
	"github.com/sentinel/ingest-service/internal/runtime"
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
		zap.String("http_addr", cfg.HTTPAddr),
		zap.String("replica_id", cfg.ReplicaID),
		zap.Int("worker_pool_size", cfg.WorkerPoolSize),
		zap.Int("batch_size", cfg.BatchSize),
		zap.Int("flush_interval_ms", cfg.FlushIntervalMs),
	)

	m := metrics.New()

	producer, err := kafkaproducer.NewProducer(cfg.KafkaBrokers, logger, m)
	if err != nil {
		logger.Fatal("failed to create kafka producer", zap.Error(err))
	}
	defer producer.Close()

	pipeline := ingest.NewPipeline(cfg, producer, logger, m)
	pipeline.Start()

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

	// Runtime: runs the enabled feeds on this replica and reports status.
	rt := runtime.New(reg, builtinFactories(cfg), pipeline.Input(), logger, m, runtime.Options{
		ReplicaID:     cfg.ReplicaID,
		StaleWarn:     time.Duration(cfg.FeedStaleWarnSec) * time.Second,
		StaleCritical: time.Duration(cfg.FeedStaleCriticalSec) * time.Second,
	})
	runtimeDone := make(chan struct{})
	go func() {
		defer close(runtimeDone)
		rt.Run(ctx)
	}()

	// HTTP: health and metrics (open) plus the management API (guarded).
	mux := http.NewServeMux()
	health.NewHandler(producer, logger).RegisterRoutes(mux)
	mgmt.NewHandler(reg, catalog, rt, logger).RegisterRoutes(mux, mgmt.Auth(mgmt.AuthOptions{ServiceToken: cfg.ServiceToken}))

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

	logger.Info("sentinel ingest service started successfully")

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	select {
	case sig := <-sigCh:
		logger.Info("shutdown signal received", zap.String("signal", sig.String()))
	case err := <-httpErr:
		logger.Error("http server failed", zap.Error(err))
	}

	// Graceful shutdown: stop feeds, drain the pipeline, stop HTTP, close Kafka.
	logger.Info("initiating graceful shutdown")
	cancel()
	select {
	case <-runtimeDone:
	case <-time.After(20 * time.Second):
		logger.Warn("feed runtime did not stop in time")
	}
	pipeline.Stop()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		logger.Error("http server shutdown error", zap.Error(err))
	}

	logger.Info("sentinel ingest service shut down gracefully")
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
