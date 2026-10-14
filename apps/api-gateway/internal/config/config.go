package config

import (
	"strings"

	commonCfg "github.com/sentinel/go-common/config"
)

// Config holds the API gateway configuration.
type Config struct {
	Port        string
	AppEnv      string
	DatabaseURL string
	KafkaBrokers string
	CORSOrigin  string

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

	// Downstream services
	EntityServiceURL       string
	TrackServiceURL        string
	SearchServiceURL       string
	LinkAnalysisServiceURL string
	AlertServiceURL        string
	IngestServiceURL       string
}

// IsProduction reports whether the gateway runs with production hardening.
func (c Config) IsProduction() bool { return c.AppEnv == "production" }

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
		Port:         commonCfg.EnvOrDefault("PORT", "3000"),
		AppEnv:       commonCfg.EnvOrDefault("APP_ENV", commonCfg.EnvOrDefault("NODE_ENV", "development")),
		DatabaseURL:  commonCfg.DatabaseURL(),
		KafkaBrokers: commonCfg.EnvOrDefault("KAFKA_BROKERS", "localhost:9092"),
		CORSOrigin:   commonCfg.EnvOrDefault("CORS_ORIGIN", "http://localhost:4200"),

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

		EntityServiceURL:       commonCfg.EnvOrDefault("ENTITY_SERVICE_URL", "http://localhost:3001"),
		TrackServiceURL:        commonCfg.EnvOrDefault("TRACK_SERVICE_URL", "http://localhost:3002"),
		SearchServiceURL:       commonCfg.EnvOrDefault("SEARCH_SERVICE_URL", "http://localhost:3003"),
		LinkAnalysisServiceURL: commonCfg.EnvOrDefault("LINK_ANALYSIS_SERVICE_URL", "http://localhost:3004"),
		AlertServiceURL:        commonCfg.EnvOrDefault("ALERT_SERVICE_URL", "http://localhost:3005"),
		IngestServiceURL:       commonCfg.EnvOrDefault("INGEST_SERVICE_URL", "http://localhost:4000"),
	}
}
