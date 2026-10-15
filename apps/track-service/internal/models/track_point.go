package models

import "time"

// TrackPointResult matches the JSON response shape from the NestJS track-service.
// Uses camelCase JSON tags for HTTP API compatibility.
type TrackPointResult struct {
	ID         string    `json:"id"`
	EntityID   string    `json:"entityId"`
	Latitude   float64   `json:"latitude"`
	Longitude  float64   `json:"longitude"`
	Heading    *float64  `json:"heading"`
	SpeedKnots *float64  `json:"speedKnots"`
	Course     *float64  `json:"course"`
	Source     *string   `json:"source"`
	Timestamp  time.Time `json:"timestamp"`
}

// TrackSegment represents a contiguous segment of track points.
type TrackSegment struct {
	StartTime time.Time          `json:"startTime"`
	EndTime   time.Time          `json:"endTime"`
	Points    []TrackPointResult `json:"points"`
}

// BufferedPoint holds a track point waiting to be flushed to the database.
type BufferedPoint struct {
	EntityID      string
	Latitude      float64
	Longitude     float64
	Heading       *float64
	SpeedKnots    *float64
	Course        *float64
	Source        *string
	Timestamp     time.Time
	Altitude      *float64
	VelocityNorth *float64
	VelocityEast  *float64
	VelocityUp    *float64
	CircularError *float64
}

// PositionEventPayload is the Kafka message shape for events.entity.position.
// Uses snake_case to match NestJS Kafka output from entity-service.
type PositionEventPayload struct {
	EntityID   string   `json:"entity_id"`
	Latitude   float64  `json:"latitude"`
	Longitude  float64  `json:"longitude"`
	Heading    *float64 `json:"heading,omitempty"`
	SpeedKnots *float64 `json:"speed_knots,omitempty"`
	Course     *float64 `json:"course,omitempty"`
	Source     string   `json:"source,omitempty"`
	Timestamp  string   `json:"timestamp"`
	Altitude   *float64 `json:"altitude_meters,omitempty"`
	VelocityNorth *float64 `json:"velocity_north,omitempty"`
	VelocityEast  *float64 `json:"velocity_east,omitempty"`
	VelocityUp    *float64 `json:"velocity_up,omitempty"`
	CircularError *float64 `json:"circular_error,omitempty"`
}
