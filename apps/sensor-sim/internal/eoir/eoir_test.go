package eoir

import (
	"context"
	"math"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/sentinel/go-common/geo"
	"github.com/sentinel/go-common/terrain"
	sentinelv1 "github.com/sentinel/proto-gen/sentinel/v1"

	"github.com/sentinel/sensor-sim/internal/config"
	"github.com/sentinel/sensor-sim/internal/detect"
	"github.com/sentinel/sensor-sim/internal/scenario"
)

var _ detect.Detector = (*Camera)(nil)

var (
	site = geo.LLA{Lat: 45, Lon: 10, Alt: 130}
	now  = time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
)

func flat(float64, float64) float64 { return 100 }

// ridge is a wall 800 m high between longitude 10.02 and 10.025.
func ridge(_, lon float64) float64 {
	if lon > 10.02 && lon < 10.025 {
		return 800
	}
	return 100
}

func cfg() config.Sensor {
	return config.Sensor{
		Kind: "eoir", Name: "cam1", Lat: site.Lat, Lon: site.Lon,
		Boresight: 90, Width: 40, RangeKm: 10,
		Rate: 1, Pd: 1, SigmaAzDeg: 0.05, SigmaElDeg: 0.05, SigmaRangeM: 5, Band: "MWIR",
	}
}

func at(id, kind string, az, el, rangeM float64) scenario.Truth {
	return scenario.Truth{ID: id, Kind: kind, DisplayName: id, At: geo.AERToLLA(site, az, el, rangeM)}
}

func TestCameraDirectionOnly(t *testing.T) {
	c := New(cfg(), site, terrain.NewStatic(flat), 1)
	obs := c.Detect(context.Background(), now, []scenario.Truth{at("v1", "vehicle", 92, 1, 3000)})
	if len(obs) != 1 {
		t.Fatalf("got %d observations, want 1", len(obs))
	}
	o := obs[0]
	p := o.GetPolar()
	if p == nil || !p.DirectionOnly {
		t.Fatalf("polar %v, want direction_only", p)
	}
	if p.RangeM != 0 || p.RangeRateMps != nil {
		t.Errorf("range %v rate %v, want absent", p.RangeM, p.RangeRateMps)
	}
	if p.GetSigma().RangeM != nil {
		t.Errorf("a camera has no range sigma, got %v", *p.GetSigma().RangeM)
	}
	if math.Abs(p.AzimuthDeg-92) > 0.4 || p.ElevationDeg == nil || math.Abs(*p.ElevationDeg-1) > 0.4 {
		t.Errorf("az %v el %v, want about 92 and 1", p.AzimuthDeg, p.ElevationDeg)
	}
	if p.GetSigma().GetAzimuthDeg() != 0.05 || p.GetSigma().GetElevationDeg() != 0.05 {
		t.Errorf("sigma %v", p.GetSigma())
	}
	if p.AzimuthReference != sentinelv1.AzimuthReference_AZIMUTH_REFERENCE_TRUE_NORTH {
		t.Errorf("azimuth reference %v", p.AzimuthReference)
	}
	if o.Position != nil {
		t.Errorf("a direction-only observation carries no position, got %v", o.Position)
	}
	if o.EntityType != sentinelv1.EntityType_ENTITY_TYPE_VEHICLE || o.TrackEnvironment != sentinelv1.TrackEnvironment_TRACK_ENVIRONMENT_GROUND {
		t.Errorf("type %v env %v", o.EntityType, o.TrackEnvironment)
	}
	if o.GetPlatform().GetRadar() != nil {
		t.Error("a camera observation has no radar detail")
	}

	d := o.GetPlatform().GetEoir()
	if d == nil {
		t.Fatal("no eoir detail")
	}
	if d.Band != sentinelv1.EoIrBand_EO_IR_BAND_MWIR || d.Classification != "vehicle" || d.CameraTrackId == "" {
		t.Errorf("detail %v", d)
	}
	if c := d.GetClassificationConfidence(); c < 0.6 || c > 0.95 {
		t.Errorf("confidence %v", c)
	}
	if d.GetHorizontalFovDeg() != 40 {
		t.Errorf("horizontal fov %v, want the 40 degree width", d.GetHorizontalFovDeg())
	}
	if v := d.GetVerticalFovDeg(); v <= 0 || v > 180 {
		t.Errorf("vertical fov %v", v)
	}
	b := d.BoundingBox
	if b == nil || b.Width <= 0 || b.Height <= 0 || b.X < 0 || b.Y < 0 || b.X+b.Width > 1+1e-9 || b.Y+b.Height > 1+1e-9 {
		t.Errorf("bounding box %v is not inside the frame", b)
	}
}

