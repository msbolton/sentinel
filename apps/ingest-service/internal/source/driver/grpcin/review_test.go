package grpcin

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/sentinel/ingest-service/internal/emplacement"
	"github.com/sentinel/ingest-service/internal/ingesttoken"
	ingestv1 "github.com/sentinel/proto-gen/sentinel/ingest/v1"
	sentinelv1 "github.com/sentinel/proto-gen/sentinel/v1"
)

const (
	acked    = ingestv1.AckStatus_ACK_STATUS_OK
	rejected = ingestv1.AckStatus_ACK_STATUS_REJECTED
)

// A sender streaming observations and states: the observations' touches must
// not use up the slot the states' positions need.
func TestPositionTouchesHaveTheirOwnThrottle(t *testing.T) {
	_, w, clk, st := shipRig(t, nil, ingesttoken.Token{}, emp("ship", "grpc-1", "MOBILE", "RF", "UNCLASSIFIED"))
	const step = 3 * time.Second
	for range 20 { // a minute
		if ack := send(t, st, claiming(observation("o", 50, -4), "ship")); ack.GetStatus() != acked {
			t.Fatalf("observation: %v", ack)
		}
		if ack := send(t, st, stateReport("ship", clk.now(), 50, -4, nil)); ack.GetStatus() != acked {
			t.Fatalf("state: %v", ack)
		}
		clk.advance(step)
	}
	var positions []time.Time
	for _, c := range w.touches() {
		if c.pos != nil {
			positions = append(positions, c.at)
		}
	}
	if len(positions) < 5 {
		t.Fatalf("%d position touches in a minute, want one about every %v", len(positions), touchEvery)
	}
	for i := 1; i < len(positions); i++ {
		if gap := positions[i].Sub(positions[i-1]); gap < touchEvery || gap > touchEvery+step {
			t.Errorf("position touches %d and %d are %v apart, want %v to %v", i-1, i, gap, touchEvery, touchEvery+step)
		}
	}
}

// Only a connection with no emplacements at all takes reports unbound: one
// whose emplacements are all unusable to this token refuses them.
func TestUnboundOnlyWhenTheConnectionHasNoEmplacements(t *testing.T) {
	off := emp("off", "grpc-1", "FIXED", "RF", "UNCLASSIFIED")
	off.Active = false
	high := emp("high", "grpc-1", "FIXED", "RF", "SECRET")
	for _, tc := range []struct {
		name   string
		rows   []emplacement.Emplacement
		status ingestv1.AckStatus
		reason string
	}{
		{"inactive only", []emplacement.Emplacement{off}, rejected, "unknown_emplacement"},
		{"above the token only", []emplacement.Emplacement{high}, rejected, "unknown_emplacement"},
		{"another connection's only", []emplacement.Emplacement{emp("theirs", "grpc-2", "FIXED", "RF", "UNCLASSIFIED")}, acked, ""},
		{"none", nil, acked, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tok := ingesttoken.Token{ID: "t1", FeedID: "grpc-1", Classification: "CONFIDENTIAL"}
			r := start(t, nil, nil, tokensWith(secretA, tok), &fakeEmplacements{rows: tc.rows}, nil)
			st := openStream(t, r, secretA)
			if ack := send(t, st, observation("a", 50, -4)); ack.GetStatus() != tc.status || ack.GetReason() != tc.reason {
				t.Fatalf("ack %v, want %v %q", ack, tc.status, tc.reason)
			}
			if n := len(r.sink.deadLetters()); n != 0 {
				t.Errorf("%d dead letters", n)
			}
		})
	}
}

