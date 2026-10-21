package terrain

import (
	"context"
	"errors"
	"math"
	"net/http"
	"testing"
	"time"

	"github.com/sentinel/go-common/geo"
)

// losOrigin is where most line-of-sight tests stand: inland, mid-latitude.
var losOrigin = geo.LLA{Lat: 38.5, Lon: -77.5}

func flat(h float64) func(lat, lon float64) float64 {
	return func(float64, float64) float64 { return h }
}

// northOf is how far north of losOrigin a point is, in metres along the
// ground.
func northOf(lat, lon float64) float64 {
	return geo.ENUOf(geo.LLA{Lat: losOrigin.Lat, Lon: losOrigin.Lon}, geo.ToECEF(geo.LLA{Lat: lat, Lon: lon})).N
}

// ridge is flat ground at 0 m with a triangular ridge of the given height,
// 400 m wide at its foot, centred at at metres north of losOrigin.
func ridge(height, at float64) func(lat, lon float64) float64 {
	return func(lat, lon float64) float64 {
		return height * math.Max(0, 1-math.Abs(northOf(lat, lon)-at)/200)
	}
}

// bisectRange is the independent reference for a ray's ground crossing
// (Ruling P3): bisection on slant range with geo.AERToLLA until the ray's
// altitude equals the surface height, curvature included. The ray must be
// above the surface at lo and at or below it at hi, crossing once between.
func bisectRange(t *testing.T, origin geo.LLA, az, el, lo, hi float64, surface func(lat, lon float64) float64) float64 {
	t.Helper()
	clear := func(r float64) float64 {
		p := geo.AERToLLA(origin, az, el, r)
		return p.Alt - surface(p.Lat, p.Lon)
	}
	if clear(lo) <= 0 || clear(hi) > 0 {
		t.Fatalf("reference bisection: no crossing between %g and %g m", lo, hi)
	}
	for hi-lo > 1e-4 {
		mid := (lo + hi) / 2
		if clear(mid) > 0 {
			lo = mid
		} else {
			hi = mid
		}
	}
	return (lo + hi) / 2
}

func intersect(t *testing.T, s *Service, origin geo.LLA, az, el, maxRange float64) Hit {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	hit, err := s.Intersect(ctx, origin, az, el, maxRange)
	if err != nil {
		t.Fatalf("Intersect(az %g, el %g): %v", az, el, err)
	}
	return hit
}

// horizontal is the ground distance between two points, in metres.
func horizontal(a, b geo.LLA) float64 {
	v := geo.ENUOf(geo.LLA{Lat: a.Lat, Lon: a.Lon}, geo.ToECEF(geo.LLA{Lat: b.Lat, Lon: b.Lon}))
	return math.Hypot(v.E, v.N)
}

// alongSightSigma is the standard deviation of a horizontal covariance along
// an azimuth.
func alongSightSigma(cov [6]float64, azDeg float64) float64 {
	s, c := math.Sincos(azDeg * math.Pi / 180)
	nn, ne, ee := cov[0], cov[1], cov[3]
	return math.Sqrt(nn*c*c + 2*ne*s*c + ee*s*s)
}

func finiteCov(t *testing.T, cov [6]float64, ok bool) {
	t.Helper()
	if !ok {
		t.Fatalf("DirectionCovariance ok = false, cov %v", cov)
	}
	for i, v := range cov {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			t.Fatalf("cov[%d] = %v", i, v)
		}
	}
}

func TestIntersectFlatMatchesClosedForm(t *testing.T) {
	const ground = 50.0
	s := NewStatic(flat(ground))
	origin := losOrigin
	origin.Alt = ground + 100
	for _, az := range []float64{0, 45, 200} {
		hit := intersect(t, s, origin, az, -1, DefaultMaxRangeM)
		if !hit.Found {
			t.Fatalf("az %g: no hit", az)
		}
		want := bisectRange(t, origin, az, -1, 0, DefaultMaxRangeM, flat(ground))
		near(t, "range", hit.RangeM, want, 1)
		// The flat-Earth figure, 100/sin(1°) = 5730 m, falls short because
		// the ground curves away from the ray: within a few percent of it.
		if flatEarth := 100 / math.Sin(math.Pi/180); math.Abs(hit.RangeM-flatEarth) > 0.05*flatEarth {
			t.Errorf("range %.1f m is far from the flat-Earth %.1f m", hit.RangeM, flatEarth)
		}
		near(t, "hit altitude", hit.At.Alt, ground, 1e-9)
		ray := geo.AERToLLA(origin, az, -1, hit.RangeM)
		near(t, "hit is on the ray", horizontal(hit.At, ray), 0, 1e-3)
	}
}

