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

	"github.com/sentinel/api-gateway/internal/auth"
	"github.com/sentinel/api-gateway/internal/config"
	"github.com/sentinel/api-gateway/internal/locations"
	"github.com/sentinel/api-gateway/internal/proxy"
	"github.com/sentinel/api-gateway/internal/registration"
	"github.com/sentinel/api-gateway/internal/server"
	"github.com/sentinel/api-gateway/internal/stream"
)

func main() {
	logger, err := newLogger()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to initialize logger: %v\n", err)
		os.Exit(1)
	}
	defer logger.Sync()

	logger.Info("starting sentinel api gateway")

	cfg := config.Load()
	logger.Info("configuration loaded",
		zap.String("port", cfg.Port),
		zap.String("env", cfg.AppEnv),
		zap.Bool("auth_dev_bypass", cfg.AuthDevBypass),
		zap.String("keycloak_url", cfg.KeycloakURL),
	)
	if cfg.IsProduction() && cfg.AuthDevBypass {
		logger.Fatal("AUTH_DEV_BYPASS must not be enabled in production")
	}

	// PostgreSQL (locations).
	pool, err := pgxpool.New(context.Background(), cfg.DatabaseURL)
	if err != nil {
		logger.Fatal("failed to connect to database", zap.Error(err))
	}
	defer pool.Close()
	if err := pool.Ping(context.Background()); err != nil {
		logger.Fatal("database ping failed", zap.Error(err))
	}
	logger.Info("connected to database")

	// Auth.
	verifier := auth.NewKeycloakVerifier(auth.KeycloakOptions{
		Keyfunc:  auth.NewJWKSKeyfunc(cfg.JWKSURL(), logger),
		Issuers:  cfg.KeycloakIssuers,
		Audience: cfg.KeycloakAudience,
		ClientID: cfg.KeycloakClientID,
	})
	authMw := auth.NewMiddleware(verifier, cfg.AuthDevBypass, logger)

	// Upstream proxies.
	up := server.Upstreams{
		Entities: mustProxy(logger, "entity-service", cfg.EntityServiceURL, nil),
		Tracks:   mustProxy(logger, "track-service", cfg.TrackServiceURL, nil),
		Alerts:   mustProxy(logger, "alert-service", cfg.AlertServiceURL, nil),
		Links:    mustProxy(logger, "link-analysis-service", cfg.LinkAnalysisServiceURL, nil),
		Search:   mustProxy(logger, "search-service", cfg.SearchServiceURL, nil),
		Feeds:    mustProxy(logger, "ingest-service", cfg.IngestServiceURL, proxy.StripPrefix("/api/v1")),
	}

	locStore := locations.NewPGStore(pool)

	// Registration and user administration (Keycloak Admin API + SMTP).
	keycloakAdmin := registration.NewKeycloakClient(cfg.KeycloakURL, cfg.KeycloakRealm,
		cfg.KeycloakServiceClientID, cfg.KeycloakServiceClientSecret, logger)
	var mailer registration.Mailer = registration.NopMailer{}
	if cfg.MailHost != "" {
		mailer = registration.NewSMTPMailer(cfg.MailHost, cfg.MailPort, cfg.MailFrom)
	}
	regHandler := registration.NewHandler(keycloakAdmin, mailer,
		registration.NewIPLimiter(5, 15*time.Minute), logger)

	// Real-time entity stream: Kafka -> hub -> websocket clients.
	hub := stream.NewHub(logger)
	batcher := stream.NewBatcher(hub, 200*time.Millisecond)
	batcher.Start()
	bridge, err := stream.NewBridge(cfg.KafkaBrokers, cfg.WSConsumerGroup, hub, batcher, logger)
	if err != nil {
		logger.Fatal("failed to create websocket kafka bridge", zap.Error(err))
	}
	bridge.Start()
	wsHandler := stream.NewHandler(hub, cfg.CORSOrigin, logger)

	// Push the area clients are looking at to ingest-service so OpenSky is
	// polled only for that area.
	var viewportSync *stream.ViewportSync
	if cfg.ViewportSyncEnabled {
		viewportSync = stream.NewViewportSync(hub, stream.NewIngestHTTPSink(cfg.IngestServiceURL), stream.ViewportSyncOptions{
			Debounce:  cfg.ViewportSyncDebounce,
			Heartbeat: cfg.ViewportSyncHeartbeat,
		}, logger)
		hub.SetViewportListener(viewportSync.Notify)
		viewportSync.Start()
	}

	handler := server.New(server.Deps{
		Logger:     logger,
		Auth:       authMw,
		Production: cfg.IsProduction(),
		CORSOrigin: cfg.CORSOrigin,
		Upstreams:  up,
		Locations:  locations.NewHandler(locStore, logger),
		Public:     []func(*http.ServeMux){regHandler.RegisterPublicRoutes},
		Protected:  []func(*http.ServeMux){regHandler.RegisterAdminRoutes},
		Root: []func(*http.ServeMux){func(mux *http.ServeMux) {
			mux.Handle("GET /ws/entities", authMw.RequireAuth(wsHandler))
		}},
		ReadyChecks: map[string]func() error{
			"database": func() error { return locStore.Ping(context.Background()) },
		},
	})

	httpServer := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
		// No WriteTimeout: the websocket stream and proxied SSE are long-lived.
	}

	go func() {
		logger.Info("http server starting", zap.String("addr", ":"+cfg.Port))
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Fatal("http server failed", zap.Error(err))
		}
	}()

	logger.Info("sentinel api gateway started successfully")

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	sig := <-sigCh
	logger.Info("shutdown signal received", zap.String("signal", sig.String()))

	if viewportSync != nil {
		viewportSync.Stop()
	}
	bridge.Stop()
	batcher.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(ctx); err != nil {
		logger.Error("http server shutdown error", zap.Error(err))
	}

	logger.Info("sentinel api gateway shut down gracefully")
}

func mustProxy(logger *zap.Logger, name, target string, rewrite func(string) string) http.Handler {
	h, err := proxy.New(proxy.Options{Name: name, Target: target, RewritePath: rewrite, Logger: logger})
	if err != nil {
		logger.Fatal("invalid upstream", zap.String("upstream", name), zap.Error(err))
	}
	return h
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