func TestCameraVerticalFovIsTheElevationSpan(t *testing.T) {
	s := cfg()
	s.ElevationMinDeg, s.ElevationMaxDeg = ptr(-5), ptr(25)
	c := New(s, site, terrain.NewStatic(flat), 1)
	o := c.Detect(context.Background(), now, []scenario.Truth{at("v", "vehicle", 90, 3, 2000)})
	if len(o) != 1 {
		t.Fatal("not detected")
	}
	if got := o[0].GetPlatform().GetEoir().GetVerticalFovDeg(); got != 30 {
		t.Errorf("vertical fov %v, want 30", got)
	}
}

func TestCameraBoundingBoxTracksThePosition(t *testing.T) {
	c := New(cfg(), site, terrain.NewStatic(flat), 1)
	box := func(az float64) *sentinelv1.NormalizedBox {
		o := c.Detect(context.Background(), now, []scenario.Truth{at("v", "vehicle", az, 2, 3000)})
		if len(o) != 1 {
			t.Fatalf("az %v not detected", az)
		}
		return o[0].GetPlatform().GetEoir().BoundingBox
	}
	left, mid, right := box(75), box(90), box(105)
	cx := func(b *sentinelv1.NormalizedBox) float64 { return b.X + b.Width/2 }
	if !(cx(left) < cx(mid) && cx(mid) < cx(right)) {
		t.Errorf("box centres %v %v %v should increase with azimuth", cx(left), cx(mid), cx(right))
	}
	if math.Abs(cx(mid)-0.5) > 0.02 {
		t.Errorf("a target on the boresight is at x=%v, want 0.5", cx(mid))
	}
}

func TestCameraOutsideFovNotDetected(t *testing.T) {
	s := cfg()
	s.ElevationMinDeg, s.ElevationMaxDeg = ptr(-5), ptr(20)
	c := New(s, site, terrain.NewStatic(flat), 1)
	tests := []struct {
		name string
		tr   scenario.Truth
		want bool
	}{
		{"inside", at("a", "vehicle", 95, 3, 3000), true},
		{"wedge edge", at("a", "vehicle", 108, 3, 3000), true},
		{"left of the wedge", at("b", "vehicle", 60, 3, 3000), false},
		{"behind", at("c", "vehicle", 270, 3, 3000), false},
		{"above the elevation window", at("d", "drone", 90, 40, 3000), false},
		{"below the elevation window", at("e", "vehicle", 90, -20, 3000), false},
		{"beyond range", at("f", "vehicle", 90, 0, 10500), false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := len(c.Detect(context.Background(), now, []scenario.Truth{tc.tr})) == 1
			if got != tc.want {
				t.Errorf("detected=%v, want %v", got, tc.want)
			}
		})
	}
}

func TestCameraOmniWidthSeesAllAround(t *testing.T) {
	s := cfg()
	s.Width = 360
	c := New(s, site, terrain.NewStatic(flat), 1)
	if got := c.Detect(context.Background(), now, []scenario.Truth{at("b", "vehicle", 270, 2, 2000)}); len(got) != 1 {
		t.Fatal("an omni camera missed a target behind the boresight")
	}
}

func TestCameraOccludedNotDetected(t *testing.T) {
	// The target is east at lon 10.05, across the ridge at 10.02-10.025.
	far := scenario.Truth{ID: "v", Kind: "vehicle", At: geo.LLA{Lat: 45, Lon: 10.05, Alt: 101}}
	if _, _, r := geo.LLAToAER(site, far.At); r > 10000 {
		t.Fatalf("test geometry: target is %.0f m away", r)
	}
	open := New(cfg(), site, terrain.NewStatic(flat), 1)
	if got := open.Detect(context.Background(), now, []scenario.Truth{far}); len(got) != 1 {
		t.Fatal("control: the target should be visible over flat ground")
	}
	blocked := New(cfg(), site, terrain.NewStatic(ridge), 1)
	if got := blocked.Detect(context.Background(), now, []scenario.Truth{far}); len(got) != 0 {
		t.Fatal("a ridge between the camera and the target did not hide it")
	}
}