func TestIntersectRidgeHidesValley(t *testing.T) {
	// Over flat ground the ray would land 3 km out; a 50 m ridge at 2 km
	// stands in the way.
	origin := losOrigin
	origin.Alt = 100
	el := -math.Atan2(100, 3000) * 180 / math.Pi
	flatHit := intersect(t, NewStatic(flat(0)), origin, 0, el, DefaultMaxRangeM)
	if !flatHit.Found || flatHit.RangeM < 2900 {
		t.Fatalf("over flat ground: %+v, want about 3 km", flatHit)
	}

	surface := ridge(50, 2000)
	hit := intersect(t, NewStatic(surface), origin, 0, el, DefaultMaxRangeM)
	if !hit.Found {
		t.Fatal("no hit")
	}
	if d := northOf(hit.At.Lat, hit.At.Lon); d < 1800 || d > 2000 {
		t.Errorf("hit %.0f m north, want on the ridge's near face (1800–2000 m)", d)
	}
	want := bisectRange(t, origin, 0, el, 0, 2000, surface)
	near(t, "range", hit.RangeM, want, 1)
}

func TestIntersectValleyFloor(t *testing.T) {
	// The ray would meet the plain at 2 km, but the plain gives way to a
	// valley 50 m deep from 1.5 km to 4 km: the ray goes on to its floor.
	origin := losOrigin
	origin.Alt = 100
	el := -math.Atan2(100, 2000) * 180 / math.Pi
	surface := func(lat, lon float64) float64 {
		if d := northOf(lat, lon); d > 1500 && d < 4000 {
			return -50
		}
		return 0
	}
	hit := intersect(t, NewStatic(surface), origin, 0, el, DefaultMaxRangeM)
	if !hit.Found {
		t.Fatal("no hit")
	}
	want := bisectRange(t, origin, 0, el, 2500, 3500, flat(-50))
	near(t, "range", hit.RangeM, want, 1)
	near(t, "hit altitude", hit.At.Alt, -50, 1e-9)
}

func TestIntersectAboveHorizonNoHit(t *testing.T) {
	origin := losOrigin
	origin.Alt = 10
	hit, err := NewStatic(flat(0)).Intersect(context.Background(), origin, 90, 2, DefaultMaxRangeM)
	if err != nil || hit.Found {
		t.Errorf("Intersect = %+v, %v; want no hit, nil", hit, err)
	}
}

func TestIntersectBeyondMaxRange(t *testing.T) {
	// The hit is about 5.9 km out; looking only 5 km finds nothing.
	origin := losOrigin
	origin.Alt = 100
	s := NewStatic(flat(0))
	hit, err := s.Intersect(context.Background(), origin, 0, -1, 5000)
	if err != nil || hit.Found {
		t.Errorf("Intersect = %+v, %v; want no hit, nil", hit, err)
	}
	if hit := intersect(t, s, origin, 0, -1, 6500); !hit.Found {
		t.Error("no hit within 6.5 km")
	}
}

func TestIntersectStraightDown(t *testing.T) {
	// A sloping plane, so the hit's horizontal position matters.
	surface := func(lat, lon float64) float64 { return 120 + (lat-losOrigin.Lat)*5000 }
	s := NewStatic(surface)
	origin := losOrigin
	origin.Alt = 500
	hit := intersect(t, s, origin, 30, -90, DefaultMaxRangeM)
	if !hit.Found {
		t.Fatal("no hit")
	}
	if d := horizontal(origin, hit.At); d >= 0.5 {
		t.Errorf("hit %.3f m from the point below, want < 0.5 m", d)
	}
	near(t, "range", hit.RangeM, 380, 0.5)
	cov, ok := s.DirectionCovariance(context.Background(), origin, 30, -90, DefaultMaxRangeM, 0.1, 0.1, hit)
	finiteCov(t, cov, ok)
	near(t, "vertical variance", cov[5], VerticalSigM*VerticalSigM, 1e-9)
	// Pointing error is isotropic looking straight down: both horizontal
	// axes come out comparable, not one collapsed to nothing.
	major, minor, _ := geo.EllipseFromCov2(cov[0], cov[1], cov[3])
	if !(minor > 0) || major/minor > 2 {
		t.Errorf("horizontal 95%% axes %.3g m and %.3g m, want within 2× of each other", major, minor)
	}
	// Close to straight down as well.
	hit = intersect(t, s, origin, 30, -89.9, DefaultMaxRangeM)
	cov, ok = s.DirectionCovariance(context.Background(), origin, 30, -89.9, DefaultMaxRangeM, 0.1, 0.1, hit)
	finiteCov(t, cov, ok)
	major, minor, _ = geo.EllipseFromCov2(cov[0], cov[1], cov[3])
	if !(minor > 0) || major/minor > 2 {
		t.Errorf("at −89.9°: horizontal 95%% axes %.3g m and %.3g m, want within 2× of each other", major, minor)
	}
}

