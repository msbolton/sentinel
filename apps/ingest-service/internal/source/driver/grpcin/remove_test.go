package grpcin

import (
	"context"
	"errors"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/sentinel/ingest-service/internal/emplacement"
	"github.com/sentinel/ingest-service/internal/ingesttoken"
	ingestv1 "github.com/sentinel/proto-gen/sentinel/ingest/v1"
)

func TestRemoveEmplacementDeletesASendersOwnSite(t *testing.T) {
	r, w, _ := consentRig(t, nil, ingesttoken.Token{})
	ctx, cancel := withToken(secretA)
	defer cancel()
	c := r.client(t)
	made, err := c.UpsertEmplacement(ctx, upsertReq("", "guest-1"))
	if err != nil {
		t.Fatal(err)
	}
	id := made.GetEmplacement().GetId()
	if _, err := c.RemoveEmplacement(ctx, &ingestv1.RemoveEmplacementRequest{Id: id}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Get(context.Background(), id); !errors.Is(err, emplacement.ErrNotFound) {
		t.Errorf("the site after a remove: %v, want ErrNotFound", err)
	}
	// A second remove is NotFound, which a caller may ignore.
	if _, err := c.RemoveEmplacement(ctx, &ingestv1.RemoveEmplacementRequest{Id: id}); status.Code(err) != codes.NotFound {
		t.Errorf("a second remove: %v, want NotFound", err)
	}
}

func TestRemoveEmplacementRefusesWithoutSenderManaged(t *testing.T) {
	emps := &fakeEmplacements{rows: []emplacement.Emplacement{senderRow("s-1")}}
	r := startWith(t, nil, nil, tokensWith(secretA, ingesttoken.Token{ID: "t1", FeedID: "grpc-1"}), emps, &fakeWriter{emps: emps}, nil, nil)
	ctx, cancel := withToken(secretA)
	defer cancel()
	if _, err := r.client(t).RemoveEmplacement(ctx, &ingestv1.RemoveEmplacementRequest{Id: "s-1"}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("%v, want PermissionDenied", err)
	}
	if _, ok := emps.Get("s-1"); !ok {
		t.Error("the site was removed")
	}
}

func TestRemoveEmplacementRefusesAnotherConnectionsSite(t *testing.T) {
	theirs := emp("their-1", "grpc-2", "MOBILE", "RF", "UNCLASSIFIED")
	theirs.CreatedBy = emplacement.SenderPrefix + "t9"
	r, _, emps := consentRig(t, nil, ingesttoken.Token{}, theirs)
	ctx, cancel := withToken(secretA)
	defer cancel()
	if _, err := r.client(t).RemoveEmplacement(ctx, &ingestv1.RemoveEmplacementRequest{Id: "their-1"}); status.Code(err) != codes.NotFound {
		t.Fatalf("%v, want NotFound", err)
	}
	if _, ok := emps.Get("their-1"); !ok {
		t.Error("the site was removed")
	}
}

func TestRemoveEmplacementRefusesAnOperatorMadeSite(t *testing.T) {
	operator := emp("op-1", "grpc-1", "FIXED", "RADAR", "UNCLASSIFIED")
	operator.CreatedBy = "u1"
	r, _, emps := consentRig(t, nil, ingesttoken.Token{}, operator)
	ctx, cancel := withToken(secretA)
	defer cancel()
	if _, err := r.client(t).RemoveEmplacement(ctx, &ingestv1.RemoveEmplacementRequest{Id: "op-1"}); status.Code(err) != codes.NotFound {
		t.Fatalf("%v, want NotFound", err)
	}
	if _, ok := emps.Get("op-1"); !ok {
		t.Error("the site was removed")
	}
}
