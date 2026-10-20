package grpcin

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/sentinel/ingest-service/internal/emplacement"
	"github.com/sentinel/ingest-service/internal/ingesttoken"
	"github.com/sentinel/ingest-service/internal/obs"
	ingestv1 "github.com/sentinel/proto-gen/sentinel/ingest/v1"
	sentinelv1 "github.com/sentinel/proto-gen/sentinel/v1"
)

func TestBinding(t *testing.T) {
	ok := func(st ingestv1.IngestService_StreamReportsClient, rep *ingestv1.Report) {
		t.Helper()
		if ack := send(t, st, rep); ack.GetStatus() != ingestv1.AckStatus_ACK_STATUS_OK {
			t.Fatalf("%s: ack %v, want OK", rep.GetClientRef(), ack)
		}
	}
	refused := func(st ingestv1.IngestService_StreamReportsClient, rep *ingestv1.Report, reason string) {
		t.Helper()
		if ack := send(t, st, rep); ack.GetReason() != reason {
			t.Fatalf("%s: ack %v, want %s", rep.GetClientRef(), ack, reason)
		}
	}
	tok := ingesttoken.Token{ID: "t1", FeedID: "grpc-1", Classification: "CONFIDENTIAL"}

	t.Run("the only emplacement is the default", func(t *testing.T) {
		emps := &fakeEmplacements{rows: []emplacement.Emplacement{emp("esm", "grpc-1", "FIXED", "RF", "UNCLASSIFIED")}}
		r := start(t, nil, nil, tokensWith(secretA, tok), emps, nil)
		st := openStream(t, r, secretA)
		ok(st, observation("a", 50, -4))
		o := r.sink.emitted()[0]
		if o.EmplacementID != "esm" || o.SensorID != "esm" || o.Source != "SIGINT" {
			t.Errorf("bound observation: emplacement %q sensor %q source %q", o.EmplacementID, o.SensorID, o.Source)
		}
	})
	t.Run("none bound: unbound reports are taken", func(t *testing.T) {
		r := start(t, nil, nil, tokensWith(secretA, tok), &fakeEmplacements{}, nil)
		st := openStream(t, r, secretA)
		ok(st, observation("a", 50, -4))
		if o := r.sink.emitted()[0]; o.EmplacementID != "" || o.SensorID != "127.0.0.1" {
			t.Errorf("unbound: emplacement %q sensor %q", o.EmplacementID, o.SensorID)
		}
	})
	t.Run("claims", func(t *testing.T) {
		inactive := emp("off", "grpc-1", "FIXED", "RF", "UNCLASSIFIED")
		inactive.Active = false
		emps := &fakeEmplacements{rows: []emplacement.Emplacement{
			emp("one", "grpc-1", "FIXED", "RF", "UNCLASSIFIED"),
			emp("two", "grpc-1", "FIXED", "RADAR", "UNCLASSIFIED"),
			emp("secret", "grpc-1", "FIXED", "RF", "SECRET"),
			emp("theirs", "grpc-2", "FIXED", "RF", "UNCLASSIFIED"),
			inactive,
		}}
		r := start(t, nil, nil, tokensWith(secretA, tok), emps, nil)
		st := openStream(t, r, secretA)
		refused(st, observation("none claimed of two", 50, -4), "unknown_emplacement")
		refused(st, claiming(observation("another feed's", 50, -4), "theirs"), "unknown_emplacement")
		refused(st, claiming(observation("above the token", 50, -4), "secret"), "unknown_emplacement")
		refused(st, claiming(observation("inactive", 50, -4), "off"), "unknown_emplacement")
		refused(st, claiming(observation("no such", 50, -4), "nope"), "unknown_emplacement")
		ok(st, claiming(observation("target", 50, -4), "two"))
		if o := r.sink.emitted()[0]; o.EmplacementID != "two" || o.Source != "RADAR" {
			t.Errorf("claimed: %q %q", o.EmplacementID, o.Source)
		}
	})
}