func TestIntersectGrazing(t *testing.T) {
	// Barely below level, the ray strikes a cliff 5 km out. At the cliff the
	// Earth's curvature has the ray climbing away from the ground, so the
	// along-sight term from the terrain's vertical error must be capped
	// rather than blow up.
	origin := losOrigin
	origin.Alt = 100
	surface := func(lat, lon float64) float64 {
		return 200 * math.Max(0, math.Min(1, (northOf(lat, lon)-5000)/50))
	}
	s := NewStatic(surface)
	const maxRange = 10000.0
	hit := intersect(t, s, origin, 0, -0.01, maxRange)
	if !hit.Found {
		t.Fatal("no hit")
	}
	if d := northOf(hit.At.Lat, hit.At.Lon); d < 5000 || d > 5050 {
		t.Errorf("hit %.0f m north, want on the cliff (5000–5050 m)", d)
	}
	cov, ok := s.DirectionCovariance(context.Background(), origin, 0, -0.01, maxRange, 0.01, 0.01, hit)
	finiteCov(t, cov, ok)
	along := alongSightSigma(cov, 0)
	if along < maxRange || along > maxRange*1.001 {
		t.Errorf("along-sight sigma %.1f m, want capped at %.0f m", along, maxRange)
	}
}

func TestCovarianceElongatesAlongSight(t *testing.T) {
	s := NewStatic(flat(0))
	origin := losOrigin
	origin.Alt = 100
	const az, sigma = 60.0, 0.1
	along := map[float64]float64{}
	for _, el := range []float64{-30, -2} {
		hit := intersect(t, s, origin, az, el, DefaultMaxRangeM)
		if !hit.Found {
			t.Fatalf("el %g: no hit", el)
		}
		cov, ok := s.DirectionCovariance(context.Background(), origin, az, el, DefaultMaxRangeM, sigma, sigma, hit)
		finiteCov(t, cov, ok)
		along[el] = alongSightSigma(cov, az)
		_, _, orient := geo.EllipseFromCov2(cov[0], cov[1], cov[3])
		if d := math.Mod(orient-az+540, 180); math.Min(d, 180-d) > 5 {
			t.Errorf("el %g: major axis at %.1f°, want within 5° of %g°", el, orient, az)
		}
		near(t, "vertical variance", cov[5], VerticalSigM*VerticalSigM, 1e-9)
	}
	if r := along[-2] / along[-30]; r <= 5 {
		t.Errorf("along-sight sigma %.1f m at −2° vs %.1f m at −30°: ratio %.1f, want > 5", along[-2], along[-30], r)
	}
}

func TestCovarianceAngularErrorAcrossSight(t *testing.T) {
	// Steeply down, the terrain term is small and the azimuth error spreads
	// the hit across the line of sight by about range × σ.
	s := NewStatic(flat(0))
	origin := losOrigin
	origin.Alt = 1000
	const az, el, sigAz = 0.0, -45.0, 0.5
	hit := intersect(t, s, origin, az, el, DefaultMaxRangeM)
	cov, ok := s.DirectionCovariance(context.Background(), origin, az, el, DefaultMaxRangeM, sigAz, 0, hit)
	finiteCov(t, cov, ok)
	ground := horizontal(origin, hit.At)
	near(t, "across-sight sigma", math.Sqrt(cov[3]), ground*sigAz*math.Pi/180, 0.01*ground*sigAz*math.Pi/180)
}

