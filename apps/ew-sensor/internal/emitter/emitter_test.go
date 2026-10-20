package emitter_test

import (
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/sentinel/ew-sensor/internal/config"
	"github.com/sentinel/ew-sensor/internal/emitter"
	"github.com/sentinel/go-common/geo"
)

const kts = 1852.0 / 3600.0

func dist(a, b geo.LLA) float64 {
	_, _, r := geo.LLAToAER(a, b)
	return r
}

func TestStaticNeverMoves(t *testing.T) {
	e := emitter.New(config.Emitter{ID: "a", Start: config.LatLon{Lat: 36.5, Lon: -122}}, 1)
	p0 := e.Position()
	for i := 0; i < 100; i++ {
		e.Advance(time.Second)
	}
	if e.Position() != p0 {
		t.Fatalf("static emitter moved: %v -> %v", p0, e.Position())
	}
	if p0.Lat != 36.5 || p0.Lon != -122 {
		t.Fatalf("start not honoured: %v", p0)
	}
}

func randomWalk(seed int64) *emitter.Emitter {
	return emitter.New(config.Emitter{
		ID: "rw", Start: config.LatLon{Lat: 36.5, Lon: -122},
		Motion: config.Motion{Type: config.MotionRandomWalk, SpeedKts: 20},
	}, seed)
}

func TestRandomWalkDeterministicAndBounded(t *testing.T) {
	a, b, c := randomWalk(42), randomWalk(42), randomWalk(43)
	differ := false
	prev := a.Position()
	prevHeading := a.Heading()
	dt := 2 * time.Second
	for i := 0; i < 500; i++ {
		a.Advance(dt)
		b.Advance(dt)
		c.Advance(dt)
		if a.Position() != b.Position() {
			t.Fatalf("step %d: same seed diverged: %v vs %v", i, a.Position(), b.Position())
		}
		if a.Position() != c.Position() {
			differ = true
		}
		// speed is honoured
		if got, want := dist(prev, a.Position()), 20*kts*dt.Seconds(); math.Abs(got-want) > 0.01*want {
			t.Fatalf("step %d: moved %.2f m, want %.2f", i, got, want)
		}
		// heading change is bounded per step
		d := math.Abs(math.Mod(a.Heading()-prevHeading+540, 360) - 180)
		if max := emitter.RandomWalkMaxTurnDegPerSec * dt.Seconds(); d > max+1e-9 {
			t.Fatalf("step %d: turned %.2f deg, bound %.2f", i, d, max)
		}
		if h := a.Heading(); h < 0 || h >= 360 {
			t.Fatalf("heading %v out of [0,360)", h)
		}
		prev, prevHeading = a.Position(), a.Heading()
	}
	if !differ {
		t.Fatal("different seeds produced the same walk")
	}
}

func TestWaypointsLoop(t *testing.T) {
	w1 := config.LatLon{Lat: 36.60, Lon: -122.00}
	w2 := config.LatLon{Lat: 36.60, Lon: -121.90}
	e := emitter.New(config.Emitter{
		ID: "wp", Start: config.LatLon{Lat: 36.50, Lon: -122.00},
		Motion: config.Motion{Type: config.MotionWaypoints, SpeedKts: 600, Waypoints: []config.LatLon{w1, w2}},
	}, 1)
	ll := func(p config.LatLon) geo.LLA { return geo.LLA{Lat: p.Lat, Lon: p.Lon} }

	// Record each arrival (within one step of travel) in order.
	var visits []string
	near := ""
	for i := 0; i < 2000 && len(visits) < 4; i++ {
		e.Advance(time.Second)
		cur := ""
		switch {
		case dist(e.Position(), ll(w1)) < 400:
			cur = "w1"
		case dist(e.Position(), ll(w2)) < 400:
			cur = "w2"
		}
		if cur != "" && cur != near {
			visits = append(visits, cur)
		}
		near = cur
	}
	want := []string{"w1", "w2", "w1", "w2"}
	if fmt.Sprint(visits) != fmt.Sprint(want) {
		t.Fatalf("visits = %v, want %v (the path must loop)", visits, want)
	}
}

func TestWaypointsAdvanceCarriesLeftoverAcrossAWaypoint(t *testing.T) {
	// A single big step spanning several legs still moves the full distance
	// along the path.
	w1 := config.LatLon{Lat: 36.50, Lon: -122.00}
	w2 := config.LatLon{Lat: 36.50, Lon: -121.99}
	e := emitter.New(config.Emitter{
		ID: "wp", Start: w1,
		Motion: config.Motion{Type: config.MotionWaypoints, SpeedKts: 100, Waypoints: []config.LatLon{w2, w1}},
	}, 1)
	e.Advance(1000 * time.Second) // 51 km on a ~0.9 km loop
	if p := e.Position(); math.IsNaN(p.Lat) || math.IsNaN(p.Lon) {
		t.Fatal("NaN position")
	}
	if d := dist(e.Position(), geo.LLA{Lat: 36.5, Lon: -122}); d > 1000 {
		t.Fatalf("emitter left its loop: %.0f m from start", d)
	}
}

