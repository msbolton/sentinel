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

	// UC2-informed fields
	Pitch            float64 `json:"pitch,omitempty"`
	Roll             float64 `json:"roll,omitempty"`
	TrackEnvironment string  `json:"track_environment,omitempty"`
	Affiliation      string  `json:"affiliation,omitempty"`

	// Operational status
	OperationalStatus string `json:"operational_status,omitempty"`
	CountryOfOrigin   string `json:"country_of_origin,omitempty"`

	// Velocity decomposition (m/s, North-East-Up)
	VelocityNorth float64 `json:"velocity_north,omitempty"`
	VelocityEast  float64 `json:"velocity_east,omitempty"`
	VelocityUp    float64 `json:"velocity_up,omitempty"`

	// Acceleration (m/s²)
	AccelNorth float64 `json:"accel_north,omitempty"`
	AccelEast  float64 `json:"accel_east,omitempty"`
	AccelUp    float64 `json:"accel_up,omitempty"`

	// Measurement uncertainty
	CircularError float64 `json:"circular_error,omitempty"`

	// Covariance matrices (upper triangle arrays)
	PosCovariance    [6]float64 `json:"pos_covariance,omitempty"`
	PosVelCovariance [9]float64 `json:"pos_vel_covariance,omitempty"`
	VelCovariance    [6]float64 `json:"vel_covariance,omitempty"`

	// Physical dimensions (meters)
	DimensionLength float64 `json:"dimension_length,omitempty"`
	DimensionWidth  float64 `json:"dimension_width,omitempty"`

	// Protocol-specific typed data (only one populated per message)
	AISData    *AISData    `json:"ais_data,omitempty"`
	ADSBData   *ADSBData   `json:"adsb_data,omitempty"`
	TLEData    *TLEData    `json:"tle_data,omitempty"`
	Link16Data *Link16Data `json:"link16_data,omitempty"`
	CoTData    *CoTData    `json:"cot_data,omitempty"`
	UAVData    *UAVData    `json:"uav_data,omitempty"`
}

// AISData contains AIS maritime identification and voyage data (per UC2 AISDataType).
type AISData struct {
	// Identity
	MMSI       string `json:"mmsi"`
	IMO        string `json:"imo,omitempty"`
	Callsign   string `json:"callsign,omitempty"`
	VesselName string `json:"vessel_name,omitempty"`

	// Classification
	ShipType     int    `json:"ship_type,omitempty"`
	ShipTypeName string `json:"ship_type_name,omitempty"`
	Flag         string `json:"flag,omitempty"`

	// Voyage
	Destination string  `json:"destination,omitempty"`
	ETA         string  `json:"eta,omitempty"`
	Draught     float64 `json:"draught,omitempty"`

	// Dimensions (reference point offsets in meters)
	DimensionA    float64 `json:"dimension_a,omitempty"`
	DimensionB    float64 `json:"dimension_b,omitempty"`
	DimensionC    float64 `json:"dimension_c,omitempty"`
	DimensionD    float64 `json:"dimension_d,omitempty"`
	LengthOverall float64 `json:"length_overall,omitempty"`
	Beam          float64 `json:"beam,omitempty"`

	// Navigation
	NavStatus            string  `json:"nav_status,omitempty"`
	RateOfTurn           float64 `json:"rate_of_turn,omitempty"`
	SpeedOverGround      float64 `json:"speed_over_ground,omitempty"`
	CourseOverGround     float64 `json:"course_over_ground,omitempty"`
	TrueHeading          float64 `json:"true_heading,omitempty"`
	PositionAccuracyHigh bool    `json:"position_accuracy_high,omitempty"`
	SpecialManoeuvre     bool    `json:"special_manoeuvre,omitempty"`

	// Message context
	MessageType     int `json:"message_type,omitempty"`
	RepeatIndicator int `json:"repeat_indicator,omitempty"`
}

