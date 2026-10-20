package sensor_test

import (
	"math"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	sentinelv1 "github.com/sentinel/proto-gen/sentinel/v1"

	"github.com/sentinel/ew-sensor/internal/config"
	"github.com/sentinel/ew-sensor/internal/ownship"
	"github.com/sentinel/ew-sensor/internal/sensor"
	"github.com/sentinel/go-common/geo"
)

var site = geo.LLA{Lat: 36.6, Lon: -121.95, Alt: 45}

var t0 = time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)

// at places a static emitter at az/range from the site.
func at(id string, azDeg, rangeM float64) config.Emitter {
	p := geo.AERToLLA(site, azDeg, 0, rangeM)
	return config.Emitter{
		ID: id, EmitterType: "RADAR_SEARCH", FrequencyHz: 9.41e9,
		Start: config.LatLon{Lat: p.Lat, Lon: p.Lon},
	}
}

func cfgWith(es ...config.Emitter) config.Config {
	return config.Config{
		Emplacement: config.Emplacement{
			Lat: site.Lat, Lon: site.Lon, Alt: site.Alt, Boresight: 270, Width: 120,
			RangeKm: 150, MinRangeKm: 2,
			Mobility: config.MobilityFixed, AzimuthReference: config.AzimuthTrueNorth,
		},
		Sensor:       config.Sensor{Rate: 1, Pd: 1, SigmaM: 250},
		Emitters:     es,
		EmitterCount: len(es),
		Seed:         42,
	}
}

func fixedPose(c config.Config) ownship.Pose { return ownship.New(c).Pose(0) }

func detected(obs []*sentinelv1.Observation) map[string]bool {
	m := map[string]bool{}
	for _, o := range obs {
		m[o.SourceEntityId] = true
	}
	return m
}

func TestCoverage(t *testing.T) {
	tests := []struct {
		name    string
		mut     func(*config.Config)
		heading float64
		emits   []config.Emitter
		want    []string
	}{
		{
			name: "sector, range window",
			emits: []config.Emitter{
				at("in-mid", 270, 50000), at("in-edge-az", 325, 50000), at("out-az", 335, 50000),
				at("out-az-east", 90, 50000), at("too-far", 270, 151000), at("in-far", 270, 149000),
				at("too-close", 270, 1500), at("in-close", 270, 2500),
			},
			want: []string{"in-mid", "in-edge-az", "in-far", "in-close"},
		},
		{
			name: "wrap-around at north",
			mut:  func(c *config.Config) { c.Emplacement.Boresight, c.Emplacement.Width = 0, 90 },
			emits: []config.Emitter{
				at("az350", 350, 40000), at("az10", 10, 40000), at("az316", 316, 40000),
				at("az44", 44, 40000), at("az314", 314, 40000), at("az46", 46, 40000), at("az180", 180, 40000),
			},
			want: []string{"az350", "az10", "az316", "az44"},
		},
		{
			name: "omni",
			mut:  func(c *config.Config) { c.Emplacement.Width = 360 },
			emits: []config.Emitter{
				at("n", 0, 30000), at("e", 90, 30000), at("s", 180, 30000), at("w", 270, 30000), at("close", 123, 1000),
			},
			want: []string{"n", "e", "s", "w"},
		},
		{
			name: "platform boresight rotates with heading",
			mut: func(c *config.Config) {
				c.Emplacement.Mobility = config.MobilityMobile
				c.Emplacement.AzimuthReference = config.AzimuthPlatform
				c.Emplacement.Boresight, c.Emplacement.Width = 0, 60
			},
			heading: 90,
			emits:   []config.Emitter{at("east", 90, 40000), at("north", 0, 40000), at("ne", 110, 40000), at("se", 130, 40000)},
			want:    []string{"east", "ne"},
		},
		{
			name: "platform boresight wraps past 360",
			mut: func(c *config.Config) {
				c.Emplacement.Mobility = config.MobilityMobile
				c.Emplacement.AzimuthReference = config.AzimuthPlatform
				c.Emplacement.Boresight, c.Emplacement.Width = 90, 60
			},
			heading: 300, // effective boresight 30
			emits:   []config.Emitter{at("az30", 30, 40000), at("az55", 55, 40000), at("az65", 65, 40000), at("az120", 120, 40000)},
			want:    []string{"az30", "az55"},
		},
		{
			name: "true north ignores heading",
			mut: func(c *config.Config) {
				c.Emplacement.Mobility = config.MobilityMobile
				c.Emplacement.Boresight, c.Emplacement.Width = 0, 60
			},
			heading: 90,
			emits:   []config.Emitter{at("east", 90, 40000), at("north", 0, 40000)},
			want:    []string{"north"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := cfgWith(tc.emits...)
			if tc.mut != nil {
				tc.mut(&c)
			}
			pose := ownship.Pose{Lat: site.Lat, Lon: site.Lon, Alt: site.Alt, HeadingDeg: tc.heading}
			got := detected(sensor.New(c).Step(t0, pose))
			want := map[string]bool{}
			for _, w := range tc.want {
				want[w] = true
			}
			for id := range want {
				if !got["EMITTER-"+id] {
					t.Errorf("%s should be covered", id)
				}
			}
			for id := range got {
				if !want[id[len("EMITTER-"):]] {
					t.Errorf("%s should not be covered", id)
				}
			}
		})
	}
}

