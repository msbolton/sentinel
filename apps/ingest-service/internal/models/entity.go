package models

import (
	"time"
)

// EntityPosition represents a normalized geospatial entity position
// from any sensor source. This is the canonical data model that all
// raw sensor feeds are converted into before being published to Kafka.
type EntityPosition struct {
	EntityID   string    `json:"entity_id"`
	EntityType string    `json:"entity_type"`
	Name       string    `json:"name"`
	Source     string    `json:"source"`
	Latitude   float64   `json:"latitude"`
	Longitude  float64   `json:"longitude"`
	Altitude   float64   `json:"altitude"`
	Heading    float64   `json:"heading"`
	SpeedKnots float64  `json:"speed_knots"`
	Course     float64   `json:"course"`
	Timestamp  time.Time `json:"timestamp"`
	RawData    []byte    `json:"raw_data,omitempty"`
}

// IngestMessage represents a raw message received from any source
// before parsing and normalization.
type IngestMessage struct {
	SourceType string // mqtt, stomp, tcp
	SourceAddr string
	Payload    []byte
	ReceivedAt time.Time
}

// Supported entity types for classification.
const (
	EntityTypeAircraft  = "aircraft"
	EntityTypeVessel    = "vessel"
	EntityTypeVehicle   = "vehicle"
	EntityTypePerson    = "person"
	EntityTypeUnknown   = "unknown"
	EntityTypeSensor    = "sensor"
	EntityTypePlatform  = "platform"
)

// Source type constants for labeling ingest origins.
const (
	SourceMQTT  = "mqtt"
	SourceSTOMP = "stomp"
	SourceTCP   = "tcp"
)
