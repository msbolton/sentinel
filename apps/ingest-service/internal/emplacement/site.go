package emplacement

import (
	"strconv"
	"time"

	"github.com/sentinel/ingest-service/internal/obs"
)

// SiteDecoder names the site reports Sentinel makes for an emplacement whose
// connection does not heartbeat it (feed.SitePublisher). The grpc driver
// stamps its own decoder on the sites it publishes.
const SiteDecoder = "emplacement"

// The GenericDetail fields of a site entity, which the client reads from the
// entity's platformData.generic.fields.
const (
	FieldHeartbeat            = "heartbeat"
	FieldHeartbeatIntervalSec = "heartbeatIntervalSec"
	FieldLastHeartbeatAt      = "lastHeartbeatAt"
	FieldSilentSince          = "silentSince"
	// FieldBearings is a camera site's bearing lines, a JSON array newest
	// first, and FieldBearingTTLSec how long each lasts (integer seconds).
	FieldBearings      = "bearings"
	FieldBearingTTLSec = "bearingTtlSec"
)

// Observation is the SENSOR report of e at time at: its site entity, under
// the canonical EMPLACEMENT identity, so the entity service always fuses an
// emplacement's reports into one entity that search finds and a geofence
// ring can anchor on.
func Observation(e Emplacement, at time.Time) *obs.Observation {
	o := &obs.Observation{
		Decoder:          SiteDecoder,
		SourceEntityID:   e.ID,
		Source:           SourceFor(e.SensorType),
		SourceTimestamp:  at,
		EntityType:       obs.EntitySensor,
		TrackEnvironment: obs.EnvGround,
		DisplayName:      e.Name,
		Classification:   e.Classification,
		EmplacementID:    e.ID,
		Exercise:         e.Exercise,
	}
	// A boresight from true north is the sensor's heading. One from the
	// platform is relative to the platform's own heading, which a mobile
	// emplacement's reports carry, so it is no heading of its own.
	if e.AzimuthReference == AzimuthTrueNorth {
		o.Heading = obs.Float(e.BoresightDeg)
	}
	if e.Mobility == MobilityMobile {
		o.TrackEnvironment = obs.EnvUnknown
	}
	o.AddIdentity(obs.IdentityEmplacement, e.ID)
	if e.Position != nil {
		o.SetPosition(e.Position.Lat, e.Position.Lon)
	}
	if e.Altitude != nil {
		o.Altitude = obs.Float(*e.Altitude)
	}
	return o
}

// SourceFor maps a sensor type to the EntitySource its reports carry.
func SourceFor(sensorType string) string {
	switch sensorType {
	case "RADAR":
		return "RADAR"
	case "AIS_RECEIVER":
		return "AIS"
	case "ADSB_RECEIVER":
		return "ADS_B"
	case "HUMAN":
		return "HUMINT"
	case "RF":
		return "SIGINT"
	}
	return "MASINT"
}

// MarkSite writes a site's heartbeat onto its SENSOR observation:
// OPERATIONAL while online, UNKNOWN while pending, INACTIVE otherwise, and
// the GenericDetail fields the client reads. silentSince is the last
// heartbeat of a site that has had one; last is zero for one that never did.
func MarkSite(o *obs.Observation, state HeartbeatState, last time.Time, intervalSec int) {
	o.Generic = map[string]string{
		FieldHeartbeat:            string(state),
		FieldHeartbeatIntervalSec: strconv.Itoa(intervalSec),
	}
	if !last.IsZero() {
		o.Generic[FieldLastHeartbeatAt] = last.UTC().Format(time.RFC3339Nano)
	}
	switch state {
	case HeartbeatOnline:
		o.OperationalStatus = "OPERATIONAL"
	case HeartbeatPending:
		o.OperationalStatus = "UNKNOWN"
	default:
		o.OperationalStatus = "INACTIVE"
		if !last.IsZero() {
			o.Generic[FieldSilentSince] = last.UTC().Format(time.RFC3339Nano)
		}
	}
}
