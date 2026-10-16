package config

import "testing"

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
