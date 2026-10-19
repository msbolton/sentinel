package lease

import (
	"context"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPreferredIsStableAndBalanced(t *testing.T) {
	live := []string{"a", "b", "c"}
	if Preferred("x", "pinned", live) != "pinned" {
		t.Error("pin must win")
	}
	if Preferred("x", "", nil) != "" {
		t.Error("empty live set gives no owner")
	}
	counts := map[string]int{}
	for i := 0; i < 300; i++ {
		id := "feed-" + string(rune('a'+i%26)) + string(rune('a'+i/26))
		owner := Preferred(id, "", live)
		if owner != Preferred(id, "", []string{"c", "a", "b"}) {
			t.Errorf("%s: owner depends on order", id)
		}
		counts[owner]++
	}
	for _, r := range live {
		if counts[r] < 60 {
			t.Errorf("replica %s got only %d of 300 feeds: %v", r, counts[r], counts)
		}
	}
	// Removing a replica moves only its feeds.
	moved := 0
	for i := 0; i < 300; i++ {
		id := "feed-" + string(rune('a'+i%26)) + string(rune('a'+i/26))
		before, after := Preferred(id, "", live), Preferred(id, "", []string{"a", "b"})
		if before != "c" && before != after {
			moved++
		}
	}
	if moved != 0 {
		t.Errorf("%d feeds moved although their owner stayed live", moved)
	}
}

// The Postgres tests need a database with the baseline applied; set
// TEST_DATABASE_URL to run them. They use feed ids starting with test-.
func testPool(t *testing.T) (*pgxpool.Pool, context.Context) {
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
	if _, err := pool.Exec(ctx, `INSERT INTO sentinel.feeds (id, name, driver, config, secret_refs, enabled, builtin) VALUES ('lease-coord', 'Lease test', 'adsblol', '{}', '{}', false, false) ON CONFLICT (id) DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM sentinel.feeds WHERE id = 'lease-coord'`)
		_, _ = pool.Exec(context.Background(), `DELETE FROM sentinel.ingest_replicas WHERE replica_id = 'lease-coord'`)
	})
	return pool, ctx
}

func TestAcquireRenewStealRelease(t *testing.T) {
	pool, ctx := testPool(t)
	a := New(pool, "test-a", Options{TTL: 500 * time.Millisecond})
	b := New(pool, "test-b", Options{TTL: 500 * time.Millisecond})
	for _, c := range []*Coordinator{a, b} {
		if err := c.Heartbeat(ctx); err != nil {
			t.Fatal(err)
		}
	}
	live, err := a.Live(ctx)
	if err != nil || len(live) < 2 {
		t.Fatalf("live = %v err %v", live, err)
	}

	tok, ok, err := a.Acquire(ctx, "lease-coord")
	if err != nil || !ok || tok != 1 {
		t.Fatalf("first acquire: tok=%d ok=%v err=%v", tok, ok, err)
	}
	if _, ok, _ := b.Acquire(ctx, "lease-coord"); ok {
		t.Fatal("b must not take a live lease")
	}
	if ok, err := a.Renew(ctx, "lease-coord", tok); err != nil || !ok {
		t.Fatalf("renew: %v %v", ok, err)
	}
	if ok, _ := a.Renew(ctx, "lease-coord", tok+5); ok {
		t.Fatal("renew with a stale token must fail")
	}
	owner, otok, _, held, _ := a.Owner(ctx, "lease-coord")
	if !held || owner != "test-a" || otok != tok {
		t.Fatalf("owner = %s/%d held=%v", owner, otok, held)
	}

	// a stops renewing: after the TTL b steals it with a higher token.
	time.Sleep(700 * time.Millisecond)
	tok2, ok, err := b.Acquire(ctx, "lease-coord")
	if err != nil || !ok || tok2 != tok+1 {
		t.Fatalf("steal: tok=%d ok=%v err=%v", tok2, ok, err)
	}
	if ok, _ := a.Renew(ctx, "lease-coord", tok); ok {
		t.Fatal("a's old token must be fenced out")
	}
	if err := b.Release(ctx, "lease-coord", tok2); err != nil {
		t.Fatal(err)
	}
	if _, _, _, held, _ := a.Owner(ctx, "lease-coord"); held {
		t.Fatal("release must free the lease")
	}
	tok3, ok, _ := a.Acquire(ctx, "lease-coord")
	if !ok || tok3 != 1 {
		t.Fatalf("after release the token restarts: %d %v", tok3, ok)
	}
	if err := a.Leave(ctx); err != nil {
		t.Fatal(err)
	}
	if _, _, _, held, _ := b.Owner(ctx, "lease-coord"); held {
		t.Fatal("leave must release every lease")
	}
	live, _ = b.Live(ctx)
	for _, r := range live {
		if r == "test-a" {
			t.Fatal("leave must remove the replica row")
		}
	}
}

