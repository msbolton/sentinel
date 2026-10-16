package openskystates

import (
	"math"
	"testing"

	"github.com/sentinel/ingest-service/internal/obs"
)

// Fixture is a realistic 3-aircraft response, shared with the driver tests.
const Fixture = `{
  "time": 1700000000,
  "states": [
    ["ab1234", "UAL123  ", "United States", 1700000000, 1700000000, -87.9045, 41.9742, 10972.8, false, 230.5, 45.0, 0.0, null, 11277.6, "1234", false, 0],
    ["cd5678", "DAL456  ", "United States", 1700000000, 1700000000, -73.7781, 40.6413, null, false, 150.2, 270.3, 0.0, null, 5486.4, "5678", false, 0],
    ["ef9012", "        ", "Germany", 1699999990, 1699999990, 13.4050, 52.5200, 3048.0, false, null, 180.0, 0.0, null, null, "9012", false, 0]
  ]
}`

const fixtureNulls = `{"time": 1700000000, "states": [
    ["ab1234", "UAL123  ", "United States", 1700000000, 1700000000, null, null, 10000.0, false, 200.0, 90.0, 0.0, null, 10500.0, "1234", false, 0],
    ["cd5678", "DAL456  ", "United States", 1700000000, 1700000000, -73.7781, 40.6413, null, false, null, null, 0.0, null, null, "5678", false, 0]
]}`

func TestParse(t *testing.T) {
	states, err := Parse([]byte(Fixture))
	if err != nil || len(states) != 3 {
		t.Fatalf("states=%d err=%v", len(states), err)
	}
	if states[0].Icao24 != "ab1234" || states[0].Callsign != "UAL123" || *states[0].Latitude != 41.9742 || *states[0].BaroAltitude != 10972.8 || *states[0].Velocity != 230.5 || states[2].Callsign != "" {
		t.Errorf("parsed = %+v", states)
	}
	for _, body := range []string{`{"time":1,"states":[]}`, `{"time":1,"states":null}`} {
		if s, err := Parse([]byte(body)); err != nil || len(s) != 0 {
			t.Errorf("%s: %v %v", body, s, err)
		}
	}
	if _, err := Parse([]byte("nope")); err == nil {
		t.Error("invalid JSON must fail")
	}
}

func TestConvert(t *testing.T) {
	out, err := Decode([]byte(Fixture))
	if err != nil || len(out) != 3 {
		t.Fatalf("out=%d err=%v", len(out), err)
	}
	o := out[0]
	if o.SourceEntityID != "ICAO-AB1234" || o.EntityType != obs.EntityAircraft || o.Source != "OPENSKY" || o.DisplayName != "UAL123" || o.CountryOfOrigin != "United States" {
		t.Errorf("identity = %+v", o)
	}
	if o.Latitude != 41.9742 || o.Longitude != -87.9045 || *o.Altitude != 10972.8 || math.Abs(*o.SpeedKnots-230.5*metersPerSecToKnots) > 0.01 || *o.Heading != 45 || *o.Course != 45 {
		t.Errorf("kinematics = %+v", o)
	}
	if o.SourceTimestamp.Unix() != 1700000000 {
		t.Errorf("timestamp = %v", o.SourceTimestamp)
	}
	if id, ok := o.CanonicalIdentity(); !ok || id.Value != "AB1234" {
		t.Errorf("identities = %v", o.Identities)
	}
	if o.ADSB == nil || o.ADSB.ICAOHex != "AB1234" || o.ADSB.AircraftID != "UAL123" || o.ADSB.AltitudeGeom != 11277.6 {
		t.Errorf("adsb = %+v", o.ADSB)
	}
	if *out[1].Altitude != 5486.4 {
		t.Errorf("geo altitude fallback = %v", *out[1].Altitude)
	}
	if out[2].DisplayName != "ef9012" {
		t.Errorf("name fallback = %q", out[2].DisplayName)
	}
}

func TestNullFields(t *testing.T) {
	out, err := Decode([]byte(fixtureNulls))
	if err != nil || len(out) != 1 {
		t.Fatalf("out=%d err=%v", len(out), err)
	}
	o := out[0]
	if o.SpeedKnots != nil || o.Heading != nil || o.Altitude != nil {
		t.Errorf("null fields must stay nil: %+v", o)
	}
	// vertical_rate is 0.0 (present), so a velocity with only the up component is reported.
	if o.Velocity == nil || *o.Velocity != [3]float64{} {
		t.Errorf("velocity = %v", o.Velocity)
	}
}