// The emplacement's level joins the connection's and the report's.
func TestTheEmplacementsLevelCounts(t *testing.T) {
	conf := emp("conf", "grpc-1", "FIXED", "RF", "CONFIDENTIAL")
	conf.Exercise = true
	emps := &fakeEmplacements{rows: []emplacement.Emplacement{conf}}
	r := start(t, nil, nil, tokensWith(secretA, ingesttoken.Token{ID: "t1", FeedID: "grpc-1", Classification: "CONFIDENTIAL"}), emps, nil)
	st := openStream(t, r, secretA)
	if ack := send(t, st, observation("a", 50, -4)); ack.GetStatus() != ingestv1.AckStatus_ACK_STATUS_OK {
		t.Fatal(ack)
	}
	o := r.sink.emitted()[0]
	if o.Classification != "CONFIDENTIAL" || !o.Exercise {
		t.Errorf("classification %q exercise %v, want CONFIDENTIAL and true", o.Classification, o.Exercise)
	}
}

func TestTargetsNeverCarryTheEmplacementIdentity(t *testing.T) {
	emps := &fakeEmplacements{rows: []emplacement.Emplacement{emp("esm", "grpc-1", "FIXED", "RF", "UNCLASSIFIED")}}
	r := start(t, nil, nil, tokensWith(secretA, ingesttoken.Token{ID: "t1", FeedID: "grpc-1"}), emps, nil)
	st := openStream(t, r, secretA)
	emplacementID := &sentinelv1.Identity{Type: sentinelv1.IdentityType_IDENTITY_TYPE_EMPLACEMENT, Value: "esm"}
	mmsi := func(v string) *sentinelv1.Identity {
		return &sentinelv1.Identity{Type: sentinelv1.IdentityType_IDENTITY_TYPE_MMSI, Value: v}
	}

	rep := observation("EMITTER-1", 50, -4)
	rep.GetObservation().Identities = []*sentinelv1.Identity{emplacementID, mmsi("235000000")}
	if ack := send(t, st, rep); ack.GetStatus() != ingestv1.AckStatus_ACK_STATUS_OK {
		t.Fatalf("ack %v, want OK", ack)
	}
	o := r.sink.emitted()[0]
	var kept []obs.Identity
	for _, id := range o.Identities {
		if id.Type == obs.IdentityEmplacement {
			t.Fatalf("a target kept identity %v", id)
		}
		kept = append(kept, id)
	}
	if len(kept) != 1 || kept[0].Type != obs.IdentityMMSI || kept[0].Value != "235000000" {
		t.Errorf("identities = %v, want only MMSI:235000000", kept)
	}

	// No source_entity_id: the target is keyed on its first real identity,
	// never on the emplacement's.
	keyless := &ingestv1.Report{ClientRef: "keyless", Body: &ingestv1.Report_Observation{Observation: &sentinelv1.Observation{
		Position:   &sentinelv1.Coordinate{Latitude: 50, Longitude: -4},
		Identities: []*sentinelv1.Identity{emplacementID, mmsi("235000001")},
	}}}
	if ack := send(t, st, keyless); ack.GetStatus() != ingestv1.AckStatus_ACK_STATUS_OK {
		t.Fatalf("ack %v, want OK", ack)
	}
	if got := r.sink.emitted()[1].SourceEntityID; got != "MMSI:235000001" {
		t.Errorf("SourceEntityID = %q, want MMSI:235000001", got)
	}

	// Only the emplacement's identity: nothing to key on.
	only := &ingestv1.Report{ClientRef: "only", Body: &ingestv1.Report_Observation{Observation: &sentinelv1.Observation{
		Position:   &sentinelv1.Coordinate{Latitude: 50, Longitude: -4},
		Identities: []*sentinelv1.Identity{emplacementID},
	}}}
	ack := send(t, st, only)
	if ack.GetStatus() != ingestv1.AckStatus_ACK_STATUS_DEAD_LETTERED || ack.GetReason() != "missing_identity" {
		t.Errorf("ack %v, want DEAD_LETTERED missing_identity", ack)
	}
	if n := len(r.sink.emitted()); n != 2 {
		t.Errorf("%d observations emitted, want 2", n)
	}
}

