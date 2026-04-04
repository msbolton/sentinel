package models

// EntityType mirrors libs/shared-models/src/entity.ts EntityType enum.
const (
	EntityTypeUnknown   = "UNKNOWN"
	EntityTypePerson    = "PERSON"
	EntityTypeVehicle   = "VEHICLE"
	EntityTypeVessel    = "VESSEL"
	EntityTypeAircraft  = "AIRCRAFT"
	EntityTypeFacility  = "FACILITY"
	EntityTypeEquipment = "EQUIPMENT"
	EntityTypeUnit      = "UNIT"
	EntityTypeSignal    = "SIGNAL"
	EntityTypeCyber     = "CYBER"
	EntityTypeSensor    = "SENSOR"
	EntityTypeSatellite = "SATELLITE"
	EntityTypeDrone     = "DRONE"
)

// EntitySource mirrors libs/shared-models/src/entity.ts EntitySource enum.
const (
	EntitySourceHUMINT    = "HUMINT"
	EntitySourceSIGINT    = "SIGINT"
	EntitySourceGEOINT    = "GEOINT"
	EntitySourceOSINT     = "OSINT"
	EntitySourceMASINT    = "MASINT"
	EntitySourceCyber     = "CYBER"
	EntitySourceManual    = "MANUAL"
	EntitySourceAIS       = "AIS"
	EntitySourceADSB      = "ADS_B"
	EntitySourceLINK16    = "LINK16"
	EntitySourceGPS       = "GPS"
	EntitySourceRADAR     = "RADAR"
	EntitySourceCELESTRAK = "CELESTRAK"
	EntitySourceOPENSKY   = "OPENSKY"
	EntitySourceADSBLOL   = "ADSB_LOL"
)

// Classification mirrors libs/shared-models/src/common.ts Classification enum.
const (
	ClassificationUnclassified = "UNCLASSIFIED"
	ClassificationConfidential = "CONFIDENTIAL"
	ClassificationSecret       = "SECRET"
	ClassificationTopSecret    = "TOP_SECRET"
)

// Affiliation mirrors libs/shared-models/src/entity.ts Affiliation enum.
const (
	AffiliationFriendly       = "FRIENDLY"
	AffiliationHostile        = "HOSTILE"
	AffiliationNeutral        = "NEUTRAL"
	AffiliationUnknown        = "UNKNOWN"
	AffiliationAssumedFriendly = "ASSUMED_FRIENDLY"
	AffiliationSuspect        = "SUSPECT"
	AffiliationPending        = "PENDING"
)

// AgeoutState represents the lifecycle state of an entity.
const (
	AgeoutStateLive    = "LIVE"
	AgeoutStateStale   = "STALE"
	AgeoutStateAgedOut = "AGED_OUT"
)

// TrackEnvironment represents the environment in which an entity operates.
const (
	TrackEnvAir        = "AIR"
	TrackEnvSeaSurface = "SEA_SURFACE"
	TrackEnvSubsurface = "SUBSURFACE"
	TrackEnvGround     = "GROUND"
	TrackEnvSpace      = "SPACE"
	TrackEnvUnknown    = "UNKNOWN"
)

// TrackProcessingState represents the processing state of a track.
const (
	TrackProcessingLive         = "LIVE"
	TrackProcessingPredicted    = "PREDICTED"
	TrackProcessingDeadReckoned = "DEAD_RECKONED"
	TrackProcessingHypothesized = "HYPOTHESIZED"
	TrackProcessingHistorical   = "HISTORICAL"
)

// OperationalStatus represents the operational state of an entity.
const (
	OpStatusOperational = "OPERATIONAL"
	OpStatusDegraded    = "DEGRADED"
	OpStatusDamaged     = "DAMAGED"
	OpStatusDestroyed   = "DESTROYED"
	OpStatusInactive    = "INACTIVE"
	OpStatusUnknown     = "UNKNOWN"
)
