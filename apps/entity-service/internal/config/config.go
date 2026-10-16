package config

import (
	"encoding/json"
	"time"

	commonCfg "github.com/sentinel/go-common/config"
)

// Config holds the entity-service configuration.
type Config struct {
	Port         string
	DatabaseURL  string
	KafkaBrokers string
	CORSOrigin   string

	// ConsumerBatchSize and ConsumerMaxWait bound one fusion batch.
	ConsumerBatchSize int
	ConsumerMaxWait   time.Duration
	// FusionSourcePriority overrides the tie-break ranking between sources
	// reporting the same timestamp (FUSION_SOURCE_PRIORITY, JSON object).
	FusionSourcePriority map[string]int
	// IdentityCacheSize bounds the in-memory identity cache.
	IdentityCacheSize int
}

// Load reads configuration from environment variables.
func Load() Config {
	cfg := Config{
		Port:              commonCfg.EnvOrDefault("PORT", "3001"),
		DatabaseURL:       commonCfg.DatabaseURL(),
		KafkaBrokers:      commonCfg.EnvOrDefault("KAFKA_BROKERS", "localhost:9092"),
		CORSOrigin:        commonCfg.EnvOrDefault("CORS_ORIGIN", "*"),
		ConsumerBatchSize: commonCfg.EnvOrDefaultInt("CONSUMER_BATCH_SIZE", 500),
		ConsumerMaxWait:   time.Duration(commonCfg.EnvOrDefaultInt("CONSUMER_MAX_WAIT_MS", 100)) * time.Millisecond,
		IdentityCacheSize: commonCfg.EnvOrDefaultInt("IDENTITY_CACHE_SIZE", 200000),
	}
	if raw := commonCfg.EnvOrDefault("FUSION_SOURCE_PRIORITY", ""); raw != "" {
		_ = json.Unmarshal([]byte(raw), &cfg.FusionSourcePriority)
	}
	return cfg
}
