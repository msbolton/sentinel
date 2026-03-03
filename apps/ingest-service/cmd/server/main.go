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
	"github.com/sentinel/ingest-service/internal/health"
	"github.com/sentinel/ingest-service/internal/ingest"
	kafkaproducer "github.com/sentinel/ingest-service/internal/kafka"
	"github.com/sentinel/ingest-service/internal/metrics"
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
		zap.Bool("opensky_enabled", cfg.OpenSkyEnabled),
		zap.Bool("adsblol_enabled", cfg.ADSBLolEnabled),
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

	// OpenSky Network listener (if enabled).
	var openSkyListener *sources.OpenSkyListener
	if cfg.OpenSkyEnabled {
		openSkyListener = sources.NewOpenSkyListener(cfg, pipelineInput, logger, m)
		if err := openSkyListener.Start(); err != nil {
			logger.Error("failed to start opensky listener (will retry)", zap.Error(err))
		}
	}

	// adsb.lol military aircraft listener (if enabled).
	var adsbLolListener *sources.ADSBLolListener
	if cfg.ADSBLolEnabled {
		adsbLolListener = sources.NewADSBLolListener(cfg, pipelineInput, logger, m)
		if err := adsbLolListener.Start(); err != nil {
			logger.Error("failed to start adsblol listener (will retry)", zap.Error(err))
		}
	}

	// Start HTTP health/metrics server.
	healthHandler := health.NewHandler(producer, logger)
	mux := http.NewServeMux()
	healthHandler.RegisterRoutes(mux)

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
	if openSkyListener != nil {
		openSkyListener.Stop()
	}
	if adsbLolListener != nil {
		adsbLolListener.Stop()
	}

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