func baseCfg() config.Config {
	return config.Config{
		Emplacement: config.Emplacement{
			Lat: 36.6, Lon: -121.95, Boresight: 270, Width: 120, RangeKm: 150, MinRangeKm: 2,
			Mobility: config.MobilityFixed, AzimuthReference: config.AzimuthTrueNorth,
		},
		Seed: 7,
	}
}

func ids(es []*emitter.Emitter) []string {
	var out []string
	for _, e := range es {
		out = append(out, e.ID())
	}
	return out
}

func TestBuildSynthesisesExtrasInsideCoverage(t *testing.T) {
	cfg := baseCfg()
	cfg.Emitters = []config.Emitter{
		{ID: "x1", EmitterType: "SATCOM", FrequencyHz: 1e9, Start: config.LatLon{Lat: 36.6, Lon: -122.5}},
		{ID: "x2", EmitterType: "SATCOM", FrequencyHz: 1e9, Start: config.LatLon{Lat: 36.6, Lon: -122.6}},
	}
	cfg.EmitterCount = 8
	es := emitter.Build(cfg)
	want := []string{"x1", "x2", "SYN-01", "SYN-02", "SYN-03", "SYN-04", "SYN-05", "SYN-06"}
	if fmt.Sprint(ids(es)) != fmt.Sprint(want) {
		t.Fatalf("ids = %v, want %v", ids(es), want)
	}

	origin := geo.LLA{Lat: cfg.Emplacement.Lat, Lon: cfg.Emplacement.Lon}
	types := map[string]bool{}
	for _, e := range es[2:] {
		az, _, r := geo.LLAToAER(origin, e.Position())
		d := math.Abs(math.Mod(az-270+540, 360) - 180)
		if d > 60 || r < 2000 || r > 150000 {
			t.Errorf("%s outside coverage: az=%.1f r=%.0f", e.ID(), az, r)
		}
		c := e.Config()
		if c.EmitterType == "" || c.FrequencyHz <= 0 || c.TrackEnvironment == "" {
			t.Errorf("%s: incomplete RF/environment fields: %+v", e.ID(), c)
		}
		types[c.EmitterType] = true
	}
	if len(types) < 4 {
		t.Errorf("want a mix of emitter types among 6 synthetic emitters, got %v", types)
	}
}

func TestBuildSynthesisIsStableForSeed(t *testing.T) {
	cfg := baseCfg()
	cfg.EmitterCount = 5
	a, b := emitter.Build(cfg), emitter.Build(cfg)
	for i := range a {
		if a[i].Position() != b[i].Position() || a[i].Config().EmitterType != b[i].Config().EmitterType {
			t.Fatalf("emitter %d differs between builds with the same seed", i)
		}
	}
	// The first k synthetic emitters do not depend on the total.
	cfg.EmitterCount = 3
	c := emitter.Build(cfg)
	for i := range c {
		if c[i].ID() != a[i].ID() || c[i].Position() != a[i].Position() {
			t.Fatalf("emitter %d changed with the total count", i)
		}
	}
	cfg.Seed = 8
	d := emitter.Build(cfg)
	if d[0].Position() == a[0].Position() {
		t.Fatal("a different seed gave the same placement")
	}
}

func TestBuildTruncatesAndHonoursEmptyList(t *testing.T) {
	cfg := baseCfg()
	for _, id := range []string{"a", "b", "c"} {
		cfg.Emitters = append(cfg.Emitters, config.Emitter{ID: id})
	}
	cfg.EmitterCount = 2
	if got := ids(emitter.Build(cfg)); fmt.Sprint(got) != "[a b]" {
		t.Fatalf("truncate: %v", got)
	}
	cfg.Emitters, cfg.EmitterCount = nil, 5
	if got := len(emitter.Build(cfg)); got != 5 {
		t.Fatalf("no list: got %d emitters", got)
	}
	cfg.EmitterCount = 0
	if got := len(emitter.Build(cfg)); got != 0 {
		t.Fatalf("zero: got %d emitters", got)
	}
}

func TestBuildOmniSynthesisSpansAllAzimuths(t *testing.T) {
	cfg := baseCfg()
	cfg.Emplacement.Width = 360
	cfg.EmitterCount = 40
	origin := geo.LLA{Lat: cfg.Emplacement.Lat, Lon: cfg.Emplacement.Lon}
	var east, west int
	for _, e := range emitter.Build(cfg) {
		az, _, _ := geo.LLAToAER(origin, e.Position())
		if az < 180 {
			east++
		} else {
			west++
		}
	}
	if east == 0 || west == 0 {
		t.Fatalf("omni synthesis should use every azimuth: east=%d west=%d", east, west)
	}
}
