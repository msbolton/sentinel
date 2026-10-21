package grpcin

import (
	"math"
	"testing"

	"github.com/sentinel/go-common/geo"
	"github.com/sentinel/ingest-service/internal/emplacement"
	"github.com/sentinel/ingest-service/internal/ingesttoken"
	ingestv1 "github.com/sentinel/proto-gen/sentinel/ingest/v1"
	sentinelv1 "github.com/sentinel/proto-gen/sentinel/v1"
)

func polarReport(id string, az, rng float64, el *float64) *ingestv1.Report {
	return &ingestv1.Report{ClientRef: id, Body: &ingestv1.Report_Observation{Observation: &sentinelv1.Observation{
		SourceEntityId: id, Polar: &sentinelv1.PolarMeasurement{RangeM: rng, AzimuthDeg: az, ElevationDeg: el},
	}}}
}

func TestPolarFromAFixedEmplacement(t *testing.T) {
	e := emp("radar", "grpc-1", "FIXED", "RADAR", "UNCLASSIFIED")
	e.DefaultSigma = emplacement.Sigma{RangeM: ptr(50.0), AzimuthDeg: ptr(1.0)}
	r := start(t, nil, nil, tokensWith(secretA, ingesttoken.Token{ID: "t1", FeedID: "grpc-1"}), &fakeEmplacements{rows: []emplacement.Emplacement{e}}, nil)
	st := openStream(t, r, secretA)
	if ack := send(t, st, polarReport("p1", 90, 10000, ptr(0.0))); ack.GetStatus() != ingestv1.AckStatus_ACK_STATUS_OK {
		t.Fatal(ack)
	}
	o := r.sink.emitted()[0]
	want := geo.AERToLLA(geo.LLA{Lat: 50.1, Lon: -4.2}, 90, 0, 10000)
	if !o.HasPosition || math.Abs(o.Latitude-want.Lat) > 1e-9 || math.Abs(o.Longitude-want.Lon) > 1e-9 {
		t.Errorf("position %v,%v want %v,%v", o.Latitude, o.Longitude, want.Lat, want.Lon)
	}
	if o.PosCovariance == nil || o.Polar == nil {
		t.Errorf("covariance %v polar %v: both should be kept", o.PosCovariance, o.Polar)
	}
	// With no elevation, the target is at the emplacement's altitude.
	send(t, st, polarReport("p2", 0, 5000, nil))
	if o2 := r.sink.emitted()[1]; o2.Altitude == nil || math.Abs(*o2.Altitude) > 0.5 {
		t.Errorf("altitude %v, want about 0", o2.Altitude)
	}
}

// A report with a position keeps it; its polar is kept for audit.
func TestAPositionWinsOverPolar(t *testing.T) {
	r := start(t, nil, nil, tokensWith(secretA, ingesttoken.Token{ID: "t1", FeedID: "grpc-1"}), &fakeEmplacements{rows: []emplacement.Emplacement{emp("radar", "grpc-1", "FIXED", "RADAR", "UNCLASSIFIED")}}, nil)
	st := openStream(t, r, secretA)
	rep := polarReport("p", 90, 10000, ptr(0.0))
	rep.GetObservation().Position = &sentinelv1.Coordinate{Latitude: 1, Longitude: 2}
	send(t, st, rep)
	if o := r.sink.emitted()[0]; o.Latitude != 1 || o.Longitude != 2 {
		t.Errorf("position %v,%v, want the sender's", o.Latitude, o.Longitude)
	}
}

