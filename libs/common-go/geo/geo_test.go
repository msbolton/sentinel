package geo

import (
	"math"
	"testing"
)

func near(t *testing.T, what string, got, want, tol float64) {
	t.Helper()
	if math.IsNaN(got) || math.Abs(got-want) > tol {
		t.Errorf("%s = %.9f, want %.9f (±%g)", what, got, want, tol)
	}
}

func TestToECEFReferencePoints(t *testing.T) {
	cases := []struct {
		p       LLA
		x, y, z float64
	}{
		{LLA{0, 0, 0}, 6378137, 0, 0},
		{LLA{0, 90, 0}, 0, 6378137, 0},
		{LLA{90, 0, 0}, 0, 0, 6356752.314245},
		{LLA{45, 45, 1000}, 3194919.1451, 3194919.1451, 4488055.5156},
		{LLA{-33.8688, 151.2093, 58}, -4646093.4773, 2553229.5358, -3534404.7109},
	}
	for _, c := range cases {
		got := ToECEF(c.p)
		near(t, "x", got.X, c.x, 0.001)
		near(t, "y", got.Y, c.y, 0.001)
		near(t, "z", got.Z, c.z, 0.001)
	}
}

func TestFromECEFRoundTrips(t *testing.T) {
	for _, p := range []LLA{{0, 0, 0}, {51.5, -0.12, 35}, {-33.8688, 151.2093, 58}, {89.9999, 10, 100}, {-90, 0, 0}, {10, 179.9, 8848}, {45, -170, -400}} {
		got := FromECEF(ToECEF(p))
		near(t, "lat", got.Lat, p.Lat, 1e-9)
		if math.Abs(p.Lat) < 90 { // longitude is undefined at a pole
			near(t, "lon", got.Lon, p.Lon, 1e-9)
		}
		near(t, "alt", got.Alt, p.Alt, 1e-4)
	}
}

func TestAERToLLAOneKilometreEastOfNullIsland(t *testing.T) {
	got := AERToLLA(LLA{0, 0, 0}, 90, 0, 1000)
	near(t, "lat", got.Lat, 0, 1e-10)
	near(t, "lon", got.Lon, 0.0089831528, 1e-9)
	near(t, "alt", got.Alt, 0.078393, 1e-5) // a horizontal ray rises above the curved Earth
}

func TestAERRoundTrips(t *testing.T) {
	origin := LLA{50.1, -4.2, 30}
	for _, c := range []struct{ az, el, r float64 }{{0, 0, 10_000}, {90, 2, 60_000}, {200, -1, 35_000}, {359.5, 10, 500}, {45, 30, 120_000}} {
		p := AERToLLA(origin, c.az, c.el, c.r)
		az, el, r := LLAToAER(origin, p)
		near(t, "az", az, c.az, 1e-7)
		near(t, "el", el, c.el, 1e-7)
		near(t, "range", r, c.r, 1e-4)
	}
	if az, _, _ := LLAToAER(origin, AERToLLA(origin, 315, 0, 1000)); az < 0 || az >= 360 {
		t.Fatalf("azimuth not in [0, 360): %v", az)
	}
}

func TestENURoundTrip(t *testing.T) {
	origin := LLA{-33.8688, 151.2093, 58}
	v := ENU{1234.5, -987.6, 42}
	back := ENUOf(origin, ECEFOf(origin, v))
	near(t, "e", back.E, v.E, 1e-6)
	near(t, "n", back.N, v.N, 1e-6)
	near(t, "u", back.U, v.U, 1e-6)
}

// Review focus 1: an unknown elevation is solved for the target's altitude.
func TestAERToLLAAtAltitude(t *testing.T) {
	origin := LLA{50.1, -4.2, 30}
	p, el, ok := AERToLLAAtAltitude(origin, 120, 40_000, 3000)
	if !ok {
		t.Fatal("a target 3 km up at 40 km is reachable")
	}
	near(t, "alt", p.Alt, 3000, 0.5)
	_, _, r := LLAToAER(origin, p)
	near(t, "range", r, 40_000, 0.5)
	if el <= 0 {
		t.Errorf("elevation to a higher target must be positive, got %v", el)
	}

	sea, el0, ok := AERToLLAAtAltitude(origin, 0, 80_000, 0) // near the horizon: curvature matters
	if !ok {
		t.Fatal("a sea-level target at 80 km is reachable")
	}
	near(t, "sea alt", sea.Alt, 0, 0.5)
	if el0 >= 0 {
		t.Errorf("a sea-level target 80 km away is below the horizontal, got el %v", el0)
	}

	if _, _, ok := AERToLLAAtAltitude(origin, 0, 1000, -5000); ok {
		t.Error("a target 5 km below the sensor cannot be 1 km away")
	}
	if p, _, ok := AERToLLAAtAltitude(origin, 0, 0, origin.Alt); !ok || p != origin {
		t.Errorf("zero range is the origin, got %+v %v", p, ok)
	}
}

// Azimuth is documented as [0, 360): a due-north target must never come back as exactly 360.
func TestLLAToAERAzimuthStaysBelow360(t *testing.T) {
	origin := LLA{50.1, -4.2, 30}
	for el := -5.0; el <= 30; el++ {
		for _, r := range []float64{500, 21858.32, 60_000, 300_000} {
			az, _, _ := LLAToAER(origin, AERToLLA(origin, 0, el, r))
			if az < 0 || az >= 360 {
				t.Errorf("el %v range %v: azimuth %.17g not in [0, 360)", el, r, az)
			}
			if math.Min(az, 360-az) > 1e-6 {
				t.Errorf("el %v range %v: due north came back as azimuth %v", el, r, az)
			}
		}
	}
}

// The round trips cannot see an error mirrored in both directions, so these
// pin the compass in absolute terms away from the equator and the prime meridian.
func TestAERPointsTheRightWayAwayFromTheEquator(t *testing.T) {
	origin := LLA{50.1, -4.2, 30}

	north := AERToLLA(origin, 0, 0, 10_000)
	if north.Lat <= origin.Lat {
		t.Errorf("north of the origin must have a higher latitude, got %v vs %v", north.Lat, origin.Lat)
	}
	near(t, "north lon", north.Lon, origin.Lon, 1e-9)

	east := AERToLLA(origin, 90, 0, 10_000)
	if east.Lon <= origin.Lon {
		t.Errorf("east of the origin must have a higher longitude, got %v vs %v", east.Lon, origin.Lon)
	}
	near(t, "east lat", east.Lat, origin.Lat, 0.001)

	if south := AERToLLA(origin, 180, 0, 10_000); south.Lat >= origin.Lat {
		t.Errorf("south of the origin must have a lower latitude, got %v vs %v", south.Lat, origin.Lat)
	}
	if west := AERToLLA(origin, 270, 0, 10_000); west.Lon >= origin.Lon {
		t.Errorf("west of the origin must have a lower longitude, got %v vs %v", west.Lon, origin.Lon)
	}

	v := ENUOf(origin, ToECEF(LLA{origin.Lat + 0.01, origin.Lon, origin.Alt}))
	if v.N <= 0 {
		t.Errorf("a point at a higher latitude must lie to the north, got N %v", v.N)
	}
	near(t, "e", v.E, 0, 1e-6)
}
