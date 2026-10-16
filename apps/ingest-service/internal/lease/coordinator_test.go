package lease

import (
	"context"
	"os"
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
