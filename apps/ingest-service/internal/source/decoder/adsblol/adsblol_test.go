package adsblol

import (
	"math"
	"testing"
	"time"

	"github.com/sentinel/ingest-service/internal/obs"
)

// Fixture is a 3-aircraft response, shared with the driver tests.
const Fixture = `{"now": 1705320000.5, "ac": [
    {"hex": "ae63ed", "flight": "ROPER23 ", "r": "67-14828", "t": "UH60L", "lat": 38.859155, "lon": -121.938660, "alt_baro": 3400, "alt_geom": 3550, "gs": 346.1, "track": 50.75, "squawk": "4540", "seen_pos": 1.5},
    {"hex": "ae1234", "flight": "EVAC01  ", "r": "90-0100", "t": "C17", "lat": 34.052235, "lon": -118.243683, "alt_baro": "ground", "alt_geom": 35200, "gs": 450.0, "track": 270.0, "squawk": "1200"},
    {"hex": "a12345", "flight": "TOPGUN  ", "lat": 36.778259, "lon": -119.417931, "alt_baro": 25000, "gs": 500.5, "track": 180.0}
]}`

var now = time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)

func TestDecode(t *testing.T) {
	out, err := Decode([]byte(Fixture), now)
	if err != nil || len(out) != 3 {
		t.Fatalf("out=%d err=%v", len(out), err)
	}
	o := out[0]
	if o.SourceEntityID != "ICAO-AE63ED" || o.Source != "ADSB_LOL" || o.EntityType != obs.EntityAircraft || o.DisplayName != "ROPER23" {
		t.Errorf("identity = %+v", o)
	}
	if o.Latitude != 38.859155 || math.Abs(*o.Altitude-3400*0.3048) > 0.01 || *o.SpeedKnots != 346.1 || *o.Heading != 50.75 {
		t.Errorf("kinematics = %+v", o)
	}
	if o.ADSB == nil || o.ADSB.Registration != "67-14828" || o.ADSB.AircraftType != "UH60L" || o.ADSB.Squawk != "4540" || math.Abs(o.ADSB.AltitudeGeom-3550*0.3048) > 0.01 {
		t.Errorf("adsb = %+v", o.ADSB)
	}
	// now = 1705320000.5 and seen_pos = 1.5 → fix at 1705319999.0
	if o.SourceTimestamp.UnixMilli() != 1705319999000 {
		t.Errorf("timestamp = %v", o.SourceTimestamp)
	}
	if o.Velocity == nil || math.Abs(o.Velocity[0]-346.1/knotsPerMeterPerSec*math.Cos(50.75*math.Pi/180)) > 0.01 {
		t.Errorf("velocity = %v", o.Velocity)
	}
	wantIDs := []obs.Identity{{Type: obs.IdentityICAO24, Value: "AE63ED"}, {Type: obs.IdentityCallsign, Value: "ROPER23"}, {Type: obs.IdentityRegistration, Value: "67-14828"}}
	if len(o.Identities) != 3 {
		t.Fatalf("identities = %v", o.Identities)
	}
	for i, w := range wantIDs {
		if o.Identities[i] != w {
			t.Errorf("identity %d = %v, want %v", i, o.Identities[i], w)
		}
	}
	if g := out[1]; !g.ADSB.OnGround || g.Altitude != nil {
		t.Errorf("ground aircraft = %+v alt=%v", g.ADSB, g.Altitude)
	}
	if out[2].ADSB.Registration != "" || out[2].DisplayName != "TOPGUN" {
		t.Errorf("third = %+v", out[2])
	}
}

func TestNullsAndEmpty(t *testing.T) {
	out, err := Decode([]byte(`{"ac":[{"hex":"ae63ed","flight":"X"},{"hex":"ae0000","lat":1,"lon":2}]}`), now)
	if err != nil || len(out) != 1 || out[0].SourceEntityID != "ICAO-AE0000" || out[0].DisplayName != "ae0000" || !out[0].SourceTimestamp.Equal(now) {
		t.Errorf("out=%+v err=%v", out, err)
	}
	for _, body := range []string{`{"ac":[]}`, `{"ac":null}`, `{}`} {
		if out, err := Decode([]byte(body), now); err != nil || len(out) != 0 {
			t.Errorf("%s: %v %v", body, out, err)
		}
	}
	if _, err := Decode([]byte(`{"ac":[{"hex":"a","alt_baro":"sky"}]}`), now); err == nil {
		t.Error("unknown alt_baro string must fail")
	}
}