func TestCoverageUsesCurrentSensorPosition(t *testing.T) {
	// The emitter is 100 km west of the site: in range from there, out of range
	// once the ship has moved 100 km east.
	c := cfgWith(at("e", 270, 100000))
	s := sensor.New(c)
	if len(s.Step(t0, fixedPose(c))) != 1 {
		t.Fatal("emitter should be covered from the site")
	}
	moved := geo.AERToLLA(site, 90, 0, 100000)
	if got := s.Step(t0.Add(time.Second), ownship.Pose{Lat: moved.Lat, Lon: moved.Lon, Alt: site.Alt}); len(got) != 0 {
		t.Fatalf("emitter should be out of range after the ship moved: %d observations", len(got))
	}
}

func TestPdRoll(t *testing.T) {
	c := cfgWith(at("e", 270, 50000))
	c.Sensor.Pd = 0.3
	s := sensor.New(c)
	hits, n := 0, 5000
	for i := 0; i < n; i++ {
		hits += len(s.Step(t0.Add(time.Duration(i)*time.Second), fixedPose(c)))
	}
	if f := float64(hits) / float64(n); math.Abs(f-0.3) > 0.03 {
		t.Fatalf("detection fraction %.3f, want ~0.3", f)
	}
}

func TestObservationFields(t *testing.T) {
	pow := -62.0
	e := at("radar-1", 270, 50000)
	e.DisplayName = "Surface search radar"
	e.EmitterType, e.Modulation, e.ScanType = "RADAR_SEARCH", "PULSE", "CIRCULAR"
	e.FrequencyHz, e.BandwidthHz, e.PriUs, e.PulseWidthUs, e.PowerDbm = 9.41e9, 20e6, 1000, 0.5, &pow
	e.Affiliation, e.TrackEnvironment = "HOSTILE", "SEA_SURFACE"
	bare := at("bare", 280, 60000)

	c := cfgWith(e, bare)
	obs := sensor.New(c).Step(t0, fixedPose(c))
	if len(obs) != 2 {
		t.Fatalf("got %d observations", len(obs))
	}
	o := obs[0]
	if o.SourceEntityId != "EMITTER-radar-1" || o.EntityType != sentinelv1.EntityType_ENTITY_TYPE_SIGNAL {
		t.Errorf("identity: %q %v", o.SourceEntityId, o.EntityType)
	}
	if o.DisplayName != "Surface search radar" || o.TrackEnvironment != sentinelv1.TrackEnvironment_TRACK_ENVIRONMENT_SEA_SURFACE {
		t.Errorf("display/env: %q %v", o.DisplayName, o.TrackEnvironment)
	}
	if o.Affiliation != sentinelv1.Affiliation_AFFILIATION_HOSTILE {
		t.Errorf("affiliation %v", o.Affiliation)
	}
	rf := o.GetPlatform().GetRf()
	if rf == nil || rf.FrequencyHz != 9.41e9 || rf.EmitterType != "RADAR_SEARCH" || rf.Modulation != "PULSE" || rf.ScanType != "CIRCULAR" {
		t.Fatalf("rf = %v", rf)
	}
	if rf.BandwidthHz == nil || *rf.BandwidthHz != 20e6 || rf.PriUs == nil || *rf.PriUs != 1000 ||
		rf.PulseWidthUs == nil || *rf.PulseWidthUs != 0.5 || rf.PowerDbm == nil || *rf.PowerDbm != -62 {
		t.Errorf("optional rf fields: %v", rf)
	}
	if o.Position == nil || o.Uncertainty == nil {
		t.Fatal("position/uncertainty missing")
	}
	if o.PositionCovariance != nil {
		t.Error("position_covariance must be left for the server to derive")
	}
	if o.SourceTimestamp != nil || o.Classification != sentinelv1.Classification_CLASSIFICATION_UNSPECIFIED {
		t.Error("timestamp and classification must be unset by default")
	}

	b := obs[1]
	if b.DisplayName != "bare" {
		t.Errorf("display name should default to the id, got %q", b.DisplayName)
	}
	if b.TrackEnvironment != sentinelv1.TrackEnvironment_TRACK_ENVIRONMENT_GROUND {
		t.Errorf("default environment %v", b.TrackEnvironment)
	}
	if b.Affiliation != sentinelv1.Affiliation_AFFILIATION_UNSPECIFIED {
		t.Errorf("affiliation should be unset, got %v", b.Affiliation)
	}
	brf := b.GetPlatform().GetRf()
	if brf.BandwidthHz != nil || brf.PriUs != nil || brf.PulseWidthUs != nil || brf.PowerDbm != nil {
		t.Errorf("unset optional rf fields must stay unset: %v", brf)
	}
}