// withPrefix keeps the ids a test created: the database may hold replicas of
// a running stack too.
func withPrefix(prefix string, ids []string) []string {
	var out []string
	for _, id := range ids {
		if strings.HasPrefix(id, prefix) {
			out = append(out, id)
		}
	}
	return out
}

// A listener's heartbeat records whether it also takes ordinary feeds, on the
// first heartbeat and when a restart changes the setting. Listeners reports
// every live listener and, among them, the dedicated ones.
func TestHeartbeatRecordsListenerSharing(t *testing.T) {
	pool, ctx := testPool(t)
	const prefix = "test-sharing-"
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM sentinel.ingest_replicas WHERE replica_id LIKE $1`, prefix+"%")
	})
	dedicated := New(pool, prefix+"dedicated", Options{Listener: true, ListenerHost: "ingest.example"})
	shared := New(pool, prefix+"shared", Options{Listener: true, ListenerShared: true})
	plain := New(pool, prefix+"plain", Options{})
	for _, c := range []*Coordinator{dedicated, shared, plain} {
		if err := c.Heartbeat(ctx); err != nil {
			t.Fatal(err)
		}
	}

	all, ded, err := plain.Listeners(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{prefix + "dedicated", prefix + "shared"}; !slices.Equal(withPrefix(prefix, all), want) {
		t.Errorf("all listeners = %v, want %v (an ordinary replica is no listener)", withPrefix(prefix, all), want)
	}
	if want := []string{prefix + "dedicated"}; !slices.Equal(withPrefix(prefix, ded), want) {
		t.Errorf("dedicated listeners = %v, want %v", withPrefix(prefix, ded), want)
	}

	// The same replica restarted with sharing on: the next heartbeat updates the row.
	restarted := New(pool, prefix+"dedicated", Options{Listener: true, ListenerShared: true})
	if err := restarted.Heartbeat(ctx); err != nil {
		t.Fatal(err)
	}
	if _, ded, err = plain.Listeners(ctx); err != nil || len(withPrefix(prefix, ded)) != 0 {
		t.Errorf("after the restart dedicated listeners = %v (err %v), want none", withPrefix(prefix, ded), err)
	}
	// And back: the flag follows the replica, it is not sticky.
	if err := dedicated.Heartbeat(ctx); err != nil {
		t.Fatal(err)
	}
	if _, ded, err = plain.Listeners(ctx); err != nil || !slices.Equal(withPrefix(prefix, ded), []string{prefix + "dedicated"}) {
		t.Errorf("after turning sharing off again dedicated listeners = %v (err %v)", withPrefix(prefix, ded), err)
	}
}

// Rows written by code from before listener_shared existed never set it, and
// the column defaults to false: such a listener is dedicated, and a replica
// that is no listener is in neither list.
func TestOldReplicaRowsDefaultToUnshared(t *testing.T) {
	pool, ctx := testPool(t)
	const prefix = "test-oldrow-"
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM sentinel.ingest_replicas WHERE replica_id LIKE $1`, prefix+"%")
	})
	// The columns older code's heartbeat wrote, and not listener_shared.
	if _, err := pool.Exec(ctx, `
		INSERT INTO sentinel.ingest_replicas (replica_id, started_at, heartbeat_at, expires_at, listener, listener_host)
		VALUES ($1, NOW(), NOW(), NOW() + INTERVAL '1 minute', TRUE, 'old.example'),
		       ($2, NOW(), NOW(), NOW() + INTERVAL '1 minute', FALSE, NULL)`, prefix+"listener", prefix+"plain"); err != nil {
		t.Fatal(err)
	}

	all, dedicated, err := New(pool, prefix+"reader", Options{}).Listeners(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{prefix + "listener"}
	if !slices.Equal(withPrefix(prefix, all), want) {
		t.Errorf("all listeners = %v, want %v", withPrefix(prefix, all), want)
	}
	if !slices.Equal(withPrefix(prefix, dedicated), want) {
		t.Errorf("dedicated listeners = %v, want %v: an old listener row is not shared", withPrefix(prefix, dedicated), want)
	}
}