func TestClockSkewIsRefusedNotDeadLettered(t *testing.T) {
	r, w, clk, st := shipRig(t, nil, ingesttoken.Token{}, emp("ship", "grpc-1", "MOBILE", "RF", "UNCLASSIFIED"))
	now := clk.now()
	for name, at := range map[string]time.Time{
		"6 s ahead":    now.Add(6 * time.Second),
		"61 s behind":  now.Add(-61 * time.Second),
		"a day behind": now.Add(-24 * time.Hour),
	} {
		if ack := send(t, st, stateReport("ship", at, 50, -4, nil)); ack.GetStatus() != rejected || ack.GetReason() != "clock_skew" {
			t.Errorf("%s: %v, want REJECTED clock_skew", name, ack)
		}
	}
	if n := len(r.sink.deadLetters()); n != 0 {
		t.Errorf("%d dead letters, want none", n)
	}
	if n := len(w.touches()); n != 0 {
		t.Errorf("%d touches after refused states", n)
	}
	// The edges of the window are in it.
	for name, at := range map[string]time.Time{"5 s ahead": now.Add(maxClockLead), "60 s behind": now.Add(-ownshipWindow)} {
		if ack := send(t, st, stateReport("ship", at, 50, -4, nil)); ack.GetStatus() != acked {
			t.Errorf("%s: %v, want OK", name, ack)
		}
	}
	// A malformed report is still bad_state, whatever its time.
	bad := stateReport("ship", now.Add(-24*time.Hour), 95, -4, nil)
	if ack := send(t, st, bad); ack.GetReason() != "bad_state" {
		t.Errorf("a bad position at a skewed time: %v, want bad_state", ack)
	}
}

// A token below the connection never writes a dead letter, whatever it sends.
func TestATokenBelowTheConnectionIsRefusedForEveryBody(t *testing.T) {
	ship := emp("ship", "grpc-1", "MOBILE", "RF", "CONFIDENTIAL")
	r := start(t, map[string]any{"classification": "CONFIDENTIAL"}, nil,
		tokensWith(secretA, ingesttoken.Token{ID: "t1", FeedID: "grpc-1", Classification: "UNCLASSIFIED"}),
		&fakeEmplacements{rows: []emplacement.Emplacement{ship}}, nil)
	st := openStream(t, r, secretA)
	for name, rep := range map[string]*ingestv1.Report{
		"no body":     {ClientRef: "n"},
		"observation": observation("a", 50, -4),
		"empty obs":   {Body: &ingestv1.Report_Observation{Observation: &sentinelv1.Observation{}}},
		"state":       stateReport("ship", time.Now(), 50, -4, nil),
		"bad state":   stateReport("ship", time.Now(), 95, -4, nil),
	} {
		if ack := send(t, st, rep); ack.GetStatus() != rejected || ack.GetReason() != "above_ceiling" {
			t.Errorf("%s: %v, want REJECTED above_ceiling", name, ack)
		}
	}
	if n := len(r.sink.deadLetters()); n != 0 {
		t.Errorf("%d dead letters, want none", n)
	}
}