func TestTrackEnvironmentLandMapsToGround(t *testing.T) {
	e := at("e", 270, 50000)
	e.TrackEnvironment = "LAND"
	c := cfgWith(e)
	if got := sensor.New(c).Step(t0, fixedPose(c))[0].TrackEnvironment; got != sentinelv1.TrackEnvironment_TRACK_ENVIRONMENT_GROUND {
		t.Fatalf("LAND -> %v", got)
	}
}

func TestTimestampsOffByDefaultOnWhenAsked(t *testing.T) {
	c := cfgWith(at("e", 270, 50000))
	pose := ownship.Pose{Lat: site.Lat, Lon: site.Lon, Alt: site.Alt, HeadingDeg: 12.5, VelNorth: 3, VelEast: 4}

	s := sensor.New(c)
	if o := s.Step(t0, pose)[0]; o.SourceTimestamp != nil {
		t.Errorf("source_timestamp set by default: %v", o.SourceTimestamp)
	}
	if st := s.EmplacementState("emp-1", t0, pose); st.Time != nil {
		t.Errorf("EmplacementState.time set by default: %v", st.Time)
	}

	c.Sensor.SendTimestamps = true
	s = sensor.New(c)
	if o := s.Step(t0, pose)[0]; o.SourceTimestamp == nil || !o.SourceTimestamp.AsTime().Equal(t0) {
		t.Errorf("source_timestamp = %v, want %v", o.SourceTimestamp, t0)
	}
	if st := s.EmplacementState("emp-1", t0, pose); st.Time == nil || !st.Time.AsTime().Equal(t0) {
		t.Errorf("EmplacementState.time = %v, want %v", st.Time, t0)
	}
}

