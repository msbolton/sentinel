package grpcin

import (
	"math"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/sentinel/go-common/geo"
	"github.com/sentinel/ingest-service/internal/emplacement"
	"github.com/sentinel/ingest-service/internal/ingesttoken"
	"github.com/sentinel/ingest-service/internal/obs"
	ingestv1 "github.com/sentinel/proto-gen/sentinel/ingest/v1"
	sentinelv1 "github.com/sentinel/proto-gen/sentinel/v1"
)

func TestOwnshipTrack(t *testing.T) {
	t0 := time.Unix(1700000000, 0)
	var tr track
	// Out of order: the 2 s fix arrives before the 0 s one.
	tr.add(fix{at: t0.Add(2 * time.Second), pos: geo.LLA{Lat: 50.2, Lon: 179.9}, heading: ptr(350.0)})
	tr.add(fix{at: t0, pos: geo.LLA{Lat: 50.0, Lon: 179.7}, heading: ptr(10.0)})
	mid, ok := tr.at(t0.Add(time.Second), 2*time.Second)
	if !ok || math.Abs(mid.at.Lat-50.1) > 1e-9 || math.Abs(mid.at.Lon-179.8) > 1e-9 {
		t.Fatalf("midpoint %+v, %v", mid, ok)
	}
	if mid.heading == nil || math.Abs(*mid.heading) > 1e-9 { // 10° and 350° meet at 0°
		t.Errorf("heading %v, want 0", mid.heading)
	}
	if _, ok := tr.at(t0.Add(-time.Second), 2*time.Second); ok {
		t.Error("before the first fix should be stale")
	}
	if _, ok := tr.at(t0.Add(5*time.Second), 2*time.Second); ok {
		t.Error("3 s past the newest with a 2 s tolerance should be stale")
	}
	if held, ok := tr.at(t0.Add(3*time.Second), 2*time.Second); !ok || held.at.Lat != 50.2 {
		t.Errorf("within tolerance, no velocity: %+v %v, want the newest position held", held, ok)
	}
	// Across the antimeridian.
	tr.add(fix{at: t0.Add(4 * time.Second), pos: geo.LLA{Lat: 50.2, Lon: -179.9}})
	if p, ok := tr.at(t0.Add(3*time.Second), 2*time.Second); !ok || math.Abs(math.Abs(p.at.Lon)-180) > 1e-9 {
		t.Errorf("antimeridian midpoint lon %v", p.at.Lon)
	}
	// Older than the window is forgotten.
	tr.add(fix{at: t0.Add(2 * ownshipWindow), pos: geo.LLA{Lat: 51}})
	if _, ok := tr.at(t0.Add(time.Second), 2*time.Second); ok {
		t.Error("a fix older than the window should be gone")
	}
}

func TestOwnshipExtrapolatesAlongItsVelocity(t *testing.T) {
	t0 := time.Unix(1700000000, 0)
	var tr track
	tr.add(fix{at: t0, pos: geo.LLA{Lat: 50, Lon: -4}, vel: &[3]float64{10, 0, 0}}) // 10 m/s north
	p, ok := tr.at(t0.Add(time.Second), 2*time.Second)
	if !ok {
		t.Fatal("within tolerance")
	}
	_, _, rng := geo.LLAToAER(geo.LLA{Lat: 50, Lon: -4}, p.at)
	if math.Abs(rng-10) > 0.01 || p.at.Lat <= 50 {
		t.Errorf("moved %v m to %v, want 10 m north", rng, p.at)
	}
}

func stateReport(id string, at time.Time, lat, lon float64, heading *float64) *ingestv1.Report {
	return &ingestv1.Report{ClientRef: "state", EmplacementId: id, Body: &ingestv1.Report_EmplacementState{EmplacementState: &sentinelv1.EmplacementState{
		Time: timestamppb.New(at), Position: &sentinelv1.Coordinate{Latitude: lat, Longitude: lon}, Heading: heading,
	}}}
}

func TestMobilePolarNeedsOwnship(t *testing.T) {
	ship := emp("ship", "grpc-1", "MOBILE", "RF", "UNCLASSIFIED")
	ship.AzimuthReference = emplacement.AzimuthPlatform
	r := start(t, nil, nil, tokensWith(secretA, ingesttoken.Token{ID: "t1", FeedID: "grpc-1"}), &fakeEmplacements{rows: []emplacement.Emplacement{ship}}, nil)
	st := openStream(t, r, secretA)
	now := time.Now()
	if ack := send(t, st, polarReport("early", 0, 1000, ptr(0.0))); ack.GetReason() != "stale_ownship" {
		t.Fatalf("before any ownship: %v", ack)
	}
	if ack := send(t, st, stateReport("ship", now, 50, -4, ptr(90.0))); ack.GetStatus() != ingestv1.AckStatus_ACK_STATUS_OK {
		t.Fatalf("state: %v", ack)
	}
	rep := polarReport("bearing 0 relative", 0, 1000, ptr(0.0))
	rep.GetObservation().SourceTimestamp = timestamppb.New(now)
	if ack := send(t, st, rep); ack.GetStatus() != ingestv1.AckStatus_ACK_STATUS_OK {
		t.Fatalf("with ownship: %v", ack)
	}
	var target *obs.Observation
	for _, o := range r.sink.emitted() {
		if o.SourceEntityID == "bearing 0 relative" {
			target = o
		}
	}
	want := geo.AERToLLA(geo.LLA{Lat: 50, Lon: -4}, 90, 0, 1000) // relative 0 + heading 90
	if target == nil || math.Abs(target.Latitude-want.Lat) > 1e-9 || math.Abs(target.Longitude-want.Lon) > 1e-9 {
		t.Errorf("target %+v, want %v", target, want)
	}
	late := polarReport("late", 0, 1000, ptr(0.0))
	late.GetObservation().SourceTimestamp = timestamppb.New(now.Add(5 * time.Second))
	if ack := send(t, st, late); ack.GetReason() != "stale_ownship" {
		t.Errorf("5 s past the newest fix: %v", ack)
	}
}

