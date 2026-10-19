package geo

import "testing"

func TestPolarCovarianceDueNorth(t *testing.T) {
	r, sr, saz := 10_000.0, 50.0, 0.5 // σ az 0.5° → 87.27 m across track at 10 km
	cov, ok := PolarCovarianceNEU(0, 0, r, Sigma{RangeM: &sr, AzimuthDeg: &saz})
	if !ok {
		t.Fatal("sigmas given")
	}
	across := r * rad(saz)
	near(t, "nn", cov[0], sr*sr, 1e-6)
	near(t, "ne", cov[1], 0, 1e-6)
	near(t, "ee", cov[3], across*across, 1e-6)
	near(t, "uu", cov[5], 0, 1e-6) // no elevation sigma
}

// Review focus 2: the Jacobian rotates with azimuth.
func TestPolarCovarianceDueEast(t *testing.T) {
	r, sr, saz := 10_000.0, 50.0, 0.5
	cov, _ := PolarCovarianceNEU(90, 0, r, Sigma{RangeM: &sr, AzimuthDeg: &saz})
	across := r * rad(saz)
	near(t, "ee", cov[3], sr*sr, 1e-6)         // range error is now east-west
	near(t, "nn", cov[0], across*across, 1e-6) // azimuth error is now north-south
	near(t, "ne", cov[1], 0, 1e-6)
}

func TestPolarCovarianceElevationAndNone(t *testing.T) {
	r, sel := 20_000.0, 0.2
	cov, ok := PolarCovarianceNEU(0, 0, r, Sigma{ElevationDeg: &sel})
	if !ok {
		t.Fatal("elevation sigma given")
	}
	up := r * rad(sel)
	near(t, "uu", cov[5], up*up, 1e-6)
	if _, ok := PolarCovarianceNEU(10, 1, r, Sigma{}); ok {
		t.Error("no sigma, no covariance")
	}
}

// Review focus 3: orientation for north, east and diagonal elongation, and the round trip.
func TestEllipseFromCov2(t *testing.T) {
	maj, min, o := EllipseFromCov2(400, 0, 100) // σn 20, σe 10
	near(t, "major", maj, Ellipse95*20, 1e-9)
	near(t, "minor", min, Ellipse95*10, 1e-9)
	near(t, "north", o, 0, 1e-9)

	_, _, o = EllipseFromCov2(100, 0, 400)
	near(t, "east", o, 90, 1e-9)

	_, _, o = EllipseFromCov2(250, 150, 250)
	near(t, "north-east", o, 45, 1e-9)

	_, _, o = EllipseFromCov2(250, -150, 250)
	near(t, "north-west", o, 135, 1e-9)

	maj, min, _ = EllipseFromCov2(100, 0, 100)
	near(t, "circle", maj, min, 1e-9)
}

func TestEllipseCovarianceRoundTrip(t *testing.T) {
	for _, e := range []struct{ maj, min, o float64 }{{1000, 400, 30}, {500, 500, 0}, {2500, 100, 170}, {800, 200, 90}} {
		nn, ne, ee := Cov2FromEllipse(e.maj, e.min, e.o)
		maj, min, o := EllipseFromCov2(nn, ne, ee)
		near(t, "major", maj, e.maj, 1e-6)
		near(t, "minor", min, e.min, 1e-6)
		if e.maj != e.min { // a circle has no orientation
			near(t, "orientation", o, e.o, 1e-6)
		}
	}
}

func TestCEP(t *testing.T) {
	near(t, "circular", CEP(100*100, 0, 100*100), 117.74, 0.01)
	near(t, "same via ellipse", CEP(Cov2FromEllipse(Ellipse95*100, Ellipse95*100, 0)), 117.74, 0.01)
}