func TestCovarianceUnresolvedRayNotOK(t *testing.T) {
	t.Run("cancelled", func(t *testing.T) {
		s := NewStatic(flat(0))
		origin := losOrigin
		origin.Alt = 100
		hit := intersect(t, s, origin, 0, -1, DefaultMaxRangeM)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if cov, ok := s.DirectionCovariance(ctx, origin, 0, -1, DefaultMaxRangeM, 0.1, 0.1, hit); ok {
			t.Errorf("ok with a cancelled context, cov %v", cov)
		}
	})
	t.Run("terrain unavailable on a perturbed ray", func(t *testing.T) {
		// The ray lands inside 38°N; the shallower perturbed ray runs on
		// south into 37°N, whose tile fails to download.
		south := testTile{south: 37, west: -78}
		ts := newTileServer(t, testTile{south: 38, west: -78}, south)
		ts.setStatus(south.key(), http.StatusInternalServerError)
		s := newTestService(t, ts, nil)
		if n, err := s.Preload(context.Background(), 38.2, -77.6, 38.8, -77.4); n != 1 || err != nil {
			t.Fatalf("Preload = %d, %v; want 1, nil", n, err)
		}
		origin := geo.LLA{Lat: 38.05, Lon: -77.5}
		origin.Alt = MSLToHAE(origin.Lat, origin.Lon, planeMSL(origin.Lat, origin.Lon)) + 100
		hit := intersect(t, s, origin, 180, -2, DefaultMaxRangeM)
		if !hit.Found || hit.At.Lat < 38.01 {
			t.Fatalf("Intersect = %+v, want a hit inside 38°N", hit)
		}
		if _, err := s.Intersect(context.Background(), origin, 180, -0.5, DefaultMaxRangeM); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("perturbed ray: error %v, want ErrUnavailable", err)
		}
		if cov, ok := s.DirectionCovariance(context.Background(), origin, 180, -2, DefaultMaxRangeM, 0.1, 1.5, hit); ok {
			t.Errorf("ok with a perturbed ray over missing terrain, cov %v", cov)
		}
	})
}

func TestCovarianceMissCappedAtMaxRange(t *testing.T) {
	// A perturbed ray that rises above the horizon misses; it counts as the
	// point at the maximum range, so the covariance stays finite.
	s := NewStatic(flat(0))
	origin := losOrigin
	origin.Alt = 100
	const maxRange = 20000.0
	hit := intersect(t, s, origin, 0, -0.5, maxRange)
	if !hit.Found {
		t.Fatal("no hit")
	}
	cov, ok := s.DirectionCovariance(context.Background(), origin, 0, -0.5, maxRange, 0, 2, hit)
	finiteCov(t, cov, ok)
	if sd := math.Sqrt(cov[0]); sd > 2*maxRange {
		t.Errorf("north sigma %.0f m, want bounded by the maximum range", sd)
	}
}

func TestIntersectOriginBelowGround(t *testing.T) {
	origin := losOrigin
	origin.Alt = 40
	hit := intersect(t, NewStatic(flat(50)), origin, 0, 10, DefaultMaxRangeM)
	if !hit.Found || hit.RangeM != 0 {
		t.Fatalf("Intersect = %+v, want a hit at range 0", hit)
	}
	near(t, "hit altitude", hit.At.Alt, 50, 0)
	near(t, "hit offset", horizontal(origin, hit.At), 0, 0)

	// Exactly on the ground is not below it: looking up sees no ground,
	// looking down meets it at once.
	origin.Alt = 50
	if hit := intersect(t, NewStatic(flat(50)), origin, 0, 10, DefaultMaxRangeM); hit.Found {
		t.Errorf("on the ground looking up: %+v, want no hit", hit)
	}
	hit = intersect(t, NewStatic(flat(50)), origin, 0, -10, DefaultMaxRangeM)
	if !hit.Found || hit.RangeM > RefineM || math.IsNaN(hit.At.Lat) {
		t.Errorf("on the ground looking down: %+v, want a hit within %g m", hit, RefineM)
	}
}