// A mobile emplacement's own report moves its SENSOR entity: one observation
// a second at most, fused with the mirror's through EMPLACEMENT:<id>, and its
// position written through Touch at most every 10 s.
func TestStateEmitsTheSensorAndTouches(t *testing.T) {
	ship := emp("ship", "grpc-1", "MOBILE", "RF", "UNCLASSIFIED")
	emps := &fakeEmplacements{rows: []emplacement.Emplacement{ship}}
	w := &fakeWriter{emps: emps}
	r := startWith(t, nil, nil, tokensWith(secretA, ingesttoken.Token{ID: "t1", FeedID: "grpc-1"}), emps, w, nil, nil)
	st := openStream(t, r, secretA)
	now := time.Now()
	send(t, st, stateReport("ship", now, 50, -4, ptr(45.0)))
	send(t, st, stateReport("ship", now.Add(100*time.Millisecond), 50.001, -4, ptr(45.0)))
	sensors := 0
	for _, o := range r.sink.emitted() {
		if o.EntityType == obs.EntitySensor {
			sensors++
			if o.EmplacementID != "ship" || o.Identities[0].Type != obs.IdentityEmplacement || o.Identities[0].Value != "ship" ||
				o.Heading == nil || *o.Heading != 45 || o.Latitude != 50 {
				t.Errorf("sensor observation %+v", o)
			}
		}
	}
	if sensors != 1 {
		t.Errorf("%d sensor observations within a second, want 1", sensors)
	}
	if c := w.touches(); len(c) != 1 || c[0].pos == nil || c[0].pos.Lat != 50 {
		t.Errorf("touches %+v, want one with the position", c)
	}
}

// The throttles run on the server's clock: a sender whose clock is days off
// neither floods the writes nor stalls them, and Touch records its fix time.
func TestStateThrottlesIgnoreTheSendersClock(t *testing.T) {
	ship := emp("ship", "grpc-1", "MOBILE", "RF", "UNCLASSIFIED")
	emps := &fakeEmplacements{rows: []emplacement.Emplacement{ship}}
	w := &fakeWriter{emps: emps}
	r := startWith(t, nil, nil, tokensWith(secretA, ingesttoken.Token{ID: "t1", FeedID: "grpc-1"}), emps, w, nil, nil)
	st := openStream(t, r, secretA)
	future := time.Now().Add(72 * time.Hour)
	for i := range 5 {
		send(t, st, stateReport("ship", future.Add(time.Duration(i)*time.Hour), 50, -4, nil))
	}
	c := w.touches()
	if len(c) != 1 || c[0].at.Sub(future).Abs() > time.Millisecond {
		t.Errorf("touches %+v, want one carrying the first fix time", c)
	}
	if n := len(r.sink.emitted()); n != 1 {
		t.Errorf("%d sensor observations, want 1", n)
	}
}

func TestStateRules(t *testing.T) {
	fixed := emp("radar", "grpc-1", "FIXED", "RADAR", "UNCLASSIFIED")
	ship := emp("ship", "grpc-1", "MOBILE", "RF", "UNCLASSIFIED")
	r := start(t, nil, nil, tokensWith(secretA, ingesttoken.Token{ID: "t1", FeedID: "grpc-1"}), &fakeEmplacements{rows: []emplacement.Emplacement{fixed, ship}}, nil)
	st := openStream(t, r, secretA)
	now := time.Now()
	if ack := send(t, st, stateReport("radar", now, 50, -4, nil)); ack.GetReason() != "unknown_emplacement" {
		t.Errorf("a fixed emplacement's state: %v", ack)
	}
	mismatch := stateReport("ship", now, 50, -4, nil)
	mismatch.GetEmplacementState().EmplacementId = "radar"
	if ack := send(t, st, mismatch); ack.GetReason() != "unknown_emplacement" {
		t.Errorf("body and report disagree: %v", ack)
	}
	if ack := send(t, st, stateReport("ship", now, 95, -4, nil)); ack.GetStatus() != ingestv1.AckStatus_ACK_STATUS_DEAD_LETTERED || ack.GetReason() != "bad_state" {
		t.Errorf("latitude 95: %v", ack)
	}
	noPos := stateReport("ship", now, 0, 0, nil)
	noPos.GetEmplacementState().Position = nil
	if ack := send(t, st, noPos); ack.GetReason() != "bad_state" {
		t.Errorf("no position: %v", ack)
	}
}