// A target keyed on its emplacement's id would share the ownship SENSOR
// observation's repeat key and observation ids.
func TestATargetKeyedOnItsEmplacementIsRefused(t *testing.T) {
	emps := &fakeEmplacements{rows: []emplacement.Emplacement{emp("esm", "grpc-1", "FIXED", "RF", "UNCLASSIFIED")}}
	r := start(t, nil, nil, tokensWith(secretA, ingesttoken.Token{ID: "t1", FeedID: "grpc-1"}), emps, nil)
	st := openStream(t, r, secretA)
	ack := send(t, st, observation("esm", 50, -4))
	if ack.GetStatus() != ingestv1.AckStatus_ACK_STATUS_REJECTED || ack.GetReason() != "bad_identity" {
		t.Errorf("ack %v, want REJECTED bad_identity", ack)
	}
	if n := len(r.sink.emitted()); n != 0 {
		t.Errorf("%d observations emitted, want 0", n)
	}
}

// A malformed report that the emplacement's level lifts above the
// connection's is refused, never dead-lettered (the C1 R8 rule).
func TestAMalformedReportOfAHigherEmplacementIsNotDeadLettered(t *testing.T) {
	tok := ingesttoken.Token{ID: "t1", FeedID: "grpc-1", Classification: "CONFIDENTIAL"}
	noID := func() *ingestv1.Report {
		return &ingestv1.Report{ClientRef: "no id", Body: &ingestv1.Report_Observation{Observation: &sentinelv1.Observation{
			Position: &sentinelv1.Coordinate{Latitude: 50, Longitude: -4},
		}}}
	}
	t.Run("above the connection", func(t *testing.T) {
		emps := &fakeEmplacements{rows: []emplacement.Emplacement{emp("conf", "grpc-1", "FIXED", "RF", "CONFIDENTIAL")}}
		r := start(t, nil, nil, tokensWith(secretA, tok), emps, nil)
		ack := send(t, openStream(t, r, secretA), noID())
		if ack.GetStatus() != ingestv1.AckStatus_ACK_STATUS_REJECTED || ack.GetReason() != "missing_identity" {
			t.Errorf("ack %v, want REJECTED missing_identity", ack)
		}
		if n := len(r.sink.deadLetters()); n != 0 {
			t.Errorf("%d dead letters, want 0", n)
		}
	})
	t.Run("at the connection", func(t *testing.T) {
		emps := &fakeEmplacements{rows: []emplacement.Emplacement{emp("low", "grpc-1", "FIXED", "RF", "UNCLASSIFIED")}}
		r := start(t, nil, nil, tokensWith(secretA, tok), emps, nil)
		ack := send(t, openStream(t, r, secretA), noID())
		if ack.GetStatus() != ingestv1.AckStatus_ACK_STATUS_DEAD_LETTERED || ack.GetReason() != "missing_identity" {
			t.Errorf("ack %v, want DEAD_LETTERED missing_identity", ack)
		}
		if n := len(r.sink.deadLetters()); n != 1 {
			t.Errorf("%d dead letters, want 1", n)
		}
	})
}

