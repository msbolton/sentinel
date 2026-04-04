package config

import (
	commonCfg "github.com/sentinel/go-common/config"
)

// Config holds the entity-service configuration.
type Config struct {
	Port          string
	DatabaseURL   string
	KafkaBrokers  string
	RedisHost     string
	RedisPort     string
	RedisPassword string
	CORSOrigin    string
}

// Load reads configuration from environment variables.
func Load() Config {
	return Config{
		Port:          commonCfg.EnvOrDefault("PORT", "3001"),
		DatabaseURL:   commonCfg.DatabaseURL(),
		KafkaBrokers:  commonCfg.EnvOrDefault("KAFKA_BROKERS", "localhost:9092"),
		RedisHost:     commonCfg.EnvOrDefault("REDIS_HOST", "localhost"),
		RedisPort:     commonCfg.EnvOrDefault("REDIS_PORT", "6379"),
		RedisPassword: commonCfg.EnvOrDefault("REDIS_PASSWORD", ""),
		CORSOrigin:    commonCfg.EnvOrDefault("CORS_ORIGIN", "*"),
	}
}
