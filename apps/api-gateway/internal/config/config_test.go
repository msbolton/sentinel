package config

import (
	"reflect"
	"strings"
	"testing"
	"time"
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

var demoVars = []string{
	"DEMO_ENABLED", "DEMO_MAX_GUESTS", "DEMO_GUEST_TTL", "DEMO_OFFLINE_REMOVE_AFTER",
	"DEMO_STATUS_ORIGIN", "DEMO_INGEST_ADDR", "DEMO_INGEST_TOKEN", "DEMO_INGEST_INSECURE",
	"DEMO_VISITORS_FEED_ID", "DEMO_GEOIP_DB", "DEMO_SESSION_RATE",
}

func clearDemoEnv(t *testing.T) {
	t.Helper()
	for _, k := range demoVars {
		t.Setenv(k, "")
	}
	t.Setenv("AUTH_DEV_BYPASS", "")
}

// demoConfig is a dev configuration with the demo enabled that passes every check.
func demoConfig() Config {
	return Config{Demo: DemoConfig{
		Enabled:            true,
		MaxGuests:          25,
		GuestTTL:           time.Hour,
		OfflineRemoveAfter: 5 * time.Minute,
		StatusOrigin:       "https://msbolton.dev",
		IngestAddr:         "ingest-service:6202",
		IngestToken:        "si_secret-token",
		IngestInsecure:     true,
		VisitorsFeedID:     "demo-visitors",
		SessionRate:        "5/10m",
	}}
}

func TestDemoDefaults(t *testing.T) {
	clearDemoEnv(t)
	d := Load().Demo
	want := DemoConfig{
		Enabled:            false,
		MaxGuests:          25,
		GuestTTL:           60 * time.Minute,
		OfflineRemoveAfter: 5 * time.Minute,
		StatusOrigin:       "https://msbolton.dev",
		IngestInsecure:     true,
		VisitorsFeedID:     "demo-visitors",
		SessionRate:        "5/10m",
	}
	if !reflect.DeepEqual(d, want) {
		t.Errorf("demo defaults =\n%+v\nwant\n%+v", d, want)
	}
	n, window, err := d.SessionLimit()
	if err != nil || n != 5 || window != 10*time.Minute {
		t.Errorf("session limit = %d/%v, %v", n, window, err)
	}
}

func TestLoadDemoSettings(t *testing.T) {
	clearDemoEnv(t)
	t.Setenv("DEMO_ENABLED", "true")
	t.Setenv("DEMO_MAX_GUESTS", "7")
	t.Setenv("DEMO_GUEST_TTL", "30m")
	t.Setenv("DEMO_OFFLINE_REMOVE_AFTER", "90s")
	t.Setenv("DEMO_STATUS_ORIGIN", "https://example.org")
	t.Setenv("DEMO_INGEST_ADDR", "ingest:6202")
	t.Setenv("DEMO_INGEST_TOKEN", "si_tok")
	t.Setenv("DEMO_INGEST_INSECURE", "false")
	t.Setenv("DEMO_VISITORS_FEED_ID", "visitors")
	t.Setenv("DEMO_GEOIP_DB", "/data/city.mmdb")
	t.Setenv("DEMO_SESSION_RATE", "3/1h")
	cfg := Load()
	want := DemoConfig{
		Enabled:            true,
		MaxGuests:          7,
		GuestTTL:           30 * time.Minute,
		OfflineRemoveAfter: 90 * time.Second,
		StatusOrigin:       "https://example.org",
		IngestAddr:         "ingest:6202",
		IngestToken:        "si_tok",
		IngestInsecure:     false,
		VisitorsFeedID:     "visitors",
		GeoIPDB:            "/data/city.mmdb",
		SessionRate:        "3/1h",
	}
	if !reflect.DeepEqual(cfg.Demo, want) {
		t.Errorf("demo =\n%+v\nwant\n%+v", cfg.Demo, want)
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("valid demo settings rejected: %v", err)
	}
}

func TestDemoEnabledRequiresIngestAddrAndToken(t *testing.T) {
	cfg := demoConfig()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("valid demo config rejected: %v", err)
	}
	cfg.Demo.IngestAddr = ""
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "DEMO_INGEST_ADDR") {
		t.Errorf("missing addr: err = %v", err)
	}
	cfg = demoConfig()
	cfg.Demo.IngestToken = ""
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "DEMO_INGEST_TOKEN") {
		t.Errorf("missing token: err = %v", err)
	}
}

