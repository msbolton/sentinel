package config

import (
	"strings"
	"testing"
)

// prodConfig is a production configuration that passes every check.
func prodConfig() Config {
	return Config{
		AppEnv:                      "production",
		KeycloakServiceClientSecret: "a-real-secret",
		MailTLS:                     "starttls",
		MailPassword:                "pw",
		EntityServiceURL:            "http://entity-service:3001",
		TrackServiceURL:             "http://track-service:3002",
		SearchServiceURL:            "http://search-service:3003",
		LinkAnalysisServiceURL:      "http://link-analysis-service:3004",
		AlertServiceURL:             "http://alert-service:3005",
		AnalyticsServiceURL:         "http://analytics-service:5000",
		IngestServiceURL:            "http://ingest-service:4000",
	}
}

func TestLoadMailSettings(t *testing.T) {
	t.Setenv("MAIL_USERNAME", "relay")
	t.Setenv("MAIL_PASSWORD", "s3cret")
	t.Setenv("MAIL_TLS", "STARTTLS")
	cfg := Load()
	if cfg.MailUsername != "relay" || cfg.MailPassword != "s3cret" || cfg.MailTLS != "starttls" {
		t.Errorf("mail = %q/%q/%q", cfg.MailUsername, cfg.MailPassword, cfg.MailTLS)
	}
}

func TestLoadMailDefaultsKeepDevBehaviour(t *testing.T) {
	for _, k := range []string{"MAIL_USERNAME", "MAIL_PASSWORD", "MAIL_TLS"} {
		t.Setenv(k, "")
	}
	cfg := Load()
	if cfg.MailUsername != "" || cfg.MailPassword != "" || cfg.MailTLS != "" {
		t.Errorf("mail defaults = %q/%q/%q", cfg.MailUsername, cfg.MailPassword, cfg.MailTLS)
	}
}

func TestValidateAcceptsDevDefaults(t *testing.T) {
	t.Setenv("APP_ENV", "")
	t.Setenv("NODE_ENV", "")
	t.Setenv("KEYCLOAK_SERVICE_CLIENT_SECRET", "")
	t.Setenv("MAIL_TLS", "")
	cfg := Load()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("dev defaults must start: %v", err)
	}
	if w := cfg.Warnings(); len(w) != 0 {
		t.Errorf("dev must not warn about localhost: %v", w)
	}
}

func TestValidateAcceptsProduction(t *testing.T) {
	cfg := prodConfig()
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if w := cfg.Warnings(); len(w) != 0 {
		t.Errorf("warnings = %v", w)
	}
}

func TestValidateProductionRejectsWeakServiceSecret(t *testing.T) {
	for _, secret := range []string{"", "sentinel-service-secret-dev"} {
		cfg := prodConfig()
		cfg.KeycloakServiceClientSecret = secret
		err := cfg.Validate()
		if err == nil || !strings.Contains(err.Error(), "KEYCLOAK_SERVICE_CLIENT_SECRET") {
			t.Errorf("secret %q: err = %v", secret, err)
		}
	}
}

func TestValidateProductionRejectsPlaintextMailPassword(t *testing.T) {
	cfg := prodConfig()
	cfg.MailTLS = "none"
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "MAIL_TLS") {
		t.Fatalf("err = %v", err)
	}
	cfg.MailPassword = ""
	if err := cfg.Validate(); err != nil {
		t.Errorf("MAIL_TLS=none without a password is allowed: %v", err)
	}
}

func TestValidateRejectsUnknownMailTLS(t *testing.T) {
	cfg := Config{MailTLS: "maybe"}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "MAIL_TLS") {
		t.Errorf("err = %v", err)
	}
}

func TestDevDoesNotEnforceProductionChecks(t *testing.T) {
	cfg := Config{AppEnv: "development", KeycloakServiceClientSecret: "sentinel-service-secret-dev", MailTLS: "none", MailPassword: "pw"}
	if err := cfg.Validate(); err != nil {
		t.Errorf("dev must start: %v", err)
	}
}

func TestWarningsFlagLocalhostServiceURLsInProduction(t *testing.T) {
	cfg := prodConfig()
	cfg.TrackServiceURL = "http://localhost:3002"
	cfg.IngestServiceURL = "http://127.0.0.1:4000"
	w := strings.Join(cfg.Warnings(), "\n")
	for _, want := range []string{"TRACK_SERVICE_URL", "INGEST_SERVICE_URL"} {
		if !strings.Contains(w, want) {
			t.Errorf("missing warning for %s in %q", want, w)
		}
	}
	if strings.Contains(w, "ENTITY_SERVICE_URL") {
		t.Errorf("unexpected warning: %s", w)
	}
}
