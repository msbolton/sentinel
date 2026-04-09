package models

// TrackPoint represents a single position observation for a tracked entity.
type TrackPoint struct {
	ID                         string                      `json:"id"`
	EntityID                   string                      `json:"entityId"`
	Position                   Coordinate                  `json:"position"`
	Heading                    *float64                    `json:"heading,omitempty"`
	SpeedKnots                 *float64                    `json:"speedKnots,omitempty"`
	Course                     *float64                    `json:"course,omitempty"`
	Source                     *string                     `json:"source,omitempty"`
	Timestamp                  string                      `json:"timestamp"`
	Altitude                   *float64                    `json:"altitude,omitempty"`
	TrackProcessingState       *string                     `json:"trackProcessingState,omitempty"`
	Velocity                   *GeodeticVelocity           `json:"velocity,omitempty"`
	Acceleration               *GeodeticAcceleration       `json:"acceleration,omitempty"`
	PositionCovariance         *PositionCovariance         `json:"positionCovariance,omitempty"`
	PositionVelocityCovariance *PositionVelocityCovariance `json:"positionVelocityCovariance,omitempty"`
	VelocityCovariance         *VelocityCovariance         `json:"velocityCovariance,omitempty"`
	CircularError              *float64                    `json:"circularError,omitempty"`
	AltitudeError              *float64                    `json:"altitudeError,omitempty"`
	SensorID                   *string                     `json:"sensorId,omitempty"`
}

// TrackSegment represents a contiguous segment of track points.
type TrackSegment struct {
	SegmentID string       `json:"segmentId"`
	EntityID  string       `json:"entityId"`
	Points    []TrackPoint `json:"points"`
	StartTime string       `json:"startTime"`
	EndTime   string       `json:"endTime"`
}
