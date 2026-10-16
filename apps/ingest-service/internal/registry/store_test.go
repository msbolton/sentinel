package registry

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// These tests need a database with the baseline applied. Set
// TEST_DATABASE_URL to run them; they create and delete feeds named test-*.
func testStore(t *testing.T) (*Store, context.Context) {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	s := NewWithPool(pool, nil)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM sentinel.feeds WHERE id LIKE 'test-%'`)
	})
	return s, ctx
}

func TestStoreCRUDAndNotify(t *testing.T) {
	s, ctx := testStore(t)
	changes := s.Watch(ctx)
	time.Sleep(200 * time.Millisecond) // let LISTEN attach

	f := &Feed{ID: "test-mqtt", Name: "Test", Driver: "stream",
		Config:     json.RawMessage(`{"transport":{"kind":"mqtt","brokerUrl":"tcp://b:1883","topics":["a"]},"decoder":{"name":"json"}}`),
		SecretRefs: map[string]string{}, Enabled: true, CreatedBy: "u1"}
	if err := s.Create(ctx, f); err != nil {
		t.Fatal(err)
	}
	if f.Version != 1 || f.Status == nil || f.Status.State != StateStopped {
		t.Fatalf("created feed = %+v status %+v", f, f.Status)
	}
	if err := s.Create(ctx, &Feed{ID: "test-mqtt", Name: "dup", Driver: "stream"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate create err = %v", err)
	}
	expectChange(t, changes, ChannelFeeds, "test-mqtt")

	f.Name = "Renamed"
	if err := s.Update(ctx, f); err != nil {
		t.Fatal(err)
	}
	if f.Version != 2 || f.Name != "Renamed" {
		t.Fatalf("updated feed = %+v", f)
	}
	expectChange(t, changes, ChannelFeeds, "test-mqtt")

	got, err := s.SetEnabled(ctx, "test-mqtt", false)
	if err != nil || got.Enabled || got.Version != 3 {
		t.Fatalf("SetEnabled = %+v err %v", got, err)
	}
	expectChange(t, changes, ChannelFeeds, "test-mqtt")

	exp := time.Now().Add(90 * time.Second).UTC().Truncate(time.Millisecond)
	if err := s.SetArea(ctx, Area{FeedID: "test-mqtt", Active: true, Clients: 2, Boxes: []Box{{North: 1, South: 0, East: 1, West: 0}}, ExpiresAt: &exp}); err != nil {
		t.Fatal(err)
	}
	expectChange(t, changes, ChannelAreas, "test-mqtt")
	a, err := s.GetArea(ctx, "test-mqtt")
	if err != nil || !a.Active || a.Clients != 2 || len(a.Boxes) != 1 || a.ExpiresAt == nil {
		t.Fatalf("GetArea = %+v err %v", a, err)
	}
	if err := s.SetArea(ctx, Area{FeedID: "test-missing", Active: true}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("area for unknown feed err = %v", err)
	}

	now := time.Now().UTC()
	if err := s.UpsertStatus(ctx, Status{FeedID: "test-mqtt", Owner: "r1", State: StateRunning, Health: HealthHealthy, LastSuccessAt: &now, MessagesTotal: 5, AppliedVersion: 3}); err != nil {
		t.Fatal(err)
	}
	got, _ = s.Get(ctx, "test-mqtt")
	if got.Status.Owner != "r1" || got.Status.State != StateRunning || got.Status.MessagesTotal != 5 {
		t.Fatalf("status = %+v", got.Status)
	}
	if err := s.ReleaseStatuses(ctx, "r1"); err != nil {
		t.Fatal(err)
	}
	got, _ = s.Get(ctx, "test-mqtt")
	if got.Status.Owner != "" || got.Status.State != StateStopped {
		t.Fatalf("released status = %+v", got.Status)
	}

	if err := s.Delete(ctx, "test-mqtt"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, "test-mqtt"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after delete err = %v", err)
	}
	if err := s.Delete(ctx, "test-mqtt"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("double delete err = %v", err)
	}
}

func TestEnsureBuiltins(t *testing.T) {
	s, ctx := testStore(t)
	if err := s.EnsureBuiltins(ctx, []string{"celestrak"}); err != nil {
		t.Fatal(err)
	}
	feeds, err := s.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]Feed{}
	for _, f := range feeds {
		seen[f.ID] = f
	}
	for _, b := range Builtins {
		if f, ok := seen[b.ID]; !ok || !f.Builtin || f.Driver != b.Driver {
			t.Errorf("builtin %s missing or wrong: %+v", b.ID, f)
		}
	}
	// Idempotent: a second call changes nothing.
	if err := s.EnsureBuiltins(ctx, nil); err != nil {
		t.Fatal(err)
	}
}

func expectChange(t *testing.T, ch <-chan Change, channel, id string) {
	t.Helper()
	select {
	case c := <-ch:
		if c.Channel != channel || c.FeedID != id {
			t.Fatalf("change = %+v, want %s/%s", c, channel, id)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("no %s notification for %s", channel, id)
	}
}
