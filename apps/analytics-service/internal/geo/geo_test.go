package geo

import (
	"math"
	"testing"
)

func TestHaversineKnownDistances(t *testing.T) {
	cases := []struct {
		name                   string
		lat1, lon1, lat2, lon2 float64
		wantM, tolM            float64
	}{
		{"same point", 51.5, -0.12, 51.5, -0.12, 0, 0.001},
		{"one degree of latitude", 0, 0, 1, 0, 111195, 50},
		{"London to Paris", 51.5074, -0.1278, 48.8566, 2.3522, 343500, 1500},
		{"across the antimeridian", 0, 179.9, 0, -179.9, 22239, 50},
	}
	for _, c := range cases {
		got := HaversineM(c.lat1, c.lon1, c.lat2, c.lon2)
		if math.Abs(got-c.wantM) > c.tolM {
			t.Errorf("%s: got %.0f m, want %.0f ± %.0f", c.name, got, c.wantM, c.tolM)
		}
	}
}

func TestCellIsStableAndTiles(t *testing.T) {
	if Cell(10.01, 20.01, 0.05) != Cell(10.04, 20.04, 0.05) {
		t.Error("two points in the same 0.05° cell map to different cells")
	}
	if Cell(10.01, 20.01, 0.05) == Cell(10.06, 20.01, 0.05) {
		t.Error("points one cell apart share a cell")
	}
	// Negative coordinates floor towards -inf, so -0.01 is not the same cell as 0.01.
	if Cell(-0.01, -0.01, 0.05) == Cell(0.01, 0.01, 0.05) {
		t.Error("cells either side of the equator and meridian collide")
	}
	if got := Cell(-0.01, -0.01, 0.05); got != "-1:-1" {
		t.Errorf("Cell(-0.01,-0.01) = %q, want -1:-1", got)
	}
	if got := Cell(1, 2, 0); got != Cell(1, 2, 0.05) {
		t.Error("a non-positive cell size does not fall back to the default")
	}
}

func TestSolarOffsetHours(t *testing.T) {
	cases := map[float64]int{0: 0, 7.4: 0, 7.6: 1, -74: -5, 139.7: 9, -180: -12, 180: 12}
	for lon, want := range cases {
		if got := SolarOffsetHours(lon); got != want {
			t.Errorf("SolarOffsetHours(%v) = %d, want %d", lon, got, want)
		}
	}
}
