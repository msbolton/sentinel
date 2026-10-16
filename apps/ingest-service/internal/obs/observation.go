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

	"github.com/sentinel/ingest-service/internal/models"
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
)

// Identity is one natural identifier of the observed object.
type Identity struct {
	Type  string
	Value string
}

// Canonical reports whether the identity type is one fusion joins on.
func (i Identity) Canonical() bool {
	switch i.Type {
	case IdentityICAO24, IdentityMMSI, IdentityNoradID, IdentityCoTUID:
		return true
	}
	return false
}

// Observation mirrors sentinel.v1.Observation with Go types. Pointer and
// bool fields carry presence: nil means the source did not report it.
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

	AIS    *models.AISData
	ADSB   *models.ADSBData
	TLE    *models.TLEData
	Link16 *models.Link16Data
	CoT    *models.CoTData
	UAV    *models.UAVData

	RawContentType string
	Raw            []byte
	RawTruncated   bool

	ReplicaID    string
	FencingToken int64
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
