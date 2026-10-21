package scenario

import (
	"context"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/sentinel/go-common/geo"
	"github.com/sentinel/go-common/terrain"
)

var epoch = time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)

// slope is an ellipsoidal terrain that rises 1 m per 0.001 degree of longitude.
func slope(lat, lon float64) float64 { return 100 + (lon-10)*1000 }

func flat(float64, float64) float64 { return 50 }

func horizontal(a, b geo.LLA) float64 {
	v := geo.ENUOf(a, geo.ToECEF(b))
	return math.Hypot(v.E, v.N)
}

// eastWest is a route along latitude 45 from lon 10 to 10.1 (about 7.86 km).
func eastWest(loop bool) Scenario {
	return Scenario{Epoch: epoch, Targets: []Target{{
		ID: "t1", Kind: "vehicle", SpeedKts: 20, Loop: loop,
		Waypoints: []LatLon{{Lat: 45, Lon: 10}, {Lat: 45, Lon: 10.1}},
	}}}
}

func TestTruthAtIsWallClockDeterministic(t *testing.T) {
	ctx := context.Background()
	tr := terrain.NewStatic(slope)
	sc := eastWest(true)
	now := epoch.Add(137 * time.Second)

	a, err := sc.TruthAt(ctx, tr, now)
	if err != nil {
		t.Fatal(err)
	}
	b, err := sc.TruthAt(ctx, tr, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(a) != 1 || len(b) != 1 || a[0] != b[0] {
		t.Fatalf("same now gave different truth:\n%+v\n%+v", a, b)
	}
	c, _ := sc.TruthAt(ctx, tr, now.Add(10*time.Second))
	if c[0].At == a[0].At {
		t.Fatalf("truth did not move in 10 s")
	}

	// 20 kt for 100 s is 1028.9 m along the route.
	d, _ := sc.TruthAt(ctx, tr, epoch.Add(100*time.Second))
	start := geo.LLA{Lat: 45, Lon: 10}
	if got, want := horizontal(start, d[0].At), 100*20*KtsToMps; math.Abs(got-want) > 2 {
		t.Fatalf("moved %.1f m in 100 s, want %.1f", got, want)
	}
	// Velocity is east at the scenario speed.
	v := d[0].VelENU
	if math.Abs(v[0]-20*KtsToMps) > 0.01 || math.Abs(v[1]) > 0.01 {
		t.Fatalf("velocity %v, want east at %.3f m/s", v, 20*KtsToMps)
	}
}

func TestTruthAtLoopWraparound(t *testing.T) {
	ctx := context.Background()
	tr := terrain.NewStatic(flat)
	sc := eastWest(true)
	// The route out and back is 2 x length; at one full cycle the target is home.
	leg := horizontal(geo.LLA{Lat: 45, Lon: 10}, geo.LLA{Lat: 45, Lon: 10.1})
	cycle := time.Duration(2 * leg / (20 * KtsToMps) * float64(time.Second))

	at := func(d time.Duration) Truth {
		tt, err := sc.TruthAt(ctx, tr, epoch.Add(d))
		if err != nil {
			t.Fatal(err)
		}
		return tt[0]
	}
	home := at(0)
	if d := horizontal(home.At, at(cycle).At); d > 1 {
		t.Fatalf("after a full cycle the target is %.2f m from the start", d)
	}
	if d := horizontal(at(500*time.Second).At, at(500*time.Second+3*cycle).At); d > 2 {
		t.Fatalf("three cycles later the target is %.2f m off", d)
	}
	// Half way round it is at the far end, and on the way back it heads west.
	if d := horizontal(at(cycle/2).At, geo.LLA{Lat: 45, Lon: 10.1}); d > 1 {
		t.Fatalf("half a cycle in, %.2f m from the far waypoint", d)
	}
	if back := at(cycle*3/4 - time.Second); back.VelENU[0] >= 0 {
		t.Fatalf("on the return leg velocity east is %v, want negative", back.VelENU[0])
	}
}

func TestTruthAtNonLoopParksAtTheEnd(t *testing.T) {
	ctx := context.Background()
	tr := terrain.NewStatic(flat)
	sc := eastWest(false)
	late, err := sc.TruthAt(ctx, tr, epoch.Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if d := horizontal(late[0].At, geo.LLA{Lat: 45, Lon: 10.1}); d > 1 {
		t.Fatalf("parked %.2f m from the last waypoint", d)
	}
	if late[0].VelENU != [3]float64{} {
		t.Fatalf("a parked target has velocity %v", late[0].VelENU)
	}
	early, _ := sc.TruthAt(ctx, tr, epoch.Add(-time.Hour))
	if d := horizontal(early[0].At, geo.LLA{Lat: 45, Lon: 10}); d > 1 {
		t.Fatalf("before the epoch the target is %.2f m from the first waypoint", d)
	}
}

func TestTruthGroundTargetsOnTerrain(t *testing.T) {
	ctx := context.Background()
	tr := terrain.NewStatic(slope)
	sc := eastWest(true)
	sc.Targets = append(sc.Targets, Target{
		ID: "d1", Kind: "drone", SpeedKts: 40, HeightAboveGroundM: 120,
		Waypoints: []LatLon{{Lat: 45.01, Lon: 10}, {Lat: 45.01, Lon: 10.05}},
	})
	got, err := sc.TruthAt(ctx, tr, epoch.Add(200*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d truths, want 2", len(got))
	}
	for i, hag := range []float64{0, 120} {
		want := slope(got[i].At.Lat, got[i].At.Lon) + hag
		if got[i].At.Alt != want {
			t.Errorf("%s altitude %v, want terrain+%v = %v", got[i].ID, got[i].At.Alt, hag, want)
		}
	}
	// Climbing east up the slope: the vertical velocity follows the terrain.
	if got[0].VelENU[2] <= 0 {
		t.Errorf("climbing a slope but vertical velocity is %v", got[0].VelENU[2])
	}
	if got[0].Kind != "vehicle" || got[1].Kind != "drone" || got[1].ID != "d1" {
		t.Errorf("identity not carried: %+v", got)
	}
}

func TestTruthAtTerrainError(t *testing.T) {
	// A real service with no cache dir would not construct, so use a cancelled
	// context against a static service: static ignores it, so test the error
	// path through an invalid position instead.
	sc := Scenario{Epoch: epoch, Targets: []Target{{ID: "x", Kind: "person", SpeedKts: 3,
		Waypoints: []LatLon{{Lat: 95, Lon: 0}}}}}
	if _, err := sc.TruthAt(context.Background(), terrain.NewStatic(flat), epoch); err == nil {
		t.Fatal("expected an error for an impossible position")
	}
}

func TestValidate(t *testing.T) {
	ok := eastWest(true)
	tests := []struct {
		name    string
		mutate  func(*Scenario)
		wantErr string
	}{
		{"valid", func(*Scenario) {}, ""},
		{"no targets", func(s *Scenario) { s.Targets = nil }, "at least one target"},
		{"no id", func(s *Scenario) { s.Targets[0].ID = "" }, "id is required"},
		{"duplicate id", func(s *Scenario) { s.Targets = append(s.Targets, s.Targets[0]) }, "duplicate id"},
		{"bad kind", func(s *Scenario) { s.Targets[0].Kind = "tank" }, "kind"},
		{"zero speed moving", func(s *Scenario) { s.Targets[0].SpeedKts = 0 }, "speedKts"},
		{"no waypoints", func(s *Scenario) { s.Targets[0].Waypoints = nil }, "waypoint"},
		{"bad waypoint", func(s *Scenario) { s.Targets[0].Waypoints[1].Lat = 100 }, "waypoint 1"},
		{"negative height", func(s *Scenario) { s.Targets[0].HeightAboveGroundM = -1 }, "heightAboveGroundM"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := ok
			s.Targets = append([]Target(nil), ok.Targets...)
			s.Targets[0].Waypoints = append([]LatLon(nil), ok.Targets[0].Waypoints...)
			tc.mutate(&s)
			err := s.Validate()
			switch {
			case tc.wantErr == "" && err != nil:
				t.Fatalf("unexpected error: %v", err)
			case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
				t.Fatalf("error %v, want it to mention %q", err, tc.wantErr)
			}
		})
	}
}
