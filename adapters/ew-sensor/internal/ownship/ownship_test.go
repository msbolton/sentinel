package ownship_test

import (
	"math"
	"testing"
	"time"

	"github.com/sentinel/ew-sensor/internal/config"
	"github.com/sentinel/ew-sensor/internal/ownship"
	"github.com/sentinel/go-common/geo"
)

const kts = 1852.0 / 3600.0

func mobileCfg(o config.Ownship) config.Config {
	return config.Config{
		Emplacement: config.Emplacement{
			Lat: 36.2, Lon: -122.4, Alt: 18, Boresight: 15,
			Mobility: config.MobilityMobile, AzimuthReference: config.AzimuthPlatform,
		},
		Ownship: o,
	}
}

func finite(t *testing.T, p ownship.Pose) {
	t.Helper()
	for _, v := range []float64{p.Lat, p.Lon, p.Alt, p.HeadingDeg, p.VelNorth, p.VelEast, p.VelUp} {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			t.Fatalf("non-finite pose %+v", p)
		}
	}
	if p.Lat < -90 || p.Lat > 90 || p.Lon < -180 || p.Lon > 180 {
		t.Fatalf("pose off the globe %+v", p)
	}
	if p.HeadingDeg < 0 || p.HeadingDeg >= 360 {
		t.Fatalf("heading %v out of [0,360)", p.HeadingDeg)
	}
}

// checkConsistent verifies the velocity matches the heading and the speed, and
// that one second of motion matches the velocity.
func checkConsistent(t *testing.T, o *ownship.Ownship, at time.Duration, speed float64) {
	t.Helper()
	p := o.Pose(at)
	finite(t, p)
	if got := math.Hypot(p.VelNorth, p.VelEast); math.Abs(got-speed) > 1e-6 {
		t.Fatalf("t=%v: speed %.4f, want %.4f", at, got, speed)
	}
	h := math.Atan2(p.VelEast, p.VelNorth) * 180 / math.Pi
	if d := math.Abs(math.Mod(h-p.HeadingDeg+540, 360) - 180); d > 1e-6 {
		t.Fatalf("t=%v: velocity direction %.3f vs heading %.3f", at, h, p.HeadingDeg)
	}
	if p.VelUp != 0 {
		t.Fatalf("vertical velocity %v", p.VelUp)
	}
	q := o.Pose(at + time.Second)
	az, _, r := geo.LLAToAER(geo.LLA{Lat: p.Lat, Lon: p.Lon}, geo.LLA{Lat: q.Lat, Lon: q.Lon})
	if math.Abs(r-speed) > 0.02*speed {
		t.Fatalf("t=%v: moved %.2f m in 1 s, want %.2f", at, r, speed)
	}
	if d := math.Abs(math.Mod(az-p.HeadingDeg+540, 360) - 180); d > 1.5 { // curvature over 1 s on a small circle
		t.Fatalf("t=%v: displacement bearing %.2f vs heading %.2f", at, az, p.HeadingDeg)
	}
}

func TestCircle(t *testing.T) {
	cfg := mobileCfg(config.Ownship{Path: config.PathCircle, SpeedKts: 12, RadiusKm: 5})
	o := ownship.New(cfg)
	centre := geo.LLA{Lat: 36.2, Lon: -122.4}
	speed := 12 * kts
	period := time.Duration(2 * math.Pi * 5000 / speed * float64(time.Second))
	for at := time.Duration(0); at < 2*period; at += 97 * time.Second {
		_, _, r := geo.LLAToAER(centre, geo.LLA{Lat: o.Pose(at).Lat, Lon: o.Pose(at).Lon})
		if math.Abs(r-5000) > 25 {
			t.Fatalf("t=%v: %.0f m from centre, want 5000", at, r)
		}
		if o.Pose(at).Alt != 18 {
			t.Fatalf("altitude %v", o.Pose(at).Alt)
		}
		checkConsistent(t, o, at, speed)
	}
	// Periodic.
	a, b := o.Pose(0), o.Pose(period)
	if math.Abs(a.Lat-b.Lat) > 1e-6 || math.Abs(a.Lon-b.Lon) > 1e-6 {
		t.Fatalf("circle not periodic: %+v vs %+v", a, b)
	}
}

func TestCircleDefaultsRadiusWhenUnset(t *testing.T) {
	o := ownship.New(mobileCfg(config.Ownship{Path: config.PathCircle, SpeedKts: 12}))
	_, _, r := geo.LLAToAER(geo.LLA{Lat: 36.2, Lon: -122.4}, geo.LLA{Lat: o.Pose(0).Lat, Lon: o.Pose(0).Lon})
	if r < 1000 || r > 20000 {
		t.Fatalf("default radius %.0f m not a few km", r)
	}
}

func TestWaypointsLoop(t *testing.T) {
	wps := []config.LatLon{{Lat: 36.2, Lon: -122.4}, {Lat: 36.2, Lon: -122.9}, {Lat: 36.45, Lon: -122.9}, {Lat: 36.45, Lon: -122.4}}
	cfg := mobileCfg(config.Ownship{Path: config.PathWaypoints, SpeedKts: 12, Waypoints: wps})
	o := ownship.New(cfg)
	speed := 12 * kts

	var perim float64
	for i := range wps {
		j := (i + 1) % len(wps)
		_, _, r := geo.LLAToAER(geo.LLA{Lat: wps[i].Lat, Lon: wps[i].Lon}, geo.LLA{Lat: wps[j].Lat, Lon: wps[j].Lon})
		perim += r
	}
	period := time.Duration(perim / speed * float64(time.Second))

	for at := time.Duration(0); at < 2*period; at += 211 * time.Second {
		// Skip instants within a second of a corner, where the heading flips.
		checkConsistentAwayFromCorners(t, o, at, speed, wps)
	}
	if p := o.Pose(0); math.Abs(p.Lat-36.2) > 1e-9 || math.Abs(p.Lon+122.4) > 1e-9 {
		t.Fatalf("does not start at the first waypoint: %+v", p)
	}
	a, b := o.Pose(0), o.Pose(period)
	if _, _, r := geo.LLAToAER(geo.LLA{Lat: a.Lat, Lon: a.Lon}, geo.LLA{Lat: b.Lat, Lon: b.Lon}); r > 5 {
		t.Fatalf("path does not loop back to the first waypoint (%.1f m off)", r)
	}
	// First leg heads due west (to the second waypoint).
	if h := o.Pose(10 * time.Second).HeadingDeg; math.Abs(h-270) > 1 {
		t.Fatalf("first-leg heading %.2f, want ~270", h)
	}
}

func checkConsistentAwayFromCorners(t *testing.T, o *ownship.Ownship, at time.Duration, speed float64, wps []config.LatLon) {
	t.Helper()
	p, q := o.Pose(at), o.Pose(at+time.Second)
	finite(t, p)
	if math.Abs(p.HeadingDeg-q.HeadingDeg) > 0.5 {
		return // a corner falls inside this second
	}
	checkConsistent(t, o, at, speed)
}

func TestFixedPose(t *testing.T) {
	cfg := mobileCfg(config.Ownship{})
	cfg.Emplacement.Mobility = config.MobilityFixed
	cfg.Emplacement.AzimuthReference = config.AzimuthTrueNorth
	o := ownship.New(cfg)
	for _, at := range []time.Duration{0, time.Hour} {
		p := o.Pose(at)
		want := ownship.Pose{Lat: 36.2, Lon: -122.4, Alt: 18, HeadingDeg: 15}
		if p != want {
			t.Fatalf("fixed pose = %+v, want %+v", p, want)
		}
	}
}
