package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	commonCfg "github.com/sentinel/go-common/config"
)

// Config holds the API gateway configuration.
type Config struct {
	Port         string
	AppEnv       string
	DatabaseURL  string
	KafkaBrokers string
	// WSConsumerGroup is unique per gateway instance so every instance sees every event.
	WSConsumerGroup string
	CORSOrigin      string

	// Auth
	AuthDevBypass    bool
	KeycloakURL      string
	KeycloakRealm    string
	KeycloakIssuers  []string
	KeycloakAudience string
	KeycloakClientID string

	// Registration (Keycloak admin API + SMTP)
	KeycloakServiceClientID     string
	KeycloakServiceClientSecret string
	MailHost                    string
	MailPort                    string
	MailFrom                    string
	// MailUsername/MailPassword authenticate to the relay (only over TLS).
	// MailTLS is starttls, tls or none; empty upgrades only if the relay offers
	// STARTTLS.
	MailUsername string
	MailPassword string
	MailTLS      string

	// Viewport sync: push the area connected map clients see to every ingest
	// feed that polls an area of interest, so a remote API is polled only
	// for what someone is looking at.
	ViewportSyncEnabled   bool
	ViewportSyncHeartbeat time.Duration
	ViewportSyncDebounce  time.Duration

	// Downstream services
	EntityServiceURL       string
	TrackServiceURL        string
	SearchServiceURL       string
	LinkAnalysisServiceURL string
	AlertServiceURL        string
	AnalyticsServiceURL    string
	IngestServiceURL       string
	// IngestServiceToken is presented to ingest-service, which is reachable
	// only through the gateway.
	IngestServiceToken string

	// IntelSkyBaseURL is where flight paths for military aircraft come from.
	IntelSkyBaseURL string
}

// IsProduction reports whether the gateway runs with production hardening.
func (c Config) IsProduction() bool { return c.AppEnv == "production" }

// devServiceSecret is the Keycloak service-client secret of the dev realm.
const devServiceSecret = "sentinel-service-secret-dev"

// Validate returns an error when the configuration must not start the gateway.
// The production checks only apply under APP_ENV=production.
func (c Config) Validate() error {
	switch c.MailTLS {
	case "", "starttls", "tls", "none":
	default:
		return fmt.Errorf("MAIL_TLS must be starttls, tls or none, got %q", c.MailTLS)
	}
	if !c.IsProduction() {
		return nil
	}
	if c.KeycloakServiceClientSecret == "" || c.KeycloakServiceClientSecret == devServiceSecret {
		return errors.New("KEYCLOAK_SERVICE_CLIENT_SECRET must be set to a non-default value in production")
	}
	if c.MailTLS == "none" && c.MailPassword != "" {
		return errors.New("MAIL_TLS=none would send MAIL_PASSWORD in cleartext; use starttls or tls in production")
	}
	return nil
}

// Warnings lists configuration that is valid but suspicious. Under production
// that is any downstream *_SERVICE_URL still pointing at localhost.
func (c Config) Warnings() []string {
	if !c.IsProduction() {
		return nil
	}
	urls := []struct{ name, value string }{
		{"ENTITY_SERVICE_URL", c.EntityServiceURL},
		{"TRACK_SERVICE_URL", c.TrackServiceURL},
		{"SEARCH_SERVICE_URL", c.SearchServiceURL},
		{"LINK_ANALYSIS_SERVICE_URL", c.LinkAnalysisServiceURL},
		{"ALERT_SERVICE_URL", c.AlertServiceURL},
		{"ANALYTICS_SERVICE_URL", c.AnalyticsServiceURL},
		{"INGEST_SERVICE_URL", c.IngestServiceURL},
	}
	var out []string
	for _, u := range urls {
		if isLoopbackURL(u.value) {
			out = append(out, u.name+" points at localhost ("+u.value+"); the service is unreachable from another pod")
		}
	}
	return out
}

func isLoopbackURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	switch u.Hostname() {
	case "localhost", "127.0.0.1", "::1":
		return true
	}
	return false
}

// JWKSURL returns the Keycloak JWKS endpoint for the configured realm.
func (c Config) JWKSURL() string {
	return strings.TrimRight(c.KeycloakURL, "/") + "/realms/" + c.KeycloakRealm + "/protocol/openid-connect/certs"
}

