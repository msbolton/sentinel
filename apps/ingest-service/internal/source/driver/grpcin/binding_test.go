package grpcin

import (
	"fmt"
	"testing"

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
		ok(st, claiming(observation("two", 50, -4), "two"))
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
	rep := observation("EMITTER-1", 50, -4)
	rep.GetObservation().Identities = []*sentinelv1.Identity{
		{Type: sentinelv1.IdentityType_IDENTITY_TYPE_EMPLACEMENT, Value: "esm"},
		{Type: sentinelv1.IdentityType_IDENTITY_TYPE_MMSI, Value: "235000000"},
	}
	send(t, st, rep)
	for _, id := range r.sink.emitted()[0].Identities {
		if id.Type == obs.IdentityEmplacement {
			t.Fatalf("a target kept identity %v", id)
		}
	}
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
