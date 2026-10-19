package ingesttoken

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/sentinel/go-common/secrettoken"

	"github.com/sentinel/ingest-service/internal/registry"
)

type memLoader struct {
	mu   sync.Mutex
	rows map[string]Stored // by id
	used map[string]time.Time
}

func (m *memLoader) All(context.Context) ([]Stored, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []Stored{}
	for _, s := range m.rows {
		out = append(out, s)
	}
	return out, nil
}
func (m *memLoader) ForFeed(_ context.Context, feed string) ([]Stored, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []Stored{}
	for _, s := range m.rows {
		if s.FeedID == feed {
			out = append(out, s)
		}
	}
	return out, nil
}
func (m *memLoader) MarkUsed(_ context.Context, used map[string]time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.used == nil {
		m.used = map[string]time.Time{}
	}
	for k, v := range used {
		m.used[k] = v
	}
	return nil
}

func add(m *memLoader, id, feed string) string {
	secret, display, hash, _ := secrettoken.Generate(Prefix)
	m.mu.Lock()
	m.rows[id] = Stored{Token: Token{ID: id, FeedID: feed, Prefix: display, Classification: "SECRET"}, Hash: hash}
	m.mu.Unlock()
	return secret
}

func TestFeedsChannelMatchesRegistry(t *testing.T) {
	if feedsChannel != registry.ChannelFeeds {
		t.Fatalf("feedsChannel %q, registry %q", feedsChannel, registry.ChannelFeeds)
	}
}

// Review focus 4.
func TestAuthenticate(t *testing.T) {
	ctx := context.Background()
	l := &memLoader{rows: map[string]Stored{}}
	good := add(l, "t1", "grpc-1")
	c := NewCache(l, nil, nil)
	if err := c.Load(ctx); err != nil {
		t.Fatal(err)
	}
	tok, ok := c.Authenticate(good)
	if !ok || tok.ID != "t1" || tok.FeedID != "grpc-1" || tok.Classification != "SECRET" {
		t.Fatalf("good secret = %+v %v", tok, ok)
	}
	for _, bad := range []string{"", "sk_" + good[3:], good[:len(good)-1], "si_" + "x" + good[4:]} {
		if _, ok := c.Authenticate(bad); ok {
			t.Errorf("authenticated %q", bad)
		}
	}
	past := time.Now().Add(-time.Minute)
	l.mu.Lock()
	r := l.rows["t1"]
	r.ExpiresAt = &past
	l.rows["t1"] = r
	l.mu.Unlock()
	_ = c.RefreshFeed(ctx, "grpc-1")
	if _, ok := c.Authenticate(good); ok {
		t.Error("an expired token authenticated")
	}
	if err := c.FlushUsed(ctx); err != nil {
		t.Fatal(err)
	}
	if _, ok := l.used["t1"]; !ok {
		t.Error("the successful use was not recorded")
	}
}

// Review focus 1 and 2.
func TestRevokeAndFeedDeletionFireOnce(t *testing.T) {
	ctx := context.Background()
	l := &memLoader{rows: map[string]Stored{}}
	s1 := add(l, "t1", "grpc-1")
	add(l, "t2", "grpc-1")
	s3 := add(l, "t3", "grpc-2")
	c := NewCache(l, nil, nil)
	_ = c.Load(ctx)
	var mu sync.Mutex
	var gone []string
	c.OnRevoke(func(tk Token) { mu.Lock(); gone = append(gone, tk.ID); mu.Unlock() })

	now := time.Now()
	l.mu.Lock()
	r := l.rows["t1"]
	r.RevokedAt = &now
	l.rows["t1"] = r
	l.mu.Unlock()
	_ = c.RefreshFeed(ctx, "grpc-1")
	_ = c.RefreshFeed(ctx, "grpc-1") // a second refresh must not fire again
	if _, ok := c.Authenticate(s1); ok {
		t.Error("a revoked token authenticated")
	}

	l.mu.Lock()
	delete(l.rows, "t3") // the feed was deleted: the cascade sent no token notice
	l.mu.Unlock()
	_ = c.RefreshFeed(ctx, "grpc-2") // driven by the sentinel_feeds notice
	if _, ok := c.Authenticate(s3); ok {
		t.Error("a deleted feed's token authenticated")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(gone) != 2 || gone[0] != "t1" || gone[1] != "t3" {
		t.Fatalf("OnRevoke saw %v, want [t1 t3]", gone)
	}
}

func TestLoadReportsTokensGoneWhileAway(t *testing.T) {
	ctx := context.Background()
	l := &memLoader{rows: map[string]Stored{}}
	add(l, "t1", "grpc-1")
	add(l, "t2", "grpc-1")
	c := NewCache(l, nil, nil)
	_ = c.Load(ctx)
	var gone []string
	c.OnRevoke(func(tk Token) { gone = append(gone, tk.ID) })
	l.mu.Lock()
	delete(l.rows, "t2")
	l.mu.Unlock()
	_ = c.Load(ctx) // a reconnect's reload
	if len(gone) != 1 || gone[0] != "t2" {
		t.Fatalf("reload reported %v, want [t2]", gone)
	}
}
