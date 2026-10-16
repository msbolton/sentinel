package registry

import (
	"errors"
	"testing"
)

func TestFencedStatusWrite(t *testing.T) {
	s, ctx := testStore(t)
	f := &Feed{ID: "test-fence", Name: "Fence", Driver: "adsblol", Config: []byte(`{}`), SecretRefs: map[string]string{}}
	if err := s.Create(ctx, f); err != nil {
		t.Fatal(err)
	}
	// No lease row: a fenced write to an existing row is refused, an unfenced one goes through.
	if err := s.UpsertStatus(ctx, Status{FeedID: f.ID, Owner: "r1", State: StateRunning, Health: HealthHealthy}); err != nil {
		t.Fatal(err)
	}
	err := s.UpsertStatus(ctx, Status{FeedID: f.ID, Owner: "r1", State: StateIdle, Health: HealthHealthy, FencedBy: "r1", FencingToken: 1})
	if !errors.Is(err, ErrFenced) {
		t.Fatalf("expected ErrFenced without a lease, got %v", err)
	}
	// With a matching lease the fenced write succeeds; a stale token is fenced out.
	if _, err := s.pool.Exec(ctx, `INSERT INTO sentinel.feed_leases (feed_id, owner_id, fencing_token, expires_at) VALUES ($1, 'r1', 2, NOW() + INTERVAL '1 minute')`, f.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertStatus(ctx, Status{FeedID: f.ID, Owner: "r1", State: StateIdle, Health: HealthHealthy, FencedBy: "r1", FencingToken: 2}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertStatus(ctx, Status{FeedID: f.ID, Owner: "r0", State: StateError, Health: HealthCritical, FencedBy: "r0", FencingToken: 1}); !errors.Is(err, ErrFenced) {
		t.Fatalf("stale owner must be fenced, got %v", err)
	}
	got, err := s.Get(ctx, f.ID)
	if err != nil || got.Status == nil || got.Status.State != StateIdle || got.Status.Owner != "r1" {
		t.Fatalf("status = %+v err %v", got.Status, err)
	}
}
