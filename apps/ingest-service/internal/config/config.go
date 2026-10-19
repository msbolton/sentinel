// Package config is the ingest service's process-level configuration. Per
// feed settings (intervals, brokers, credential references) live in the
// feeds table.
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/sentinel/ingest-service/internal/registry"
)

// Config is read from the environment by Load.
type Config struct {
	KafkaBrokers          string // KAFKA_BROKERS (default: localhost:9092)
	KafkaObservationTopic string // KAFKA_OBSERVATION_TOPIC (default: observations.raw)
	KafkaDeadLetterTopic  string // KAFKA_DEADLETTER_TOPIC (default: observations.dlq)
	DatabaseURL           string // DATABASE_URL
	HTTPAddr              string // HTTP_ADDR (default: :4000)

	AppEnv       string // APP_ENV (default: development)
	ServiceToken string // INGEST_SERVICE_TOKEN; required in production, presented by the API gateway
	ReplicaID    string // REPLICA_ID (default: hostname)
	// FeedsBootstrapEnabled lists built-in feeds to insert enabled when the
	// feeds table does not have them yet (FEEDS_BOOTSTRAP_ENABLED, comma-separated).
	FeedsBootstrapEnabled []string
	// ReservedPorts are the ports a listening connection may not take
	// (INGEST_RESERVED_PORTS, e.g. "3000-3005,5432"; default: the stack's own).
	ReservedPorts string
	// Listener makes this replica take the connections that bind a port
	// (INGEST_LISTENER=true; run it with the host's network). ListenerHost
	// is the address senders reach it at (INGEST_LISTENER_HOST).
	Listener     bool
	ListenerHost string
	// ListenerShared lets a listener replica also take its share of the
	// ordinary feeds (INGEST_LISTENER_SHARED=true). Without it the listener
	// runs listening connections and the feeds pinned to it, and nothing else.
	ListenerShared bool
	// SecretEnvAllow and SecretFileDirs limit what a secret reference may name
	// (INGEST_SECRET_ENV_ALLOW, default OPENSKY_*,AISSTREAM_*,SENTINEL_SECRET_*,
	// where a lone * allows any name; INGEST_SECRET_FILE_DIRS, default
	// /etc/sentinel, absolute folders). The defaults cover the built-in feeds,
	// the TAK key material and the listener secrets.
	SecretEnvAllow []string
	SecretFileDirs []string
	// TLSReloadSec is how often (in seconds) a feed with TLS reads its
	// certificates, keys and CAs again, so a rotation takes effect without a
	// restart (INGEST_TLS_RELOAD_SEC, default 60); 0 turns reloading off.
	TLSReloadSec int

	// loadErrs are the integer and boolean settings Load could not read.
	loadErrs []error

	PublisherShards    int // PUBLISHER_SHARDS (default: 8)
	ObservationBuffer  int // OBS_BUFFER (default: 1000) observations queued per publisher shard
	FrameBuffer        int // FRAME_BUFFER (default: 1024) frames queued between a transport and its decoder
	RawPayloadMaxBytes int // RAW_PAYLOAD_MAX_BYTES (default: 4096) raw bytes kept per observation under the bounded policy
	DLQRatePerSec      int // DLQ_RATE_PER_SEC (default: 10) dead letters per feed per second
	DLQPeekMaxScan     int // DLQ_PEEK_MAX_SCAN (default: 2000) messages of each partition read to find a connection's recent dead letters; 0 turns viewing them off

	FeedStaleWarn     time.Duration // FEED_STALE_WARN_SEC (default: 120) default health thresholds
	FeedStaleCritical time.Duration // FEED_STALE_CRITICAL_SEC (default: 300)
	FeedRetryMin      time.Duration // FEED_RETRY_MIN_SEC (default: 5) first restart delay after a feed failure
	FeedRetryMax      time.Duration // FEED_RETRY_MAX_SEC (default: 60)
	ShutdownTimeout   time.Duration // SHUTDOWN_TIMEOUT_SEC (default: 25)

	// Feed leases: each feed runs on exactly one replica.
	LeaseEnabled   bool          // LEASE_ENABLED (default: true); false runs every feed on this replica
	LeaseTTL       time.Duration // LEASE_TTL_SEC (default: 15) lease and replica expiry
	LeaseTick      time.Duration // LEASE_TICK_SEC (default: 2) renew and re-evaluate ownership
	LeaseRebalance time.Duration // LEASE_REBALANCE_SEC (default: 30) hand a feed to its preferred replica after this long
}

