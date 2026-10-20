package grpcin

import (
	"context"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/sentinel/ingest-service/internal/emplacement"
	"github.com/sentinel/ingest-service/internal/ingesttoken"
	ingestv1 "github.com/sentinel/proto-gen/sentinel/ingest/v1"
	sentinelv1 "github.com/sentinel/proto-gen/sentinel/v1"
)

func upsertReq(id, name string) *ingestv1.UpsertEmplacementRequest {
	return &ingestv1.UpsertEmplacementRequest{Emplacement: &sentinelv1.Emplacement{
		Id: id, Name: name, Mobility: sentinelv1.Mobility_MOBILITY_MOBILE, SensorType: sentinelv1.SensorType_SENSOR_TYPE_RF,
		Classification: sentinelv1.Classification_CLASSIFICATION_UNCLASSIFIED,
	}}
}

func TestUpsertEmplacement(t *testing.T) {
	emps := &fakeEmplacements{}
	w := &fakeWriter{emps: emps}
	tok := ingesttoken.Token{ID: "t1", FeedID: "grpc-1", Classification: "CONFIDENTIAL"}
	r := startWith(t, map[string]any{"senderManagedEmplacements": true, "maxEmplacements": 2}, nil, tokensWith(secretA, tok), emps, w, nil, nil)
	c := r.client(t)
	ctx, cancel := withToken(secretA)
	defer cancel()

	created, err := c.UpsertEmplacement(ctx, upsertReq("", "Ship ESM"))
	if err != nil {
		t.Fatal(err)
	}
	got := created.GetEmplacement()
	if got.GetId() == "" || got.GetFeedId() != "grpc-1" || !got.GetActive() {
		t.Fatalf("created %v", got)
	}
	stored, _ := w.Get(context.Background(), got.GetId())
	if stored.CreatedBy != emplacement.SenderPrefix+"t1" {
		t.Errorf("createdBy %q", stored.CreatedBy)
	}
	// The connection's sessions see it.
	s, err := c.GetSession(ctx, &ingestv1.GetSessionRequest{})
	if err != nil || len(s.GetEmplacements()) != 1 {
		t.Fatalf("session after create: %v %v", s, err)
	}
	// Update by id.
	upd := upsertReq(got.GetId(), "Ship ESM")
	upd.Emplacement.Description = "aft mast"
	if res, err := c.UpsertEmplacement(ctx, upd); err != nil || res.GetEmplacement().GetDescription() != "aft mast" {
		t.Fatalf("update: %v %v", res, err)
	}
	// An unknown id creates; the cap refuses the third.
	if _, err := c.UpsertEmplacement(ctx, upsertReq("no-such-id", "Second")); err != nil {
		t.Fatalf("an unknown id should create: %v", err)
	}
	if _, err := c.UpsertEmplacement(ctx, upsertReq("", "Third")); status.Code(err) != codes.ResourceExhausted {
		t.Errorf("over maxEmplacements: %v", err)
	}
	// A name taken elsewhere.
	emps.put(emp("Taken", "", "FIXED", "RADAR", "UNCLASSIFIED"))
	w.Delete(context.Background(), got.GetId()) // make room
	if _, err := c.UpsertEmplacement(ctx, upsertReq("", "taken")); status.Code(err) != codes.AlreadyExists {
		t.Errorf("a taken name: %v", err)
	}
	// Above the token's ceiling.
	secret := upsertReq("", "Secret ESM")
	secret.Emplacement.Classification = sentinelv1.Classification_CLASSIFICATION_SECRET
	if _, err := c.UpsertEmplacement(ctx, secret); status.Code(err) != codes.InvalidArgument {
		t.Errorf("above the ceiling: %v", err)
	}
}

func TestUpsertTouchesOnlyThisConnectionsSenderRows(t *testing.T) {
	operator := emp("op-1", "grpc-1", "FIXED", "RADAR", "UNCLASSIFIED")
	operator.CreatedBy = "u1"
	theirs := emp("their-1", "grpc-2", "MOBILE", "RF", "UNCLASSIFIED")
	theirs.CreatedBy = emplacement.SenderPrefix + "t9"
	emps := &fakeEmplacements{rows: []emplacement.Emplacement{operator, theirs}}
	w := &fakeWriter{emps: emps}
	r := startWith(t, map[string]any{"senderManagedEmplacements": true}, nil, tokensWith(secretA, ingesttoken.Token{ID: "t1", FeedID: "grpc-1"}), emps, w, nil, nil)
	ctx, cancel := withToken(secretA)
	defer cancel()
	for _, id := range []string{"op-1", "their-1"} {
		if _, err := r.client(t).UpsertEmplacement(ctx, upsertReq(id, "x")); status.Code(err) != codes.NotFound {
			t.Errorf("updating %s: %v, want NotFound", id, err)
		}
	}
}

func TestUpsertNeedsTheConnectionsConsent(t *testing.T) {
	r := startWith(t, nil, nil, tokensWith(secretA, ingesttoken.Token{ID: "t1", FeedID: "grpc-1"}), &fakeEmplacements{}, &fakeWriter{emps: &fakeEmplacements{}}, nil, nil)
	ctx, cancel := withToken(secretA)
	defer cancel()
	if _, err := r.client(t).UpsertEmplacement(ctx, upsertReq("", "x")); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("%v, want PermissionDenied", err)
	}
}
