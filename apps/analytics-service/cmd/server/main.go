// The analytics service learns what is normal for each entity from its
// track and raises findings when it departs from that: unusual places,
// hours, movement, going dark and reappearing. Findings that reach HIGH or
// are confirmed by an analyst become alerts on alerts.events.
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

	"github.com/sentinel/analytics-service/internal/config"
	"github.com/sentinel/analytics-service/internal/consumer"
	"github.com/sentinel/analytics-service/internal/handler"
	"github.com/sentinel/analytics-service/internal/stats"
	"github.com/sentinel/analytics-service/internal/store"
)

func main() {
	logger, err := newLogger()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to initialize logger: %v\n", err)
		os.Exit(1)
	}
	defer logger.Sync()

	logger.Info("starting sentinel analytics service")

	cfg := config.Load()
	logger.Info("configuration loaded",
		zap.String("port", cfg.Port),
		zap.String("kafka_brokers", cfg.KafkaBrokers),
		zap.Duration("baseline_window", cfg.Baseline.Window),
		zap.Duration("baseline_interval", cfg.Baseline.Interval),
	)

	pool, err := pgxpool.New(context.Background(), cfg.DatabaseURL)
	if err != nil {
		logger.Fatal("failed to connect to database", zap.Error(err))
	}
	defer pool.Close()
	if err := pool.Ping(context.Background()); err != nil {
		logger.Fatal("database ping failed", zap.Error(err))
	}
	logger.Info("connected to database")

	producer, err := kafka.NewProducer(cfg.KafkaBrokers, logger)
	if err != nil {
		logger.Fatal("failed to create kafka producer", zap.Error(err))
	}
	defer producer.Close()

	counters := stats.New()
	coverageStore := store.NewCoverageStore(pool)
	entityStore := store.NewEntityStore(pool)

	posConsumer, err := consumer.NewPositionConsumer(cfg.KafkaBrokers, consumer.NopSink{}, coverageStore,
		cfg.Detectors.CoverageCellDeg, counters, logger)
	if err != nil {
		logger.Fatal("failed to create position consumer", zap.Error(err))
	}
	posConsumer.Start()

	mux := http.NewServeMux()
	healthHandler := health.NewHandler("sentinel-analytics-service", logger, func() error {
		return entityStore.Ping(context.Background())
	})
	healthHandler.RegisterRoutes(mux)

	api := handler.NewAnalyticsHandler(counters, coverageStore, logger)
	api.RegisterRoutes(mux)

	httpServer := &http.Server{
		Addr:         ":" + cfg.Port,
		Handler:      middleware.CORS(cfg.CORSOrigin)(middleware.AuthFromHeaders(mux)),
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

	logger.Info("sentinel analytics service started successfully")

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	sig := <-sigCh
	logger.Info("shutdown signal received", zap.String("signal", sig.String()))

	posConsumer.Stop()
	producer.Flush(5000)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(ctx); err != nil {
		logger.Error("http server shutdown error", zap.Error(err))
	}
	logger.Info("sentinel analytics service shut down gracefully")
}

func newLogger() (*zap.Logger, error) {
	level := zapcore.InfoLevel
	if lvl := os.Getenv("LOG_LEVEL"); lvl != "" {
		if err := level.UnmarshalText([]byte(lvl)); err != nil {
			return nil, fmt.Errorf("invalid LOG_LEVEL %q: %w", lvl, err)
		}
	}
	cfg := zap.Config{
		Level:    zap.NewAtomicLevelAt(level),
		Encoding: "json",
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