func TestBadPolarIsRefused(t *testing.T) {
	r := start(t, nil, nil, tokensWith(secretA, ingesttoken.Token{ID: "t1", FeedID: "grpc-1"}), &fakeEmplacements{rows: []emplacement.Emplacement{emp("radar", "grpc-1", "FIXED", "RADAR", "UNCLASSIFIED")}}, nil)
	st := openStream(t, r, secretA)
	for _, rep := range []*ingestv1.Report{
		polarReport("nan range", 90, math.NaN(), nil),
		polarReport("inf range", 90, math.Inf(1), nil),
		polarReport("negative range", 90, -1, nil),
		polarReport("nan azimuth", math.NaN(), 1000, nil),
		polarReport("elevation 95", 90, 1000, ptr(95.0)),
	} {
		if ack := send(t, st, rep); ack.GetReason() != "bad_polar" {
			t.Errorf("%s: %v, want bad_polar", rep.GetClientRef(), ack)
		}
	}
	bad := polarReport("zero sigma", 90, 1000, nil)
	bad.GetObservation().Polar.Sigma = &sentinelv1.PolarSigma{RangeM: ptr(0.0)}
	if ack := send(t, st, bad); ack.GetReason() != "bad_polar" {
		t.Errorf("zero sigma: %v", ack)
	}
	if n := len(r.sink.emitted()); n != 0 {
		t.Errorf("%d observations emitted", n)
	}
}

func TestPolarNeedsAnEmplacement(t *testing.T) {
	r := start(t, nil, nil, tokensWith(secretA, ingesttoken.Token{ID: "t1", FeedID: "grpc-1"}), &fakeEmplacements{}, nil)
	st := openStream(t, r, secretA)
	if ack := send(t, st, polarReport("p", 90, 1000, nil)); ack.GetReason() != "unknown_emplacement" {
		t.Errorf("%v, want unknown_emplacement", ack)
	}
}

func TestEnforceCoverage(t *testing.T) {
	e := emp("radar", "grpc-1", "FIXED", "RADAR", "UNCLASSIFIED")
	e.BoresightDeg = 90
	e.Coverage = emplacement.Coverage{MinRangeM: 100, MaxRangeM: 20000, AzimuthWidthDeg: 60}
	r := start(t, map[string]any{"enforceCoverage": true}, nil, tokensWith(secretA, ingesttoken.Token{ID: "t1", FeedID: "grpc-1"}), &fakeEmplacements{rows: []emplacement.Emplacement{e}}, nil)
	st := openStream(t, r, secretA)
	for _, tc := range []struct {
		rep  *ingestv1.Report
		want string
	}{
		{polarReport("inside", 100, 10000, ptr(0.0)), ""},
		{polarReport("behind", 270, 10000, ptr(0.0)), "outside_coverage"},
		{polarReport("too far", 90, 30000, ptr(0.0)), "outside_coverage"},
		{polarReport("too near", 90, 50, ptr(0.0)), "outside_coverage"},
	} {
		if ack := send(t, st, tc.rep); ack.GetReason() != tc.want {
			t.Errorf("%s: %v, want %q", tc.rep.GetClientRef(), ack, tc.want)
		}
	}
	// A positional report is held to the coverage too.
	far := claiming(observation("far away", 10, 10), "radar")
	if ack := send(t, st, far); ack.GetReason() != "outside_coverage" {
		t.Errorf("positional: %v", ack)
	}
}

func TestTrueAzimuth(t *testing.T) {
	for _, tc := range []struct {
		az      float64
		ref     string
		heading *float64
		want    float64
		ok      bool
	}{
		{370, emplacement.AzimuthTrueNorth, nil, 10, true},
		{-10, emplacement.AzimuthTrueNorth, nil, 350, true},
		{30, emplacement.AzimuthPlatform, ptr(340.0), 10, true},
		{30, emplacement.AzimuthPlatform, nil, 0, false},
	} {
		got, ok := trueAzimuth(tc.az, tc.ref, tc.heading)
		if ok != tc.ok || (ok && math.Abs(got-tc.want) > 1e-9) {
			t.Errorf("trueAzimuth(%v, %s, %v) = %v, %v", tc.az, tc.ref, tc.heading, got, ok)
		}
	}
}

