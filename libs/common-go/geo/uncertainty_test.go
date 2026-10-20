package geo

import (
	"math"
	"math/rand/v2"
	"slices"
	"testing"
)

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

	// A covariance that is not positive semi-definite gives zero axes, not NaN.
	maj, min, o = EllipseFromCov2(-4, 0, -1)
	if math.IsNaN(maj) || math.IsNaN(min) || math.IsNaN(o) || maj != 0 || min != 0 {
		t.Fatalf("non-PSD covariance = %v, %v, %v", maj, min, o)
	}
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

func TestCEP95(t *testing.T) {
	near(t, "circular", CEP95(100*100, 0, 100*100), 244.77, 0.01)
	near(t, "same via ellipse", CEP95(Cov2FromEllipse(Ellipse95*100, Ellipse95*100, 0)), 244.77, 0.01)
	if got := CEP95(0, 0, 0); got != 0 {
		t.Errorf("CEP95(0,0,0) = %v, want 0", got)
	}

	// Verify CEP95 against both the 1-D radial/Cartesian erf integral
	// P(X²+Y² ≤ R²) = 0.95 and a fixed-seed Monte Carlo across aspect ratios
	// k = σ2/σ1 ∈ {0, 0.1, 0.25, 0.5, 1}.
	rng := rand.New(rand.NewPCG(42, 1024))
	const N = 200_000
	z1 := make([]float64, N)
	z2 := make([]float64, N)
	for i := 0; i < N; i++ {
		z1[i] = rng.NormFloat64()
		z2[i] = rng.NormFloat64()
	}
	radii := make([]float64, N)

	// Cartesian integral P(X²+Y² ≤ R²) for k > 0 using math.Erf.
	cdfNumeric := func(s1, s2, r float64) float64 {
		if s2 == 0 {
			return math.Erf(r / (math.Sqrt2 * s1))
		}
		const m = 512
		dPhi := (math.Pi / 2) / m
		sum := 0.0
		for i := 0; i < m; i++ {
			phi := (float64(i) + 0.5) * dPhi
			s, c := math.Sincos(phi)
			y := r * s
			xMax := r * c
			sum += c * math.Exp(-0.5*(y*y)/(s2*s2)) * math.Erf(xMax/(math.Sqrt2*s1))
		}
		return (2 * r / (math.Sqrt(2*math.Pi) * s2)) * sum * dPhi
	}

	s1 := 100.0
	for _, k := range []float64{0, 0.1, 0.25, 0.5, 1.0} {
		s2 := k * s1
		got := CEP95(s1*s1, 0, s2*s2)
		prob := cdfNumeric(s1, s2, got)
		if math.Abs(prob-0.95) > 1e-4 {
			t.Errorf("k=%v: P(r <= CEP95=%v) = %.6f, want 0.95", k, got, prob)
		}
		for i := 0; i < N; i++ {
			radii[i] = math.Hypot(s1*z1[i], s2*z2[i])
		}
		slices.Sort(radii)
		mc := radii[int(0.95*float64(N))]
		relErr := math.Abs(got-mc) / mc
		if relErr > 0.01 {
			t.Errorf("k=%v: CEP95=%v vs Monte Carlo=%v (relErr=%.4f > 1%%)", k, got, mc, relErr)
		}
	}

	// Specifically check the 1800 m x 420 m 95% ellipse example (~1474 m).
	r1800 := CEP95(Cov2FromEllipse(1800, 420, 32))
	if r1800 < 1450 || r1800 > 1500 {
		t.Errorf("CEP95(1800x420) = %v, want in [1450, 1500]", r1800)
	}
}

// The axis-aligned cases above zero every cross term. At an oblique geometry
// the covariance must equal J·diag(σ²)·Jᵀ, with J taken numerically from
// AERToENU, so a mirrored azimuth or a wrong elevation column cannot pass.
func TestPolarCovarianceObliqueMatchesNumericJacobian(t *testing.T) {
	az, el, r := 30.0, 20.0, 25_000.0
	sr, saz, sel := 40.0, 0.3, 0.4
	cov, ok := PolarCovarianceNEU(az, el, r, Sigma{RangeM: &sr, AzimuthDeg: &saz, ElevationDeg: &sel})
	if !ok {
		t.Fatal("sigmas given")
	}

	// Central differences; the angle steps are in degrees, so the derivative
	// is converted to per radian.
	const hR, hDeg = 1.0, 1e-3
	col := func(plus, minus ENU, h float64) [3]float64 { // [e, n, u]
		return [3]float64{(plus.E - minus.E) / h, (plus.N - minus.N) / h, (plus.U - minus.U) / h}
	}
	jr := col(AERToENU(az, el, r+hR), AERToENU(az, el, r-hR), 2*hR)
	ja := col(AERToENU(az+hDeg, el, r), AERToENU(az-hDeg, el, r), rad(2*hDeg))
	je := col(AERToENU(az, el+hDeg, r), AERToENU(az, el-hDeg, r), rad(2*hDeg))
	varR, varAz, varEl := sr*sr, rad(saz)*rad(saz), rad(sel)*rad(sel)
	c := func(i, j int) float64 { return jr[i]*jr[j]*varR + ja[i]*ja[j]*varAz + je[i]*je[j]*varEl }
	const e, n, u = 0, 1, 2
	want := [6]float64{c(n, n), c(n, e), c(n, u), c(e, e), c(e, u), c(u, u)}

	largest := 0.0
	for _, w := range want {
		largest = math.Max(largest, math.Abs(w))
	}
	for i, name := range []string{"nn", "ne", "nu", "ee", "eu", "uu"} {
		near(t, name, cov[i], want[i], 1e-6*largest)
	}
}

// Azimuth runs clockwise from north: a range error at 45° is positively
// correlated between north and east.
func TestPolarCovarianceSignAnchor(t *testing.T) {
	sr := 100.0
	cov, _ := PolarCovarianceNEU(45, 0, 5_000, Sigma{RangeM: &sr})
	near(t, "nn", cov[0], sr*sr/2, 1e-6)
	near(t, "ne", cov[1], sr*sr/2, 1e-6)
	near(t, "ee", cov[3], sr*sr/2, 1e-6)
}
