package ingesttoken

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sentinel/go-common/secrettoken"
)

// These tests need a database with db/schema applied. Set TEST_DATABASE_URL
// to run them; they use the feeds test-tok-* and delete them before and after.
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
	clean := func(ctx context.Context) {
		_, _ = pool.Exec(ctx, `DELETE FROM sentinel.feeds WHERE id LIKE 'test-tok-%'`)
	}
	clean(ctx)
	t.Cleanup(func() { clean(context.Background()) })
	for _, id := range []string{"test-tok-a", "test-tok-b"} {
		if _, err := pool.Exec(ctx, `INSERT INTO sentinel.feeds (id, name, description, driver, config, secret_refs, enabled)
			VALUES ($1, $1, '', 'grpc', '{"port":6100}', '{}', false)`, id); err != nil {
			t.Fatal(err)
		}
	}
	return pool, ctx
}

func newToken(feed, name string) (Token, string, []byte) {
	secret, display, hash, _ := secrettoken.Generate(Prefix)
	return Token{FeedID: feed, Name: name, Prefix: display, Classification: "UNCLASSIFIED", CreatedBy: "u1"}, secret, hash
}

func listenTokens(t *testing.T, ctx context.Context, pool *pgxpool.Pool) <-chan string {
	t.Helper()
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	raw := conn.Hijack()
	if _, err := raw.Exec(ctx, "LISTEN "+Channel); err != nil {
		t.Fatal(err)
	}
	lctx, cancel := context.WithCancel(ctx)
	out := make(chan string, 16)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			n, err := raw.WaitForNotification(lctx)
			if err != nil {
				return
			}
			out <- n.Payload
		}
	}()
	t.Cleanup(func() { cancel(); <-done; _ = raw.Close(context.Background()) })
	return out
}

func expectOp(t *testing.T, ch <-chan string, id, feed, op string) {
	t.Helper()
	select {
	case p := <-ch:
		var m struct{ ID, FeedID, Op string }
		if err := json.Unmarshal([]byte(p), &m); err != nil || m.ID != id || m.FeedID != feed || m.Op != op {
			t.Fatalf("notice %q, want %s %s %s", p, id, feed, op)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("no %s notice for %s", op, id)
	}
}

func TestStoreCreateListRevoke(t *testing.T) {
	pool, ctx := testPool(t)
	s := NewPGStore(pool)
	notices := listenTokens(t, ctx, pool)

	tok, _, hash := newToken("test-tok-a", "sensor one")
	exp := time.Now().Add(24 * time.Hour).UTC().Truncate(time.Microsecond)
	tok.ExpiresAt = &exp
	created, err := s.Create(ctx, tok, hash)
	if err != nil {
		t.Fatal(err)
	}
	if created.ID == "" || created.CreatedAt.IsZero() || created.Prefix != tok.Prefix || !created.ExpiresAt.Equal(exp) {
		t.Fatalf("created = %+v", created)
	}
	expectOp(t, notices, created.ID, "test-tok-a", "create")

	list, err := s.List(ctx, "test-tok-a")
	if err != nil || len(list) != 1 || list[0].ID != created.ID {
		t.Fatalf("list = %+v %v", list, err)
	}
	if other, _ := s.List(ctx, "test-tok-b"); len(other) != 0 {
		t.Fatalf("another feed's list = %+v", other)
	}

	at := time.Now().UTC().Truncate(time.Microsecond)
	if err := s.Revoke(ctx, "test-tok-b", created.ID, at); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revoking through another feed = %v", err)
	}
	if err := s.Revoke(ctx, "test-tok-a", created.ID, at); err != nil {
		t.Fatal(err)
	}
	expectOp(t, notices, created.ID, "test-tok-a", "revoke")
	if err := s.Revoke(ctx, "test-tok-a", created.ID, at.Add(time.Hour)); err != nil { // idempotent; keeps the first time
		t.Fatal(err)
	}
	<-notices
	all, _ := s.ForFeed(ctx, "test-tok-a")
	if len(all) != 1 || all[0].RevokedAt == nil || !all[0].RevokedAt.Equal(at) || string(all[0].Hash) != string(hash) {
		t.Fatalf("for feed = %+v", all)
	}
	if err := s.Revoke(ctx, "test-tok-a", "not-a-uuid", at); !errors.Is(err, ErrNotFound) {
		t.Fatalf("malformed id = %v", err)
	}
}

func TestStoreUnknownFeedAndCascade(t *testing.T) {
	pool, ctx := testPool(t)
	s := NewPGStore(pool)
	tok, _, hash := newToken("test-tok-none", "x")
	if _, err := s.Create(ctx, tok, hash); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown feed = %v", err)
	}
	tok, _, hash = newToken("test-tok-b", "x")
	if _, err := s.Create(ctx, tok, hash); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM sentinel.feeds WHERE id = 'test-tok-b'`); err != nil {
		t.Fatal(err)
	}
	if left, _ := s.ForFeed(ctx, "test-tok-b"); len(left) != 0 {
		t.Fatalf("tokens outlived their feed: %+v", left)
	}
}

func TestStoreMarkUsed(t *testing.T) {
	pool, ctx := testPool(t)
	s := NewPGStore(pool)
	tok, _, hash := newToken("test-tok-a", "x")
	created, err := s.Create(ctx, tok, hash)
	if err != nil {
		t.Fatal(err)
	}
	t1 := time.Now().UTC().Truncate(time.Microsecond)
	if err := s.MarkUsed(ctx, map[string]time.Time{created.ID: t1}); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkUsed(ctx, map[string]time.Time{created.ID: t1.Add(-time.Hour)}); err != nil { // older: ignored
		t.Fatal(err)
	}
	got, _ := s.List(ctx, "test-tok-a")
	if got[0].LastUsedAt == nil || !got[0].LastUsedAt.Equal(t1) {
		t.Fatalf("lastUsedAt = %v, want %v", got[0].LastUsedAt, t1)
	}
}
