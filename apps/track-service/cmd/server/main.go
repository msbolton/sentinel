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
	"github.com/sentinel/go-common/middleware"
	"github.com/sentinel/track-service/internal/batch"
	"github.com/sentinel/track-service/internal/config"
	"github.com/sentinel/track-service/internal/consumer"
	"github.com/sentinel/track-service/internal/handler"
	"github.com/sentinel/track-service/internal/store"
)

func main() {
	logger, err := newLogger()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to initialize logger: %v\n", err)
		os.Exit(1)
	}
	defer logger.Sync()

	logger.Info("starting sentinel track service")

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

	// Initialize store and batcher.
	trackStore := store.NewTrackStore(pool, logger)
	batcher := batch.NewBatcher(trackStore, logger)
	defer batcher.Stop()

	// Start Kafka consumer.
	posConsumer, err := consumer.NewPositionConsumer(cfg.KafkaBrokers, batcher, logger)
	if err != nil {
		logger.Fatal("failed to create position consumer", zap.Error(err))
	}
	posConsumer.Start()

	// Set up HTTP server.
	mux := http.NewServeMux()

	healthHandler := health.NewHandler("sentinel-track-service", logger, func() error {
		return trackStore.Ping(context.Background())
	})
	healthHandler.RegisterRoutes(mux)

	trackHandler := handler.NewTrackHandler(trackStore, logger)
	trackHandler.RegisterRoutes(mux)

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

	logger.Info("sentinel track service started successfully",
		zap.String("kafka_group", "sentinel-track-service"),
	)

	// Wait for shutdown signal.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	sig := <-sigCh

	logger.Info("shutdown signal received", zap.String("signal", sig.String()))

	// Graceful shutdown.
	posConsumer.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(ctx); err != nil {
		logger.Error("http server shutdown error", zap.Error(err))
	}

	logger.Info("sentinel track service shut down gracefully")
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
