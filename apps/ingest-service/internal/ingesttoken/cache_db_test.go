package ingesttoken_test

import (
	"context"
	"testing"
	"time"

	. "github.com/sentinel/ingest-service/internal/ingesttoken"
	"github.com/sentinel/ingest-service/internal/registry"
)

// Run, on a real pool, follows token notices and feed notices.
func TestRunFollowsRevocationAndFeedDeletion(t *testing.T) {
	pool, ctx := TestPool(t)
	store := NewPGStore(pool)
	cache := NewCache(store, pool, nil)
	revoked := make(chan Token, 8)
	cache.OnRevoke(func(tk Token) { revoked <- tk })

	runCtx, stop := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); cache.Run(runCtx) }()
	t.Cleanup(func() { stop(); <-done }) // registered after the pool's, so it runs first

	authenticates := func(secret string) func() bool {
		return func() bool { _, ok := cache.Authenticate(secret); return ok }
	}
	expectRevoked := func(id string) {
		t.Helper()
		select {
		case tk := <-revoked:
			if tk.ID != id {
				t.Fatalf("OnRevoke saw %s, want %s", tk.ID, id)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("OnRevoke did not fire for %s", id)
		}
	}

	// A created token becomes usable, a revoked one stops, and its subscribers hear once.
	tok, secret, hash := NewToken("test-tok-a", "sensor one")
	created, err := store.Create(ctx, tok, hash)
	if err != nil {
		t.Fatal(err)
	}
	Within(t, "the created token to authenticate", authenticates(secret))
	if err := store.Revoke(ctx, "test-tok-a", created.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	Within(t, "the revoked token to stop authenticating", func() bool { return !authenticates(secret)() })
	expectRevoked(created.ID)

	// Deleting a feed cascades its tokens away with only a feed notice.
	tok, secret, hash = NewToken("test-tok-b", "sensor two")
	created, err = store.Create(ctx, tok, hash)
	if err != nil {
		t.Fatal(err)
	}
	Within(t, "the second token to authenticate", authenticates(secret))
	// The real registry delete, which sends the feed notice itself.
	if err := registry.NewWithPool(pool, nil).Delete(ctx, "test-tok-b"); err != nil {
		t.Fatal(err)
	}
	Within(t, "the deleted feed's token to stop authenticating", func() bool { return !authenticates(secret)() })
	expectRevoked(created.ID)
}