func TestClassificationStamped(t *testing.T) {
	c := cfgWith(at("e", 270, 50000))
	for name, want := range map[string]sentinelv1.Classification{
		"UNCLASSIFIED": sentinelv1.Classification_CLASSIFICATION_UNCLASSIFIED,
		"CONFIDENTIAL": sentinelv1.Classification_CLASSIFICATION_CONFIDENTIAL,
		"SECRET":       sentinelv1.Classification_CLASSIFICATION_SECRET,
		"TOP_SECRET":   sentinelv1.Classification_CLASSIFICATION_TOP_SECRET,
		"secret":       sentinelv1.Classification_CLASSIFICATION_SECRET,
		"":             sentinelv1.Classification_CLASSIFICATION_UNSPECIFIED,
	} {
		c.Sensor.Classification = name
		if got := sensor.New(c).Step(t0, fixedPose(c))[0].Classification; got != want {
			t.Errorf("%q -> %v, want %v", name, got, want)
		}
	}
}

func TestEmplacementState(t *testing.T) {
	c := cfgWith(at("e", 270, 50000))
	pose := ownship.Pose{Lat: 36.61, Lon: -121.9, Alt: 18, HeadingDeg: 123, VelNorth: -1.5, VelEast: 4.5, VelUp: 0}
	st := sensor.New(c).EmplacementState("emp-1", t0, pose)
	if st.EmplacementId != "emp-1" {
		t.Errorf("id %q", st.EmplacementId)
	}
	if st.Position.GetLatitude() != 36.61 || st.Position.GetLongitude() != -121.9 || st.Position.AltitudeMeters == nil || *st.Position.AltitudeMeters != 18 {
		t.Errorf("position %v", st.Position)
	}
	if st.Heading == nil || *st.Heading != 123 {
		t.Errorf("heading %v", st.Heading)
	}
	if v := st.Velocity; v.GetNorth() != -1.5 || v.GetEast() != 4.5 || v.GetUp() != 0 {
		t.Errorf("velocity %v", v)
	}
}

// The reported ellipse must match the noise actually drawn: about 95% of fixes
// fall inside it, the major axis lies across the line of sight, and it grows
// with range.
func TestErrorEllipseMatchesNoise(t *testing.T) {
	const sigma = 250.0
	for _, rangeM := range []float64{5000, 50000, 100000} {
		c := cfgWith(at("e", 300, rangeM))
		c.Sensor.SigmaM = sigma
		c.Seed = 5
		s := sensor.New(c)
		truth := geo.AERToLLA(site, 300, 0, rangeM)
		_, _, slant := geo.LLAToAER(site, geo.LLA{Lat: truth.Lat, Lon: truth.Lon})

		wantCross := sigma * math.Max(1, slant/1000/10)
		n, inside := 6000, 0
		var sumAlong2, sumCross2 float64
		for i := 0; i < n; i++ {
			o := s.Step(t0.Add(time.Duration(i)*time.Second), fixedPose(c))[0]
			u := o.Uncertainty
			if math.Abs(u.SemiMajor-geo.Ellipse95*wantCross) > 1e-6 || math.Abs(u.SemiMinor-geo.Ellipse95*sigma) > 1e-6 {
				t.Fatalf("range %.0f: ellipse %.3f x %.3f, want %.3f x %.3f", rangeM, u.SemiMajor, u.SemiMinor, geo.Ellipse95*wantCross, geo.Ellipse95*sigma)
			}
			if u.SemiMajor < u.SemiMinor || u.SemiMinor <= 0 {
				t.Fatalf("bad ellipse %+v", u)
			}
			az, _, _ := geo.LLAToAER(site, geo.LLA{Lat: truth.Lat, Lon: truth.Lon, Alt: 0})
			wantOrient := math.Mod(az+90, 180)
			if d := math.Abs(u.Orientation - wantOrient); d > 0.05 {
				t.Fatalf("orientation %.3f, want %.3f", u.Orientation, wantOrient)
			}

			// Offset of the fix from truth in the sensor's ENU frame, rotated into
			// along/cross components.
			enu := geo.ENUOf(site, geo.ToECEF(geo.LLA{Lat: o.Position.Latitude, Lon: o.Position.Longitude, Alt: 0}))
			tru := geo.ENUOf(site, geo.ToECEF(geo.LLA{Lat: truth.Lat, Lon: truth.Lon, Alt: 0}))
			dE, dN := enu.E-tru.E, enu.N-tru.N
			sa, ca := math.Sincos(az * math.Pi / 180)
			along, cross := dE*sa+dN*ca, dE*ca-dN*sa
			sumAlong2 += along * along
			sumCross2 += cross * cross

			x := cross / (u.SemiMajor / geo.Ellipse95)
			y := along / (u.SemiMinor / geo.Ellipse95)
			if x*x+y*y <= geo.Ellipse95*geo.Ellipse95 {
				inside++
			}
		}
		if f := float64(inside) / float64(n); math.Abs(f-0.95) > 0.015 {
			t.Errorf("range %.0f: %.3f of fixes inside the 95%% ellipse", rangeM, f)
		}
		if got := math.Sqrt(sumAlong2 / float64(n)); math.Abs(got-sigma) > 0.05*sigma {
			t.Errorf("range %.0f: along-sigma %.1f, want %.1f", rangeM, got, sigma)
		}
		if got := math.Sqrt(sumCross2 / float64(n)); math.Abs(got-wantCross) > 0.05*wantCross {
			t.Errorf("range %.0f: cross-sigma %.1f, want %.1f", rangeM, got, wantCross)
		}
	}
}

