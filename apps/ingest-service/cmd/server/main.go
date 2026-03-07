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
	"github.com/sentinel/ingest-service/internal/feeds"
	"github.com/sentinel/ingest-service/internal/health"
	"github.com/sentinel/ingest-service/internal/ingest"
	kafkaproducer "github.com/sentinel/ingest-service/internal/kafka"
	"github.com/sentinel/ingest-service/internal/metrics"
	"github.com/sentinel/ingest-service/internal/models"
	"github.com/sentinel/ingest-service/internal/sources"
)

func main() {
	// Initialize structured logger.
	logger, err := newLogger()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to initialize logger: %v\n", err)
		os.Exit(1)
	}
	defer logger.Sync()

	logger.Info("starting sentinel ingest service")

	// Load configuration from environment variables.
	cfg := config.Load()
	logger.Info("configuration loaded",
		zap.String("kafka_brokers", cfg.KafkaBrokers),
		zap.String("mqtt_broker", cfg.MQTTBroker),
		zap.String("stomp_addr", cfg.STOMPAddr),
		zap.String("tcp_addr", cfg.TCPAddr),
		zap.String("http_addr", cfg.HTTPAddr),
		zap.Int("worker_pool_size", cfg.WorkerPoolSize),
		zap.Int("batch_size", cfg.BatchSize),
		zap.Int("flush_interval_ms", cfg.FlushIntervalMs),
	)

	// Initialize Prometheus metrics.
	m := metrics.New()

	// Create Kafka producer.
	producer, err := kafkaproducer.NewProducer(cfg.KafkaBrokers, logger, m)
	if err != nil {
		logger.Fatal("failed to create kafka producer", zap.Error(err))
	}
	defer producer.Close()

	// Create and start the ingestion pipeline.
	pipeline := ingest.NewPipeline(cfg, producer, logger, m)
	pipeline.Start()

	// Start source listeners. Each listener runs in its own goroutine and
	// feeds messages into the pipeline's input channel. Listeners that fail
	// to connect log an error but do not prevent the service from starting,
	// since other sources may still function.
	pipelineInput := pipeline.Input()

	// MQTT listener.
	mqttListener := sources.NewMQTTListener(cfg.MQTTBroker, cfg.MQTTTopics, pipelineInput, logger, m)
	if err := mqttListener.Start(); err != nil {
		logger.Error("failed to start mqtt listener (will retry on reconnect)", zap.Error(err))
	}

	// STOMP/ActiveMQ listener.
	stompListener := sources.NewSTOMPListener(cfg.STOMPAddr, cfg.STOMPQueue, pipelineInput, logger, m)
	if err := stompListener.Start(); err != nil {
		logger.Error("failed to start stomp listener (will retry on reconnect)", zap.Error(err))
	}

	// Raw TCP listener.
	tcpListener := sources.NewTCPListener(cfg.TCPAddr, pipelineInput, logger, m)
	if err := tcpListener.Start(); err != nil {
		logger.Fatal("failed to start tcp listener", zap.Error(err))
	}

	// Feed manager for runtime-toggleable data sources.
	feedManager := feeds.NewManager(logger)

	// Register OpenSky feed (toggleable via /feeds API).
	if err := feedManager.Register(
		"opensky", "OpenSky Network", models.SourceOpenSky,
		"Global ADS-B aircraft positions from OpenSky Network",
		func() (sources.Listener, error) {
			return sources.NewOpenSkyListener(cfg, pipelineInput, logger, m,
				func(count int, at time.Time) { feedManager.RecordSuccess("opensky", count, at) },
				func() { feedManager.RecordError("opensky") },
			), nil
		},
		cfg.OpenSkyEnabled,
	); err != nil {
		logger.Error("failed to register opensky feed", zap.Error(err))
	}

	// Register adsb.lol military flights feed (toggleable via /feeds API).
	if err := feedManager.Register(
		"adsb-lol", "Military Flights", models.SourceADSBLol,
		"Military/government aircraft positions from adsb.lol",
		func() (sources.Listener, error) {
			return sources.NewADSBLolListener(cfg, pipelineInput, logger, m,
				func(count int, at time.Time) { feedManager.RecordSuccess("adsb-lol", count, at) },
				func() { feedManager.RecordError("adsb-lol") },
			), nil
		},
		cfg.ADSBLolEnabled,
	); err != nil {
		logger.Error("failed to register adsb-lol feed", zap.Error(err))
	}

	// Register CelesTrak satellite feed (toggleable via /feeds API).
	if err := feedManager.Register(
		"celestrak", "CelesTrak Satellites", models.SourceCelesTrak,
		"Satellite positions computed from CelesTrak TLE data via SGP4",
		func() (sources.Listener, error) {
			return sources.NewCelesTrakListener(cfg, pipelineInput, logger, m,
				func(count int, at time.Time) { feedManager.RecordSuccess("celestrak", count, at) },
				func() { feedManager.RecordError("celestrak") },
			), nil
		},
		cfg.CelesTrakEnabled,
	); err != nil {
		logger.Error("failed to register celestrak feed", zap.Error(err))
	}

	// Configure per-feed staleness thresholds.
	feedManager.SetStaleThresholds("opensky",
		staleThreshold(cfg.OpenSkyStaleWarnSec, cfg.FeedStaleWarnSec),
		staleThreshold(cfg.OpenSkyStaleCriticalSec, cfg.FeedStaleCriticalSec),
	)
	feedManager.SetStaleThresholds("adsb-lol",
		staleThreshold(cfg.ADSBLolStaleWarnSec, cfg.FeedStaleWarnSec),
		staleThreshold(cfg.ADSBLolStaleCriticalSec, cfg.FeedStaleCriticalSec),
	)
	feedManager.SetStaleThresholds("celestrak",
		staleThreshold(cfg.CelesTrakStaleWarnSec, cfg.FeedStaleWarnSec),
		staleThreshold(cfg.CelesTrakStaleCriticalSec, cfg.FeedStaleCriticalSec),
	)

	// Start HTTP health/metrics/feeds server.
	healthHandler := health.NewHandler(producer, logger)
	feedsHandler := feeds.NewHandler(feedManager, logger)
	mux := http.NewServeMux()
	healthHandler.RegisterRoutes(mux)
	feedsHandler.RegisterRoutes(mux)

	httpServer := &http.Server{
		Addr:         cfg.HTTPAddr,
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		logger.Info("http server starting", zap.String("addr", cfg.HTTPAddr))
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Fatal("http server failed", zap.Error(err))
		}
	}()

	logger.Info("sentinel ingest service started successfully")

	// Wait for shutdown signal.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	sig := <-sigCh

	logger.Info("shutdown signal received", zap.String("signal", sig.String()))

	// Graceful shutdown: stop sources first, then pipeline, then Kafka.
	logger.Info("initiating graceful shutdown")

	// Stop accepting new messages from sources.
	mqttListener.Stop()
	stompListener.Stop()
	tcpListener.Stop()
	feedManager.StopAll()

	// Stop the pipeline (drains remaining messages).
	pipeline.Stop()

	// Shut down the HTTP server.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(ctx); err != nil {
		logger.Error("http server shutdown error", zap.Error(err))
	}

	// Kafka producer is closed by its deferred Close().
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

// staleThreshold returns the per-feed threshold if set (> 0), otherwise the global default.
func staleThreshold(perFeed, global int) int {
	if perFeed > 0 {
		return perFeed
	}
	return global
}
