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

	"github.com/sentinel/analytics-service/internal/alerts"
	"github.com/sentinel/analytics-service/internal/baseline"
	"github.com/sentinel/analytics-service/internal/config"
	"github.com/sentinel/analytics-service/internal/consumer"
	"github.com/sentinel/analytics-service/internal/detect"
	"github.com/sentinel/analytics-service/internal/handler"
	"github.com/sentinel/analytics-service/internal/job"
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
	baselineStore := store.NewBaselineStore(pool)
	baselines := baseline.NewCache(baselineStore, cfg.Baseline.CacheTTL)

	// Baselines are recomputed on a schedule; the first pass runs shortly
	// after start so a fresh deployment has something to judge against.
	baselineJob := job.New(pool, entityStore, baselineStore, baselines, counters, job.Options{
		Window:       cfg.Baseline.Window,
		Interval:     cfg.Baseline.Interval,
		ActiveWindow: cfg.Baseline.ActiveWindow,
		Thin:         cfg.Baseline.ThinInterval,
		Workers:      cfg.Baseline.Workers,
		Builder: baseline.Config{
			WindowDays:    int(cfg.Baseline.Window.Hours() / 24),
			ThinSec:       int(cfg.Baseline.ThinInterval.Seconds()),
			SlowKnots:     cfg.Baseline.SlowKnots.For("DEFAULT"),
			EpsM:          cfg.Baseline.DBSCANEpsM,
			MinPts:        cfg.Baseline.DBSCANMinPts,
			MaxSlowPoints: cfg.Baseline.MaxSlowPoints,
			MinPoints:     cfg.Baseline.MinPoints,
			MinDays:       cfg.Baseline.MinDays,
			MaxPlaces:     20,
		},
		SlowKnots: cfg.Baseline.SlowKnots,
	}, logger)
	baselineJob.After = append(baselineJob.After, func(ctx context.Context) error {
		n, err := coverageStore.Prune(ctx, time.Now().Add(-cfg.Detectors.CoverageTTL))
		if n > 0 {
			logger.Info("coverage cells pruned", zap.Int64("rows", n))
		}
		return err
	})
	// Findings: the detectors judge every fix against the cached baseline;
	// findings that reach the alerting severity, or are confirmed, go out on
	// alerts.events under the finding's own id.
	findingStore := store.NewFindingStore(pool)
	engine := detect.NewEngine(baselines, findingStore, coverageStore, alerts.NewPublisher(producer), counters, cfg.Detectors, logger)
	if fixes, err := entityStore.LastFixes(context.Background(), time.Now().Add(-cfg.Baseline.ActiveWindow)); err != nil {
		logger.Warn("last fixes not seeded; silence before this start is not judged", zap.Error(err))
	} else {
		engine.Seed(fixes)
		logger.Info("last fixes seeded", zap.Int("entities", len(fixes)))
	}
	baselineJob.After = append(baselineJob.After, func(ctx context.Context) error {
		_, err := findingStore.CloseStale(ctx, time.Now().Add(-cfg.Detectors.AbsorbWindow))
		return err
	})
	jobCtx, stopJob := context.WithCancel(context.Background())
	go baselineJob.Run(jobCtx)
	go engine.RunTicker(jobCtx)

	posConsumer, err := consumer.NewPositionConsumer(cfg.KafkaBrokers, engine, coverageStore,
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

	api := handler.NewAnalyticsHandler(counters, coverageStore, baselineStore, entityStore, findingStore, engine, logger)
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

	stopJob()
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
