// Package obs is the ingest service's internal observation: one report
// about one object from one feed, with provenance. Decoders produce it and
// the wire package turns it into the sentinel.v1.Observation protobuf; no
// decoder touches generated code.
package obs

import (
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Identity types, spelled as the bare sentinel.v1.IdentityType names.
const (
	IdentityICAO24       = "ICAO24"
	IdentityMMSI         = "MMSI"
	IdentityIMO          = "IMO"
	IdentityNoradID      = "NORAD_ID"
	IdentityCoTUID       = "COT_UID"
	IdentityLink16TN     = "LINK16_TN"
	IdentityFeedLocal    = "FEED_LOCAL"
	IdentityCallsign     = "CALLSIGN"
	IdentityRegistration = "REGISTRATION"
	IdentityEmplacement  = "EMPLACEMENT"
)

// Entity types, spelled as the bare sentinel.v1.EntityType names.
const (
	EntityAircraft  = "AIRCRAFT"
	EntityVessel    = "VESSEL"
	EntityVehicle   = "VEHICLE"
	EntityPerson    = "PERSON"
	EntitySignal    = "SIGNAL"
	EntitySensor    = "SENSOR"
	EntitySatellite = "SATELLITE"
	EntityUnknown   = "UNKNOWN"
)

// Track environments, spelled as the bare sentinel.v1.TrackEnvironment names.
const (
	EnvAir        = "AIR"
	EnvGround     = "GROUND"
	EnvSeaSurface = "SEA_SURFACE"
	EnvSubsurface = "SUBSURFACE"
	EnvSpace      = "SPACE"
	EnvUnknown    = "UNKNOWN"
)

// Classification levels, spelled as the bare sentinel.v1.Classification
// names, lowest first.
const (
	ClassUnclassified = "UNCLASSIFIED"
	ClassConfidential = "CONFIDENTIAL"
	ClassSecret       = "SECRET"
	ClassTopSecret    = "TOP_SECRET"
)

// ClassificationRank orders classification levels: 0 for UNCLASSIFIED,
// unknown or empty, up to 3 for TOP_SECRET. Unknown values rank lowest so
// a typo never raises a report's level.
func ClassificationRank(level string) int {
	switch strings.ToUpper(level) {
	case ClassTopSecret:
		return 3
	case ClassSecret:
		return 2
	case ClassConfidential:
		return 1
	}
	return 0
}

// Identity is one natural identifier of the observed object.
type Identity struct {
	Type  string
	Value string
}

// Canonical reports whether the identity type is one fusion joins on.
func (i Identity) Canonical() bool {
	switch i.Type {
	case IdentityICAO24, IdentityMMSI, IdentityNoradID, IdentityCoTUID, IdentityEmplacement:
		return true
	}
	return false
}

// Observation mirrors sentinel.v1.Observation with Go types. Pointer and
// bool fields carry presence: nil means the source did not report it.
//
// A decoder fills the object fields (source entity id, source, identities,
// position, kinematics, platform detail). The feed runner fills the
// provenance fields (feed id, driver, received time, raw payload, replica)
// and computes the id.
type Observation struct {
	ID string // deterministic; see ComputeID

	FeedID         string
	Driver         string
	Decoder        string
	SourceEntityID string
	Source         string // bare EntitySource name, e.g. OPENSKY, ADS_B
	SensorID       string

	SourceTimestamp time.Time // zero when the source carries no time
	ReceivedAt      time.Time

	EntityType       string // bare EntityType name
	TrackEnvironment string // bare TrackEnvironment name
	DisplayName      string

	Identities []Identity // Identities[0] is the canonical key

	HasPosition bool
	Latitude    float64
	Longitude   float64
	Altitude    *float64
	Heading     *float64
	SpeedKnots  *float64
	Course      *float64

	Velocity     *[3]float64 // north, east, up (m/s)
	Acceleration *[3]float64
	Orientation  *[3]float64 // yaw, pitch, roll (degrees)

	CircularError    *float64
	PosCovariance    *[6]float64
	PosVelCovariance *[9]float64
	VelCovariance    *[6]float64

	CountryOfOrigin   string
	Affiliation       string
	OperationalStatus string
	Dimensions        *[3]float64 // length, width, height

	// Classification is the bare Classification name. A decoder sets it only
	// when the source carries a level; the runner raises it to the feed's
	// configured default and never lowers it.
	Classification string

	// EmplacementID is the emplacement the report came from. Only the
	// service sets it (a driver, from its session or its own emplacements);
	// wire.FromProto never copies a sender's.
	EmplacementID string
	// Polar is a measurement relative to the emplacement, kept for audit.
	Polar *Polar
	// Exercise marks synthetic data. Only the service sets it.
	Exercise bool

	AIS    *AISData
	ADSB   *ADSBData
	TLE    *TLEData
	Link16 *Link16Data
	CoT    *CoTData
	UAV    *UAVData
	RF     *RFData

	// Generic is decoder or service output with no typed block, sent as a
	// GenericDetail: a site's heartbeat (emplacement.MarkSite). Only service
	// code sets it; wire.FromProto never copies a sender's.
	Generic map[string]string

	RawContentType string
	Raw            []byte
	RawTruncated   bool

	ReplicaID    string
	FencingToken int64
}

// Polar mirrors sentinel.v1.PolarMeasurement. AzimuthRef is the bare
// AzimuthReference name (TRUE_NORTH, PLATFORM) or "".
type Polar struct {
	RangeM            float64
	AzimuthDeg        float64
	ElevationDeg      *float64
	RangeRateMps      *float64
	AzimuthRef        string
	SigmaRangeM       *float64
	SigmaAzimuthDeg   *float64
	SigmaElevationDeg *float64
}

// observationNamespace is the UUID v5 namespace for observation ids.
var observationNamespace = uuid.MustParse("6f1c5d2e-9a3b-5c4d-8e7f-0a1b2c3d4e5f")

// ComputeID derives the deterministic id from feed, source entity id, source
// timestamp and decoder, so a redelivered or duplicated report shares the
// id of the original.
func (o *Observation) ComputeID() string {
	ts := ""
	if !o.SourceTimestamp.IsZero() {
		ts = strconv.FormatInt(o.SourceTimestamp.UnixNano(), 10)
	} else if !o.ReceivedAt.IsZero() {
		ts = "r" + strconv.FormatInt(o.ReceivedAt.UnixNano(), 10)
	}
	key := strings.Join([]string{o.FeedID, o.SourceEntityID, ts, o.Decoder}, "|")
	return uuid.NewSHA1(observationNamespace, []byte(key)).String()
}

// CanonicalIdentity returns Identities[0] when it is a canonical type.
func (o *Observation) CanonicalIdentity() (Identity, bool) {
	if len(o.Identities) == 0 || !o.Identities[0].Canonical() {
		return Identity{}, false
	}
	return o.Identities[0], true
}

// SetPosition records a fix.
func (o *Observation) SetPosition(lat, lon float64) {
	o.HasPosition = true
	o.Latitude, o.Longitude = lat, lon
}

// AddIdentity appends an identity, keeping canonical types first and
// dropping blanks and duplicates.
func (o *Observation) AddIdentity(typ, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	id := Identity{Type: typ, Value: value}
	for _, existing := range o.Identities {
		if existing == id {
			return
		}
	}
	if !id.Canonical() {
		o.Identities = append(o.Identities, id)
		return
	}
	n := 0
	for n < len(o.Identities) && o.Identities[n].Canonical() {
		n++
	}
	o.Identities = append(o.Identities, Identity{})
	copy(o.Identities[n+1:], o.Identities[n:])
	o.Identities[n] = id
}

// EnsureIdentity guarantees Identities[0] exists: when no canonical identity
// is known, a feed-local identity over feed id and source entity id is
// placed first.
func (o *Observation) EnsureIdentity() {
	if _, ok := o.CanonicalIdentity(); ok {
		return
	}
	local := Identity{Type: IdentityFeedLocal, Value: o.FeedID + ":" + o.SourceEntityID}
	for i, existing := range o.Identities {
		if existing == local {
			o.Identities = append(o.Identities[:i], o.Identities[i+1:]...)
			break
		}
	}
	o.Identities = append([]Identity{local}, o.Identities...)
}

// Float returns a pointer to v, for the optional numeric fields.
func Float(v float64) *float64 { return &v }
