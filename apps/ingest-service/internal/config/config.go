package config

import (
	"errors"
	"os"
	"strconv"
	"strings"
)

// Config holds the ingest service's process-level configuration. Per-feed
// settings (poll intervals, brokers, credentials references) live in the
// feeds table; the driver factories copy them into the listener fields
// below for each feed they build.
type Config struct {
	KafkaBrokers          string // KAFKA_BROKERS (default: localhost:9092)
	KafkaObservationTopic string // KAFKA_OBSERVATION_TOPIC (default: observations.raw)
	KafkaDeadLetterTopic  string // KAFKA_DEADLETTER_TOPIC (default: observations.dlq)
	PublisherShards    int // PUBLISHER_SHARDS (default: 8)
	ObservationBuffer  int // OBS_BUFFER (default: 1000) observations queued per publisher shard
	RawPayloadMaxBytes int // RAW_PAYLOAD_MAX_BYTES (default: 4096) raw bytes kept per observation, 0 keeps none
	DLQRatePerSec      int // DLQ_RATE_PER_SEC (default: 10) dead letters per feed per second
	HTTPAddr           string // HTTP_ADDR (default: :4000)
	WorkerPoolSize   int    // WORKER_POOL_SIZE (default: 100)
	BatchSize        int    // BATCH_SIZE (default: 500)
	FlushIntervalMs  int    // FLUSH_INTERVAL_MS (default: 100)
	DatabaseURL      string // DATABASE_URL (default: postgres://sentinel:sentinel_dev@localhost:5432/sentinel?sslmode=disable)

	AppEnv       string // APP_ENV (default: development)
	ServiceToken string // INGEST_SERVICE_TOKEN; required in production, presented by the API gateway
	ReplicaID    string // REPLICA_ID (default: hostname)
	// FeedsBootstrapEnabled lists built-in feeds to insert enabled when the
	// feeds table does not have them yet (FEEDS_BOOTSTRAP_ENABLED, comma-separated).
	FeedsBootstrapEnabled []string

	FeedStaleWarnSec     int // FEED_STALE_WARN_SEC (default: 120) default health thresholds
	FeedStaleCriticalSec int // FEED_STALE_CRITICAL_SEC (default: 300)

	// Listener settings. Only OpenSkyTokenURL is read from the environment
	// (OPENSKY_TOKEN_URL); the rest are filled per feed from its config and
	// resolved secrets by the driver factories.
	OpenSkyIntervalSec       int
	OpenSkyBBox              string
	OpenSkyClientID          string
	OpenSkyClientSecret      string
	OpenSkyTokenURL          string
	OpenSkyViewportMode      bool
	OpenSkyViewportTTLSec    int
	OpenSkyMinPollSpacingSec int
	OpenSkyMaxAreaSqDeg      float64
	OpenSkyMaxIntervalSec    int
	OpenSkyCreditFloor       int

	ADSBLolIntervalSec int

	CelesTrakGroups                 string
	CelesTrakTLERefreshHours        int
	CelesTrakPropagationIntervalSec int
}

// Load reads configuration from environment variables, applying defaults
// where no value is set.
func Load() *Config {
	hostname, _ := os.Hostname()
	if hostname == "" {
		hostname = "local"
	}
	return &Config{
		KafkaBrokers:          envOrDefault("KAFKA_BROKERS", "localhost:9092"),
		KafkaObservationTopic: envOrDefault("KAFKA_OBSERVATION_TOPIC", "observations.raw"),
		KafkaDeadLetterTopic:  envOrDefault("KAFKA_DEADLETTER_TOPIC", "observations.dlq"),
		PublisherShards:       envOrDefaultInt("PUBLISHER_SHARDS", 8),
		ObservationBuffer:     envOrDefaultInt("OBS_BUFFER", 1000),
		RawPayloadMaxBytes:    envOrDefaultInt("RAW_PAYLOAD_MAX_BYTES", 4096),
		DLQRatePerSec:         envOrDefaultInt("DLQ_RATE_PER_SEC", 10),
		HTTPAddr:              envOrDefault("HTTP_ADDR", ":4000"),
		WorkerPoolSize:   envOrDefaultInt("WORKER_POOL_SIZE", 100),
		BatchSize:        envOrDefaultInt("BATCH_SIZE", 500),
		FlushIntervalMs:  envOrDefaultInt("FLUSH_INTERVAL_MS", 100),
		DatabaseURL:      envOrDefault("DATABASE_URL", "postgres://sentinel:sentinel_dev@localhost:5432/sentinel?sslmode=disable"),

		AppEnv:                envOrDefault("APP_ENV", "development"),
		ServiceToken:          envOrDefault("INGEST_SERVICE_TOKEN", ""),
		ReplicaID:             envOrDefault("REPLICA_ID", hostname),
		FeedsBootstrapEnabled: splitList(envOrDefault("FEEDS_BOOTSTRAP_ENABLED", "")),

		FeedStaleWarnSec:     envOrDefaultInt("FEED_STALE_WARN_SEC", 120),
		FeedStaleCriticalSec: envOrDefaultInt("FEED_STALE_CRITICAL_SEC", 300),

		OpenSkyTokenURL: envOrDefault("OPENSKY_TOKEN_URL", "https://auth.opensky-network.org/auth/realms/opensky-network/protocol/openid-connect/token"),
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
	if c.PublisherShards <= 0 || c.ObservationBuffer <= 0 || c.RawPayloadMaxBytes < 0 {
		return errors.New("PUBLISHER_SHARDS and OBS_BUFFER must be positive and RAW_PAYLOAD_MAX_BYTES non-negative")
	}
	if c.DatabaseURL == "" {
		return errors.New("DATABASE_URL is required")
	}
	if c.BatchSize <= 0 || c.FlushIntervalMs <= 0 || c.WorkerPoolSize <= 0 {
		return errors.New("BATCH_SIZE, FLUSH_INTERVAL_MS and WORKER_POOL_SIZE must be positive")
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

func envOrDefault(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}

func envOrDefaultInt(key string, defaultVal int) int {
	val := os.Getenv(key)
	if val == "" {
		return defaultVal
	}
	parsed, err := strconv.Atoi(val)
	if err != nil {
		return defaultVal
	}
	return parsed
}