func run(c config.Config, ticks int) [][]byte {
	s := sensor.New(c)
	o := ownship.New(c)
	var out [][]byte
	for i := 0; i < ticks; i++ {
		tt := t0.Add(time.Duration(i) * time.Second)
		for _, ob := range s.Step(tt, o.Pose(tt.Sub(t0))) {
			b, _ := proto.MarshalOptions{Deterministic: true}.Marshal(ob)
			out = append(out, b)
		}
	}
	return out
}

func TestDeterministicForSeed(t *testing.T) {
	c := cfgWith()
	c.EmitterCount = 6
	c.Sensor.Pd = 0.7
	c.Emplacement.Mobility = config.MobilityMobile
	c.Ownship = config.Ownship{Path: config.PathCircle, SpeedKts: 12, RadiusKm: 5}
	a, b := run(c, 120), run(c, 120)
	if len(a) == 0 {
		t.Fatal("no observations")
	}
	if len(a) != len(b) {
		t.Fatalf("lengths differ: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if string(a[i]) != string(b[i]) {
			t.Fatalf("observation %d differs between runs with the same seed", i)
		}
	}
	c.Seed++
	d := run(c, 120)
	same := len(a) == len(d)
	for i := 0; same && i < len(a); i++ {
		same = string(a[i]) == string(d[i])
	}
	if same {
		t.Fatal("a different seed produced identical output")
	}
}

// Golden: seed 42, one static emitter 60 km west of the site, pd 1, sigma 250.
// Positions are compared to 1e-9 degrees (about 0.1 mm) rather than bit for
// bit, so that fused multiply-add differences between architectures pass.
func TestGolden(t *testing.T) {
	c := cfgWith(at("g1", 270, 60000))
	pose := ownship.Pose{Lat: site.Lat, Lon: site.Lon, Alt: site.Alt}
	s := sensor.New(c)

	type golden struct{ lat, lon float64 }
	want := []golden{
		{36.601044761183, -122.620417371639},
		{36.591620865289, -122.619012413304},
		{36.578295919742, -122.619851650129},
	}
	for i, w := range want {
		obs := s.Step(t0.Add(time.Duration(i)*time.Second), pose)
		if len(obs) != 1 {
			t.Fatalf("tick %d: %d observations", i, len(obs))
		}
		o := obs[0]
		if math.Abs(o.Position.Latitude-w.lat) > 1e-9 || math.Abs(o.Position.Longitude-w.lon) > 1e-9 {
			t.Errorf("tick %d: position (%.12f, %.12f), want (%.12f, %.12f)", i, o.Position.Latitude, o.Position.Longitude, w.lat, w.lon)
		}
		u := o.Uncertainty
		if math.Abs(u.SemiMajor-3671.416553529) > 1e-6 || math.Abs(u.SemiMinor-611.925) > 1e-6 || math.Abs(u.Orientation-0) > 1e-6 {
			t.Errorf("tick %d: uncertainty %+v", i, u)
		}
		if o.SourceEntityId != "EMITTER-g1" || o.GetPlatform().GetRf().GetFrequencyHz() != 9.41e9 {
			t.Errorf("tick %d: identity/rf wrong", i)
		}
	}
}