func TestDemoDisabledIgnoresDemoVars(t *testing.T) {
	clearDemoEnv(t)
	t.Setenv("DEMO_MAX_GUESTS", "0")
	t.Setenv("DEMO_GUEST_TTL", "forever")
	t.Setenv("DEMO_OFFLINE_REMOVE_AFTER", "-1m")
	t.Setenv("DEMO_SESSION_RATE", "lots")
	t.Setenv("AUTH_DEV_BYPASS", "true")
	cfg := Load()
	if cfg.Demo.Enabled {
		t.Fatal("demo must be off by default")
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("demo vars must be ignored while the demo is off: %v", err)
	}
}

func TestDemoEnabledRejectsDevBypass(t *testing.T) {
	cfg := demoConfig()
	cfg.AuthDevBypass = true
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "AUTH_DEV_BYPASS") {
		t.Errorf("err = %v", err)
	}
}

func TestDemoValidatesLimits(t *testing.T) {
	cases := map[string]func(*DemoConfig){
		"DEMO_MAX_GUESTS":           func(d *DemoConfig) { d.MaxGuests = 0 },
		"DEMO_GUEST_TTL":            func(d *DemoConfig) { d.GuestTTL = 0 },
		"DEMO_OFFLINE_REMOVE_AFTER": func(d *DemoConfig) { d.OfflineRemoveAfter = -time.Minute },
		"DEMO_SESSION_RATE":         func(d *DemoConfig) { d.SessionRate = "0/10m" },
	}
	for name, mutate := range cases {
		cfg := demoConfig()
		mutate(&cfg.Demo)
		err := cfg.Validate()
		if err == nil || !strings.Contains(err.Error(), name) {
			t.Errorf("%s: err = %v", name, err)
		}
		if err != nil && strings.Contains(err.Error(), cfg.Demo.IngestToken) {
			t.Errorf("%s: error leaks the ingest token: %v", name, err)
		}
	}
}

func TestDemoRejectsUnparseableValues(t *testing.T) {
	for name, value := range map[string]string{
		"DEMO_MAX_GUESTS":           "many",
		"DEMO_GUEST_TTL":            "an hour",
		"DEMO_OFFLINE_REMOVE_AFTER": "5",
	} {
		t.Run(name, func(t *testing.T) {
			clearDemoEnv(t)
			t.Setenv("DEMO_ENABLED", "true")
			t.Setenv("DEMO_INGEST_ADDR", "ingest:6202")
			t.Setenv("DEMO_INGEST_TOKEN", "si_tok")
			t.Setenv(name, value)
			err := Load().Validate()
			if err == nil || !strings.Contains(err.Error(), name) {
				t.Errorf("err = %v", err)
			}
		})
	}
}

func TestParseRate(t *testing.T) {
	good := map[string]struct {
		n      int
		window time.Duration
	}{
		"5/10m":     {5, 10 * time.Minute},
		" 1/1s ":    {1, time.Second},
		"100/1h30m": {100, 90 * time.Minute},
	}
	for in, want := range good {
		n, window, err := ParseRate(in)
		if err != nil || n != want.n || window != want.window {
			t.Errorf("ParseRate(%q) = %d, %v, %v", in, n, window, err)
		}
	}
	for _, in := range []string{"", "5", "5/", "/10m", "0/10m", "-1/10m", "x/10m", "5/0s", "5/-1m", "5/ten", "5/10m/2"} {
		if _, _, err := ParseRate(in); err == nil {
			t.Errorf("ParseRate(%q) accepted", in)
		}
	}
}
