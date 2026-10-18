package config

import (
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
