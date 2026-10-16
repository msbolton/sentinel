package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/sentinel/go-common/health"
	"github.com/sentinel/go-common/kafka"
	"github.com/sentinel/go-common/middleware"

	"github.com/sentinel/entity-service/internal/ageout"
	"github.com/sentinel/entity-service/internal/config"
	"github.com/sentinel/entity-service/internal/consumer"
	"github.com/sentinel/entity-service/internal/events"
	"github.com/sentinel/entity-service/internal/fusion"
	"github.com/sentinel/entity-service/internal/handler"
	"github.com/sentinel/entity-service/internal/store"
)

func main() {
	logger, err := newLogger()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to initialize logger: %v\n", err)
		os.Exit(1)
	}
	defer logger.Sync()

	logger.Info("starting sentinel entity service")

	cfg := config.Load()
	logger.Info("configuration loaded",
		zap.String("port", cfg.Port),
		zap.String("kafka_brokers", cfg.KafkaBrokers),
	)

	// Connect to PostgreSQL.
	pool, err := pgxpool.New(context.Background(), cfg.DatabaseURL)
	if err != nil {
		logger.Fatal("failed to connect to database", zap.Error(err))
	}
	defer pool.Close()

	if err := pool.Ping(context.Background()); err != nil {
		logger.Fatal("database ping failed", zap.Error(err))
	}
	logger.Info("connected to database")

	// Kafka producer: typed entity events plus, while enabled, the legacy JSON topics.
	producer, err := kafka.NewProducer(cfg.KafkaBrokers, logger)
	if err != nil {
		logger.Fatal("failed to create kafka producer", zap.Error(err))
	}
	defer producer.Close()
	publisher := events.NewPublisher(producer, logger)

	// Stores.
	entityStore := store.NewEntityStore(pool, logger)
	identityStore := store.NewIdentityStore(pool, logger)
	observationStore := store.NewObservationStore(pool, logger)
	sourceStore := store.NewSourceStore(pool, logger)
	ageoutConfigStore := store.NewAgeoutConfigStore(pool, logger)

	if err := ageoutConfigStore.SeedDefaults(context.Background()); err != nil {
		logger.Warn("failed to seed ageout defaults", zap.Error(err))
	}

	ageoutService := ageout.NewService(pool, publisher, logger)
	ageoutService.Start()

	// Fusion: observations.raw -> resolve identities -> newest fix wins -> events.
	var priority fusion.Priority
	if len(cfg.FusionSourcePriority) > 0 {
		priority = fusion.Priority(cfg.FusionSourcePriority)
	}
	resolver := fusion.NewResolver(identityStore, cfg.IdentityCacheSize, time.Hour)
	processor := fusion.NewProcessor(resolver, entityStore, observationStore, sourceStore, publisher, priority, logger)
	obsConsumer, err := consumer.New(consumer.Options{Brokers: cfg.KafkaBrokers, MaxMessages: cfg.ConsumerBatchSize, MaxWait: cfg.ConsumerMaxWait}, processor, producer, logger)
	if err != nil {
		logger.Fatal("failed to create observation consumer", zap.Error(err))
	}
	consumerCtx, stopConsumer := context.WithCancel(context.Background())
	consumerDone := make(chan struct{})
	go func() {
		defer close(consumerDone)
		if err := obsConsumer.Run(consumerCtx); err != nil {
			logger.Error("observation consumer stopped", zap.Error(err))
		}
	}()

	// Set up HTTP server.
	mux := http.NewServeMux()

	healthHandler := health.NewHandler("sentinel-entity-service", logger, func() error {
		return entityStore.Ping(context.Background())
	})
	healthHandler.RegisterRoutes(mux)

	entityHandler := handler.NewEntityHandler(entityStore, observationStore, publisher, logger)
	entityHandler.RegisterRoutes(mux)

	ageoutConfigHandler := handler.NewAgeoutConfigHandler(ageoutConfigStore, logger)
	ageoutConfigHandler.RegisterRoutes(mux)

	corsMiddleware := middleware.CORS(cfg.CORSOrigin)
	authMiddleware := middleware.AuthFromHeaders

	httpServer := &http.Server{
		Addr:         ":" + cfg.Port,
		Handler:      corsMiddleware(authMiddleware(mux)),
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		logger.Info("http server starting", zap.String("addr", ":"+cfg.Port))
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Fatal("http server failed", zap.Error(err))
		}
	}()

	logger.Info("sentinel entity service started successfully")

	// Wait for shutdown signal.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	sig := <-sigCh

	logger.Info("shutdown signal received", zap.String("signal", sig.String()))

	// Graceful shutdown: stop consuming (offsets of the in-flight batch are
	// committed only after it is stored), then the ageout loop, then flush.
	stopConsumer()
	select {
	case <-consumerDone:
	case <-time.After(15 * time.Second):
		logger.Warn("observation consumer did not stop in time")
	}
	_ = obsConsumer.Close()
	ageoutService.Stop()
	producer.Flush(5000)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(ctx); err != nil {
		logger.Error("http server shutdown error", zap.Error(err))
	}

	logger.Info("sentinel entity service shut down gracefully")
}

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
