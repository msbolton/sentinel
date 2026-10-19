package config

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/sentinel/ingest-service/internal/registry"
)

func TestDefaultsAreValid(t *testing.T) {
	if err := Load().Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestValidateRejects(t *testing.T) {
	base := Load()
	cases := map[string]func(c *Config){
		"prod without token": func(c *Config) { c.AppEnv = "production"; c.ServiceToken = "" },
		"no brokers":         func(c *Config) { c.KafkaBrokers = "" },
		"zero shards":        func(c *Config) { c.PublisherShards = 0 },
		"warn above crit":    func(c *Config) { c.FeedStaleWarn = c.FeedStaleCritical },
		"retry inverted":     func(c *Config) { c.FeedRetryMin = c.FeedRetryMax * 2 },
		"zero shutdown":      func(c *Config) { c.ShutdownTimeout = 0 },
		"tick above ttl/2":   func(c *Config) { c.LeaseTick = c.LeaseTTL },
	}
	for name, mutate := range cases {
		c := *base
		mutate(&c)
		if err := c.Validate(); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestReservedPortsDefaultAndOverride(t *testing.T) {
	if got := Load().ReservedPorts; got != registry.DefaultReservedPorts {
		t.Errorf("default = %q", got)
	}
	t.Setenv("INGEST_RESERVED_PORTS", "7000-7002")
	if got := Load().ReservedPorts; got != "7000-7002" {
		t.Errorf("override = %q", got)
	}
}

func TestListenerSharedDefaultsToFalse(t *testing.T) {
	t.Setenv("INGEST_LISTENER_SHARED", "") // an empty value reads as unset
	if Load().ListenerShared {
		t.Error("a listener replica must be dedicated unless INGEST_LISTENER_SHARED says otherwise")
	}
	t.Setenv("INGEST_LISTENER_SHARED", "true")
	if !Load().ListenerShared {
		t.Error("INGEST_LISTENER_SHARED=true is not read")
	}
}

// Sharing is a property of a listener, so setting it on a replica that is not
// one points at the wrong service or a forgotten INGEST_LISTENER.
func TestListenerSharedNeedsListener(t *testing.T) {
	for _, tc := range []struct {
		name             string
		listener, shared bool
		wantErr          bool
	}{
		{"dedicated listener", true, false, false},
		{"shared listener", true, true, false},
		{"ordinary replica", false, false, false},
		{"shared without a listener", false, true, true},
	} {
		c := Load()
		c.Listener, c.ListenerShared = tc.listener, tc.shared
		err := c.Validate()
		if (err != nil) != tc.wantErr {
			t.Errorf("%s: Validate() = %v, want an error: %v", tc.name, err, tc.wantErr)
		}
		if err != nil && !strings.Contains(err.Error(), "INGEST_LISTENER_SHARED") {
			t.Errorf("%s: the error does not name the variable: %v", tc.name, err)
		}
	}
}

func TestDLQPeekMaxScan(t *testing.T) {
	t.Setenv("DLQ_PEEK_MAX_SCAN", "") // an empty value reads as unset
	if got := Load().DLQPeekMaxScan; got != 2000 {
		t.Errorf("default = %d, want 2000 messages per partition", got)
	}
	t.Setenv("DLQ_PEEK_MAX_SCAN", "500")
	if got := Load().DLQPeekMaxScan; got != 500 {
		t.Errorf("override = %d, want 500", got)
	}
	if err := Load().Validate(); err != nil {
		t.Errorf("a positive scan bound is valid: %v", err)
	}

	// Zero turns dead-letter viewing off. It is a setting, not a missing value:
	// the default does not replace it.
	t.Setenv("DLQ_PEEK_MAX_SCAN", "0")
	c := Load()
	if c.DLQPeekMaxScan != 0 {
		t.Errorf("DLQ_PEEK_MAX_SCAN=0 reads as %d, want 0", c.DLQPeekMaxScan)
	}
	if err := c.Validate(); err != nil {
		t.Errorf("DLQ_PEEK_MAX_SCAN=0 turns the endpoint off and must be valid: %v", err)
	}

	t.Setenv("DLQ_PEEK_MAX_SCAN", "-1")
	if err := Load().Validate(); err == nil || !strings.Contains(err.Error(), "DLQ_PEEK_MAX_SCAN") {
		t.Errorf("DLQ_PEEK_MAX_SCAN=-1: Validate() = %v, want an error that names the variable", err)
	}
}

// Config fills the secret policy, so a deployment that sets nothing is not
// unrestricted (a zero registry.SecretPolicy would be).
func TestSecretPolicyDefaultsAreNotEmpty(t *testing.T) {
	c := Load()
	if got, want := strings.Join(c.SecretEnvAllow, ","), "OPENSKY_*,AISSTREAM_*,SENTINEL_SECRET_*"; got != want {
		t.Errorf("INGEST_SECRET_ENV_ALLOW default = %q, want %q", got, want)
	}
	if got, want := strings.Join(c.SecretFileDirs, ","), "/etc/sentinel"; got != want {
		t.Errorf("INGEST_SECRET_FILE_DIRS default = %q, want %q", got, want)
	}
	p := c.SecretPolicy()
	if len(p.EnvAllow) == 0 || len(p.FileDirs) == 0 {
		t.Fatalf("policy %+v", p)
	}
	if p.Check("env:DATABASE_URL") == nil || p.Check("env:OPENSKY_CLIENT_ID") != nil || p.Check("file:/etc/sentinel/tak/c.pem") != nil || p.Check("file:/etc/passwd") == nil {
		t.Error("the default policy does not do what the defaults promise")
	}
}

func TestSecretPolicyFromEnv(t *testing.T) {
	t.Setenv("INGEST_SECRET_ENV_ALLOW", " MY_*, DB_PASSWORD ,")
	t.Setenv("INGEST_SECRET_FILE_DIRS", "/etc/sentinel,/run/secrets")
	c := Load()
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	p := c.SecretPolicy()
	if p.Check("env:MY_KEY") != nil || p.Check("env:DB_PASSWORD") != nil || p.Check("env:OPENSKY_CLIENT_ID") == nil || p.Check("file:/run/secrets/k") != nil {
		t.Errorf("policy %+v", p)
	}
	t.Setenv("INGEST_SECRET_ENV_ALLOW", "*")
	if p := Load().SecretPolicy(); p.Check("env:ANYTHING") != nil {
		t.Error("a lone * allows any name")
	}
}

func TestSecretPolicyMustBeValid(t *testing.T) {
	for name, env := range map[string][2]string{
		"relative folder": {"INGEST_SECRET_FILE_DIRS", "etc/sentinel"},
		"bad name":        {"INGEST_SECRET_ENV_ALLOW", "my-var"},
		"mid-name star":   {"INGEST_SECRET_ENV_ALLOW", "A*B"},
	} {
		t.Setenv(env[0], env[1])
		err := Load().Validate()
		if err == nil || !strings.Contains(err.Error(), env[0]) {
			t.Errorf("%s: %v", name, err)
		}
		t.Setenv(env[0], "")
	}
}

// INGEST_TLS_RELOAD_SEC is how often feeds with TLS read their certificates
// again: 60 seconds unless set, and 0 turns it off.
func TestTLSReloadSec(t *testing.T) {
	if got := Load().TLSReloadSec; got != 60 {
		t.Errorf("INGEST_TLS_RELOAD_SEC default = %d, want 60", got)
	}
	t.Setenv("INGEST_TLS_RELOAD_SEC", "0")
	c := Load()
	if c.TLSReloadSec != 0 || c.Validate() != nil {
		t.Errorf("INGEST_TLS_RELOAD_SEC=0: %d, Validate() = %v; want 0 (off) and valid", c.TLSReloadSec, c.Validate())
	}
	t.Setenv("INGEST_TLS_RELOAD_SEC", "15")
	if got := Load().TLSReloadSec; got != 15 {
		t.Errorf("INGEST_TLS_RELOAD_SEC=15 reads as %d", got)
	}
	t.Setenv("INGEST_TLS_RELOAD_SEC", "-5")
	if err := Load().Validate(); err == nil || !strings.Contains(err.Error(), "INGEST_TLS_RELOAD_SEC") {
		t.Errorf("INGEST_TLS_RELOAD_SEC=-5: Validate() = %v, want an error that names the variable", err)
	}
}

// An integer setting that is not an integer is refused, naming the variable. It
// used to fall back to the default without a word, so DLQ_PEEK_MAX_SCAN=off kept
// viewing dead letters on. The variables are found in the source, so a setting
// added later is held to this too.
func TestIntegerSettingsRefuseText(t *testing.T) {
	src, err := os.ReadFile("config.go")
	if err != nil {
		t.Fatal(err)
	}
	found := regexp.MustCompile(`\.(?:int|seconds)\("([A-Z0-9_]+)"`).FindAllStringSubmatch(string(src), -1)
	if len(found) < 15 {
		t.Fatalf("found %d integer settings in config.go, want the 15 or more it has: the pattern is out of date", len(found))
	}
	for _, m := range found {
		name := m[1]
		t.Run(name, func(t *testing.T) {
			for _, bad := range []string{"off", "1.5", "12s", "0x10"} {
				t.Setenv(name, bad)
				err := Load().Validate()
				if err == nil || !strings.Contains(err.Error(), name) || !strings.Contains(err.Error(), "integer") {
					t.Errorf("%s=%q: Validate() = %v, want an error that names the variable and says integer", name, bad, err)
				}
			}
			t.Setenv(name, "")
			if err := Load().Validate(); err != nil {
				t.Errorf("%s unset: %v", name, err)
			}
		})
	}
	t.Setenv("DLQ_PEEK_MAX_SCAN", "0")
	if c := Load(); c.Validate() != nil || c.DLQPeekMaxScan != 0 {
		t.Errorf("DLQ_PEEK_MAX_SCAN=0 must stay valid and read as 0: %d, %v", c.DLQPeekMaxScan, c.Validate())
	}
}

// A boolean setting is read as strconv.ParseBool does and refused, naming the
// variable, when it is anything else: "ture" used to read as false for
// INGEST_LISTENER and as true for LEASE_ENABLED. The variables are found in
// the source, so a setting added later is held to this too.
func TestBooleanSettingsRefuseText(t *testing.T) {
	src, err := os.ReadFile("config.go")
	if err != nil {
		t.Fatal(err)
	}
	found := regexp.MustCompile(`\.bool\("([A-Z0-9_]+)"`).FindAllStringSubmatch(string(src), -1)
	if len(found) < 3 {
		t.Fatalf("found %d boolean settings in config.go, want the 3 or more it has: the pattern is out of date", len(found))
	}
	for _, m := range found {
		name := m[1]
		t.Run(name, func(t *testing.T) {
			for _, bad := range []string{"ture", "yes", "on", "2", "enabled"} {
				t.Setenv(name, bad)
				err := Load().Validate()
				if err == nil || !strings.Contains(err.Error(), name) || !strings.Contains(err.Error(), "true or false") {
					t.Errorf("%s=%q: Validate() = %v, want an error that names the variable and says true or false", name, bad, err)
				}
			}
			t.Setenv(name, "")
			if err := Load().Validate(); err != nil {
				t.Errorf("%s unset: %v", name, err)
			}
		})
	}
}

func TestBooleanSettingsAcceptWhatParseBoolDoes(t *testing.T) {
	for v, want := range map[string]bool{"true": true, "TRUE": true, "True": true, "t": true, "1": true, "false": false, "False": false, "f": false, "0": false} {
		t.Setenv("INGEST_LISTENER", v)
		t.Setenv("LEASE_ENABLED", "true")
		if got := Load().Listener; got != want {
			t.Errorf("INGEST_LISTENER=%q read as %v, want %v", v, got, want)
		}
	}
	t.Setenv("INGEST_LISTENER", "")
	for v, want := range map[string]bool{"0": false, "F": false, "false": false, "1": true, "true": true} {
		t.Setenv("LEASE_ENABLED", v)
		if got := Load().LeaseEnabled; got != want {
			t.Errorf("LEASE_ENABLED=%q read as %v, want %v", v, got, want)
		}
	}
}

// A listener without leases would run every feed, so it is refused, naming the
// variable; an ordinary replica may still run without leases.
func TestListenerNeedsLeases(t *testing.T) {
	for _, tc := range []struct {
		name             string
		listener, shared bool
		leases, wantErr  bool
	}{
		{"dedicated listener, leases", true, false, true, false},
		{"dedicated listener, no leases", true, false, false, true},
		{"shared listener, no leases", true, true, false, true},
		{"ordinary replica, no leases", false, false, false, false},
	} {
		c := Load()
		c.Listener, c.ListenerShared, c.LeaseEnabled = tc.listener, tc.shared, tc.leases
		err := c.Validate()
		if (err != nil) != tc.wantErr {
			t.Errorf("%s: Validate() = %v, want an error: %v", tc.name, err, tc.wantErr)
		}
		if err != nil && !strings.Contains(err.Error(), "LEASE_ENABLED") {
			t.Errorf("%s: the error does not name LEASE_ENABLED: %v", tc.name, err)
		}
	}
}

// A listener with no host is valid, but the send guide would show senders a
// blank address, so the service warns.
func TestWarnsOfAListenerWithoutAHost(t *testing.T) {
	c := Load()
	c.Listener, c.ListenerHost = true, ""
	if w := c.Warnings(); len(w) != 1 || !strings.Contains(w[0], "INGEST_LISTENER_HOST") {
		t.Errorf("warnings = %v, want one naming INGEST_LISTENER_HOST", w)
	}
	c.ListenerHost = "ingest.example.com"
	if w := c.Warnings(); len(w) != 0 {
		t.Errorf("warnings with a host = %v, want none", w)
	}
	c.Listener, c.ListenerHost = false, ""
	if w := c.Warnings(); len(w) != 0 {
		t.Errorf("warnings for an ordinary replica = %v, want none", w)
	}
}

// INGEST_RESERVED_PORTS is checked with the rest of the configuration, before
// anything starts, and the parsed set is what the management API gets.
func TestReservedPortsAreValidated(t *testing.T) {
	t.Setenv("INGEST_RESERVED_PORTS", "7000-7002, 9000")
	c := Load()
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	set, err := c.ReservedPortSet()
	if err != nil || !set[7001] || !set[9000] || set[7003] {
		t.Errorf("ReservedPortSet() = %v, %v", set.Ranges(), err)
	}
	for _, bad := range []string{"70000", "abc", "9000-8000", "5000-"} {
		t.Setenv("INGEST_RESERVED_PORTS", bad)
		if err := Load().Validate(); err == nil || !strings.Contains(err.Error(), "INGEST_RESERVED_PORTS") {
			t.Errorf("INGEST_RESERVED_PORTS=%q: Validate() = %v, want an error that names the variable", bad, err)
		}
	}
}