// The effective boresight of a PLATFORM sensor is boresight + heading, so it
// can be anywhere in [0,720). The azimuth offset must wrap for all of them.
func TestCoveredAzimuthOffsetWrapsForAnyBoresight(t *testing.T) {
	tests := []struct {
		name        string
		bore, az    float64
		width       float64
		wantCovered bool
		wantOffset  float64 // documented true offset, for readability
	}{
		{"300+300 az10 offset 130 (narrow: out)", 600, 10, 120, false, 130},
		{"300+300 az10 offset 130 (wide 270: in)", 600, 10, 270, true, 130},
		{"300+300 az10 offset 130 (wide 250: out)", 600, 10, 250, false, 130},
		{"600 az 240 offset 0", 600, 240, 10, true, 0},
		{"719 az 318 offset 41", 719, 318, 90, true, 41},
		{"719 az 318 offset 41 narrow", 719, 318, 80, false, 41},
		{"719 az 355 offset 4", 719, 355, 10, true, 4},
		{"719 az 5 offset 6 (seam)", 719, 5, 20, true, 6},
		{"700 az 0 offset 20 (seam)", 700, 0, 50, true, 20},
		{"700 az 0 offset 20 (seam) narrow", 700, 0, 38, false, 20},
		{"exactly 360 az 0", 360, 0, 10, true, 0},
		{"exactly 360 az 100 offset 100", 360, 100, 200, true, 100},
		{"exactly 360 az 100 offset 100 narrow", 360, 100, 190, false, 100},
		{"exactly 720 az 350 offset 10", 720, 350, 20, true, 10},
		{"exactly 720 az 350 offset 10 narrow", 720, 350, 19, false, 10},
		{"az above bore by more than 180: bore 10 az 300 offset 70", 10, 300, 140, true, 70},
		{"az above bore by more than 180: out", 10, 300, 139, false, 70},
		{"bore 100 az 350 offset 110", 100, 350, 220, true, 110},
		{"bore 100 az 350 offset 110 narrow", 100, 350, 219, false, 110},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			target := geo.AERToLLA(site, tc.az, 0, 40000)
			e := config.Emplacement{Width: tc.width, RangeKm: 150}
			_, _, _, ok := sensor.Covered(site, tc.bore, e, target)
			if ok != tc.wantCovered {
				t.Errorf("bore %v az %v width %v (offset %v): covered=%v, want %v", tc.bore, tc.az, tc.width, tc.wantOffset, ok, tc.wantCovered)
			}
		})
	}
}

func TestPlatformSeamThroughStep(t *testing.T) {
	// boresight 300 + heading 300 = 600 (== 240); a 100-degree sector covers
	// azimuths 190..290 only.
	c := cfgWith(at("sw", 240, 40000), at("north", 10, 40000), at("w", 285, 40000), at("nw", 330, 40000))
	c.Emplacement.Mobility = config.MobilityMobile
	c.Emplacement.AzimuthReference = config.AzimuthPlatform
	c.Emplacement.Boresight, c.Emplacement.Width = 300, 100
	pose := ownship.Pose{Lat: site.Lat, Lon: site.Lon, Alt: site.Alt, HeadingDeg: 300}
	got := detected(sensor.New(c).Step(t0, pose))
	if !got["EMITTER-sw"] || !got["EMITTER-w"] || got["EMITTER-north"] || got["EMITTER-nw"] || len(got) != 2 {
		t.Fatalf("covered = %v, want sw and w only", got)
	}
}

func TestEmplacementStateDeclaresTheHeartbeatInterval(t *testing.T) {
	c := cfgWith(at("e", 270, 50000))
	c.Sensor.HeartbeatInterval = 7 * time.Second
	if st := sensor.New(c).EmplacementState("emp-1", t0, fixedPose(c)); st.HeartbeatIntervalSec != 7 {
		t.Fatalf("heartbeat_interval_sec = %d, want 7", st.HeartbeatIntervalSec)
	}
}