// A refused report is not the emplacement reporting.
func TestRefusedReportsDoNotTouch(t *testing.T) {
	e := emp("radar", "grpc-1", "FIXED", "RADAR", "UNCLASSIFIED")
	e.Coverage = emplacement.Coverage{MaxRangeM: 1000}
	emps := &fakeEmplacements{rows: []emplacement.Emplacement{e}}
	w := &fakeWriter{emps: emps}
	r := startWith(t, map[string]any{"enforceCoverage": true}, nil, tokensWith(secretA, ingesttoken.Token{ID: "t1", FeedID: "grpc-1"}), emps, w, nil, nil)
	st := openStream(t, r, secretA)
	if ack := send(t, st, polarReport("far", 90, 30000, ptr(0.0))); ack.GetReason() != "outside_coverage" {
		t.Fatal(ack)
	}
	if n := len(w.touches()); n != 0 {
		t.Fatalf("%d touches after a refused report", n)
	}
	if ack := send(t, st, polarReport("near", 90, 500, ptr(0.0))); ack.GetStatus() != ingestv1.AckStatus_ACK_STATUS_OK {
		t.Fatal(ack)
	}
	if n := len(w.touches()); n != 1 {
		t.Fatalf("%d touches after an accepted report, want 1", n)
	}
}

func polarRig(t *testing.T, cfg map[string]any, e emplacement.Emplacement) (*rig, ingestv1.IngestService_StreamReportsClient) {
	t.Helper()
	r := start(t, cfg, nil, tokensWith(secretA, ingesttoken.Token{ID: "t1", FeedID: "grpc-1"}), &fakeEmplacements{rows: []emplacement.Emplacement{e}}, nil)
	return r, openStream(t, r, secretA)
}

func TestPolarNeverYieldsNonFiniteValues(t *testing.T) {
	r, st := polarRig(t, nil, emp("radar", "grpc-1", "FIXED", "RADAR", "UNCLASSIFIED"))
	if ack := send(t, st, polarReport("too far", 90, 3e7, nil)); ack.GetReason() != "bad_polar" {
		t.Errorf("range 3e7: %v, want bad_polar", ack)
	}
	huge := polarReport("huge sigma", 90, 1000, nil)
	huge.GetObservation().Polar.Sigma = &sentinelv1.PolarSigma{RangeM: ptr(1e200)}
	if ack := send(t, st, huge); ack.GetStatus() != ingestv1.AckStatus_ACK_STATUS_OK {
		t.Fatalf("huge sigma: %v", ack)
	}
	o := r.sink.emitted()[0]
	if !o.HasPosition || o.PosCovariance != nil {
		t.Errorf("position %v covariance %v: want a position and no covariance", o.HasPosition, o.PosCovariance)
	}
}

func TestAltitudeOnlyPolarIsPlacedAtThatAltitude(t *testing.T) {
	r, st := polarRig(t, nil, emp("radar", "grpc-1", "FIXED", "RADAR", "UNCLASSIFIED"))
	rep := polarReport("high", 90, 10000, nil)
	rep.GetObservation().Position = &sentinelv1.Coordinate{AltitudeMeters: ptr(3000.0)}
	if ack := send(t, st, rep); ack.GetStatus() != ingestv1.AckStatus_ACK_STATUS_OK {
		t.Fatal(ack)
	}
	o := r.sink.emitted()[0]
	if !o.HasPosition || o.Latitude == 0 || o.Altitude == nil || math.Abs(*o.Altitude-3000) > 1 {
		t.Errorf("position %v,%v altitude %v: want a point about 3000 m up from the emplacement", o.Latitude, o.Longitude, o.Altitude)
	}
}