// Load reads configuration from environment variables.
func Load() Config {
	keycloakURL := commonCfg.EnvOrDefault("KEYCLOAK_URL", commonCfg.EnvOrDefault("KEYCLOAK_BASE_URL", "http://localhost:8080"))
	realm := commonCfg.EnvOrDefault("KEYCLOAK_REALM", "sentinel")

	issuers := []string{strings.TrimRight(keycloakURL, "/") + "/realms/" + realm}
	if raw := commonCfg.EnvOrDefault("KEYCLOAK_ISSUERS", ""); raw != "" {
		issuers = nil
		for _, s := range strings.Split(raw, ",") {
			if s = strings.TrimSpace(s); s != "" {
				issuers = append(issuers, s)
			}
		}
	}

	return Config{
		Port:            commonCfg.EnvOrDefault("PORT", "3000"),
		AppEnv:          commonCfg.EnvOrDefault("APP_ENV", commonCfg.EnvOrDefault("NODE_ENV", "development")),
		DatabaseURL:     commonCfg.DatabaseURL(),
		KafkaBrokers:    commonCfg.EnvOrDefault("KAFKA_BROKERS", "localhost:9092"),
		WSConsumerGroup: commonCfg.EnvOrDefault("KAFKA_WS_CONSUMER_GROUP", "sentinel-gateway-ws-"+hostname()),
		CORSOrigin:      commonCfg.EnvOrDefault("CORS_ORIGIN", "http://localhost:4200"),

		AuthDevBypass:    commonCfg.EnvOrDefaultBool("AUTH_DEV_BYPASS", false),
		KeycloakURL:      keycloakURL,
		KeycloakRealm:    realm,
		KeycloakIssuers:  issuers,
		KeycloakAudience: commonCfg.EnvOrDefault("KEYCLOAK_AUDIENCE", "sentinel-api"),
		KeycloakClientID: commonCfg.EnvOrDefault("KEYCLOAK_CLIENT_ID", "sentinel-api"),

		KeycloakServiceClientID:     commonCfg.EnvOrDefault("KEYCLOAK_SERVICE_CLIENT_ID", "sentinel-service"),
		KeycloakServiceClientSecret: commonCfg.EnvOrDefault("KEYCLOAK_SERVICE_CLIENT_SECRET", "sentinel-service-secret-dev"),
		MailHost:                    commonCfg.EnvOrDefault("MAIL_HOST", "localhost"),
		MailPort:                    commonCfg.EnvOrDefault("MAIL_PORT", "1025"),
		MailFrom:                    commonCfg.EnvOrDefault("MAIL_FROM", "\"SENTINEL Platform\" <sentinel@sentinel.local>"),
		MailUsername:                commonCfg.EnvOrDefault("MAIL_USERNAME", ""),
		MailPassword:                commonCfg.EnvOrDefault("MAIL_PASSWORD", ""),
		MailTLS:                     strings.ToLower(strings.TrimSpace(commonCfg.EnvOrDefault("MAIL_TLS", ""))),

		ViewportSyncEnabled:   commonCfg.EnvOrDefaultBool("VIEWPORT_SYNC_ENABLED", true),
		ViewportSyncHeartbeat: time.Duration(commonCfg.EnvOrDefaultInt("VIEWPORT_SYNC_HEARTBEAT_SEC", 30)) * time.Second,
		ViewportSyncDebounce:  time.Duration(commonCfg.EnvOrDefaultInt("VIEWPORT_SYNC_DEBOUNCE_MS", 1000)) * time.Millisecond,

		EntityServiceURL:       commonCfg.EnvOrDefault("ENTITY_SERVICE_URL", "http://localhost:3001"),
		TrackServiceURL:        commonCfg.EnvOrDefault("TRACK_SERVICE_URL", "http://localhost:3002"),
		SearchServiceURL:       commonCfg.EnvOrDefault("SEARCH_SERVICE_URL", "http://localhost:3003"),
		LinkAnalysisServiceURL: commonCfg.EnvOrDefault("LINK_ANALYSIS_SERVICE_URL", "http://localhost:3004"),
		AlertServiceURL:        commonCfg.EnvOrDefault("ALERT_SERVICE_URL", "http://localhost:3005"),
		AnalyticsServiceURL:    commonCfg.EnvOrDefault("ANALYTICS_SERVICE_URL", "http://localhost:5000"),
		IngestServiceURL:       commonCfg.EnvOrDefault("INGEST_SERVICE_URL", "http://localhost:4000"),
		IngestServiceToken:     commonCfg.EnvOrDefault("INGEST_SERVICE_TOKEN", ""),
		IntelSkyBaseURL:        commonCfg.EnvOrDefault("INTELSKY_BASE_URL", "https://intelsky.org"),
	}
}

func hostname() string {
	if h, err := os.Hostname(); err == nil && h != "" {
		return h
	}
	return "local"
}
