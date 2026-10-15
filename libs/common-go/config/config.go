package config

import (
	"fmt"
	"os"
	"strconv"
)

// EnvOrDefault returns the value of the environment variable named by key,
// or defaultVal if the variable is not set or empty.
func EnvOrDefault(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}

// EnvOrDefaultInt returns the integer value of the environment variable,
// or defaultVal if not set, empty, or not a valid integer.
func EnvOrDefaultInt(key string, defaultVal int) int {
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

// EnvOrDefaultBool returns true if the environment variable equals "true".
func EnvOrDefaultBool(key string, defaultVal bool) bool {
	val := os.Getenv(key)
	if val == "" {
		if defaultVal {
			return true
		}
		return false
	}
	return val == "true"
}

// DatabaseURL builds a PostgreSQL connection string from individual env vars,
// or returns DATABASE_URL if set directly.
func DatabaseURL() string {
	if url := os.Getenv("DATABASE_URL"); url != "" {
		return url
	}

	host := EnvOrDefault("DB_HOST", "localhost")
	port := EnvOrDefault("DB_PORT", "5432")
	user := EnvOrDefault("DB_USERNAME", "sentinel")
	pass := EnvOrDefault("DB_PASSWORD", "sentinel")
	name := EnvOrDefault("DB_DATABASE", "sentinel")

	return fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable", user, pass, host, port, name)
}
