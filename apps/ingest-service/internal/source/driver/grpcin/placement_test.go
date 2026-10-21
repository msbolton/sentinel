package grpcin

import (
	"context"
	"math"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/sentinel/go-common/geo"
	"github.com/sentinel/go-common/terrain"
	"github.com/sentinel/ingest-service/internal/emplacement"
	"github.com/sentinel/ingest-service/internal/ingesttoken"
	"github.com/sentinel/ingest-service/internal/obs"
	"github.com/sentinel/ingest-service/internal/source/driver"
	ingestv1 "github.com/sentinel/proto-gen/sentinel/ingest/v1"
	sentinelv1 "github.com/sentinel/proto-gen/sentinel/v1"
)

// plateauM is the height of the test ground above the ellipsoid.
const plateauM = 1000.0

func plateau() *terrain.Service {
	return terrain.NewStatic(func(_, _ float64) float64 { return plateauM })
}

// site is where emp puts a fixed emplacement.
var site = geo.LLA{Lat: 50.1, Lon: -4.2}

// camera is a fixed EO/IR emplacement at site, at altitude alt (nil for none).
func camera(alt *float64) emplacement.Emplacement {
	e := emp("cam", "grpc-1", "FIXED", "EO_IR", "UNCLASSIFIED")
	e.Altitude = alt
	return e
}

// placer is a service over a driver made by New with the given terrain, for
// calling placeDirectionOnly and applyRadarTerrain directly.
func placer(t *testing.T, terr driver.Terrain, tweak func(*Driver)) *service {
	t.Helper()
	dv, err := New(spec(map[string]any{"port": 20000}, nil), driver.Deps{Logger: zap.NewNop(), Terrain: terr})
	if err != nil {
		t.Fatal(err)
	}
	d := dv.(*Driver)
	if tweak != nil {
		tweak(d)
	}
	return &service{d: d}
}

// dirObs is a direction-only detection, as Validate leaves it.
func dirObs(az, el float64) *obs.Observation {
	now := time.Now().UTC()
	return &obs.Observation{
		SourceEntityID: "target", SourceTimestamp: now.Add(-time.Second), ReceivedAt: now,
		Polar: &obs.Polar{AzimuthDeg: az, ElevationDeg: &el, DirectionOnly: true},
	}
}

// dirReport is a direction-only report; range_m is left at 0.
func dirReport(id string, az float64, el *float64) *ingestv1.Report {
	return &ingestv1.Report{ClientRef: id, Body: &ingestv1.Report_Observation{Observation: &sentinelv1.Observation{
		SourceEntityId: id, Polar: &sentinelv1.PolarMeasurement{AzimuthDeg: az, ElevationDeg: el, DirectionOnly: true},
	}}}
}

// terrainRig runs a driver with the one emplacement e and terrain terr.
func terrainRig(t *testing.T, cfg map[string]any, e emplacement.Emplacement, terr driver.Terrain, tweak func(*Driver)) (*rig, ingestv1.IngestService_StreamReportsClient) {
	t.Helper()
	r := startWith(t, cfg, nil, tokensWith(secretA, ingesttoken.Token{ID: "t1", FeedID: "grpc-1"}),
		&fakeEmplacements{rows: []emplacement.Emplacement{e}}, nil, nil, func(d *Driver) {
			d.terrain = terr
			if tweak != nil {
				tweak(d)
			}
		})
	return r, openStream(t, r, secretA)
}

// groundRangeFromSite is the horizontal distance and azimuth from site to p.
func groundRangeFromSite(p geo.LLA) (az, rng float64) {
	az, _, rng = geo.LLAToAER(geo.LLA{Lat: site.Lat, Lon: site.Lon, Alt: p.Alt}, p)
	return az, rng
}

// wantGroundRange is how far a ray depressed elDown degrees from heightM
// above flat ground meets it (curvature is negligible this close).
func wantGroundRange(heightM, elDown float64) float64 {
	return heightM / math.Tan(elDown*math.Pi/180)
}

// blockingTerrain answers nothing until its context ends.
type blockingTerrain struct{}

