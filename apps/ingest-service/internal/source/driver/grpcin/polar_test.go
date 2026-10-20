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