func TestIntersectUnavailable(t *testing.T) {
	ts := newTileServer(t)
	ts.setStatus(tileListName, http.StatusInternalServerError)
	s := newTestService(t, ts, nil)
	origin := losOrigin
	origin.Alt = 100
	if _, err := s.Intersect(context.Background(), origin, 0, -1, DefaultMaxRangeM); !errors.Is(err, ErrUnavailable) {
		t.Errorf("Intersect error = %v, want ErrUnavailable", err)
	}
	if _, err := s.Visible(context.Background(), origin, geo.LLA{Lat: 38.51, Lon: -77.5}); !errors.Is(err, ErrUnavailable) {
		t.Errorf("Visible error = %v, want ErrUnavailable", err)
	}
}

func TestIntersectHonoursCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	origin := losOrigin
	origin.Alt = 100
	if _, err := NewStatic(flat(0)).Intersect(ctx, origin, 0, 2, DefaultMaxRangeM); !errors.Is(err, context.Canceled) {
		t.Errorf("Intersect error = %v, want context.Canceled", err)
	}
}

func TestIntersectRejectsBadInput(t *testing.T) {
	s := NewStatic(flat(0))
	origin := losOrigin
	origin.Alt = 100
	for _, c := range []struct {
		name              string
		origin            geo.LLA
		az, el, maxRangeM float64
	}{
		{"NaN azimuth", origin, math.NaN(), -1, 1000},
		{"infinite elevation", origin, 0, math.Inf(-1), 1000},
		{"NaN altitude", geo.LLA{Lat: 38.5, Lon: -77.5, Alt: math.NaN()}, 0, -1, 1000},
		{"negative range", origin, 0, -1, -1},
		{"infinite range", origin, 0, -1, math.Inf(1)},
	} {
		if _, err := s.Intersect(context.Background(), c.origin, c.az, c.el, c.maxRangeM); err == nil {
			t.Errorf("%s: no error", c.name)
		}
	}
}

func TestVisibleOccludedByRidge(t *testing.T) {
	s := NewStatic(ridge(50, 2000))
	from := losOrigin
	from.Alt = 10
	to := geo.AERToLLA(geo.LLA{Lat: losOrigin.Lat, Lon: losOrigin.Lon}, 0, 0, 4000)
	to.Alt = 2
	visible, err := s.Visible(context.Background(), from, to)
	if err != nil || visible {
		t.Errorf("Visible = %v, %v; want false, nil", visible, err)
	}
}

func TestVisibleClear(t *testing.T) {
	s := NewStatic(flat(0))
	from := losOrigin
	from.Alt = 10
	for _, c := range []struct {
		name         string
		dist, height float64
	}{
		{"above the ground 4 km out", 4000, 2},
		{"on the ground 1 km out", 1000, 0},
		{"overhead", 0, 500},
		{"the same point", 0, 10},
	} {
		to := geo.AERToLLA(geo.LLA{Lat: losOrigin.Lat, Lon: losOrigin.Lon}, 0, 0, c.dist)
		to.Alt = c.height
		visible, err := s.Visible(context.Background(), from, to)
		if err != nil || !visible {
			t.Errorf("%s: Visible = %v, %v; want true, nil", c.name, visible, err)
		}
	}
}

func TestIntersectCrossesTileEdge(t *testing.T) {
	// Two tiles stacked north–south; the ray starts 100 m south of 39°N
	// heading north and lands about 6 km into the northern tile. The
	// default synthetic surface is a plane, which the tiles reproduce
	// exactly, so the reference crossing uses the same plane.
	ts := newTileServer(t, testTile{south: 38, west: -78}, testTile{south: 39, west: -78})
	s := newTestService(t, ts, nil)
	if n, err := s.Preload(context.Background(), 38.5, -77.6, 39.5, -77.4); n != 2 || err != nil {
		t.Fatalf("Preload = %d, %v; want 2, nil", n, err)
	}
	surface := func(lat, lon float64) float64 { return MSLToHAE(lat, lon, planeMSL(lat, lon)) }
	origin := geo.LLA{Lat: 39 - 100*oneMetreLat, Lon: -77.5}
	origin.Alt = surface(origin.Lat, origin.Lon) + 100
	hit := intersect(t, s, origin, 0, -1, DefaultMaxRangeM)
	if !hit.Found {
		t.Fatal("no hit")
	}
	if hit.At.Lat <= 39 {
		t.Errorf("hit at %.5f°N, want beyond 39°N", hit.At.Lat)
	}
	want := bisectRange(t, origin, 0, -1, 0, DefaultMaxRangeM, surface)
	near(t, "range", hit.RangeM, want, 1)
}
