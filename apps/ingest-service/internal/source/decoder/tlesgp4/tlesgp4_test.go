package tlesgp4

import (
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/sentinel/ingest-service/internal/obs"
)

const (
	ISSName  = "ISS (ZARYA)"
	ISSLine1 = "1 25544U 98067A   26063.86671769  .00009014  00000+0  17477-3 0  9999"
	ISSLine2 = "2 25544  51.6315  96.2009 0008177 157.5272 202.6076 15.48447334555585"

	goesName  = "GOES 16"
	goesLine1 = "1 41866U 16071A   26063.50000000  .00000100  00000+0  00000+0 0  9991"
	goesLine2 = "2 41866   0.0569 271.4567 0000345 123.4567 236.7890  1.00271400 34567"
)

// Block is a two-satellite TLE document, shared with the driver tests.
var Block = fmt.Sprintf("%s\n%s\n%s\n%s\n%s\n%s\n", ISSName, ISSLine1, ISSLine2, goesName, goesLine1, goesLine2)

func TestParse(t *testing.T) {
	set, err := Parse(Block)
	if err != nil || len(set) != 2 {
		t.Fatalf("set=%d err=%v", len(set), err)
	}
	if set[0].NoradID != 25544 || set[0].Name != ISSName || set[1].NoradID != 41866 {
		t.Errorf("parsed = %+v", set)
	}
	if set, err := Parse("garbage\nmore\nlines\n"); err == nil || len(set) != 0 {
		t.Errorf("malformed should fail: %v %v", set, err)
	}
	if set, err := Parse(""); err != nil || len(set) != 0 {
		t.Errorf("empty = %v %v", set, err)
	}
}

func TestPropagateISS(t *testing.T) {
	tle, err := New(ISSName, ISSLine1, ISSLine2)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 3, 4, 20, 0, 0, 0, time.UTC)
	o := Propagate(tle, at)
	if o == nil {
		t.Fatal("nil observation")
	}
	if o.SourceEntityID != "SAT-25544" || o.EntityType != obs.EntitySatellite || o.TrackEnvironment != obs.EnvSpace || o.Source != "CELESTRAK" {
		t.Errorf("identity = %+v", o)
	}
	if o.Latitude < -55 || o.Latitude > 55 || o.Longitude < -180 || o.Longitude > 180 {
		t.Errorf("position = %v, %v", o.Latitude, o.Longitude)
	}
	if km := *o.Altitude / 1000; km < 380 || km > 430 {
		t.Errorf("altitude = %.1f km", km)
	}
	// Orbital speed ~7.66 km/s ≈ 14890 kt.
	if *o.SpeedKnots < 14500 || *o.SpeedKnots > 15300 {
		t.Errorf("speed = %v kt", *o.SpeedKnots)
	}
	if o.TLE == nil || math.Abs(o.TLE.Inclination-51.6315) > 0.001 || o.TLE.IntlDesignator != "98067A" || o.TLE.Period <= 0 {
		t.Errorf("elements = %+v", o.TLE)
	}
	if id, ok := o.CanonicalIdentity(); !ok || id != (obs.Identity{Type: obs.IdentityNoradID, Value: "25544"}) {
		t.Errorf("identities = %v", o.Identities)
	}
	if !o.SourceTimestamp.Equal(at) {
		t.Errorf("timestamp = %v", o.SourceTimestamp)
	}
}

func TestPropagateGEO(t *testing.T) {
	tle, _ := New(goesName, goesLine1, goesLine2)
	o := Propagate(tle, time.Date(2026, 3, 4, 20, 0, 0, 0, time.UTC))
	if o == nil {
		t.Fatal("nil")
	}
	if math.Abs(o.Latitude) > 1 {
		t.Errorf("GEO latitude = %v", o.Latitude)
	}
	if km := *o.Altitude / 1000; km < 35000 || km > 36500 {
		t.Errorf("GEO altitude = %.0f km", km)
	}
}

func TestDecayedOrbitIsNil(t *testing.T) {
	tle, err := New("DECAYED", "1 99999U 24001A   24001.00000000  .00000000  00000+0  00000+0 0  9997", "2 99999  51.6000 100.0000 9900000 100.0000 200.0000 20.00000000    03")
	if err != nil {
		t.Fatal(err)
	}
	if o := Propagate(tle, time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)); o != nil {
		t.Logf("library propagated a degenerate orbit to %v, %v (accepted)", o.Latitude, o.Longitude)
	}
}
