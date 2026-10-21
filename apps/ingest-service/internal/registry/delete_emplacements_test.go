package registry

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/sentinel/ingest-service/internal/emplacement"
)

// Deleting a feed soft-deletes the emplacements its senders made and unbinds
// the rest, each announced on the emplacement channel, so every cache drops
// the binding: the FK's SET NULL alone is silent.
func TestDeleteLetsGoOfTheFeedsEmplacements(t *testing.T) {
	s, ctx := testStore(t)
	pool := s.Pool()
	feed := "test-grpc-del"
	if err := s.Create(ctx, &Feed{ID: feed, Name: "Test", Driver: "stream",
		Config:     json.RawMessage(`{"transport":{"kind":"mqtt","brokerUrl":"tcp://b:1883","topics":["a"]},"decoder":{"name":"json"}}`),
		SecretRefs: map[string]string{}, Enabled: true, CreatedBy: "u1"}); err != nil {
		t.Fatal(err)
	}
	dropEmplacements := func(ctx context.Context) {
		_, _ = pool.Exec(ctx, `DELETE FROM sentinel.emplacements WHERE name LIKE 'test-del-%'`)
	}
	dropEmplacements(ctx)
	t.Cleanup(func() { dropEmplacements(context.Background()) })

	emps := emplacement.NewPGStore(pool)
	mine := emplacement.New()
	mine.Name, mine.Mobility, mine.SensorType, mine.FeedID, mine.CreatedBy = "test-del-sender", "MOBILE", "RF", &feed, emplacement.SenderPrefix+"t1"
	theirs := emplacement.New()
	theirs.Name, theirs.Mobility, theirs.SensorType, theirs.FeedID, theirs.CreatedBy = "test-del-operator", "FIXED", "RADAR", &feed, "u1"
	theirs.Position = &emplacement.Point{Lat: 50.1, Lon: -4.2}
	m, err := emps.Create(ctx, mine)
	if err != nil {
		t.Fatal(err)
	}
	o, err := emps.Create(ctx, theirs)
	if err != nil {
		t.Fatal(err)
	}

	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	defer func() { _, _ = conn.Exec(context.Background(), "UNLISTEN *") }()
	if _, err := conn.Exec(ctx, "LISTEN "+emplacement.Channel); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, feed); err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for len(got) < 2 {
		// Other packages' tests write to the same channel; only these two count.
		wctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		n, err := conn.Conn().WaitForNotification(wctx)
		cancel()
		if err != nil {
			t.Fatalf("notices so far %v: %v", got, err)
		}
		var p struct{ ID, Op string }
		if err := json.Unmarshal([]byte(n.Payload), &p); err != nil {
			t.Fatal(err)
		}
		if p.ID == m.ID || p.ID == o.ID {
			got[p.ID] = p.Op
		}
	}
	if got[m.ID] != "delete" || got[o.ID] != "upsert" {
		t.Errorf("notices = %v, want %s delete and %s upsert", got, m.ID, o.ID)
	}
	if _, err := emps.Get(ctx, m.ID); !errors.Is(err, emplacement.ErrNotFound) {
		t.Errorf("the sender's emplacement is still live: %v", err)
	}
	after, err := emps.Get(ctx, o.ID)
	if err != nil || after.FeedID != nil || after.Active || after.Version != o.Version+1 {
		t.Errorf("the operator's emplacement = %+v, %v; want unbound, inactive, with its version bumped", after, err)
	}
}

// A row deleted earlier still names its connection, and the foreign key
// (ON DELETE RESTRICT) would refuse deleting the connection: Delete lets it go.
func TestDeleteUnbindsSoftDeletedEmplacements(t *testing.T) {
	s, ctx := testStore(t)
	pool := s.Pool()
	feed := "test-grpc-softdel"
	if err := s.Create(ctx, &Feed{ID: feed, Name: "Test", Driver: "stream",
		Config:     json.RawMessage(`{"transport":{"kind":"mqtt","brokerUrl":"tcp://b:1883","topics":["a"]},"decoder":{"name":"json"}}`),
		SecretRefs: map[string]string{}, Enabled: true, CreatedBy: "u1"}); err != nil {
		t.Fatal(err)
	}
	drop := func(ctx context.Context) {
		_, _ = pool.Exec(ctx, `DELETE FROM sentinel.emplacements WHERE name LIKE 'test-softdel-%'`)
	}
	drop(ctx)
	t.Cleanup(func() { drop(context.Background()) })

	emps := emplacement.NewPGStore(pool)
	gone := emplacement.New()
	gone.Name, gone.Mobility, gone.SensorType, gone.FeedID, gone.CreatedBy = "test-softdel-gone", "MOBILE", "RF", &feed, "u1"
	g, err := emps.Create(ctx, gone)
	if err != nil {
		t.Fatal(err)
	}
	if err := emps.Delete(ctx, g.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, feed); err != nil {
		t.Fatalf("deleting a connection whose emplacement was deleted earlier: %v", err)
	}
	var still *string
	if err := pool.QueryRow(ctx, `SELECT "feedId" FROM sentinel.emplacements WHERE id = $1::uuid`, g.ID).Scan(&still); err != nil || still != nil {
		t.Fatalf("the deleted row's connection = %v, %v; want none", still, err)
	}
}

func TestEmplacementChannelMatches(t *testing.T) {
	if emplacementsChannel != emplacement.Channel {
		t.Fatalf("registry announces on %q, the emplacement cache listens on %q", emplacementsChannel, emplacement.Channel)
	}
}
