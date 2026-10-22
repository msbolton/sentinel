package radar

import (
	"context"
	"math"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/sentinel/go-common/geo"
	sentinelv1 "github.com/sentinel/proto-gen/sentinel/v1"

	"github.com/sentinel/sensor-sim/internal/config"
	"github.com/sentinel/sensor-sim/internal/detect"
	"github.com/sentinel/sensor-sim/internal/scenario"
)

var _ detect.Detector = (*Radar)(nil)

var (
	site = geo.LLA{Lat: 45, Lon: 10, Alt: 100}
	now  = time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
)

func cfg() config.Sensor {
	return config.Sensor{
		Kind: "radar", Name: "r1", Lat: site.Lat, Lon: site.Lon,
		Boresight: 90, Width: 120, RangeKm: 10, MinRangeKm: 0.2,
		Rate: 1, Pd: 1, SigmaAzDeg: 0.1, SigmaElDeg: 0.1, SigmaRangeM: 5,
	}
}

// at puts a target at az/el/range from the site.
func at(id, kind string, az, el, rangeM float64, vel [3]float64) scenario.Truth {
	return scenario.Truth{ID: id, Kind: kind, DisplayName: id, At: geo.AERToLLA(site, az, el, rangeM), VelENU: vel}
}

func detect1(t *testing.T, r *Radar, tr scenario.Truth) *sentinelv1.Observation {
	t.Helper()
	obs := r.Detect(context.Background(), now, []scenario.Truth{tr})
	if len(obs) != 1 {
		t.Fatalf("got %d observations, want 1", len(obs))
	}
	return obs[0]
}

func TestRadarDetectsWithinRange(t *testing.T) {
	r := New(cfg(), site, 1)
	o := detect1(t, r, at("v1", "vehicle", 90, 0, 3000, [3]float64{0, 0, 0}))

	p := o.GetPolar()
	if p == nil {
		t.Fatal("no polar measurement")
	}
	if math.Abs(p.RangeM-3000) > 30 { // 6 sigma
		t.Errorf("range %v, want about 3000", p.RangeM)
	}
	if math.Abs(detect.AngleDiff(p.AzimuthDeg, 90)) > 0.7 {
		t.Errorf("azimuth %v, want about 90", p.AzimuthDeg)
	}
	if p.ElevationDeg == nil || math.Abs(*p.ElevationDeg) > 0.7 {
		t.Errorf("elevation %v, want about 0", p.ElevationDeg)
	}
	if p.DirectionOnly {
		t.Error("a radar measures range")
	}
	if p.AzimuthReference != sentinelv1.AzimuthReference_AZIMUTH_REFERENCE_TRUE_NORTH {
		t.Errorf("azimuth reference %v", p.AzimuthReference)
	}
	if s := p.GetSigma(); s == nil || s.GetRangeM() != 5 || s.GetAzimuthDeg() != 0.1 || s.GetElevationDeg() != 0.1 {
		t.Errorf("sigma %v", s)
	}
	if o.Position != nil {
		t.Errorf("polar observations carry no position, got %v", o.Position)
	}
	if o.EntityType != sentinelv1.EntityType_ENTITY_TYPE_VEHICLE || o.TrackEnvironment != sentinelv1.TrackEnvironment_TRACK_ENVIRONMENT_GROUND {
		t.Errorf("type %v env %v", o.EntityType, o.TrackEnvironment)
	}
	if o.SourceEntityId == "" || o.DisplayName != "v1" {
		t.Errorf("identity %q %q", o.SourceEntityId, o.DisplayName)
	}
	d := o.GetPlatform().GetRadar()
	if d == nil {
		t.Fatal("no radar detail")
	}
	if d.RadarTrackId == "" || d.RcsDbsm == nil || d.SnrDb == nil || d.ClassificationConfidence == nil || d.TrackQuality == nil {
		t.Errorf("detail incomplete: %v", d)
	}
	if c := d.GetClassificationConfidence(); c < 0.6 || c > 0.95 {
		t.Errorf("classification confidence %v outside 0.6-0.95", c)
	}
	if d.Mode != sentinelv1.RadarMode_RADAR_MODE_TRACK {
		t.Errorf("mode %v", d.Mode)
	}
}