// A nil observation is only reachable in-process, so this calls observation
// directly. Like any other report of the emplacement, it is not stored as a
// dead letter when the emplacement is above the connection's level.
func TestAnEmptyReportOfAHigherEmplacement(t *testing.T) {
	tok := ingesttoken.Token{ID: "t1", FeedID: "grpc-1", Classification: "CONFIDENTIAL"}
	emps := &fakeEmplacements{rows: []emplacement.Emplacement{
		emp("low", "grpc-1", "FIXED", "RF", "UNCLASSIFIED"),
		emp("conf", "grpc-1", "FIXED", "RADAR", "CONFIDENTIAL"),
	}}
	r := start(t, nil, nil, tokensWith(secretA, tok), emps, nil)
	s := newService(r.d, r.sink, context.Background())
	check := func(claim string, status ingestv1.AckStatus, reason string, letters int) {
		t.Helper()
		rep := &ingestv1.Report{ClientRef: "n", EmplacementId: claim}
		ack, err := s.observation(context.Background(), authed{tok: tok}, "127.0.0.1:9", rep, nil, &ingestv1.Ack{ClientRef: "n"})
		if err != nil || ack.GetStatus() != status || ack.GetReason() != reason {
			t.Errorf("claim %q: ack %v err %v, want %v %s", claim, ack, err, status, reason)
		}
		if n := len(r.sink.deadLetters()); n != letters {
			t.Errorf("claim %q: %d dead letters, want %d", claim, n, letters)
		}
	}
	check("conf", ingestv1.AckStatus_ACK_STATUS_REJECTED, "empty_report", 0)
	check("nope", ingestv1.AckStatus_ACK_STATUS_REJECTED, "unknown_emplacement", 0)
	check("low", ingestv1.AckStatus_ACK_STATUS_DEAD_LETTERED, "empty_report", 1)
}

func TestReportsTouchTheirEmplacementAtMostEveryTenSeconds(t *testing.T) {
	emps := &fakeEmplacements{rows: []emplacement.Emplacement{emp("esm", "grpc-1", "FIXED", "RF", "UNCLASSIFIED")}}
	w := &fakeWriter{emps: emps}
	r := startWith(t, nil, nil, tokensWith(secretA, ingesttoken.Token{ID: "t1", FeedID: "grpc-1"}), emps, w, nil, nil)
	st := openStream(t, r, secretA)
	for i := 0; i < 3; i++ {
		send(t, st, observation(fmt.Sprint("a", i), 50, -4))
	}
	if n := len(w.touches()); n != 1 {
		t.Fatalf("touches = %d, want 1", n)
	}
	if c := w.touches()[0]; c.id != "esm" || c.pos != nil {
		t.Errorf("touch = %+v, want esm with no position", c)
	}
}

func TestTouchThrottlesEachEmplacementOnItsOwn(t *testing.T) {
	emps := &fakeEmplacements{}
	w := &fakeWriter{emps: emps}
	r := startWith(t, nil, nil, nil, emps, w, nil, nil)
	ctx := context.Background()
	t0 := time.Unix(1700000000, 0).UTC()
	ids := func() []string {
		var out []string
		for _, c := range w.touches() {
			out = append(out, c.id)
		}
		return out
	}
	r.d.touch(ctx, "a", nil, nil, t0)
	r.d.touch(ctx, "a", nil, nil, t0.Add(touchEvery-time.Second)) // throttled
	r.d.touch(ctx, "b", nil, nil, t0.Add(time.Second))            // its own clock
	r.d.touch(ctx, "b", nil, nil, t0.Add(2*time.Second))          // throttled
	if got := ids(); len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("touches = %v, want [a b]", got)
	}
	r.d.touch(ctx, "a", nil, nil, t0.Add(touchEvery)) // due again
	if got := ids(); len(got) != 3 || got[2] != "a" {
		t.Fatalf("touches = %v, want [a b a]", got)
	}
	r.d.touch(ctx, "b", nil, nil, t0.Add(touchEvery)) // 9 s after its last
	if n := len(w.touches()); n != 3 {
		t.Errorf("b was touched again after %v, touches = %d", touchEvery-time.Second, n)
	}
	r.d.touch(ctx, "b", nil, nil, t0.Add(touchEvery+time.Second))
	if n := len(w.touches()); n != 4 {
		t.Errorf("touches = %d, want 4 once b's interval has passed", n)
	}
}
