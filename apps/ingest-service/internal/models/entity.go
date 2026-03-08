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
	FeedID     string    `json:"feed_id,omitempty"`
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
	FeedID     string
	Format     string // "" = auto-detect, or "json","nmea","cot","ais","adsb","link16"
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
	EntityTypeSatellite = "satellite"
)

// Source type constants for labeling ingest origins.
const (
	SourceMQTT      = "mqtt"
	SourceSTOMP     = "stomp"
	SourceTCP       = "tcp"
	SourceOpenSky   = "opensky"
	SourceADSBLol   = "adsblol"
	SourceCelesTrak = "celestrak"
)

// Well-known feed UUID constants for built-in feeds.
const (
	FeedIDMQTT      = "00000000-0000-0000-0000-000000000001"
	FeedIDSTOMP     = "00000000-0000-0000-0000-000000000002"
	FeedIDTCP       = "00000000-0000-0000-0000-000000000003"
	FeedIDOpenSky   = "00000000-0000-0000-0000-000000000004"
	FeedIDADSBLol   = "00000000-0000-0000-0000-000000000005"
	FeedIDCelesTrak = "00000000-0000-0000-0000-000000000006"
)
