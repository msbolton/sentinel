package batch

import (
	"reflect"
	"testing"
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
