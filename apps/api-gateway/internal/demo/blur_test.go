package demo

import (
	"math"
	"testing"
)

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestBlurSnapsToCellCentre(t *testing.T) {
	lat, lon := Blur(51.5074, -0.1278)
	if !near(lat, 51.55) || !near(lon, -0.15) {
		t.Fatalf("Blur(51.5074, -0.1278) = (%v, %v), want (51.55, -0.15)", lat, lon)
	}
}

func TestBlurIsStable(t *testing.T) {
	lat1, lon1 := Blur(51.5010, -0.1990)
	lat2, lon2 := Blur(51.5990, -0.1010)
	if lat1 != lat2 || lon1 != lon2 {
		t.Fatalf("same cell gave (%v, %v) and (%v, %v)", lat1, lon1, lat2, lon2)
	}
}

func TestBlurNegativeAndEdges(t *testing.T) {
	lat, lon := Blur(-33.8688, 151.2093)
	if !near(lat, -33.85) || !near(lon, 151.25) {
		t.Fatalf("Blur(-33.8688, 151.2093) = (%v, %v), want (-33.85, 151.25)", lat, lon)
	}

	lat, _ = Blur(90, 0)
	if lat > 90 || lat < -90 {
		t.Fatalf("latitude 90 blurred to %v, outside [-90, 90]", lat)
	}
	lat, _ = Blur(-90, 0)
	if lat > 90 || lat < -90 {
		t.Fatalf("latitude -90 blurred to %v, outside [-90, 90]", lat)
	}

	_, lon = Blur(0, 180)
	if lon < -180 || lon >= 180 {
		t.Fatalf("longitude 180 blurred to %v, outside [-180, 180)", lon)
	}
	_, lon = Blur(0, -180)
	if lon < -180 || lon >= 180 {
		t.Fatalf("longitude -180 blurred to %v, outside [-180, 180)", lon)
	}
}
