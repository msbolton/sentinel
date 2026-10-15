package config

import (
	commonCfg "github.com/sentinel/go-common/config"
)

// Config holds the alert-service configuration.
type Config struct {
	Port         string
	DatabaseURL  string
	KafkaBrokers string
	CORSOrigin   string
}

// Load reads configuration from environment variables.
func Load() Config {
	return Config{
		Port:         commonCfg.EnvOrDefault("PORT", "3005"),
		DatabaseURL:  commonCfg.DatabaseURL(),
		KafkaBrokers: commonCfg.EnvOrDefault("KAFKA_BROKERS", "localhost:9092"),
		CORSOrigin:   commonCfg.EnvOrDefault("CORS_ORIGIN", "*"),
	}
}