func TestRadarRangeRateInBothPlaces(t *testing.T) {
	// The target is due east; the sensor looks east. Moving east is receding.
	tests := []struct {
		name string
		vel  [3]float64
		want float64
	}{
		{"receding", [3]float64{12, 0, 0}, 12},
		{"approaching", [3]float64{-7, 0, 0}, -7},
		{"crossing", [3]float64{0, 15, 0}, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := New(cfg(), site, 1)
			// At 2 km east the line of sight departs a little from due east
			// because of the Earth's curvature, hence the tolerance.
			o := detect1(t, r, at("v", "vehicle", 90, 0, 2000, tc.vel))
			polar := o.GetPolar().GetRangeRateMps()
			detail := o.GetPlatform().GetRadar().GetRangeRateMps()
			if math.Abs(polar-tc.want) > 0.05 || math.Abs(detail-tc.want) > 0.05 {
				t.Errorf("polar %v detail %v, want %v", polar, detail, tc.want)
			}
			if o.GetPolar().RangeRateMps == nil || o.GetPlatform().GetRadar().RangeRateMps == nil {
				t.Error("range rate must be set in both RadarDetail and PolarMeasurement")
			}
		})
	}
}

func TestRadarNoDetectionBeyondRange(t *testing.T) {
	r := New(cfg(), site, 1)
	for i := 0; i < 50; i++ {
		if obs := r.Detect(context.Background(), now, []scenario.Truth{at("far", "vehicle", 90, 0, 10500, [3]float64{})}); len(obs) != 0 {
			t.Fatalf("scan %d detected a target beyond range", i)
		}
	}
}

func TestRadarCoverageLimits(t *testing.T) {
	c := cfg()
	c.ElevationMinDeg, c.ElevationMaxDeg = ptr(-2.0), ptr(30.0)
	r := New(c, site, 1)
	tests := []struct {
		name string
		tr   scenario.Truth
		want bool
	}{
		{"inside", at("a", "vehicle", 90, 5, 3000, [3]float64{}), true},
		{"inside the wedge edge", at("a", "vehicle", 145, 5, 3000, [3]float64{}), true},
		{"outside the wedge", at("b", "vehicle", 200, 5, 3000, [3]float64{}), false},
		{"inside minimum range", at("c", "vehicle", 90, 5, 100, [3]float64{}), false},
		{"above the elevation limit", at("d", "drone", 90, 40, 3000, [3]float64{}), false},
		{"below the elevation limit", at("e", "drone", 90, -10, 3000, [3]float64{}), false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := len(r.Detect(context.Background(), now, []scenario.Truth{tc.tr})) == 1
			if got != tc.want {
				t.Errorf("detected=%v, want %v", got, tc.want)
			}
		})
	}
}

func TestRadarDroneIsAir(t *testing.T) {
	r := New(cfg(), site, 1)
	tests := []struct {
		kind  string
		typ   sentinelv1.EntityType
		env   sentinelv1.TrackEnvironment
		class sentinelv1.RadarClass
	}{
		{"drone", sentinelv1.EntityType_ENTITY_TYPE_DRONE, sentinelv1.TrackEnvironment_TRACK_ENVIRONMENT_AIR, sentinelv1.RadarClass_RADAR_CLASS_DRONE},
		{"person", sentinelv1.EntityType_ENTITY_TYPE_PERSON, sentinelv1.TrackEnvironment_TRACK_ENVIRONMENT_GROUND, sentinelv1.RadarClass_RADAR_CLASS_PERSON},
		{"vehicle", sentinelv1.EntityType_ENTITY_TYPE_VEHICLE, sentinelv1.TrackEnvironment_TRACK_ENVIRONMENT_GROUND, sentinelv1.RadarClass_RADAR_CLASS_VEHICLE},
	}
	for _, tc := range tests {
		t.Run(tc.kind, func(t *testing.T) {
			// Close in, so even a drone is certain to be seen.
			var o *sentinelv1.Observation
			for i := 0; i < 20 && o == nil; i++ {
				if obs := r.Detect(context.Background(), now, []scenario.Truth{at("x", tc.kind, 90, 3, 700, [3]float64{})}); len(obs) == 1 {
					o = obs[0]
				}
			}
			if o == nil {
				t.Fatal("never detected")
			}
			if o.EntityType != tc.typ || o.TrackEnvironment != tc.env {
				t.Errorf("type %v env %v", o.EntityType, o.TrackEnvironment)
			}
			if c := o.GetPlatform().GetRadar().Classification; c != tc.class && c != sentinelv1.RadarClass_RADAR_CLASS_UNKNOWN {
				t.Errorf("classification %v, want %v or UNKNOWN", c, tc.class)
			}
		})
	}
}

