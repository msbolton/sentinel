package models

// Observation represents a single sensor observation of an entity.
type Observation struct {
	ID                         string                      `json:"id"`
	EntityID                   string                      `json:"entityId"`
	SensorID                   *string                     `json:"sensorId,omitempty"`
	FeedID                     *string                     `json:"feedId,omitempty"`
	Source                     *string                     `json:"source,omitempty"`
	Position                   *Coordinate                 `json:"position,omitempty"`
	Altitude                   *float64                    `json:"altitude,omitempty"`
	Heading                    *float64                    `json:"heading,omitempty"`
	SpeedKnots                 *float64                    `json:"speedKnots,omitempty"`
	Course                     *float64                    `json:"course,omitempty"`
	Velocity                   *GeodeticVelocity           `json:"velocity,omitempty"`
	Acceleration               *GeodeticAcceleration       `json:"acceleration,omitempty"`
	Uncertainty                *MeasurementUncertainty     `json:"uncertainty,omitempty"`
	PositionCovariance         *PositionCovariance         `json:"positionCovariance,omitempty"`
	PositionVelocityCovariance *PositionVelocityCovariance `json:"positionVelocityCovariance,omitempty"`
	VelocityCovariance         *VelocityCovariance         `json:"velocityCovariance,omitempty"`
	DetectionConfidence        *float64                    `json:"detectionConfidence,omitempty"`
	TrackProcessingState       *string                     `json:"trackProcessingState,omitempty"`
	Azimuth                    *float64                    `json:"azimuth,omitempty"`
	Elevation                  *float64                    `json:"elevation,omitempty"`
	Range                      *float64                    `json:"range,omitempty"`
	AzimuthError               *float64                    `json:"azimuthError,omitempty"`
	ElevationError             *float64                    `json:"elevationError,omitempty"`
	RangeError                 *float64                    `json:"rangeError,omitempty"`
	RawData                    map[string]interface{}      `json:"rawData,omitempty"`
	Timestamp                  string                      `json:"timestamp"`
}
