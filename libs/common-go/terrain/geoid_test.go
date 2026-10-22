package terrain

import (
	"math"
	"testing"
)

func near(t *testing.T, what string, got, want, tol float64) {
	t.Helper()
	if math.IsNaN(got) || math.Abs(got-want) > tol {
		t.Errorf("%s = %.6f, want %.6f (±%g)", what, got, want, tol)
	}
}

func TestGeoidGridSize(t *testing.T) {
	// 721 rows x 1441 columns x 2 bytes.
	if got, want := len(egm96Raw), 721*1441*2; got != want {
		t.Fatalf("embedded grid is %d bytes, want %d", got, want)
	}
}

func TestGeoidHeightReferencePoints(t *testing.T) {
	cases := []struct {
		name     string
		lat, lon float64
		want     float64
	}{
		{"null island", 0, 0, 17.16},
		{"Everest", 27.9881, 86.9250, -28.74},
		{"south pole", -90, 0, -29.53},
		{"Washington", 38.8895, -77.0353, -32.6},
	}
	for _, c := range cases {
		near(t, c.name, GeoidHeight(c.lat, c.lon), c.want, 1.0)
	}
}

// TestGeoidHeightNGAPublished checks the grid against the outputs NGA ships
// with the grid itself (OUTINTPT.DAT, produced by NGA's INTPT.F with its
// default spline window). The same program, run for the reference points
// above, gives 17.162 (0, 0), -28.732 (Everest) and -33.220 (38.8895,
// 282.9647), so the plan's -32.6 for Washington is 0.6 m off the published
// value; it is still inside the 1 m tolerance above.
//
// NGA's program uses a spline; this package is bilinear, as the plan
// specifies. They agree to a few millimetres on smooth ground and differ by up
// to 0.13 m where the geoid is rough (the Himalaya, the Australian point
// below). A bilinear evaluation of the raw NGA grid, computed outside this
// package, gives 15.927 and -28.867 for those two points, matching
// GeoidHeight, so the gap is the interpolation scheme and not the data.
// 0.15 m therefore still catches any real error in the grid, the row and
// column order, or the longitude convention.
func TestGeoidHeightNGAPublished(t *testing.T) {
	cases := []struct {
		lat, lon float64 // longitude in NGA's 0..360 convention
		want     float64
	}{
		{38.628155, 269.779155, -31.628},
		{-14.621217, 305.021114, -2.969},
		{46.874319, 102.448729, -43.575},
		{-23.617446, 133.874712, 15.871},
		{38.625473, 359.9995, 50.066},
		{-0.466744, 0.0023, 17.329},
		{0, 0, 17.162},
		{27.9881, 86.9250, -28.732},
		{38.8895, 282.9647, -33.220},
		{0, 180, 21.153},
	}
	for _, c := range cases {
		near(t, "N", GeoidHeight(c.lat, c.lon), c.want, 0.15)
	}
}

func TestGeoidHeightGridNodes(t *testing.T) {
	// Nodes read straight from NGA's WW15MGH.GRD.
	cases := []struct {
		lat, lon float64
		want     float64
	}{
		{90, 0, 13.606},
		{90, 123, 13.606},
		{-90, 0, -29.534},
		{-90, -77, -29.534},
	}
	for _, c := range cases {
		near(t, "node", GeoidHeight(c.lat, c.lon), c.want, 0.006)
	}
}

func TestGeoidWrapsLongitude(t *testing.T) {
	near(t, "180 vs -180", GeoidHeight(10, 180), GeoidHeight(10, -180), 1e-9)
	near(t, "359.9 vs -0.1", GeoidHeight(33.3, 359.9), GeoidHeight(33.3, -0.1), 1e-9)
	near(t, "360 vs 0", GeoidHeight(-12, 360), GeoidHeight(-12, 0), 1e-9)
	near(t, "540 vs -180", GeoidHeight(5, 540), GeoidHeight(5, -180), 1e-9)
	// Across the seam the surface is continuous.
	near(t, "seam", GeoidHeight(38.6, 359.9999), GeoidHeight(38.6, 0.0001), 0.01)
}

func TestGeoidHeightOutOfRange(t *testing.T) {
	near(t, "lat above 90", GeoidHeight(95, 10), GeoidHeight(90, 10), 1e-9)
	near(t, "lat below -90", GeoidHeight(-120, 10), GeoidHeight(-90, 10), 1e-9)
	if got := GeoidHeight(math.NaN(), 0); !math.IsNaN(got) {
		t.Errorf("NaN latitude gave %v, want NaN", got)
	}
	if got := GeoidHeight(0, math.Inf(1)); !math.IsNaN(got) {
		t.Errorf("infinite longitude gave %v, want NaN", got)
	}
}

func TestMSLHAERoundTrip(t *testing.T) {
	pts := [][2]float64{{0, 0}, {27.9881, 86.925}, {-33.8688, 151.2093}, {38.8895, -77.0353}, {89.9, 45}, {-60, -179.99}}
	for _, p := range pts {
		n := GeoidHeight(p[0], p[1])
		near(t, "MSLToHAE", MSLToHAE(p[0], p[1], 100), 100+n, 1e-9)
		near(t, "HAEToMSL", HAEToMSL(p[0], p[1], 100), 100-n, 1e-9)
		for _, h := range []float64{-50, 0, 8848, 12345.678} {
			near(t, "round trip", HAEToMSL(p[0], p[1], MSLToHAE(p[0], p[1], h)), h, 1e-9)
		}
	}
}