// ADSBData contains ADS-B / IFF transponder data (per UC2 IFFDataType).
type ADSBData struct {
	// Core identity
	ICAOHex          string `json:"icao_hex"`
	Registration     string `json:"registration,omitempty"`
	AircraftType     string `json:"aircraft_type,omitempty"`
	AircraftTypeName string `json:"aircraft_type_name,omitempty"`
	OperatorICAO     string `json:"operator_icao,omitempty"`
	OperatorName     string `json:"operator_name,omitempty"`

	// Transponder codes
	Squawk    string `json:"squawk,omitempty"`
	Emergency string `json:"emergency,omitempty"`
	Mode1     string `json:"mode1,omitempty"`
	Mode2     string `json:"mode2,omitempty"`

	// Mode S data
	AircraftID      string  `json:"aircraft_id,omitempty"`
	FlightAirborne  bool    `json:"flight_airborne,omitempty"`
	IndicatedAirSpd float64 `json:"indicated_air_speed,omitempty"`
	TrueAirSpeed    float64 `json:"true_air_speed,omitempty"`
	GroundSpeed     float64 `json:"ground_speed,omitempty"`
	MagneticHeading float64 `json:"magnetic_heading,omitempty"`

	// Mode 5 data (military)
	Mode5FigureOfMerit int `json:"mode5_figure_of_merit,omitempty"`
	NationalOriginCode int `json:"national_origin_code,omitempty"`
	MissionCode        int `json:"mission_code,omitempty"`

	// Position/altitude
	AltitudeBaro float64 `json:"altitude_baro,omitempty"`
	AltitudeGeom float64 `json:"altitude_geom,omitempty"`
	VerticalRate float64 `json:"vertical_rate,omitempty"`
	OnGround     bool    `json:"on_ground,omitempty"`

	// Quality indicators
	Category string  `json:"category,omitempty"`
	NacP     int     `json:"nac_p,omitempty"`
	NacV     int     `json:"nac_v,omitempty"`
	SIL      int     `json:"sil,omitempty"`
	SILType  string  `json:"sil_type,omitempty"`
	NIC      int     `json:"nic,omitempty"`
	RC       float64 `json:"rc,omitempty"`
	GVA      int     `json:"gva,omitempty"`
	SDA      int     `json:"sda,omitempty"`
}

// TLEData contains satellite TLE orbital data.
type TLEData struct {
	// Identity
	NoradID        int    `json:"norad_id"`
	IntlDesignator string `json:"intl_designator,omitempty"`
	SatName        string `json:"sat_name,omitempty"`

	// TLE elements
	Line1 string `json:"line1"`
	Line2 string `json:"line2"`
	Epoch string `json:"epoch,omitempty"`

	// Orbital elements (derived)
	Inclination  float64 `json:"inclination,omitempty"`
	Eccentricity float64 `json:"eccentricity,omitempty"`
	RAAN         float64 `json:"raan,omitempty"`
	ArgOfPerigee float64 `json:"arg_of_perigee,omitempty"`
	MeanAnomaly  float64 `json:"mean_anomaly,omitempty"`
	MeanMotion   float64 `json:"mean_motion,omitempty"`
	Period       float64 `json:"period,omitempty"`
	Apogee       float64 `json:"apogee,omitempty"`
	Perigee      float64 `json:"perigee,omitempty"`

	// Classification
	ObjectType string `json:"object_type,omitempty"`
	RCSSize    string `json:"rcs_size,omitempty"`
	LaunchDate string `json:"launch_date,omitempty"`
	DecayDate  string `json:"decay_date,omitempty"`
	Country    string `json:"country,omitempty"`
}

// Link16Data contains Link 16 / JREAP-C tactical data link metadata.
type Link16Data struct {
	TrackNumber         int    `json:"track_number"`
	JSeriesLabel        string `json:"j_series_label"`
	OriginatingUnit     string `json:"originating_unit,omitempty"`
	Quality             int    `json:"quality,omitempty"`
	ExerciseIndicator   bool   `json:"exercise_indicator,omitempty"`
	SimulationIndicator bool   `json:"simulation_indicator,omitempty"`
	ForceIdentity       string `json:"force_identity,omitempty"`
}

// CoTData contains Cursor on Target metadata.
type CoTData struct {
	UID           string  `json:"uid"`
	CoTType       string  `json:"cot_type"`
	How           string  `json:"how,omitempty"`
	CE            float64 `json:"ce,omitempty"`
	LE            float64 `json:"le,omitempty"`
	StaleTime     string  `json:"stale_time,omitempty"`
	AccessControl string  `json:"access_control,omitempty"`
	Opex          string  `json:"opex,omitempty"`
	QOS           string  `json:"qos,omitempty"`
}

// UAVData contains UAV-specific telemetry data (per UC2 UAVDataType).
type UAVData struct {
	Make               string  `json:"make,omitempty"`
	Model              string  `json:"model,omitempty"`
	SerialNumber       string  `json:"serial_number,omitempty"`
	MACAddress         string  `json:"mac_address,omitempty"`
	OperatingFrequency float64 `json:"operating_frequency,omitempty"`
	FrequencyRange     float64 `json:"frequency_range,omitempty"`
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
