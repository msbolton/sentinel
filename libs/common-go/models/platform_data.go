package models

// PlatformData is a discriminated union — only one field populated per entity.
type PlatformData struct {
	AIS    *AISData    `json:"ais,omitempty"`
	ADSB   *ADSBData   `json:"adsb,omitempty"`
	TLE    *TLEData    `json:"tle,omitempty"`
	Link16 *Link16Data `json:"link16,omitempty"`
	CoT    *CoTData    `json:"cot,omitempty"`
	UAV    *UAVData    `json:"uav,omitempty"`
}

// AISData holds AIS maritime identification and voyage data.
type AISData struct {
	MMSI                 string  `json:"mmsi"`
	IMO                  *string `json:"imo,omitempty"`
	Callsign             *string `json:"callsign,omitempty"`
	VesselName           *string `json:"vesselName,omitempty"`
	ShipType             *int    `json:"shipType,omitempty"`
	ShipTypeName         *string `json:"shipTypeName,omitempty"`
	Flag                 *string `json:"flag,omitempty"`
	Destination          *string `json:"destination,omitempty"`
	ETA                  *string `json:"eta,omitempty"`
	Draught              *float64 `json:"draught,omitempty"`
	DimensionA           *float64 `json:"dimensionA,omitempty"`
	DimensionB           *float64 `json:"dimensionB,omitempty"`
	DimensionC           *float64 `json:"dimensionC,omitempty"`
	DimensionD           *float64 `json:"dimensionD,omitempty"`
	LengthOverall        *float64 `json:"lengthOverall,omitempty"`
	Beam                 *float64 `json:"beam,omitempty"`
	NavStatus            *string  `json:"navStatus,omitempty"`
	RateOfTurn           *float64 `json:"rateOfTurn,omitempty"`
	SpeedOverGround      *float64 `json:"speedOverGround,omitempty"`
	CourseOverGround     *float64 `json:"courseOverGround,omitempty"`
	TrueHeading          *float64 `json:"trueHeading,omitempty"`
	PositionAccuracyHigh *bool    `json:"positionAccuracyHigh,omitempty"`
	SpecialManoeuvre     *bool    `json:"specialManoeuvre,omitempty"`
	MessageType          *int     `json:"messageType,omitempty"`
	RepeatIndicator      *int     `json:"repeatIndicator,omitempty"`
}

// ADSBData holds ADS-B / IFF transponder data.
type ADSBData struct {
	ICAOHex           string   `json:"icaoHex"`
	Registration      *string  `json:"registration,omitempty"`
	AircraftType      *string  `json:"aircraftType,omitempty"`
	AircraftTypeName  *string  `json:"aircraftTypeName,omitempty"`
	OperatorICAO      *string  `json:"operatorIcao,omitempty"`
	OperatorName      *string  `json:"operatorName,omitempty"`
	Squawk            *string  `json:"squawk,omitempty"`
	Emergency         *string  `json:"emergency,omitempty"`
	Mode1             *string  `json:"mode1,omitempty"`
	Mode2             *string  `json:"mode2,omitempty"`
	AircraftID        *string  `json:"aircraftId,omitempty"`
	FlightAirborne    *bool    `json:"flightAirborne,omitempty"`
	IndicatedAirSpeed *float64 `json:"indicatedAirSpeed,omitempty"`
	TrueAirSpeed      *float64 `json:"trueAirSpeed,omitempty"`
	GroundSpeed       *float64 `json:"groundSpeed,omitempty"`
	MagneticHeading   *float64 `json:"magneticHeading,omitempty"`
	Mode5FigureOfMerit *int    `json:"mode5FigureOfMerit,omitempty"`
	NationalOriginCode *int    `json:"nationalOriginCode,omitempty"`
	MissionCode       *int     `json:"missionCode,omitempty"`
	AltitudeBaro      *float64 `json:"altitudeBaro,omitempty"`
	AltitudeGeom      *float64 `json:"altitudeGeom,omitempty"`
	VerticalRate      *float64 `json:"verticalRate,omitempty"`
	OnGround          *bool    `json:"onGround,omitempty"`
	Category          *string  `json:"category,omitempty"`
	NacP              *int     `json:"nacP,omitempty"`
	NacV              *int     `json:"nacV,omitempty"`
	SIL               *int     `json:"sil,omitempty"`
	SILType           *string  `json:"silType,omitempty"`
	NIC               *int     `json:"nic,omitempty"`
	RC                *int     `json:"rc,omitempty"`
	GVA               *int     `json:"gva,omitempty"`
	SDA               *int     `json:"sda,omitempty"`
}

// TLEData holds satellite TLE orbital data.
type TLEData struct {
	NoradID         int      `json:"noradId"`
	IntlDesignator  *string  `json:"intlDesignator,omitempty"`
	SatName         *string  `json:"satName,omitempty"`
	Line1           string   `json:"line1"`
	Line2           string   `json:"line2"`
	Epoch           *string  `json:"epoch,omitempty"`
	Inclination     *float64 `json:"inclination,omitempty"`
	Eccentricity    *float64 `json:"eccentricity,omitempty"`
	RAAN            *float64 `json:"raan,omitempty"`
	ArgOfPerigee    *float64 `json:"argOfPerigee,omitempty"`
	MeanAnomaly     *float64 `json:"meanAnomaly,omitempty"`
	MeanMotion      *float64 `json:"meanMotion,omitempty"`
	Period          *float64 `json:"period,omitempty"`
	Apogee          *float64 `json:"apogee,omitempty"`
	Perigee         *float64 `json:"perigee,omitempty"`
	ObjectType      *string  `json:"objectType,omitempty"`
	RCSSize         *string  `json:"rcsSize,omitempty"`
	LaunchDate      *string  `json:"launchDate,omitempty"`
	DecayDate       *string  `json:"decayDate,omitempty"`
	Country         *string  `json:"country,omitempty"`
}

// Link16Data holds Link 16 / JREAP-C tactical data link info.
type Link16Data struct {
	TrackNumber         int     `json:"trackNumber"`
	JSeriesLabel        string  `json:"jSeriesLabel"`
	OriginatingUnit     *string `json:"originatingUnit,omitempty"`
	Quality             *int    `json:"quality,omitempty"`
	ExerciseIndicator   *bool   `json:"exerciseIndicator,omitempty"`
	SimulationIndicator *bool   `json:"simulationIndicator,omitempty"`
	ForceIdentity       *string `json:"forceIdentity,omitempty"`
}

// CoTData holds Cursor on Target metadata.
type CoTData struct {
	UID           string  `json:"uid"`
	CoTType       string  `json:"cotType"`
	How           *string `json:"how,omitempty"`
	CE            *float64 `json:"ce,omitempty"`
	LE            *float64 `json:"le,omitempty"`
	StaleTime     *string  `json:"staleTime,omitempty"`
	AccessControl *string  `json:"accessControl,omitempty"`
	Opex          *string  `json:"opex,omitempty"`
	QOS           *string  `json:"qos,omitempty"`
}

// UAVData holds UAV-specific telemetry data.
type UAVData struct {
	Make               *string  `json:"make,omitempty"`
	Model              *string  `json:"model,omitempty"`
	SerialNumber       *string  `json:"serialNumber,omitempty"`
	MACAddress         *string  `json:"macAddress,omitempty"`
	OperatingFrequency *float64 `json:"operatingFrequency,omitempty"`
	FrequencyRange     *float64 `json:"frequencyRange,omitempty"`
}
