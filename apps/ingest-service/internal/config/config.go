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

	PublisherShards    int // PUBLISHER_SHARDS (default: 8)
	ObservationBuffer  int // OBS_BUFFER (default: 1000) observations queued per publisher shard
	FrameBuffer        int // FRAME_BUFFER (default: 1024) frames queued between a transport and its decoder
	RawPayloadMaxBytes int // RAW_PAYLOAD_MAX_BYTES (default: 4096) raw bytes kept per observation under the bounded policy
	DLQRatePerSec      int // DLQ_RATE_PER_SEC (default: 10) dead letters per feed per second

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
	return &Config{
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
		Listener:              envOr("INGEST_LISTENER", "false") == "true",
		ListenerHost:          envOr("INGEST_LISTENER_HOST", ""),

		PublisherShards:    envOrInt("PUBLISHER_SHARDS", 8),
		ObservationBuffer:  envOrInt("OBS_BUFFER", 1000),
		FrameBuffer:        envOrInt("FRAME_BUFFER", 1024),
		RawPayloadMaxBytes: envOrInt("RAW_PAYLOAD_MAX_BYTES", 4096),
		DLQRatePerSec:      envOrInt("DLQ_RATE_PER_SEC", 10),

		FeedStaleWarn:     envOrSeconds("FEED_STALE_WARN_SEC", 120),
		FeedStaleCritical: envOrSeconds("FEED_STALE_CRITICAL_SEC", 300),
		FeedRetryMin:      envOrSeconds("FEED_RETRY_MIN_SEC", 5),
		FeedRetryMax:      envOrSeconds("FEED_RETRY_MAX_SEC", 60),
		ShutdownTimeout:   envOrSeconds("SHUTDOWN_TIMEOUT_SEC", 25),

		LeaseEnabled:   envOr("LEASE_ENABLED", "true") != "false",
		LeaseTTL:       envOrSeconds("LEASE_TTL_SEC", 15),
		LeaseTick:      envOrSeconds("LEASE_TICK_SEC", 2),
		LeaseRebalance: envOrSeconds("LEASE_REBALANCE_SEC", 30),
	}
}

// Validate rejects configurations that must not start.
func (c *Config) Validate() error {
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
	return nil
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

func envOrInt(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

func envOrSeconds(key string, def int) time.Duration {
	return time.Duration(envOrInt(key, def)) * time.Second
}