func TestRadarNoiseSeeded(t *testing.T) {
	truth := []scenario.Truth{
		at("a", "vehicle", 80, 1, 2500, [3]float64{3, 1, 0}),
		at("b", "person", 100, 0, 1200, [3]float64{}),
		at("c", "drone", 90, 8, 900, [3]float64{-4, 0, 1}),
	}
	run := func(seed int64) []*sentinelv1.Observation {
		r := New(cfg(), site, seed)
		var all []*sentinelv1.Observation
		for i := 0; i < 20; i++ {
			all = append(all, r.Detect(context.Background(), now.Add(time.Duration(i)*time.Second), truth)...)
		}
		return all
	}
	a, b, other := run(42), run(42), run(43)
	if len(a) == 0 || len(a) != len(b) {
		t.Fatalf("lengths %d and %d", len(a), len(b))
	}
	for i := range a {
		if !proto.Equal(a[i], b[i]) {
			t.Fatalf("observation %d differs for the same seed:\n%v\n%v", i, a[i], b[i])
		}
	}
	same := len(a) == len(other)
	for i := 0; same && i < len(a); i++ {
		same = proto.Equal(a[i], other[i])
	}
	if same {
		t.Error("a different seed gave identical output")
	}
}

func TestRadarNoiseHasTheConfiguredSpread(t *testing.T) {
	c := cfg()
	c.SigmaRangeM, c.SigmaAzDeg = 20, 0.5
	r := New(c, site, 7)
	truth := []scenario.Truth{at("v", "vehicle", 90, 0, 3000, [3]float64{})}
	const n = 4000
	var sumR, sumR2, sumA2 float64
	trueAz, _, trueR := geo.LLAToAER(site, truth[0].At)
	for i := 0; i < n; i++ {
		p := r.Detect(context.Background(), now, truth)[0].GetPolar()
		dr := p.RangeM - trueR
		da := detect.AngleDiff(p.AzimuthDeg, trueAz)
		sumR += dr
		sumR2 += dr * dr
		sumA2 += da * da
	}
	if mean := sumR / n; math.Abs(mean) > 2 {
		t.Errorf("range bias %v", mean)
	}
	if sd := math.Sqrt(sumR2 / n); sd < 18 || sd > 22 {
		t.Errorf("range sigma %v, want about 20", sd)
	}
	if sd := math.Sqrt(sumA2 / n); sd < 0.45 || sd > 0.55 {
		t.Errorf("azimuth sigma %v, want about 0.5", sd)
	}
}

