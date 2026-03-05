package config

import (
	"os"
	"strconv"
)

// Config holds all configuration for the ingest service.
// Values are populated from environment variables with sensible defaults.
type Config struct {
	KafkaBrokers    string // KAFKA_BROKERS (default: localhost:9092)
	KafkaEntityTopic string // KAFKA_ENTITY_TOPIC (default: events.entity.position)
	KafkaIngestTopic string // KAFKA_INGEST_TOPIC (default: ingest.raw)
	MQTTBroker      string // MQTT_BROKER (default: tcp://localhost:1883)
	MQTTTopics      string // MQTT_TOPICS (default: sensors/#)
	STOMPAddr       string // STOMP_ADDR (default: localhost:61613)
	STOMPQueue      string // STOMP_QUEUE (default: /queue/sensor-feeds)
	TCPAddr         string // TCP_ADDR (default: :4001)
	HTTPAddr        string // HTTP_ADDR (default: :4000)
	WorkerPoolSize  int    // WORKER_POOL_SIZE (default: 100)
	BatchSize       int    // BATCH_SIZE (default: 500)
	FlushIntervalMs int    // FLUSH_INTERVAL_MS (default: 100)

	OpenSkyEnabled     bool   // OPENSKY_ENABLED (default: false)
	OpenSkyIntervalSec int    // OPENSKY_INTERVAL_SEC (default: 15)
	OpenSkyBBox        string // OPENSKY_BBOX (default: "" = global)
	OpenSkyUsername    string // OPENSKY_USERNAME (default: "")
	OpenSkyPassword    string // OPENSKY_PASSWORD (default: "")

	ADSBLolEnabled     bool // ADSBLOL_ENABLED (default: false)
	ADSBLolIntervalSec int  // ADSBLOL_INTERVAL_SEC (default: 10)
}

// Load reads configuration from environment variables, applying defaults
// where no value is set.
func Load() *Config {
	return &Config{
		KafkaBrokers:    envOrDefault("KAFKA_BROKERS", "localhost:9092"),
		KafkaEntityTopic: envOrDefault("KAFKA_ENTITY_TOPIC", "ingest.raw"),
		KafkaIngestTopic: envOrDefault("KAFKA_INGEST_TOPIC", "ingest.raw"),
		MQTTBroker:      envOrDefault("MQTT_BROKER", "tcp://localhost:1883"),
		MQTTTopics:      envOrDefault("MQTT_TOPICS", "sensors/#"),
		STOMPAddr:       envOrDefault("STOMP_ADDR", "localhost:61613"),
		STOMPQueue:      envOrDefault("STOMP_QUEUE", "/queue/sensor-feeds"),
		TCPAddr:         envOrDefault("TCP_ADDR", ":4001"),
		HTTPAddr:        envOrDefault("HTTP_ADDR", ":4000"),
		WorkerPoolSize:  envOrDefaultInt("WORKER_POOL_SIZE", 100),
		BatchSize:       envOrDefaultInt("BATCH_SIZE", 500),
		FlushIntervalMs: envOrDefaultInt("FLUSH_INTERVAL_MS", 100),

		OpenSkyEnabled:     os.Getenv("OPENSKY_ENABLED") == "true",
		OpenSkyIntervalSec: envOrDefaultInt("OPENSKY_INTERVAL_SEC", 15),
		OpenSkyBBox:        envOrDefault("OPENSKY_BBOX", ""),
		OpenSkyUsername:    envOrDefault("OPENSKY_USERNAME", ""),
		OpenSkyPassword:    envOrDefault("OPENSKY_PASSWORD", ""),

		ADSBLolEnabled:     os.Getenv("ADSBLOL_ENABLED") == "true",
		ADSBLolIntervalSec: envOrDefaultInt("ADSBLOL_INTERVAL_SEC", 10),
	}
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
