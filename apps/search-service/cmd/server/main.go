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
	"github.com/sentinel/search-service/internal/config"
	"github.com/sentinel/search-service/internal/consumer"
	"github.com/sentinel/search-service/internal/handler"
	"github.com/sentinel/search-service/internal/opensearch"
)

func main() {
	logger, err := newLogger()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to initialize logger: %v\n", err)
		os.Exit(1)
	}
	defer logger.Sync()

	logger.Info("starting sentinel search service")

	cfg := config.Load()
	logger.Info("configuration loaded",
		zap.String("port", cfg.Port),
		zap.String("kafka_brokers", cfg.KafkaBrokers),
		zap.String("opensearch_host", cfg.OpenSearchHost),
	)

	// Connect to PostgreSQL (used for index warming).
	pool, err := pgxpool.New(context.Background(), cfg.DatabaseURL)
	if err != nil {
		logger.Fatal("failed to connect to database", zap.Error(err))
	}
	defer pool.Close()

	if err := pool.Ping(context.Background()); err != nil {
		logger.Fatal("database ping failed", zap.Error(err))
	}
	logger.Info("connected to database")

	// Initialize OpenSearch client.
	osClient, err := opensearch.NewClient(cfg.OpenSearchHost, pool, logger)
	if err != nil {
		logger.Fatal("failed to create opensearch client", zap.Error(err))
	}

	// Ensure index exists and warm from DB if empty.
	if err := osClient.EnsureIndex(context.Background()); err != nil {
		logger.Fatal("failed to ensure opensearch index", zap.Error(err))
	}
	if err := osClient.WarmIndex(context.Background()); err != nil {
		logger.Warn("index warming failed", zap.Error(err))
	}

	// Start Kafka consumer for entity indexing.
	indexer, err := consumer.NewEntityIndexer(cfg.KafkaBrokers, osClient, logger)
	if err != nil {
		logger.Fatal("failed to create entity indexer", zap.Error(err))
	}
	indexer.Start()

	// Set up HTTP server.
	mux := http.NewServeMux()

	healthHandler := health.NewHandler("sentinel-search-service", logger, func() error {
		return osClient.Ping(context.Background())
	})
	healthHandler.RegisterRoutes(mux)

	searchHandler := handler.NewSearchHandler(osClient, logger)
	searchHandler.RegisterRoutes(mux)

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

	logger.Info("sentinel search service started successfully")

	// Wait for shutdown signal.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	sig := <-sigCh

	logger.Info("shutdown signal received", zap.String("signal", sig.String()))

	// Graceful shutdown.
	indexer.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(ctx); err != nil {
		logger.Error("http server shutdown error", zap.Error(err))
	}

	logger.Info("sentinel search service shut down gracefully")
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
