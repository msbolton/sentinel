package config

import (
	commonconfig "github.com/sentinel/go-common/config"
)

type Config struct {
	Port         string
	DatabaseURL  string
	KafkaBrokers string
	CORSOrigin   string
}

func Load() *Config {
	return &Config{
		Port:         commonconfig.EnvOrDefault("PORT", "3002"),
		DatabaseURL:  commonconfig.DatabaseURL(),
		KafkaBrokers: commonconfig.EnvOrDefault("KAFKA_BROKERS", "localhost:9092"),
		CORSOrigin:   commonconfig.EnvOrDefault("CORS_ORIGIN", "http://localhost:4200"),
	}
}