// Load reads configuration from environment variables, applying defaults
// where no value is set.
func Load() *Config {
	hostname, _ := os.Hostname()
	if hostname == "" {
		hostname = "local"
	}
	ld := &loader{}
	cfg := &Config{
		KafkaBrokers:          envOr("KAFKA_BROKERS", "localhost:9092"),
		KafkaObservationTopic: envOr("KAFKA_OBSERVATION_TOPIC", "observations.raw"),
		KafkaDeadLetterTopic:  envOr("KAFKA_DEADLETTER_TOPIC", "observations.dlq"),
		DatabaseURL:           envOr("DATABASE_URL", "postgres://sentinel:sentinel_dev@localhost:5432/sentinel?sslmode=disable"),
		HTTPAddr:              envOr("HTTP_ADDR", ":4000"),

		AppEnv:                envOr("APP_ENV", "development"),
		ServiceToken:          envOr("INGEST_SERVICE_TOKEN", ""),
		ReplicaID:             envOr("REPLICA_ID", hostname),
		FeedsBootstrapEnabled: splitList(envOr("FEEDS_BOOTSTRAP_ENABLED", "")),
		ReservedPorts:         envOr("INGEST_RESERVED_PORTS", registry.DefaultReservedPorts),
		Listener:              ld.bool("INGEST_LISTENER", false),
		ListenerHost:          envOr("INGEST_LISTENER_HOST", ""),
		ListenerShared:        ld.bool("INGEST_LISTENER_SHARED", false),
		SecretEnvAllow:        splitList(envOr("INGEST_SECRET_ENV_ALLOW", registry.DefaultSecretEnvAllow)),
		SecretFileDirs:        splitList(envOr("INGEST_SECRET_FILE_DIRS", registry.DefaultSecretFileDirs)),
		TLSReloadSec:          ld.int("INGEST_TLS_RELOAD_SEC", 60),

		PublisherShards:    ld.int("PUBLISHER_SHARDS", 8),
		ObservationBuffer:  ld.int("OBS_BUFFER", 1000),
		FrameBuffer:        ld.int("FRAME_BUFFER", 1024),
		RawPayloadMaxBytes: ld.int("RAW_PAYLOAD_MAX_BYTES", 4096),
		DLQRatePerSec:      ld.int("DLQ_RATE_PER_SEC", 10),
		DLQPeekMaxScan:     ld.int("DLQ_PEEK_MAX_SCAN", 2000),

		FeedStaleWarn:     ld.seconds("FEED_STALE_WARN_SEC", 120),
		FeedStaleCritical: ld.seconds("FEED_STALE_CRITICAL_SEC", 300),
		FeedRetryMin:      ld.seconds("FEED_RETRY_MIN_SEC", 5),
		FeedRetryMax:      ld.seconds("FEED_RETRY_MAX_SEC", 60),
		ShutdownTimeout:   ld.seconds("SHUTDOWN_TIMEOUT_SEC", 25),

		LeaseEnabled:   ld.bool("LEASE_ENABLED", true),
		LeaseTTL:       ld.seconds("LEASE_TTL_SEC", 15),
		LeaseTick:      ld.seconds("LEASE_TICK_SEC", 2),
		LeaseRebalance: ld.seconds("LEASE_REBALANCE_SEC", 30),
	}
	cfg.loadErrs = ld.errs
	return cfg
}

