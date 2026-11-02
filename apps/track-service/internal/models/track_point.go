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

	// Provenance of the accepted observation this point came from.
	FeedID         *string
	SourceEntityID *string
	ObservationID  *string
	EntityType     *string
}

// BBox is a geographic box in degrees. West > East means it crosses the
// antimeridian.
type BBox struct{ West, South, East, North float64 }

// ReplayEntity is one track in a replay; Points are
// [epochMs, lon, lat, altM|null, headingDeg|null].
type ReplayEntity struct {
	EntityID       string        `json:"entityId"`
	EntityType     string        `json:"entityType"`
	Name           string        `json:"name"`
	Classification string        `json:"classification"`
	Points         [][5]*float64 `json:"points"`
}
