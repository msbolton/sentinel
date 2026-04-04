package config

import (
	commonCfg "github.com/sentinel/go-common/config"
)

// Config holds the search-service configuration.
type Config struct {
	Port           string
	DatabaseURL    string
	KafkaBrokers   string
	OpenSearchHost string
	CORSOrigin     string
}

// Load reads configuration from environment variables.
func Load() Config {
	return Config{
		Port:           commonCfg.EnvOrDefault("PORT", "3003"),
		DatabaseURL:    commonCfg.DatabaseURL(),
		KafkaBrokers:   commonCfg.EnvOrDefault("KAFKA_BROKERS", "localhost:9092"),
		OpenSearchHost: commonCfg.EnvOrDefault("OPENSEARCH_HOST", "http://localhost:9200"),
		CORSOrigin:     commonCfg.EnvOrDefault("CORS_ORIGIN", "*"),
	}
}