// Validate rejects configurations that must not start.
func (c *Config) Validate() error {
	if len(c.loadErrs) > 0 {
		return errors.Join(c.loadErrs...)
	}
	if c.AppEnv == "production" && c.ServiceToken == "" {
		return errors.New("INGEST_SERVICE_TOKEN is required in production")
	}
	if c.KafkaBrokers == "" {
		return errors.New("KAFKA_BROKERS is required")
	}
	if c.KafkaObservationTopic == "" || c.KafkaDeadLetterTopic == "" {
		return errors.New("KAFKA_OBSERVATION_TOPIC and KAFKA_DEADLETTER_TOPIC are required")
	}
	if c.DatabaseURL == "" {
		return errors.New("DATABASE_URL is required")
	}
	if c.PublisherShards <= 0 || c.ObservationBuffer <= 0 || c.FrameBuffer <= 0 {
		return errors.New("PUBLISHER_SHARDS, OBS_BUFFER and FRAME_BUFFER must be positive")
	}
	if c.RawPayloadMaxBytes < 0 || c.DLQRatePerSec <= 0 {
		return errors.New("RAW_PAYLOAD_MAX_BYTES must be non-negative and DLQ_RATE_PER_SEC positive")
	}
	if c.DLQPeekMaxScan < 0 {
		return errors.New("DLQ_PEEK_MAX_SCAN must not be negative (0 turns dead-letter viewing off)")
	}
	for name, d := range map[string]time.Duration{"FEED_STALE_WARN_SEC": c.FeedStaleWarn, "FEED_STALE_CRITICAL_SEC": c.FeedStaleCritical, "FEED_RETRY_MIN_SEC": c.FeedRetryMin, "FEED_RETRY_MAX_SEC": c.FeedRetryMax, "SHUTDOWN_TIMEOUT_SEC": c.ShutdownTimeout} {
		if d <= 0 {
			return fmt.Errorf("%s must be positive", name)
		}
	}
	if c.FeedStaleWarn >= c.FeedStaleCritical {
		return errors.New("FEED_STALE_WARN_SEC must be below FEED_STALE_CRITICAL_SEC")
	}
	if c.FeedRetryMin > c.FeedRetryMax {
		return errors.New("FEED_RETRY_MIN_SEC must not exceed FEED_RETRY_MAX_SEC")
	}
	if c.LeaseTTL <= 0 || c.LeaseTick <= 0 || c.LeaseRebalance <= 0 {
		return errors.New("LEASE_TTL_SEC, LEASE_TICK_SEC and LEASE_REBALANCE_SEC must be positive")
	}
	if c.LeaseTick*2 > c.LeaseTTL {
		return errors.New("LEASE_TICK_SEC must be at most half of LEASE_TTL_SEC")
	}
	if len(c.SecretEnvAllow) == 0 || len(c.SecretFileDirs) == 0 {
		return errors.New("INGEST_SECRET_ENV_ALLOW and INGEST_SECRET_FILE_DIRS must each list at least one entry (a lone * allows any variable name)")
	}
	if err := c.SecretPolicy().Validate(); err != nil {
		return err
	}
	if c.TLSReloadSec < 0 {
		return errors.New("INGEST_TLS_RELOAD_SEC must not be negative (0 turns TLS reloading off)")
	}
	if _, err := c.ReservedPortSet(); err != nil {
		return err
	}
	if c.ListenerShared && !c.Listener {
		return errors.New("INGEST_LISTENER_SHARED needs INGEST_LISTENER=true: only a listener replica can share")
	}
	// Without leases nothing tells a listener's feeds from the others': it would
	// run every enabled feed, which a dedicated listener must not.
	if (c.Listener || c.ListenerShared) && !c.LeaseEnabled {
		return errors.New("INGEST_LISTENER (or INGEST_LISTENER_SHARED) needs leases: LEASE_ENABLED=false would make the listener run every feed; remove LEASE_ENABLED=false or turn the listener off")
	}
	return nil
}

// Warnings lists settings that are valid but probably not what was meant; the
// service logs them at startup.
func (c *Config) Warnings() []string {
	var out []string
	if c.Listener && c.ListenerHost == "" {
		out = append(out, "INGEST_LISTENER_HOST is empty: the send guide will show a blank host to senders; set it to the address they reach this replica at")
	}
	return out
}

func splitList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// loader reads the integer and boolean settings and remembers the ones that
// cannot be read, for Validate to refuse. A setting that falls back to its default
// when it cannot be read does the opposite of what was asked for (a typo for
// "off" leaves the feature on), so none does.
type loader struct{ errs []error }

// int reads key as an integer, def when it is unset or empty. Text that is not
// an integer reads as def here and is refused by Validate.
func (l *loader) int(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		l.errs = append(l.errs, fmt.Errorf("%s must be an integer, not %q", key, v))
		return def
	}
	return n
}

// bool reads key as strconv.ParseBool does (1, t, T, TRUE, true, True, 0, f, F,
// FALSE, false, False), def when it is unset or empty. Anything else reads as
// def here and is refused by Validate: "ture" must not leave a feature in the
// state its default has.
func (l *loader) bool(key string, def bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		l.errs = append(l.errs, fmt.Errorf("%s must be true or false, not %q", key, v))
		return def
	}
	return b
}

func (l *loader) seconds(key string, def int) time.Duration {
	return time.Duration(l.int(key, def)) * time.Second
}

// ReservedPortSet is the set of ports INGEST_RESERVED_PORTS names. Validate has
// parsed it, so the service can fail on a bad value before it starts anything.
func (c *Config) ReservedPortSet() (registry.PortSet, error) {
	set, err := registry.ParsePortSet(c.ReservedPorts)
	if err != nil {
		return nil, fmt.Errorf("INGEST_RESERVED_PORTS: %w", err)
	}
	return set, nil
}

// SecretPolicy is the policy the secret settings describe.
func (c *Config) SecretPolicy() registry.SecretPolicy {
	return registry.SecretPolicy{EnvAllow: c.SecretEnvAllow, FileDirs: c.SecretFileDirs}
}
