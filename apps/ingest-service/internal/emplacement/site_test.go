package emplacement

import (
	"testing"
	"time"

	"github.com/sentinel/ingest-service/internal/obs"
)

func TestObservationIsTheSiteEntity(t *testing.T) {
	alt := 12.5
	e := Emplacement{ID: "e-1", Name: "Coastal", Mobility: MobilityFixed, SensorType: "RF", Position: &Point{Lat: 50.1, Lon: -4.2}, Altitude: &alt,
		BoresightDeg: 200, AzimuthReference: AzimuthTrueNorth, Classification: "SECRET", Exercise: true}
	at := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	o := Observation(e, at)
	if o.SourceEntityID != "e-1" || o.EmplacementID != "e-1" || o.EntityType != obs.EntitySensor || o.Source != "SIGINT" ||
		o.Decoder != SiteDecoder || !o.SourceTimestamp.Equal(at) || o.Classification != "SECRET" || !o.Exercise || o.DisplayName != "Coastal" ||
		o.TrackEnvironment != obs.EnvGround || o.Heading == nil || *o.Heading != 200 || !o.HasPosition || o.Latitude != 50.1 ||
		o.Altitude == nil || *o.Altitude != 12.5 {
		t.Fatalf("observation = %+v", o)
	}
	if len(o.Identities) != 1 || o.Identities[0] != (obs.Identity{Type: obs.IdentityEmplacement, Value: "e-1"}) {
		t.Fatalf("identities = %+v", o.Identities)
	}
	e.AzimuthReference, e.Mobility = AzimuthPlatform, MobilityMobile
	if o := Observation(e, at); o.Heading != nil || o.TrackEnvironment != obs.EnvUnknown {
		t.Fatalf("a mobile, platform-relative site = heading %v, env %s", o.Heading, o.TrackEnvironment)
	}
}

func TestSourceFor(t *testing.T) {
	for in, want := range map[string]string{"RADAR": "RADAR", "AIS_RECEIVER": "AIS", "ADSB_RECEIVER": "ADS_B", "HUMAN": "HUMINT", "RF": "SIGINT", "EO_IR": "MASINT", "OTHER": "MASINT"} {
		if got := SourceFor(in); got != want {
			t.Errorf("SourceFor(%s) = %s, want %s", in, got, want)
		}
	}
}

func TestMarkSite(t *testing.T) {
	last := time.Date(2026, 10, 20, 14, 2, 0, 0, time.UTC)
	o := &obs.Observation{}
	MarkSite(o, HeartbeatOnline, last, 5)
	if o.OperationalStatus != "OPERATIONAL" || o.Generic[FieldHeartbeat] != "online" || o.Generic[FieldHeartbeatIntervalSec] != "5" ||
		o.Generic[FieldLastHeartbeatAt] != "2026-10-20T14:02:00Z" {
		t.Fatalf("online: %s %v", o.OperationalStatus, o.Generic)
	}
	if _, ok := o.Generic[FieldSilentSince]; ok {
		t.Fatal("an online site carries silentSince")
	}
	MarkSite(o, HeartbeatOffline, last, 5)
	if o.OperationalStatus != "INACTIVE" || o.Generic[FieldSilentSince] != "2026-10-20T14:02:00Z" || o.Generic[FieldHeartbeat] != "offline" {
		t.Fatalf("offline: %s %v", o.OperationalStatus, o.Generic)
	}
	MarkSite(o, HeartbeatNever, time.Time{}, 10)
	if _, ok := o.Generic[FieldSilentSince]; ok || o.OperationalStatus != "INACTIVE" || o.Generic[FieldLastHeartbeatAt] != "" {
		t.Fatalf("never: %s %v", o.OperationalStatus, o.Generic)
	}
	MarkSite(o, HeartbeatPending, time.Time{}, 10)
	if o.OperationalStatus != "UNKNOWN" || o.Generic[FieldHeartbeat] != "pending" {
		t.Fatalf("pending: %s %v", o.OperationalStatus, o.Generic)
	}
}