func (blockingTerrain) HeightHAE(ctx context.Context, _, _ float64) (float64, error) {
	<-ctx.Done()
	return 0, ctx.Err()
}
func (blockingTerrain) Intersect(ctx context.Context, _ geo.LLA, _, _, _ float64) (terrain.Hit, error) {
	<-ctx.Done()
	return terrain.Hit{}, ctx.Err()
}
func (blockingTerrain) DirectionCovariance(ctx context.Context, _ geo.LLA, _, _, _, _, _ float64, _ terrain.Hit) ([6]float64, bool) {
	<-ctx.Done()
	return [6]float64{}, false
}

// slowTerrain answers as the plateau does, each call delay late (sooner if
// its context ends first, with the context's error).
type slowTerrain struct {
	*terrain.Service
	delay time.Duration
}

func (s slowTerrain) wait(ctx context.Context) error {
	select {
	case <-time.After(s.delay):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (s slowTerrain) HeightHAE(ctx context.Context, lat, lon float64) (float64, error) {
	if err := s.wait(ctx); err != nil {
		return 0, err
	}
	return s.Service.HeightHAE(ctx, lat, lon)
}
func (s slowTerrain) Intersect(ctx context.Context, o geo.LLA, az, el, maxR float64) (terrain.Hit, error) {
	if err := s.wait(ctx); err != nil {
		return terrain.Hit{}, err
	}
	return s.Service.Intersect(ctx, o, az, el, maxR)
}
func (s slowTerrain) DirectionCovariance(ctx context.Context, o geo.LLA, az, el, maxR, sAz, sEl float64, hit terrain.Hit) ([6]float64, bool) {
	if s.wait(ctx) != nil {
		return [6]float64{}, false
	}
	return s.Service.DirectionCovariance(ctx, o, az, el, maxR, sAz, sEl, hit)
}

// unavailableTerrain has no tiles.
type unavailableTerrain struct{}

func (unavailableTerrain) HeightHAE(context.Context, float64, float64) (float64, error) {
	return 0, terrain.ErrUnavailable
}
func (unavailableTerrain) Intersect(context.Context, geo.LLA, float64, float64, float64) (terrain.Hit, error) {
	return terrain.Hit{}, terrain.ErrUnavailable
}
func (unavailableTerrain) DirectionCovariance(context.Context, geo.LLA, float64, float64, float64, float64, float64, terrain.Hit) ([6]float64, bool) {
	return [6]float64{}, false
}

// covFailTerrain is the plateau, except a perturbed ray never resolves.
type covFailTerrain struct{ *terrain.Service }

func (covFailTerrain) DirectionCovariance(context.Context, geo.LLA, float64, float64, float64, float64, float64, terrain.Hit) ([6]float64, bool) {
	return [6]float64{}, false
}

func TestNewCopiesTheTerrainDeps(t *testing.T) {
	d := placer(t, nil, nil).d
	if d.terrain != nil || d.terrainWait != 2*time.Second || d.maxRangeM != 30000 {
		t.Errorf("defaults: terrain %v wait %v max range %v, want nil, 2s, 30000", d.terrain, d.terrainWait, d.maxRangeM)
	}
	p := plateau()
	dv, err := New(spec(map[string]any{"port": 20000}, nil), driver.Deps{Logger: zap.NewNop(), Terrain: p, TerrainWait: time.Second, TerrainMaxRangeM: 5000})
	if err != nil {
		t.Fatal(err)
	}
	if d := dv.(*Driver); d.terrain != driver.Terrain(p) || d.terrainWait != time.Second || d.maxRangeM != 5000 {
		t.Errorf("set: terrain %v wait %v max range %v", d.terrain, d.terrainWait, d.maxRangeM)
	}
}

func TestDirectionOnlyPlacedOnTerrain(t *testing.T) {
	r, st := terrainRig(t, nil, camera(ptr(plateauM+12)), plateau(), nil)
	if ack := send(t, st, dirReport("walker", 90, ptr(-5.0))); ack.GetStatus() != ingestv1.AckStatus_ACK_STATUS_OK {
		t.Fatal(ack)
	}
	em := r.sink.emitted()
	if len(em) != 1 {
		t.Fatalf("%d observations emitted, want 1", len(em))
	}
	o := em[0]
	if !o.HasPosition || o.Altitude == nil || math.Abs(*o.Altitude-plateauM) > 0.5 {
		t.Fatalf("position %v altitude %v, want a point on the plateau", o.HasPosition, o.Altitude)
	}
	az, rng := groundRangeFromSite(geo.LLA{Lat: o.Latitude, Lon: o.Longitude, Alt: *o.Altitude})
	if want := wantGroundRange(12, 5); math.Abs(rng-want) > 1.5 || math.Abs(az-90) > 0.1 {
		t.Errorf("placed %.1f m out at azimuth %.2f, want about %.1f m at 90", rng, az, want)
	}
	if o.PosCovariance == nil || o.CircularError == nil || *o.CircularError <= 0 {
		t.Errorf("covariance %v circular error %v: both should be set", o.PosCovariance, o.CircularError)
	}
	if o.TrackEnvironment != obs.EnvGround || o.Polar == nil || !o.Polar.DirectionOnly {
		t.Errorf("environment %q polar %+v", o.TrackEnvironment, o.Polar)
	}
}

// A sent environment is kept, and a sent circular error wins.
func TestDirectionOnlyPlacementKeepsWhatWasSent(t *testing.T) {
	s := placer(t, plateau(), nil)
	o := dirObs(90, -5)
	o.TrackEnvironment, o.CircularError = "SURFACE", ptr(3.0)
	if b, why := s.placeDirectionOnly(context.Background(), o, camera(ptr(plateauM+12)), fixedOrigin(camera(ptr(plateauM+12)))); b != nil || why != "" {
		t.Fatalf("bearing %+v reason %q, want it placed", b, why)
	}
	if o.TrackEnvironment != "SURFACE" || *o.CircularError != 3 {
		t.Errorf("environment %q circular error %v", o.TrackEnvironment, *o.CircularError)
	}
}

func TestDirectionOnlyAirTargetBecomesBearing(t *testing.T) {
	p := plateau()
	s := placer(t, p, nil)
	e := camera(ptr(plateauM + 12))
	o := dirObs(90, -5)
	o.EntityType = "DRONE"
	o.EoIr = &obs.EoIrData{CameraTrackID: "c-7", Classification: "quadcopter", ClassificationConfidence: ptr(0.8), Band: "LWIR"}
	b, why := s.placeDirectionOnly(context.Background(), o, e, fixedOrigin(e))
	if b == nil || why != reasonAirTarget || b.Reason != reasonAirTarget {
		t.Fatalf("bearing %+v reason %q, want an air_target bearing", b, why)
	}
	hit, err := p.Intersect(context.Background(), geo.LLA{Lat: site.Lat, Lon: site.Lon, Alt: plateauM + 12}, 90, -5, 30000)
	if err != nil || !hit.Found {
		t.Fatal(hit, err)
	}
	if math.Abs(b.LengthM-hit.RangeM) > 1e-6 {
		t.Errorf("length %v, want the terrain hit's range %v", b.LengthM, hit.RangeM)
	}
	if b.AzimuthDeg != 90 || b.ElevationDeg != -5 || b.TrackID != "c-7" || b.Classification != "quadcopter" ||
		b.Confidence == nil || *b.Confidence != 0.8 || b.Band != "LWIR" || !b.ObservedAt.Equal(o.SourceTimestamp) {
		t.Errorf("bearing %+v", b)
	}
	if o.HasPosition || o.PosCovariance != nil {
		t.Errorf("an air target must not be placed: position %v covariance %v", o.HasPosition, o.PosCovariance)
	}
	// Looking up, nothing is hit: the length is the coverage's maximum range.
	e.Coverage.MaxRangeM = 5000
	o = dirObs(90, 10)
	o.TrackEnvironment = obs.EnvAir
	if b, why = s.placeDirectionOnly(context.Background(), o, e, fixedOrigin(e)); b == nil || why != reasonAirTarget || b.LengthM != 5000 {
		t.Errorf("bearing %+v reason %q, want air_target 5000 m long", b, why)
	}
	// Through a report: accepted, nothing emitted.
	r, st := terrainRig(t, nil, camera(ptr(plateauM+12)), p, nil)
	rep := dirReport("drone", 90, ptr(-5.0))
	rep.GetObservation().EntityType = sentinelv1.EntityType_ENTITY_TYPE_DRONE
	if ack := send(t, st, rep); ack.GetStatus() != ingestv1.AckStatus_ACK_STATUS_OK {
		t.Fatal(ack)
	}
	if n := len(r.sink.emitted()); n != 0 {
		t.Errorf("%d observations emitted for a bearing", n)
	}
}

func TestDirectionOnlyAboveHorizonBecomesBearing(t *testing.T) {
	s := placer(t, plateau(), nil)
	e := camera(ptr(plateauM + 12))
	o := dirObs(90, 5)
	b, why := s.placeDirectionOnly(context.Background(), o, e, fixedOrigin(e))
	if b == nil || why != reasonNoGround || b.LengthM != 30000 {
		t.Errorf("bearing %+v reason %q, want no_ground_intersection 30000 m long", b, why)
	}
	if b != nil && !b.ObservedAt.Equal(o.SourceTimestamp) {
		t.Errorf("observed at %v, want %v", b.ObservedAt, o.SourceTimestamp)
	}
	// Without a source time, the bearing is stamped when it was received.
	o = dirObs(90, 5)
	o.SourceTimestamp = time.Time{}
	if b, _ = s.placeDirectionOnly(context.Background(), o, e, fixedOrigin(e)); b == nil || !b.ObservedAt.Equal(o.ReceivedAt) {
		t.Errorf("bearing %+v, want it observed at the receive time %v", b, o.ReceivedAt)
	}
}

func TestDirectionOnlyTerrainOffBecomesBearing(t *testing.T) {
	e := camera(ptr(plateauM + 12))
	for name, terr := range map[string]driver.Terrain{"off": nil, "unavailable": unavailableTerrain{}} {
		s := placer(t, terr, nil)
		o := dirObs(90, -5)
		b, why := s.placeDirectionOnly(context.Background(), o, e, fixedOrigin(e))
		if b == nil || why != reasonTerrainUnavail || b.LengthM != 30000 || o.HasPosition {
			t.Errorf("%s: bearing %+v reason %q placed %v, want a terrain_unavailable bearing", name, b, why, o.HasPosition)
		}
	}
}

// Ruling: a covariance that cannot be worked out leaves no placed point.
func TestDirectionOnlyUnresolvedCovarianceBecomesBearing(t *testing.T) {
	s := placer(t, covFailTerrain{plateau()}, nil)
	e := camera(ptr(plateauM + 12))
	o := dirObs(90, -5)
	b, why := s.placeDirectionOnly(context.Background(), o, e, fixedOrigin(e))
	if b == nil || why != reasonTerrainUnavail || o.HasPosition || o.PosCovariance != nil || o.Altitude != nil {
		t.Errorf("bearing %+v reason %q position %v covariance %v altitude %v, want a terrain_unavailable bearing and o untouched",
			b, why, o.HasPosition, o.PosCovariance, o.Altitude)
	}
}

func TestDirectionOnlyNoOriginAltitude(t *testing.T) {
	s := placer(t, plateau(), nil)
	e := camera(nil)
	o := dirObs(90, -5)
	b, why := s.placeDirectionOnly(context.Background(), o, e, fixedOrigin(e))
	if b == nil || why != reasonNoOriginAltitude || b.LengthM != 30000 {
		t.Fatalf("bearing %+v reason %q, want no_origin_altitude 30000 m long", b, why)
	}
	// Mounted 12 m above the ground: the origin is the terrain plus that.
	e.HeightAboveGroundM = ptr(12.0)
	o = dirObs(90, -5)
	if b, why = s.placeDirectionOnly(context.Background(), o, e, fixedOrigin(e)); b != nil || why != "" {
		t.Fatalf("bearing %+v reason %q, want it placed", b, why)
	}
	_, rng := groundRangeFromSite(geo.LLA{Lat: o.Latitude, Lon: o.Longitude, Alt: *o.Altitude})
	if want := wantGroundRange(12, 5); math.Abs(*o.Altitude-plateauM) > 0.5 || math.Abs(rng-want) > 1.5 {
		t.Errorf("altitude %v %.1f m out, want the plateau about %.1f m out", *o.Altitude, rng, want)
	}
	// Mounted at 0 m, the origin is on the ground: the line of sight meets
	// it at once, and nothing honest can be placed.
	e.HeightAboveGroundM = ptr(0.0)
	o = dirObs(90, -5)
	if b, why = s.placeDirectionOnly(context.Background(), o, e, fixedOrigin(e)); b == nil || why != reasonNoOriginAltitude || b.LengthM != 30000 || o.HasPosition {
		t.Errorf("height 0: bearing %+v reason %q placed %v, want no_origin_altitude 30000 m long", b, why, o.HasPosition)
	}
	e.HeightAboveGroundM = ptr(12.0)
	// With the height but no terrain, the terrain is what is missing.
	if b, why = placer(t, nil, nil).placeDirectionOnly(context.Background(), dirObs(90, -5), e, fixedOrigin(e)); b == nil || why != reasonTerrainUnavail {
		t.Errorf("bearing %+v reason %q, want terrain_unavailable", b, why)
	}
}

// An origin below the ground under it (a wrong stored altitude) meets the
// ground at range 0: a bearing, never a point at the camera's own site.
func TestDirectionOnlyOriginBelowGroundBecomesBearing(t *testing.T) {
	s := placer(t, plateau(), nil)
	e := camera(ptr(plateauM - 50))
	o := dirObs(90, -5)
	b, why := s.placeDirectionOnly(context.Background(), o, e, fixedOrigin(e))
	if b == nil || why != reasonNoOriginAltitude || b.LengthM != 30000 || o.HasPosition || o.PosCovariance != nil {
		t.Errorf("bearing %+v reason %q placed %v covariance %v, want a no_origin_altitude bearing 30000 m long and o untouched",
			b, why, o.HasPosition, o.PosCovariance)
	}
}

func TestDirectionOnlyMissingElevationIsBadPolar(t *testing.T) {
	r, st := terrainRig(t, nil, camera(ptr(plateauM+12)), plateau(), nil)
	for _, rep := range []*ingestv1.Report{
		dirReport("no elevation", 90, nil),
		dirReport("nan elevation", 90, ptr(math.NaN())),
		dirReport("nan azimuth", math.NaN(), ptr(-5.0)),
	} {
		if ack := send(t, st, rep); ack.GetStatus() != ingestv1.AckStatus_ACK_STATUS_REJECTED || ack.GetReason() != "bad_polar" {
			t.Errorf("%s: %v, want REJECTED bad_polar", rep.GetClientRef(), ack)
		}
	}
	// Its range is not looked at: a non-finite one is no reason to refuse it.
	rep := dirReport("nan range", 90, ptr(-5.0))
	rep.GetObservation().Polar.RangeM = math.NaN()
	if ack := send(t, st, rep); ack.GetStatus() != ingestv1.AckStatus_ACK_STATUS_OK {
		t.Errorf("nan range: %v, want OK", ack)
	}
	if em := r.sink.emitted(); len(em) != 1 || !em[0].HasPosition || em[0].Polar == nil || em[0].Polar.RangeM != 0 {
		t.Errorf("emitted %d, want 1 placed on the terrain with its polar's range 0", len(em))
	}
}

// Review Focus 2: a detection waits terrainWait for terrain in all, however
// many calls it makes, and a stalled terrain does not stall the stream.
func TestDirectionOnlyTerrainWaitBounded(t *testing.T) {
	const wait = 300 * time.Millisecond
	const slack = 200 * time.Millisecond
	short := func(d *Driver) { d.terrainWait = wait }
	e := camera(ptr(plateauM + 12))
	start := time.Now()
	b, why := placer(t, blockingTerrain{}, short).placeDirectionOnly(context.Background(), dirObs(90, -5), e, fixedOrigin(e))
	if took := time.Since(start); took > wait+slack || b == nil || why != reasonTerrainUnavail {
		t.Errorf("blocked terrain: %v, bearing %+v reason %q, want terrain_unavailable within %v", took, b, why, wait+slack)
	}
	// Each call answers inside the wait, but together they overrun it.
	mounted := camera(nil)
	mounted.HeightAboveGroundM = ptr(12.0)
	slow := slowTerrain{Service: plateau(), delay: 200 * time.Millisecond}
	start = time.Now()
	b, why = placer(t, slow, short).placeDirectionOnly(context.Background(), dirObs(90, -5), mounted, fixedOrigin(mounted))
	if took := time.Since(start); took > wait+slack || b == nil || why != reasonTerrainUnavail {
		t.Errorf("slow terrain: %v, bearing %+v reason %q, want terrain_unavailable within %v", took, b, why, wait+slack)
	}
	// Through a report: acked in time, nothing emitted.
	r, st := terrainRig(t, nil, e, blockingTerrain{}, short)
	start = time.Now()
	if ack := send(t, st, dirReport("stalled", 90, ptr(-5.0))); ack.GetStatus() != ingestv1.AckStatus_ACK_STATUS_OK {
		t.Fatal(ack)
	}
	if took := time.Since(start); took > wait+slack {
		t.Errorf("ack took %v, want within %v", took, wait+slack)
	}
	if n := len(r.sink.emitted()); n != 0 {
		t.Errorf("%d observations emitted", n)
	}
}

// Placed detections are held to the coverage, as positions are.
func TestDirectionOnlyPlacedIsHeldToCoverage(t *testing.T) {
	e := camera(ptr(plateauM + 12))
	e.BoresightDeg = 270
	e.Coverage = emplacement.Coverage{MaxRangeM: 5000, AzimuthWidthDeg: 60}
	_, st := terrainRig(t, map[string]any{"enforceCoverage": true}, e, plateau(), nil)
	if ack := send(t, st, dirReport("behind", 90, ptr(-5.0))); ack.GetReason() != "outside_coverage" {
		t.Errorf("%v, want outside_coverage", ack)
	}
	if ack := send(t, st, dirReport("ahead", 270, ptr(-5.0))); ack.GetStatus() != ingestv1.AckStatus_ACK_STATUS_OK {
		t.Errorf("%v, want OK", ack)
	}
}

// radarReport is a range-polar radar report of a target at altitude alt
// (an altitude-only position), carrying the given detail.
func radarReport(id string, alt float64, detail *sentinelv1.RadarDetail) *ingestv1.Report {
	rep := polarReport(id, 90, 2000, nil)
	m := rep.GetObservation()
	m.Position = &sentinelv1.Coordinate{AltitudeMeters: ptr(alt)}
	m.Platform = &sentinelv1.PlatformDetail{Detail: &sentinelv1.PlatformDetail_Radar{Radar: detail}}
	return rep
}

func radarSite() emplacement.Emplacement {
	e := emp("radar", "grpc-1", "FIXED", "RADAR", "UNCLASSIFIED")
	e.Altitude = ptr(plateauM + 10)
	return e
}

func lastEmitted(t *testing.T, r *rig) *obs.Observation {
	t.Helper()
	em := r.sink.emitted()
	if len(em) == 0 {
		t.Fatal("nothing emitted")
	}
	return em[len(em)-1]
}

func TestRadarGroundTargetClamped(t *testing.T) {
	r, st := terrainRig(t, nil, radarSite(), plateau(), nil)
	for _, tc := range []struct {
		name string
		rep  *ingestv1.Report
	}{
		{"vehicle", radarReport("vehicle", plateauM+30, &sentinelv1.RadarDetail{Classification: sentinelv1.RadarClass_RADAR_CLASS_VEHICLE, HeightAboveGroundM: ptr(55.0)})},
		{"person below", radarReport("person", plateauM-20, &sentinelv1.RadarDetail{Classification: sentinelv1.RadarClass_RADAR_CLASS_PERSON})},
		{"ground environment", func() *ingestv1.Report {
			rep := radarReport("unknown", plateauM+30, &sentinelv1.RadarDetail{})
			rep.GetObservation().TrackEnvironment = sentinelv1.TrackEnvironment_TRACK_ENVIRONMENT_GROUND
			return rep
		}()},
	} {
		if ack := send(t, st, tc.rep); ack.GetStatus() != ingestv1.AckStatus_ACK_STATUS_OK {
			t.Fatalf("%s: %v", tc.name, ack)
		}
		o := lastEmitted(t, r)
		if o.Altitude == nil || math.Abs(*o.Altitude-plateauM) > 1e-6 || o.Radar == nil || o.Radar.HeightAboveGroundM != nil {
			t.Errorf("%s: altitude %v radar %+v, want the plateau and no height above ground", tc.name, o.Altitude, o.Radar)
		}
	}
}

func TestRadarDroneHeightAboveGround(t *testing.T) {
	r, st := terrainRig(t, nil, radarSite(), plateau(), nil)
	rep := radarReport("drone", plateauM+120, &sentinelv1.RadarDetail{Classification: sentinelv1.RadarClass_RADAR_CLASS_DRONE, HeightAboveGroundM: ptr(7.0)})
	rep.GetObservation().EntityType = sentinelv1.EntityType_ENTITY_TYPE_DRONE
	if ack := send(t, st, rep); ack.GetStatus() != ingestv1.AckStatus_ACK_STATUS_OK {
		t.Fatal(ack)
	}
	o := lastEmitted(t, r)
	if o.Altitude == nil || math.Abs(*o.Altitude-(plateauM+120)) > 1 {
		t.Errorf("altitude %v, want the measured %v kept", o.Altitude, plateauM+120)
	}
	if o.Radar == nil || o.Radar.HeightAboveGroundM == nil || math.Abs(*o.Radar.HeightAboveGroundM-(*o.Altitude-plateauM)) > 1e-6 ||
		math.Abs(*o.Radar.HeightAboveGroundM-120) > 1 {
		t.Errorf("height above ground %v, want about 120 (altitude less the plateau)", o.Radar.HeightAboveGroundM)
	}
	// An AIR environment is airborne too.
	rep = radarReport("air", plateauM+300, &sentinelv1.RadarDetail{})
	rep.GetObservation().TrackEnvironment = sentinelv1.TrackEnvironment_TRACK_ENVIRONMENT_AIR
	send(t, st, rep)
	if o := lastEmitted(t, r); o.Radar.HeightAboveGroundM == nil || math.Abs(*o.Radar.HeightAboveGroundM-300) > 1 {
		t.Errorf("AIR: height above ground %v, want about 300", o.Radar.HeightAboveGroundM)
	}
}

func TestRadarTerrainUnavailableKeepsAltitude(t *testing.T) {
	for name, terr := range map[string]driver.Terrain{"off": nil, "unavailable": unavailableTerrain{}, "stalled": blockingTerrain{}} {
		r, st := terrainRig(t, nil, radarSite(), terr, func(d *Driver) { d.terrainWait = 100 * time.Millisecond })
		send(t, st, radarReport("vehicle", plateauM+30, &sentinelv1.RadarDetail{Classification: sentinelv1.RadarClass_RADAR_CLASS_VEHICLE}))
		if o := lastEmitted(t, r); o.Altitude == nil || math.Abs(*o.Altitude-(plateauM+30)) > 1 {
			t.Errorf("%s: vehicle altitude %v, want the measured %v kept", name, o.Altitude, plateauM+30)
		}
		drone := radarReport("drone", plateauM+120, &sentinelv1.RadarDetail{HeightAboveGroundM: ptr(50.0)})
		drone.GetObservation().EntityType = sentinelv1.EntityType_ENTITY_TYPE_DRONE
		send(t, st, drone)
		if o := lastEmitted(t, r); o.Radar.HeightAboveGroundM != nil || math.Abs(*o.Altitude-(plateauM+120)) > 1 {
			t.Errorf("%s: drone height above ground %v altitude %v, want none and the measured altitude", name, o.Radar.HeightAboveGroundM, *o.Altitude)
		}
		if n := r.d.Health().Detail["terrainClampSkipped"]; n != int64(2) {
			t.Errorf("%s: terrainClampSkipped %v, want 2", name, n)
		}
	}
}

// Ruling R2: height above ground is the service's own figure. A sender's
// never survives, whatever the report.
func TestRadarSenderHeightAboveGroundNeverSurvives(t *testing.T) {
	r, st := terrainRig(t, nil, radarSite(), plateau(), nil)
	hag := &sentinelv1.RadarDetail{HeightAboveGroundM: ptr(99.0)}
	positional := claiming(observation("positional", 50.2, -4.1), "radar")
	positional.GetObservation().Platform = &sentinelv1.PlatformDetail{Detail: &sentinelv1.PlatformDetail_Radar{Radar: hag}}
	positional.GetObservation().EntityType = sentinelv1.EntityType_ENTITY_TYPE_DRONE
	for _, rep := range []*ingestv1.Report{
		radarReport("unclassified", plateauM+30, hag), // neither airborne nor ground
		radarReport("bird", plateauM+30, &sentinelv1.RadarDetail{Classification: sentinelv1.RadarClass_RADAR_CLASS_BIRD, HeightAboveGroundM: ptr(99.0)}),
		positional, // not placed from a polar
	} {
		if ack := send(t, st, rep); ack.GetStatus() != ingestv1.AckStatus_ACK_STATUS_OK {
			t.Fatalf("%s: %v", rep.GetClientRef(), ack)
		}
		if o := lastEmitted(t, r); o.Radar == nil || o.Radar.HeightAboveGroundM != nil {
			t.Errorf("%s: radar %+v, want no height above ground", rep.GetClientRef(), o.Radar)
		}
	}
}

// Terrain changes nothing for a range-polar report that is not a radar's.
func TestRangePolarUnchanged(t *testing.T) {
	e := emp("radar", "grpc-1", "FIXED", "RADAR", "UNCLASSIFIED")
	e.DefaultSigma = emplacement.Sigma{RangeM: ptr(50.0), AzimuthDeg: ptr(1.0)}
	r, st := terrainRig(t, nil, e, plateau(), nil)
	if ack := send(t, st, polarReport("p1", 90, 10000, ptr(0.0))); ack.GetStatus() != ingestv1.AckStatus_ACK_STATUS_OK {
		t.Fatal(ack)
	}
	o := r.sink.emitted()[0]
	want := geo.AERToLLA(geo.LLA{Lat: site.Lat, Lon: site.Lon}, 90, 0, 10000)
	if !o.HasPosition || o.Latitude != want.Lat || o.Longitude != want.Lon || o.Altitude == nil || *o.Altitude != want.Alt {
		t.Errorf("position %v,%v alt %v, want %v,%v alt %v exactly", o.Latitude, o.Longitude, o.Altitude, want.Lat, want.Lon, want.Alt)
	}
	if o.TrackEnvironment != "" || o.PosCovariance == nil {
		t.Errorf("environment %q covariance %v", o.TrackEnvironment, o.PosCovariance)
	}
}

// A mobile emplacement's origin has an altitude only when its reports gave
// one; without, a direction-only detection is no_origin_altitude.
func TestMobileOriginAltitudeIsKnownOnlyWhenSent(t *testing.T) {
	now := time.Now().UTC()
	state := func(alt *float64) *sentinelv1.EmplacementState {
		return &sentinelv1.EmplacementState{Position: &sentinelv1.Coordinate{Latitude: site.Lat, Longitude: site.Lon, AltitudeMeters: alt}}
	}
	with, why := fixOf(state(ptr(plateauM+12)), now)
	without, why2 := fixOf(state(nil), now.Add(time.Second))
	if why != "" || why2 != "" || !with.hasAlt || without.hasAlt {
		t.Fatalf("fixes %+v %+v (%q %q), want only the first with an altitude", with, without, why, why2)
	}
	var tr track
	tr.add(with)
	if org, ok := tr.at(now.Add(500*time.Millisecond), time.Second); !ok || !org.hasAlt || org.at.Alt != plateauM+12 {
		t.Errorf("carried on from a fix with an altitude: %+v %v", org, ok)
	}
	tr.add(without)
	if org, ok := tr.at(now.Add(500*time.Millisecond), time.Second); !ok || org.hasAlt {
		t.Errorf("between fixes, one without an altitude: %+v %v, want no altitude", org, ok)
	}
	if org, ok := tr.at(now.Add(1500*time.Millisecond), time.Second); !ok || org.hasAlt {
		t.Errorf("carried on from a fix without an altitude: %+v %v, want no altitude", org, ok)
	}
	e := emp("ship", "grpc-1", "MOBILE", "EO_IR", "UNCLASSIFIED")
	org, _ := tr.at(now.Add(1500*time.Millisecond), time.Second)
	if b, why := placer(t, plateau(), nil).placeDirectionOnly(context.Background(), dirObs(90, -5), e, org); b == nil || why != reasonNoOriginAltitude {
		t.Errorf("bearing %+v reason %q, want no_origin_altitude", b, why)
	}
}
