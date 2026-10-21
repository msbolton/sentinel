package obs

// AISData carries AIS maritime identification and voyage detail.
type AISData struct {
	MMSI       string
	IMO        string
	Callsign   string
	VesselName string

	ShipType     int
	ShipTypeName string
	Flag         string

	Destination string
	ETA         string
	Draught     float64

	DimensionA    float64
	DimensionB    float64
	DimensionC    float64
	DimensionD    float64
	LengthOverall float64
	Beam          float64

	NavStatus            string
	RateOfTurn           float64
	SpeedOverGround      float64
	CourseOverGround     float64
	TrueHeading          float64
	PositionAccuracyHigh bool
	SpecialManoeuvre     bool

	MessageType     int
	RepeatIndicator int
}

// ADSBData carries ADS-B and IFF transponder detail.
type ADSBData struct {
	ICAOHex          string
	Registration     string
	AircraftType     string
	AircraftTypeName string
	OperatorICAO     string
	OperatorName     string

	Squawk    string
	Emergency string
	Mode1     string
	Mode2     string

	AircraftID      string
	FlightAirborne  bool
	IndicatedAirSpd float64
	TrueAirSpeed    float64
	GroundSpeed     float64
	MagneticHeading float64

	Mode5FigureOfMerit int
	NationalOriginCode int
	MissionCode        int

	AltitudeBaro float64
	AltitudeGeom float64
	VerticalRate float64
	OnGround     bool

	Category string
	NacP     int
	NacV     int
	SIL      int
	SILType  string
	NIC      int
	RC       float64
	GVA      int
	SDA      int
}

// TLEData carries satellite two-line-element detail.
type TLEData struct {
	NoradID        int
	IntlDesignator string
	SatName        string

	Line1 string
	Line2 string
	Epoch string

	Inclination  float64
	Eccentricity float64
	RAAN         float64
	ArgOfPerigee float64
	MeanAnomaly  float64
	MeanMotion   float64
	Period       float64
	Apogee       float64
	Perigee      float64

	ObjectType string
	RCSSize    string
	LaunchDate string
	DecayDate  string
	Country    string
}

// Link16Data carries Link 16 / JREAP-C track metadata.
type Link16Data struct {
	TrackNumber         int
	JSeriesLabel        string
	OriginatingUnit     string
	Quality             int
	ExerciseIndicator   bool
	SimulationIndicator bool
	ForceIdentity       string
}

// CoTData carries Cursor on Target event metadata.
type CoTData struct {
	UID           string
	CoTType       string
	How           string
	CE            float64
	LE            float64
	StaleTime     string
	AccessControl string
	Opex          string
	QOS           string
}

// UAVData carries UAV telemetry detail.
type UAVData struct {
	Make               string
	Model              string
	SerialNumber       string
	MACAddress         string
	OperatingFrequency float64
	FrequencyRange     float64
}

// RFData carries intercepted RF / electronic-warfare emitter parameters.
type RFData struct {
	FrequencyHz  float64
	BandwidthHz  *float64
	EmitterType  string
	Modulation   string
	PriUs        *float64
	PulseWidthUs *float64
	ScanType     string
	PowerDbm     *float64
}

// RadarData carries a radar track's detail. Mode and Classification are the
// bare RadarMode and RadarClass names (SEARCH, TRACK; VEHICLE, DRONE, ...).
// HeightAboveGroundM is set by the service, not trusted from a sender.
type RadarData struct {
	RangeRateMps             *float64
	RcsDbsm                  *float64
	SnrDb                    *float64
	Mode                     string
	TrackQuality             *float64
	Classification           string
	ClassificationConfidence *float64
	RadarTrackID             string
	HeightAboveGroundM       *float64
}

// EoIrData carries an electro-optical or infrared camera detection. Band is
// the bare EoIrBand name (VISIBLE, SWIR, MWIR, LWIR); Classification is the
// camera's free-text label. BoundingBox is normalised image coordinates
// {x, y, width, height}.
type EoIrData struct {
	Band                     string
	Classification           string
	ClassificationConfidence *float64
	HorizontalFovDeg         *float64
	VerticalFovDeg           *float64
	BoundingBox              *[4]float64
	CameraTrackID            string
}