func TestUpsertByNameIsIdempotent(t *testing.T) {
	operator := emp("op-1", "grpc-1", "FIXED", "RADAR", "UNCLASSIFIED")
	operator.Name, operator.CreatedBy = "Operator Radar", "u1"
	high := senderRow("high-1")
	high.Name, high.Classification = "Hidden", "SECRET"
	r, w, emps := consentRig(t, map[string]any{"maxEmplacements": 4}, ingesttoken.Token{Classification: "CONFIDENTIAL"}, operator, high)
	ctx, cancel := withToken(secretA)
	defer cancel()
	c := r.client(t)

	first, err := c.UpsertEmplacement(ctx, upsertReq("", "Ship ESM"))
	if err != nil {
		t.Fatal(err)
	}
	id := first.GetEmplacement().GetId()
	req := upsertReq("", "  ship esm ")
	req.Emplacement.Description = "aft mast"
	second, err := c.UpsertEmplacement(ctx, req)
	if err != nil {
		t.Fatalf("a second upsert by name: %v", err)
	}
	if second.GetEmplacement().GetId() != id || second.GetEmplacement().GetDescription() != "aft mast" {
		t.Errorf("second upsert %v, want an update of %s", second.GetEmplacement(), id)
	}
	if e, _ := w.Get(context.Background(), id); e.Version != 2 {
		t.Errorf("version %d, want 2", e.Version)
	}
	// An id that names no row falls back to the name too.
	if res, err := c.UpsertEmplacement(ctx, upsertReq("no-such-id", "Ship ESM")); err != nil || res.GetEmplacement().GetId() != id {
		t.Errorf("unknown id with a known name: %v %v", res, err)
	}
	// high-1 is SECRET, above this token, and the operator's row is not a
	// sender's: both names are taken. So is another connection's.
	theirs := senderRow("their-1")
	theirs.Name = "Elsewhere"
	other := "grpc-2"
	theirs.FeedID = &other
	emps.put(theirs)
	for _, name := range []string{"Operator Radar", "Hidden", "Elsewhere"} {
		if _, err := c.UpsertEmplacement(ctx, upsertReq("", name)); status.Code(err) != codes.AlreadyExists {
			t.Errorf("%q: %v, want AlreadyExists", name, err)
		}
	}
	// An update by name is not a new row: it fits when the cap is reached
	// (Ship ESM, Hidden, Second, Third).
	for _, name := range []string{"Second", "Third"} {
		if _, err := c.UpsertEmplacement(ctx, upsertReq("", name)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := c.UpsertEmplacement(ctx, upsertReq("", "Fourth")); status.Code(err) != codes.ResourceExhausted {
		t.Errorf("over the cap: %v, want ResourceExhausted", err)
	}
	if res, err := c.UpsertEmplacement(ctx, upsertReq("", "Ship ESM")); err != nil || res.GetEmplacement().GetId() != id {
		t.Errorf("at the cap, an update by name: %v %v", res, err)
	}
}

func TestUpsertIsRateLimited(t *testing.T) {
	for name, cfg := range map[string]map[string]any{
		"per sender": {"maxMessagesPerSecPerSender": 1},
		"shared":     {"maxMessagesPerSec": 1},
	} {
		t.Run(name, func(t *testing.T) {
			r, _, _ := consentRig(t, cfg, ingesttoken.Token{})
			ctx, cancel := withToken(secretA)
			defer cancel()
			c := r.client(t)
			if _, err := c.UpsertEmplacement(ctx, upsertReq("", "First")); err != nil {
				t.Fatal(err)
			}
			_, err := c.UpsertEmplacement(ctx, upsertReq("", "Second"))
			if status.Code(err) != codes.ResourceExhausted || status.Convert(err).Message() != "rate limited" {
				t.Fatalf("%v, want ResourceExhausted rate limited", err)
			}
		})
	}
}

// A sender may set the exercise flag but never clear it.
func TestUpsertExerciseIsSticky(t *testing.T) {
	r, w, _ := consentRig(t, nil, ingesttoken.Token{}, senderRow("mine-1"))
	ctx, cancel := withToken(secretA)
	defer cancel()
	c := r.client(t)
	exercise := func(on bool) bool {
		t.Helper()
		req := upsertReq("mine-1", "mine-1")
		req.Emplacement.Exercise = on
		res, err := c.UpsertEmplacement(ctx, req)
		if err != nil {
			t.Fatal(err)
		}
		e, _ := w.Get(context.Background(), "mine-1")
		if res.GetEmplacement().GetExercise() != e.Exercise {
			t.Errorf("response exercise %v, stored %v", res.GetEmplacement().GetExercise(), e.Exercise)
		}
		return e.Exercise
	}
	if exercise(false) {
		t.Error("an update without the flag set it")
	}
	if !exercise(true) {
		t.Error("an update with the flag did not set it")
	}
	if !exercise(false) {
		t.Error("an update without the flag cleared it")
	}
	// A new emplacement takes the flag as sent.
	req := upsertReq("", "New")
	req.Emplacement.Exercise = true
	res, err := c.UpsertEmplacement(ctx, req)
	if err != nil || !res.GetEmplacement().GetExercise() {
		t.Errorf("create with exercise: %v %v", res, err)
	}
}

// A write the cache is slow to show is saved, not failed: the sender retries
// and the retry (by name) updates the same row.
func TestUpsertAfterASlowCacheIsUnavailableAndRetriable(t *testing.T) {
	emps := &fakeEmplacements{}
	w := &fakeWriter{emps: emps, lag: true}
	tok := ingesttoken.Token{ID: "t1", FeedID: "grpc-1"}
	r := startWith(t, map[string]any{"senderManagedEmplacements": true}, nil, tokensWith(secretA, tok), emps, w, nil,
		func(d *Driver) { d.cacheWait = 100 * time.Millisecond })
	ctx, cancel := withToken(secretA)
	defer cancel()
	c := r.client(t)
	_, err := c.UpsertEmplacement(ctx, upsertReq("", "Ship ESM"))
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("%v, want Unavailable", err)
	}
	rows, _ := w.List(context.Background(), emplacement.Filter{FeedID: "grpc-1"})
	if len(rows) != 1 {
		t.Fatalf("%d rows after the slow write, want it saved", len(rows))
	}
	id := rows[0].ID
	w.lag = false
	res, err := c.UpsertEmplacement(ctx, upsertReq("", "Ship ESM"))
	if err != nil || res.GetEmplacement().GetId() != id {
		t.Fatalf("retry %v %v, want an update of %s", res, err, id)
	}
	if rows, _ := w.List(context.Background(), emplacement.Filter{FeedID: "grpc-1"}); len(rows) != 1 {
		t.Errorf("%d rows after the retry, want 1", len(rows))
	}
}

// The cache keys are lower-case ids; senders may print them any way.
func TestClaimsIgnoreCaseAndSpace(t *testing.T) {
	ship := emp("ship-1", "grpc-1", "MOBILE", "RF", "UNCLASSIFIED")
	other := emp("other-1", "grpc-1", "FIXED", "RADAR", "UNCLASSIFIED")
	r, w, clk, st := shipRig(t, nil, ingesttoken.Token{}, ship, other)
	if ack := send(t, st, claiming(observation("a", 50, -4), " Other-1 ")); ack.GetStatus() != acked {
		t.Errorf("observation claim: %v", ack)
	} else if o := r.sink.emitted()[0]; o.EmplacementID != "other-1" {
		t.Errorf("bound to %q", o.EmplacementID)
	}
	if ack := send(t, st, stateReport("SHIP-1", clk.now(), 50, -4, nil)); ack.GetStatus() != acked {
		t.Errorf("state claim in the report: %v", ack)
	}
	body := stateReport("", clk.now(), 50, -4, nil)
	body.GetEmplacementState().EmplacementId = " Ship-1"
	if ack := send(t, st, body); ack.GetStatus() != acked {
		t.Errorf("state claim in the body: %v", ack)
	}
	both := stateReport("SHIP-1", clk.now(), 50, -4, nil)
	both.GetEmplacementState().EmplacementId = "ship-1 "
	if ack := send(t, st, both); ack.GetStatus() != acked {
		t.Errorf("the same claim twice, in different case: %v", ack)
	}
	if c := w.touches(); len(c) == 0 || c[0].id != "other-1" {
		t.Errorf("touches %+v", c)
	}
}

// The observation path runs on the driver's clock.
func TestObservationsUseTheDriversClock(t *testing.T) {
	r, w, clk, st := shipRig(t, nil, ingesttoken.Token{}, emp("esm", "grpc-1", "FIXED", "RF", "UNCLASSIFIED"))
	clk.advance(48 * time.Hour)
	send(t, st, observation("a", 50, -4))
	if o := r.sink.emitted()[0]; !o.ReceivedAt.Equal(clk.now()) {
		t.Errorf("ReceivedAt %v, want %v", o.ReceivedAt, clk.now())
	}
	if c := w.touches(); len(c) != 1 || !c[0].at.Equal(clk.now()) {
		t.Errorf("touches %+v, want one at %v", c, clk.now())
	}
}

// On the wire an unset sub-message and an empty one are the same: neither is
// an empty_report (only a report with no body is).
func TestEmptyBodiesOnTheWire(t *testing.T) {
	r, _, _, st := shipRig(t, nil, ingesttoken.Token{}, emp("ship", "grpc-1", "MOBILE", "RF", "UNCLASSIFIED"))
	empty := &ingestv1.Report{EmplacementId: "ship", Body: &ingestv1.Report_EmplacementState{EmplacementState: &sentinelv1.EmplacementState{}}}
	if ack := send(t, st, empty); ack.GetStatus() != ingestv1.AckStatus_ACK_STATUS_DEAD_LETTERED || ack.GetReason() != "bad_state" {
		t.Errorf("empty emplacement_state: %v", ack)
	}
	noObs := &ingestv1.Report{EmplacementId: "ship", Body: &ingestv1.Report_Observation{Observation: &sentinelv1.Observation{}}}
	if ack := send(t, st, noObs); ack.GetStatus() != ingestv1.AckStatus_ACK_STATUS_DEAD_LETTERED || ack.GetReason() != "missing_identity" {
		t.Errorf("empty observation: %v", ack)
	}
	if ack := send(t, st, &ingestv1.Report{}); ack.GetReason() != "empty_report" {
		t.Errorf("no body: %v", ack)
	}
	if n := len(r.sink.deadLetters()); n != 3 {
		t.Errorf("%d dead letters, want 3", n)
	}
}

// An exercise connection's emplacements are exercise: an upsert that asks for
// exercise=false, on create or on update, stores true, so the row matches the
// SENSOR entity the connection reports. A connection that is not exercise takes
// the flag as sent.
func TestUpsertOnAnExerciseConnectionStoresExercise(t *testing.T) {
	r, w, _ := consentRig(t, map[string]any{"exercise": true}, ingesttoken.Token{}, senderRow("mine-1"))
	ctx, cancel := withToken(secretA)
	defer cancel()
	c := r.client(t)

	res, err := c.UpsertEmplacement(ctx, upsertReq("mine-1", "mine-1")) // update, exercise=false
	if err != nil || !res.GetEmplacement().GetExercise() {
		t.Fatalf("update: %v %v", res, err)
	}
	if e, _ := w.Get(context.Background(), "mine-1"); !e.Exercise {
		t.Error("an update without the flag, on an exercise connection, stored false")
	}
	res, err = c.UpsertEmplacement(ctx, upsertReq("", "New")) // create, exercise=false
	if err != nil || !res.GetEmplacement().GetExercise() {
		t.Fatalf("create: %v %v", res, err)
	}
	if e, _ := w.Get(context.Background(), res.GetEmplacement().GetId()); !e.Exercise {
		t.Error("a create without the flag, on an exercise connection, stored false")
	}
}

// The session and the list tell a sender what the connection reports: an
// emplacement of an exercise connection is exercise even when an operator
// wrote its row without the flag. A connection that is not exercise reports
// the row as it is.
func TestSessionReportsEmplacementsOfAnExerciseConnectionAsExercise(t *testing.T) {
	feed := "grpc-1"
	row := func(id string, exercise bool) emplacement.Emplacement {
		e := emp(id, feed, "FIXED", "RF", "UNCLASSIFIED")
		e.Exercise = exercise
		return e
	}
	for _, tc := range []struct {
		name     string
		cfg      map[string]any
		wantReal bool
	}{{"exercise connection", map[string]any{"exercise": true}, false}, {"real connection", nil, true}} {
		t.Run(tc.name, func(t *testing.T) {
			emps := &fakeEmplacements{rows: []emplacement.Emplacement{row("plain", false), row("marked", true)}}
			r := start(t, tc.cfg, nil, tokensWith(secretA, ingesttoken.Token{ID: "t1", FeedID: feed}), emps, nil)
			ctx, cancel := withToken(secretA)
			defer cancel()
			s, err := r.client(t).GetSession(ctx, &ingestv1.GetSessionRequest{})
			if err != nil {
				t.Fatal(err)
			}
			l, err := r.client(t).ListEmplacements(ctx, &ingestv1.ListEmplacementsRequest{})
			if err != nil {
				t.Fatal(err)
			}
			for where, list := range map[string][]*sentinelv1.Emplacement{"session": s.GetEmplacements(), "list": l.GetEmplacements()} {
				if len(list) != 2 {
					t.Fatalf("%s lists %d emplacements, want 2", where, len(list))
				}
				for _, e := range list {
					want := e.GetId() == "marked" || !tc.wantReal
					if e.GetExercise() != want {
						t.Errorf("%s: %s exercise %v, want %v", where, e.GetId(), e.GetExercise(), want)
					}
				}
			}
		})
	}
}
