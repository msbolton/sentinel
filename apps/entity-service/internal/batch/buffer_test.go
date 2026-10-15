package batch

import (
	"encoding/json"
	"github.com/sentinel/entity-service/internal/store"
	"reflect"
	"testing"
	"time"
)

func f(v float64) *float64 { return &v }

func TestMapEntityType(t *testing.T) {
	cases := map[string]string{
		"aircraft": "AIRCRAFT", "AIRCRAFT": "AIRCRAFT", "Vessel": "VESSEL", "vehicle": "VEHICLE", "person": "PERSON",
		"satellite": "SATELLITE", "drone": "DRONE", "equipment": "EQUIPMENT", "spaceship": "UNKNOWN", "": "UNKNOWN",
	}
	for in, want := range cases {
		if got := mapEntityType(in); got != want {
			t.Errorf("mapEntityType(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMapSource(t *testing.T) {
	cases := map[string]string{
		"adsb": "ADS_B", "ADS-B": "ADS_B", "ads_b": "ADS_B", "adsblol": "ADSB_LOL", "ADSB_LOL": "ADSB_LOL",
		"opensky": "OPENSKY", "celestrak": "CELESTRAK", "ais": "AIS", "link16": "LINK16", "gps": "GPS", "radar": "UNKNOWN",
	}
	for in, want := range cases {
		if got := mapSource(in); got != want {
			t.Errorf("mapSource(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBuildPlatformData(t *testing.T) {
	if buildPlatformData(IngestMessage{}) != nil {
		t.Error("no protocol data should yield nil")
	}
	ais := map[string]interface{}{"mmsi": "123"}
	got := buildPlatformData(IngestMessage{AISData: ais, ADSBData: map[string]interface{}{"icao": "x"}})
	if !reflect.DeepEqual(got, map[string]interface{}{"ais": ais}) {
		t.Errorf("first protocol wins: %v", got)
	}
	got = buildPlatformData(IngestMessage{UAVData: map[string]interface{}{"battery": 50}})
	if _, ok := got["uav"]; !ok {
		t.Errorf("uav data not keyed: %v", got)
	}
}

func TestBuildKinematics(t *testing.T) {
	if buildKinematics(IngestMessage{}) != nil {
		t.Error("no vectors should yield nil")
	}
	k := buildKinematics(IngestMessage{VelocityNorth: f(1), AccelUp: f(-9.8)})
	vel, ok := k["velocity"].(map[string]interface{})
	if !ok || *vel["north"].(*float64) != 1 || vel["east"].(*float64) != nil {
		t.Errorf("velocity = %v", k["velocity"])
	}
	acc, ok := k["acceleration"].(map[string]interface{})
	if !ok || *acc["up"].(*float64) != -9.8 {
		t.Errorf("acceleration = %v", k["acceleration"])
	}
	if _, has := buildKinematics(IngestMessage{VelocityEast: f(2)})["acceleration"]; has {
		t.Error("acceleration should be absent without accel fields")
	}
}

func TestResolveSourceEntityID(t *testing.T) {
	if resolveSourceEntityID(IngestMessage{EntityID: "abc"}) != "abc" {
		t.Error("source entity id should be the feed's entity id")
	}
}

func TestPositionEventPayloadCarriesVelocityAndLastSeen(t *testing.T) {
	vn, ve, vu := 100.0, -20.0, 2.5
	msg := IngestMessage{
		Latitude: 41.9, Longitude: -87.9, Altitude: 10000, Heading: 270, SpeedKnots: 200, Course: 271,
		VelocityNorth: &vn, VelocityEast: &ve, VelocityUp: &vu, Source: "opensky", Timestamp: "2026-10-14T00:00:00Z",
	}
	seen := time.Date(2026, 10, 14, 0, 0, 5, 0, time.UTC)
	p := positionEventPayload("id-1", "AIRCRAFT", msg, seen)

	if p["entity_id"] != "id-1" || p["entity_type"] != "AIRCRAFT" || p["latitude"] != 41.9 {
		t.Errorf("identity/position fields wrong: %+v", p)
	}
	if p["velocity_north"] != &vn || p["velocity_east"] != &ve || p["velocity_up"] != &vu {
		t.Errorf("velocity pointers not passed through: %+v", p)
	}
	if p["last_seen_at"] != "2026-10-14T00:00:05Z" {
		t.Errorf("last_seen_at = %v", p["last_seen_at"])
	}

	// Missing velocities serialise as null, not as zero.
	raw, _ := json.Marshal(positionEventPayload("id-2", "AIRCRAFT", IngestMessage{}, seen))
	var decoded map[string]interface{}
	_ = json.Unmarshal(raw, &decoded)
	if v, ok := decoded["velocity_north"]; !ok || v != nil {
		t.Errorf("absent velocity should be null, got %v", v)
	}
}

func TestCreatedEventPayloadCarriesMotion(t *testing.T) {
	vn := 50.0
	entity := &store.EntityRecord{ID: "id-1", EntityType: "AIRCRAFT", Name: "UAL123", Source: "OPENSKY", Classification: "UNCLASSIFIED", CreatedAt: "2026-10-14T00:00:00Z", UpdatedAt: "2026-10-14T00:00:00Z"}
	msg := IngestMessage{Latitude: 1, Longitude: 2, Altitude: 3000, Heading: 90, SpeedKnots: 150, Course: 90, VelocityNorth: &vn}
	p := createdEventPayload(entity, msg)
	if p["id"] != "id-1" || p["altitude"] != 3000.0 || p["heading"] != 90.0 || p["speed_knots"] != 150.0 || p["velocity_north"] != &vn {
		t.Errorf("created payload missing motion fields: %+v", p)
	}
	if p["last_seen_at"] != "2026-10-14T00:00:00Z" {
		t.Errorf("last_seen_at = %v", p["last_seen_at"])
	}
}
