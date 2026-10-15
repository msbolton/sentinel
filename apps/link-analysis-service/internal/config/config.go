package config

import (
	commonCfg "github.com/sentinel/go-common/config"
)

// Config holds the link-analysis-service configuration.
type Config struct {
	Port        string
	DatabaseURL string
	CORSOrigin  string
}

// Load reads configuration from environment variables.
func Load() Config {
	return Config{
		Port:        commonCfg.EnvOrDefault("PORT", "3004"),
		DatabaseURL: commonCfg.DatabaseURL(),
		CORSOrigin:  commonCfg.EnvOrDefault("CORS_ORIGIN", "*"),
	}
}