func TestPolarCovarianceOrientation(t *testing.T) {
	e := emp("radar", "grpc-1", "FIXED", "RADAR", "UNCLASSIFIED")
	e.DefaultSigma = emplacement.Sigma{RangeM: ptr(50.0), AzimuthDeg: ptr(1.0)}
	r, st := polarRig(t, nil, e)
	only := func(id string, az float64, s *sentinelv1.PolarSigma) *[6]float64 {
		rep := polarReport(id, az, 10000, ptr(0.0))
		rep.GetObservation().Polar.Sigma = s
		if ack := send(t, st, rep); ack.GetStatus() != ingestv1.AckStatus_ACK_STATUS_OK {
			t.Fatal(ack)
		}
		em := r.sink.emitted()
		return em[len(em)-1].PosCovariance
	}
	tiny := 1e-6
	// Range sigma only, due east: the error is east-west. [nn ne nu ee eu uu]
	c := only("range", 90, &sentinelv1.PolarSigma{RangeM: ptr(50.0), AzimuthDeg: &tiny, ElevationDeg: &tiny})
	if c == nil || math.Abs(c[3]-2500) > 5 || math.Abs(c[0]) > 1 {
		t.Errorf("range sigma at az 90: %v, want ee about 2500 and nn about 0", c)
	}
	// Azimuth sigma only, due east: the error is north-south.
	want := math.Pow(10000*math.Pi/180, 2)
	c = only("azimuth", 90, &sentinelv1.PolarSigma{RangeM: &tiny, AzimuthDeg: ptr(1.0), ElevationDeg: &tiny})
	if c == nil || math.Abs(c[0]-want) > want*0.01 {
		t.Errorf("azimuth sigma at az 90: %v, want nn about %v", c, want)
	}
	// A sent sigma wins over the default (50 m, 1 degree) on its own axis;
	// the unsent axes keep the default.
	c = only("sent wins", 90, &sentinelv1.PolarSigma{RangeM: ptr(10.0)})
	if c == nil || math.Abs(c[3]-100) > 5 || math.Abs(c[0]-want) > want*0.01 {
		t.Errorf("sent range sigma 10, default azimuth 1: %v, want ee about 100 and nn about %v", c, want)
	}
}

func TestCoverageAcrossTheNorthSeam(t *testing.T) {
	e := emp("radar", "grpc-1", "FIXED", "RADAR", "UNCLASSIFIED")
	e.BoresightDeg = 350
	e.Coverage = emplacement.Coverage{MaxRangeM: 20000, AzimuthWidthDeg: 40}
	_, st := polarRig(t, map[string]any{"enforceCoverage": true}, e)
	for _, tc := range []struct {
		az   float64
		want string
	}{{5, ""}, {335, ""}, {30, "outside_coverage"}, {320, "outside_coverage"}} {
		if ack := send(t, st, polarReport("az", tc.az, 10000, ptr(0.0))); ack.GetReason() != tc.want {
			t.Errorf("azimuth %v: %v, want %q", tc.az, ack, tc.want)
		}
	}
}

func TestUnboundPolarWithAPositionKeepsIt(t *testing.T) {
	r := start(t, nil, nil, tokensWith(secretA, ingesttoken.Token{ID: "t1", FeedID: "grpc-1"}), &fakeEmplacements{}, nil)
	st := openStream(t, r, secretA)
	rep := polarReport("p", 90, 1000, nil)
	rep.GetObservation().Position = &sentinelv1.Coordinate{Latitude: 1, Longitude: 2}
	if ack := send(t, st, rep); ack.GetStatus() != ingestv1.AckStatus_ACK_STATUS_OK {
		t.Fatal(ack)
	}
	if o := r.sink.emitted()[0]; o.Latitude != 1 || o.Longitude != 2 || o.Polar == nil {
		t.Errorf("position %v,%v polar %v", o.Latitude, o.Longitude, o.Polar)
	}
}

func TestUnboundAltitudeOnlyPolarIsRefused(t *testing.T) {
	r := start(t, nil, nil, tokensWith(secretA, ingesttoken.Token{ID: "t1", FeedID: "grpc-1"}), &fakeEmplacements{}, nil)
	st := openStream(t, r, secretA)
	rep := polarReport("p", 90, 1000, nil)
	rep.GetObservation().Position = &sentinelv1.Coordinate{AltitudeMeters: ptr(3000.0)}
	if ack := send(t, st, rep); ack.GetReason() != "unknown_emplacement" {
		t.Errorf("%v, want unknown_emplacement", ack)
	}
}