func TestCameraWithoutTerrainAssumesVisible(t *testing.T) {
	c := New(cfg(), site, nil, 1)
	if got := c.Detect(context.Background(), now, []scenario.Truth{at("v", "vehicle", 90, 1, 3000)}); len(got) != 1 {
		t.Fatalf("got %d observations without terrain, want 1", len(got))
	}
}

func TestCameraDroneIsAir(t *testing.T) {
	c := New(cfg(), site, terrain.NewStatic(flat), 1)
	tests := []struct {
		kind string
		typ  sentinelv1.EntityType
		env  sentinelv1.TrackEnvironment
	}{
		{"drone", sentinelv1.EntityType_ENTITY_TYPE_DRONE, sentinelv1.TrackEnvironment_TRACK_ENVIRONMENT_AIR},
		{"person", sentinelv1.EntityType_ENTITY_TYPE_PERSON, sentinelv1.TrackEnvironment_TRACK_ENVIRONMENT_GROUND},
		{"vehicle", sentinelv1.EntityType_ENTITY_TYPE_VEHICLE, sentinelv1.TrackEnvironment_TRACK_ENVIRONMENT_GROUND},
	}
	for _, tc := range tests {
		t.Run(tc.kind, func(t *testing.T) {
			o := c.Detect(context.Background(), now, []scenario.Truth{at("x", tc.kind, 90, 6, 1500)})
			if len(o) != 1 {
				t.Fatalf("got %d observations", len(o))
			}
			if o[0].EntityType != tc.typ || o[0].TrackEnvironment != tc.env {
				t.Errorf("type %v env %v", o[0].EntityType, o[0].TrackEnvironment)
			}
			if got := o[0].GetPlatform().GetEoir().Classification; got != tc.kind || got != strings.ToLower(got) {
				t.Errorf("classification %q, want %q", got, tc.kind)
			}
		})
	}
}

func TestCameraIdentityStable(t *testing.T) {
	c := New(cfg(), site, terrain.NewStatic(flat), 1)
	a, b := at("alpha", "vehicle", 90, 1, 2000), at("bravo", "person", 100, 1, 2500)
	first := c.Detect(context.Background(), now, []scenario.Truth{a, b})
	second := c.Detect(context.Background(), now.Add(time.Second), []scenario.Truth{a, b})
	if len(first) != 2 || len(second) != 2 {
		t.Fatalf("got %d and %d observations", len(first), len(second))
	}
	for i := range first {
		if first[i].SourceEntityId != second[i].SourceEntityId ||
			first[i].GetPlatform().GetEoir().CameraTrackId != second[i].GetPlatform().GetEoir().CameraTrackId {
			t.Errorf("target %d changed identity", i)
		}
	}
	if first[0].SourceEntityId == first[1].SourceEntityId ||
		first[0].GetPlatform().GetEoir().CameraTrackId == first[1].GetPlatform().GetEoir().CameraTrackId {
		t.Error("two targets share an identity")
	}
}

func TestCameraPdAndSeed(t *testing.T) {
	s := cfg()
	s.Pd = 0.5
	truth := []scenario.Truth{at("v", "vehicle", 90, 1, 2000)}
	run := func(seed int64) []*sentinelv1.Observation {
		c := New(s, site, terrain.NewStatic(flat), seed)
		var all []*sentinelv1.Observation
		for i := 0; i < 400; i++ {
			all = append(all, c.Detect(context.Background(), now, truth)...)
		}
		return all
	}
	a, b := run(5), run(5)
	if f := float64(len(a)) / 400; f < 0.42 || f > 0.58 {
		t.Errorf("detected %.2f of scans at pd 0.5", f)
	}
	if len(a) != len(b) {
		t.Fatalf("same seed, %d and %d detections", len(a), len(b))
	}
	for i := range a {
		if !proto.Equal(a[i], b[i]) {
			t.Fatalf("observation %d differs for the same seed", i)
		}
	}
}

func ptr(v float64) *float64 { return &v }