func TestDetectionProbability(t *testing.T) {
	const rmax = 10000.0
	tests := []struct {
		name  string
		kind  string
		r, pd float64
		want  float64
	}{
		{"vehicle at max range", "vehicle", rmax, 0.9, 0.9},
		{"person at max range", "person", rmax, 0.9, 0.9 * 0.3},
		{"drone at max range", "drone", rmax, 1, 0.15},
		{"vehicle inside clamps to pd", "vehicle", rmax / 2, 0.8, 0.8},
		{"drone at 70 percent range", "drone", 7000, 1, math.Pow(1/0.7, 4) * 0.15},
		{"drone close clamps to pd", "drone", 1000, 0.7, 0.7},
		{"zero range", "vehicle", 0, 0.5, 0.5},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := DetectionProbability(tc.kind, tc.r, rmax, tc.pd); math.Abs(got-tc.want) > 1e-9 {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestRadarSmallTargetsAreSeenLessOften(t *testing.T) {
	c := cfg()
	c.Pd = 1
	count := func(kind string) int {
		r := New(c, site, 5)
		n := 0
		for i := 0; i < 2000; i++ {
			n += len(r.Detect(context.Background(), now, []scenario.Truth{at("t", kind, 90, 2, 9500, [3]float64{})}))
		}
		return n
	}
	v, p, d := count("vehicle"), count("person"), count("drone")
	if !(v > p && p > d && d > 0) {
		t.Errorf("detections near max range: vehicle %d person %d drone %d, want vehicle > person > drone > 0", v, p, d)
	}
}

func TestRadarIdentityAndClassification(t *testing.T) {
	r := New(cfg(), site, 9)
	a := at("alpha", "vehicle", 90, 0, 2000, [3]float64{})
	b := at("bravo", "vehicle", 95, 0, 2500, [3]float64{})

	first := r.Detect(context.Background(), now, []scenario.Truth{a, b})
	second := r.Detect(context.Background(), now.Add(time.Second), []scenario.Truth{a, b})
	if len(first) != 2 || len(second) != 2 {
		t.Fatalf("got %d and %d observations", len(first), len(second))
	}
	for i := range first {
		if first[i].SourceEntityId != second[i].SourceEntityId ||
			first[i].GetPlatform().GetRadar().RadarTrackId != second[i].GetPlatform().GetRadar().RadarTrackId {
			t.Errorf("target %d changed identity between scans", i)
		}
	}
	if first[0].SourceEntityId == first[1].SourceEntityId ||
		first[0].GetPlatform().GetRadar().RadarTrackId == first[1].GetPlatform().GetRadar().RadarTrackId {
		t.Error("two targets share an identity")
	}

	// Classification is right about 90% of the time and otherwise UNKNOWN.
	correct, unknown := 0, 0
	const n = 3000
	for i := 0; i < n; i++ {
		switch r.Detect(context.Background(), now, []scenario.Truth{a})[0].GetPlatform().GetRadar().Classification {
		case sentinelv1.RadarClass_RADAR_CLASS_VEHICLE:
			correct++
		case sentinelv1.RadarClass_RADAR_CLASS_UNKNOWN:
			unknown++
		default:
			t.Fatal("classified as something other than the truth or UNKNOWN")
		}
	}
	if f := float64(correct) / n; f < 0.87 || f > 0.93 || correct+unknown != n {
		t.Errorf("correct %d unknown %d of %d", correct, unknown, n)
	}
}

func TestRadarRCSByKind(t *testing.T) {
	r := New(cfg(), site, 3)
	rcs := func(kind string) float64 {
		return detect1(t, r, at("t", kind, 90, 2, 800, [3]float64{})).GetPlatform().GetRadar().GetRcsDbsm()
	}
	if v, p, d := rcs("vehicle"), rcs("person"), rcs("drone"); !(v > p && p > d) {
		t.Errorf("rcs vehicle %v person %v drone %v, want descending", v, p, d)
	}
}

func ptr(v float64) *float64 { return &v }

// SNR follows the fourth-power range law but stays finite: a target at (or
// numerically at) the radar, or one closer than the law makes sense for, is
// capped at MaxSNRDb.
func TestSNR(t *testing.T) {
	tests := []struct {
		name           string
		rcs, rangeM    float64
		rangeMax, want float64
	}{
		{"vehicle at max range", 10, 10000, 10000, snrAtMaxRange},
		{"vehicle at a tenth of max range", 10, 1000, 10000, snrAtMaxRange + 40},
		{"drone at max range is floored", -15, 10000, 10000, snrFloor},
		{"vehicle very close is capped", 10, 1, 10000, MaxSNRDb},
		{"zero range is capped", 10, 0, 10000, MaxSNRDb},
		{"negative range is capped", 10, -5, 10000, MaxSNRDb},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := snrDb(tc.rcs, tc.rangeM, tc.rangeMax)
			if math.IsNaN(got) || math.IsInf(got, 0) || math.Abs(got-tc.want) > 1e-9 {
				t.Fatalf("snrDb(%v, %v, %v) = %v, want %v", tc.rcs, tc.rangeM, tc.rangeMax, got, tc.want)
			}
		})
	}
}
