package models

// GeodeticVelocity is a 3-axis velocity in m/s (North-East-Up frame).
type GeodeticVelocity struct {
	North float64 `json:"north"`
	East  float64 `json:"east"`
	Up    float64 `json:"up"`
}

// GeodeticAcceleration is a 3-axis acceleration in m/s² (North-East-Up frame).
type GeodeticAcceleration struct {
	North float64 `json:"north"`
	East  float64 `json:"east"`
	Up    float64 `json:"up"`
}

// PositionCovariance is a 3x3 symmetric position covariance (upper triangle).
type PositionCovariance struct {
	PnPn float64 `json:"pnPn"`
	PnPe float64 `json:"pnPe"`
	PnPu float64 `json:"pnPu"`
	PePe float64 `json:"pePe"`
	PePu float64 `json:"pePu"`
	PuPu float64 `json:"puPu"`
}

// PositionVelocityCovariance is a 3x3 cross-covariance (full matrix).
type PositionVelocityCovariance struct {
	PnVn float64 `json:"pnVn"`
	PnVe float64 `json:"pnVe"`
	PnVu float64 `json:"pnVu"`
	PeVn float64 `json:"peVn"`
	PeVe float64 `json:"peVe"`
	PeVu float64 `json:"peVu"`
	PuVn float64 `json:"puVn"`
	PuVe float64 `json:"puVe"`
	PuVu float64 `json:"puVu"`
}

// VelocityCovariance is a 3x3 symmetric velocity covariance (upper triangle).
type VelocityCovariance struct {
	VnVn float64 `json:"vnVn"`
	VnVe float64 `json:"vnVe"`
	VnVu float64 `json:"vnVu"`
	VeVe float64 `json:"veVe"`
	VeVu float64 `json:"veVu"`
	VuVu float64 `json:"vuVu"`
}

// KinematicState is the full kinematic state vector.
type KinematicState struct {
	Velocity                   *GeodeticVelocity           `json:"velocity,omitempty"`
	Acceleration               *GeodeticAcceleration       `json:"acceleration,omitempty"`
	PositionCovariance         *PositionCovariance         `json:"positionCovariance,omitempty"`
	PositionVelocityCovariance *PositionVelocityCovariance `json:"positionVelocityCovariance,omitempty"`
	VelocityCovariance         *VelocityCovariance         `json:"velocityCovariance,omitempty"`
}

// Orientation represents ECEF-based orientation.
type Orientation struct {
	Yaw   float64 `json:"yaw"`
	Pitch float64 `json:"pitch"`
	Roll  float64 `json:"roll"`
}

// MeasurementUncertainty holds uncertainty metrics for observations.
type MeasurementUncertainty struct {
	CircularError *float64 `json:"circularError,omitempty"`
	SemiMajor     *float64 `json:"semiMajor,omitempty"`
	SemiMinor     *float64 `json:"semiMinor,omitempty"`
	Orientation   *float64 `json:"orientation,omitempty"`
	AltitudeError *float64 `json:"altitudeError,omitempty"`
	Confidence    *float64 `json:"confidence,omitempty"`
}
