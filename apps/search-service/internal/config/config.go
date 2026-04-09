package config

import (
	"fmt"

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

func opensearchURL() string {
	host := commonCfg.EnvOrDefault("OPENSEARCH_HOST", "localhost")
	port := commonCfg.EnvOrDefault("OPENSEARCH_PORT", "9200")
	return fmt.Sprintf("http://%s:%s", host, port)
}

// Load reads configuration from environment variables.
func Load() Config {
	return Config{
		Port:           commonCfg.EnvOrDefault("PORT", "3003"),
		DatabaseURL:    commonCfg.DatabaseURL(),
		KafkaBrokers:   commonCfg.EnvOrDefault("KAFKA_BROKERS", "localhost:9092"),
		OpenSearchHost: opensearchURL(),
		CORSOrigin:     commonCfg.EnvOrDefault("CORS_ORIGIN", "*"),
	}
}
