package grpcin

import (
	"math"
	"sync"
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

// testClock is a server clock the test moves by hand.
type testClock struct {
	mu sync.Mutex
	t  time.Time
}

func newClock() *testClock { return &testClock{t: time.Unix(1700000000, 0).UTC()} }

func (c *testClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *testClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func (c *testClock) tweak(d *Driver) { d.now = c.now }

func sensorsOf(r *rig) []*obs.Observation {
	var out []*obs.Observation
	for _, o := range r.sink.emitted() {
		if o.EntityType == obs.EntitySensor {
			out = append(out, o)
		}
	}
	return out
}

// shipRig runs a driver with one mobile emplacement and a hand-moved clock.
func shipRig(t *testing.T, cfg map[string]any, tok ingesttoken.Token, ships ...emplacement.Emplacement) (*rig, *fakeWriter, *testClock, ingestv1.IngestService_StreamReportsClient) {
	t.Helper()
	emps := &fakeEmplacements{rows: ships}
	w := &fakeWriter{emps: emps}
	clk := newClock()
	tok.ID, tok.FeedID = "t1", "grpc-1"
	r := startWith(t, cfg, nil, tokensWith(secretA, tok), emps, w, nil, clk.tweak)
	return r, w, clk, openStream(t, r, secretA)
}

// A mobile emplacement's own report moves its SENSOR entity: one observation
// a second at most, fused with the mirror's through EMPLACEMENT:<id>, and its
// position written through Touch at most every 10 s.
func TestStateEmitsTheSensorAndTouches(t *testing.T) {
	r, w, clk, st := shipRig(t, nil, ingesttoken.Token{}, emp("ship", "grpc-1", "MOBILE", "RF", "UNCLASSIFIED"))
	now := clk.now()
	send(t, st, stateReport("ship", now, 50, -4, ptr(45.0)))
	send(t, st, stateReport("ship", now.Add(100*time.Millisecond), 50.001, -4, ptr(45.0)))
	sensors := sensorsOf(r)
	if len(sensors) != 1 {
		t.Fatalf("%d sensor observations within a second, want 1", len(sensors))
	}
	o := sensors[0]
	if o.EmplacementID != "ship" || o.Identities[0].Type != obs.IdentityEmplacement || o.Identities[0].Value != "ship" ||
		o.Heading == nil || *o.Heading != 45 || o.Latitude != 50 {
		t.Errorf("sensor observation %+v", o)
	}
	if c := w.touches(); len(c) != 1 || c[0].pos == nil || c[0].pos.Lat != 50 {
		t.Fatalf("touches %+v, want one with the position", c)
	}
	clk.advance(sensorEvery)
	send(t, st, stateReport("ship", clk.now(), 50.002, -4, ptr(45.0)))
	if n := len(sensorsOf(r)); n != 2 {
		t.Errorf("%d sensor observations a second later, want 2", n)
	}
	if n := len(w.touches()); n != 1 {
		t.Errorf("%d touches 1 s later, want still 1", n)
	}
	clk.advance(touchEvery)
	send(t, st, stateReport("ship", clk.now(), 50.003, -4, ptr(45.0)))
	if c := w.touches(); len(c) != 2 || c[1].pos.Lat != 50.003 {
		t.Errorf("touches %+v, want a second one after %v", c, touchEvery)
	}
}

// The throttles run on the server's clock: a sender whose clock is hours off
// neither floods the writes nor stalls them, and Touch records its fix time.
func TestStateThrottlesIgnoreTheSendersClock(t *testing.T) {
	r, w, clk, st := shipRig(t, nil, ingesttoken.Token{}, emp("ship", "grpc-1", "MOBILE", "RF", "UNCLASSIFIED"))
	first := clk.now().Add(-5 * time.Hour)
	for i := range 5 {
		send(t, st, stateReport("ship", first.Add(time.Duration(i)*time.Hour), 50, -4, nil))
	}
	c := w.touches()
	if len(c) != 1 || !c[0].at.Equal(first) {
		t.Fatalf("touches %+v, want one carrying the first fix time %v", c, first)
	}
	if n := len(sensorsOf(r)); n != 1 {
		t.Errorf("%d sensor observations, want 1", n)
	}
	clk.advance(touchEvery)
	send(t, st, stateReport("ship", first, 50, -4, nil))
	if n := len(w.touches()); n != 2 {
		t.Errorf("%d touches once the server's interval has passed, want 2", n)
	}
	if n := len(sensorsOf(r)); n != 2 {
		t.Errorf("%d sensor observations, want 2", n)
	}
}

// A fix stamped ahead of the server is clamped for the write, and one far
// ahead is refused: it would push the track's window past every real fix.
func TestStateRefusesTheFarFutureAndKeepsTheTrackUsable(t *testing.T) {
	ship := emp("ship", "grpc-1", "MOBILE", "RF", "UNCLASSIFIED")
	ship.AzimuthReference = emplacement.AzimuthPlatform
	r, w, clk, st := shipRig(t, nil, ingesttoken.Token{}, ship)
	now := clk.now()
	if ack := send(t, st, stateReport("ship", now.AddDate(1, 0, 0), 50, -4, ptr(90.0))); ack.GetStatus() != ingestv1.AckStatus_ACK_STATUS_DEAD_LETTERED || ack.GetReason() != "bad_state" {
		t.Fatalf("+1 year: %v", ack)
	}
	invalid := stateReport("ship", now, 50, -4, nil)
	invalid.GetEmplacementState().Time = &timestamppb.Timestamp{Seconds: -1 << 40}
	if ack := send(t, st, invalid); ack.GetReason() != "bad_state" {
		t.Errorf("an invalid timestamp: %v", ack)
	}
	if n := len(w.touches()); n != 0 {
		t.Errorf("%d touches after refused reports", n)
	}
	// A little ahead is fine, and the write carries the server's time.
	if ack := send(t, st, stateReport("ship", now.Add(maxClockLead/2), 50, -4, ptr(90.0))); ack.GetStatus() != ingestv1.AckStatus_ACK_STATUS_OK {
		t.Fatalf("slightly ahead: %v", ack)
	}
	if c := w.touches(); len(c) != 1 || !c[0].at.Equal(now) {
		t.Errorf("touches %+v, want one at the server's time %v", c, now)
	}
	if sensors := sensorsOf(r); len(sensors) != 1 || !sensors[0].SourceTimestamp.Equal(now) {
		t.Errorf("sensor observations %+v, want one stamped no later than the server's time %v", sensors, now)
	}
	rep := polarReport("target", 0, 1000, ptr(0.0))
	rep.GetObservation().SourceTimestamp = timestamppb.New(now.Add(maxClockLead / 2))
	if ack := send(t, st, rep); ack.GetStatus() != ingestv1.AckStatus_ACK_STATUS_OK {
		t.Errorf("a polar after the refused fixes: %v", ack)
	}
}

func TestATrackIsBounded(t *testing.T) {
	t0 := time.Unix(1700000000, 0)
	var tr track
	total := 2*maxFixes + 5
	for i := range total {
		tr.add(fix{at: t0.Add(time.Duration(i) * time.Millisecond), pos: geo.LLA{Lat: float64(i) / 1e6}})
		if len(tr.fixes) > 2*maxFixes {
			t.Fatalf("%d fixes after %d adds, want at most %d", len(tr.fixes), i+1, 2*maxFixes)
		}
	}
	// Compacted to the newest maxFixes when it reached 2*maxFixes, then five more.
	if len(tr.fixes) != maxFixes+5 {
		t.Fatalf("%d fixes, want %d", len(tr.fixes), maxFixes+5)
	}
	if want := total - len(tr.fixes); !tr.fixes[0].at.Equal(t0.Add(time.Duration(want) * time.Millisecond)) {
		t.Errorf("oldest kept at %v, want the %dth", tr.fixes[0].at.Sub(t0), want)
	}
	if _, ok := tr.at(t0, time.Second); ok {
		t.Error("a dropped fix's time should be stale")
	}
}

func TestStateAcceptsASmallClockLeadOnly(t *testing.T) {
	_, w, clk, st := shipRig(t, nil, ingesttoken.Token{}, emp("ship", "grpc-1", "MOBILE", "RF", "UNCLASSIFIED"))
	now := clk.now()
	if ack := send(t, st, stateReport("ship", now.Add(6*time.Second), 50, -4, nil)); ack.GetReason() != "bad_state" {
		t.Errorf("6 s ahead: %v", ack)
	}
	if ack := send(t, st, stateReport("ship", now.Add(4*time.Second), 50, -4, nil)); ack.GetStatus() != ingestv1.AckStatus_ACK_STATUS_OK {
		t.Errorf("4 s ahead: %v", ack)
	}
	if c := w.touches(); len(c) != 1 || !c[0].at.Equal(now) {
		t.Errorf("touches %+v, want one clamped to the server's time", c)
	}
}

func TestReadingAnOwnshipNeverMakesATrack(t *testing.T) {
	r := start(t, nil, nil, tokensWith(secretA, ingesttoken.Token{ID: "t1", FeedID: "grpc-1"}), &fakeEmplacements{}, nil)
	if _, ok := r.d.originAt("ghost", time.Now()); ok {
		t.Fatal("no fixes, yet an origin")
	}
	if n := len(r.d.tracks); n != 0 {
		t.Errorf("%d tracks after a read", n)
	}
}

func TestOwnshipExtrapolatesEast(t *testing.T) {
	t0 := time.Unix(1700000000, 0)
	var tr track
	tr.add(fix{at: t0, pos: geo.LLA{Lat: 50, Lon: -4}, vel: &[3]float64{0, 10, 0}}) // 10 m/s east
	p, ok := tr.at(t0.Add(time.Second), 2*time.Second)
	if !ok {
		t.Fatal("within tolerance")
	}
	_, _, rng := geo.LLAToAER(geo.LLA{Lat: 50, Lon: -4}, p.at)
	if math.Abs(rng-10) > 0.01 || p.at.Lon <= -4 || math.Abs(p.at.Lat-50) > 1e-8 {
		t.Errorf("moved %v m to %v, want 10 m east", rng, p.at)
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

func TestStateAboveTheTokenIsRefused(t *testing.T) {
	ship := emp("ship", "grpc-1", "MOBILE", "RF", "UNCLASSIFIED")
	f := tokensWith(secretA, ingesttoken.Token{ID: "t1", FeedID: "grpc-1", Classification: "UNCLASSIFIED"})
	r := start(t, map[string]any{"classification": "SECRET"}, nil, f, &fakeEmplacements{rows: []emplacement.Emplacement{ship}}, nil)
	st := openStream(t, r, secretA)
	if ack := send(t, st, stateReport("ship", time.Now(), 50, -4, nil)); ack.GetStatus() != ingestv1.AckStatus_ACK_STATUS_REJECTED || ack.GetReason() != "above_ceiling" {
		t.Errorf("a feed above the token: %v", ack)
	}
	if n := len(r.sink.emitted()) + len(r.sink.deadLetters()); n != 0 {
		t.Errorf("%d things stored", n)
	}
}

// A malformed state of an emplacement above the connection's level is
// refused, not dead-lettered: dead letters are visible at the connection's.
func TestMalformedStateAboveTheConnectionIsRejectedNotStored(t *testing.T) {
	ship := emp("ship", "grpc-1", "MOBILE", "RF", "CONFIDENTIAL")
	f := tokensWith(secretA, ingesttoken.Token{ID: "t1", FeedID: "grpc-1", Classification: "CONFIDENTIAL"})
	r := start(t, nil, nil, f, &fakeEmplacements{rows: []emplacement.Emplacement{ship}}, nil)
	st := openStream(t, r, secretA)
	if ack := send(t, st, stateReport("ship", time.Now(), 95, -4, nil)); ack.GetStatus() != ingestv1.AckStatus_ACK_STATUS_REJECTED || ack.GetReason() != "bad_state" {
		t.Errorf("latitude 95: %v", ack)
	}
	if n := len(r.sink.deadLetters()); n != 0 {
		t.Errorf("%d dead letters, want none", n)
	}
}

func TestStateRefusesNonFiniteHeadingAndVelocity(t *testing.T) {
	r, w, clk, st := shipRig(t, nil, ingesttoken.Token{}, emp("ship", "grpc-1", "MOBILE", "RF", "UNCLASSIFIED"))
	now := clk.now()
	for name, heading := range map[string]float64{"NaN": math.NaN(), "+Inf": math.Inf(1)} {
		if ack := send(t, st, stateReport("ship", now, 50, -4, ptr(heading))); ack.GetReason() != "bad_state" {
			t.Errorf("heading %s: %v", name, ack)
		}
	}
	for _, v := range []*sentinelv1.GeodeticVelocity{{North: math.NaN()}, {East: math.Inf(-1)}, {Up: math.Inf(1)}} {
		rep := stateReport("ship", now, 50, -4, nil)
		rep.GetEmplacementState().Velocity = v
		if ack := send(t, st, rep); ack.GetReason() != "bad_state" {
			t.Errorf("velocity %v: %v", v, ack)
		}
	}
	if n := len(w.touches()) + len(sensorsOf(r)); n != 0 {
		t.Errorf("%d writes or observations after malformed states", n)
	}
}

// The body alone may name the emplacement, and a state with no time is
// stamped with the server's.
func TestStateNamedByItsBodyAndStampedByTheServer(t *testing.T) {
	a := emp("ship-a", "grpc-1", "MOBILE", "RF", "UNCLASSIFIED")
	b := emp("ship-b", "grpc-1", "MOBILE", "RF", "UNCLASSIFIED")
	r, w, clk, st := shipRig(t, nil, ingesttoken.Token{}, a, b)
	rep := stateReport("", time.Time{}, 50, -4, nil)
	rep.GetEmplacementState().EmplacementId = "ship-b"
	rep.GetEmplacementState().Time = nil
	if ack := send(t, st, rep); ack.GetStatus() != ingestv1.AckStatus_ACK_STATUS_OK {
		t.Fatalf("body-only id: %v", ack)
	}
	sensors := sensorsOf(r)
	if len(sensors) != 1 || sensors[0].EmplacementID != "ship-b" || !sensors[0].SourceTimestamp.Equal(clk.now()) {
		t.Errorf("sensors %+v, want ship-b stamped %v", sensors, clk.now())
	}
	if c := w.touches(); len(c) != 1 || c[0].id != "ship-b" || !c[0].at.Equal(clk.now()) {
		t.Errorf("touches %+v", c)
	}
	// With no claim at all and two emplacements, there is no telling which.
	none := stateReport("", clk.now(), 50, -4, nil)
	if ack := send(t, st, none); ack.GetReason() != "unknown_emplacement" {
		t.Errorf("no id with two emplacements: %v", ack)
	}
}
