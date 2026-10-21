package ingesttoken

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/sentinel/go-common/secrettoken"
)

type memLoader struct {
	mu   sync.Mutex
	rows map[string]Stored // by id
	used map[string]time.Time

	err           error  // returned by All, ForFeed and MarkUsed when set
	afterSnapshot func() // called by All after it has copied the rows, before it returns

	duringMarkUsed func() // called by MarkUsed before it answers, with no lock held
}

func (m *memLoader) All(context.Context) ([]Stored, error) {
	m.mu.Lock()
	if m.err != nil {
		defer m.mu.Unlock()
		return nil, m.err
	}
	out := []Stored{}
	for _, s := range m.rows {
		out = append(out, s)
	}
	hook := m.afterSnapshot
	m.mu.Unlock()
	if hook != nil {
		hook()
	}
	return out, nil
}
func (m *memLoader) ForFeed(_ context.Context, feed string) ([]Stored, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return nil, m.err
	}
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
	hook := m.duringMarkUsed
	m.mu.Unlock()
	if hook != nil {
		hook()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return m.err
	}
	if m.used == nil {
		m.used = map[string]time.Time{}
	}
	for k, v := range used {
		m.used[k] = v
	}
	return nil
}

func (m *memLoader) setErr(err error) {
	m.mu.Lock()
	m.err = err
	m.mu.Unlock()
}

func (m *memLoader) mutate(id string, fn func(*Stored)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r := m.rows[id]
	fn(&r)
	m.rows[id] = r
}

func (m *memLoader) remove(id string) {
	m.mu.Lock()
	delete(m.rows, id)
	m.mu.Unlock()
}

func add(t *testing.T, m *memLoader, id, feed string) string {
	t.Helper()
	secret, display, hash, err := secrettoken.Generate(Prefix)
	if err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	m.rows[id] = Stored{Token: Token{ID: id, FeedID: feed, Prefix: display, Classification: "SECRET"}, Hash: hash}
	m.mu.Unlock()
	return secret
}

func mustLoad(t *testing.T, c *Cache) {
	t.Helper()
	if err := c.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func mustRefresh(t *testing.T, c *Cache, feed string) {
	t.Helper()
	if err := c.RefreshFeed(context.Background(), feed); err != nil {
		t.Fatal(err)
	}
}

// recordRevokes subscribes and returns a reader of the ids seen so far.
func recordRevokes(c *Cache) func() []string {
	var mu sync.Mutex
	var gone []string
	c.OnRevoke(func(tk Token) { mu.Lock(); gone = append(gone, tk.ID); mu.Unlock() })
	return func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), gone...)
	}
}

// Review focus 4.
func TestAuthenticate(t *testing.T) {
	ctx := context.Background()
	l := &memLoader{rows: map[string]Stored{}}
	good := add(t, l, "t1", "grpc-1")
	c := NewCache(l, nil, nil)
	mustLoad(t, c)
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
	l.mutate("t1", func(s *Stored) { s.ExpiresAt = &past })
	mustRefresh(t, c, "grpc-1")
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
	l := &memLoader{rows: map[string]Stored{}}
	s1 := add(t, l, "t1", "grpc-1")
	s2 := add(t, l, "t2", "grpc-1")
	s3 := add(t, l, "t3", "grpc-2")
	c := NewCache(l, nil, nil)
	mustLoad(t, c)
	gone := recordRevokes(c)

	now := time.Now()
	l.mutate("t1", func(s *Stored) { s.RevokedAt = &now })
	mustRefresh(t, c, "grpc-1")
	mustRefresh(t, c, "grpc-1") // a second refresh must not fire again
	if _, ok := c.Authenticate(s1); ok {
		t.Error("a revoked token authenticated")
	}
	if _, ok := c.Authenticate(s2); !ok {
		t.Error("a sibling of the revoked token stopped authenticating")
	}

	l.remove("t3")              // the feed was deleted: the cascade sent no token notice
	mustRefresh(t, c, "grpc-2") // driven by the sentinel_feeds notice
	if _, ok := c.Authenticate(s3); ok {
		t.Error("a deleted feed's token authenticated")
	}
	if _, ok := c.Authenticate(s2); !ok {
		t.Error("a token of another feed stopped authenticating")
	}

	if got := gone(); len(got) != 2 || got[0] != "t1" || got[1] != "t3" {
		t.Fatalf("OnRevoke saw %v, want [t1 t3]", got)
	}
}

func TestExpiredTokenDoesNotFireWhenItsFeedIsDeleted(t *testing.T) {
	l := &memLoader{rows: map[string]Stored{}}
	add(t, l, "t1", "grpc-1")
	past := time.Now().Add(-time.Minute)
	l.mutate("t1", func(s *Stored) { s.ExpiresAt = &past })
	c := NewCache(l, nil, nil)
	mustLoad(t, c)
	gone := recordRevokes(c)
	l.remove("t1")
	mustRefresh(t, c, "grpc-1")
	if got := gone(); len(got) != 0 {
		t.Fatalf("OnRevoke saw %v for a token that was already unusable", got)
	}
}

// A feed deleted and recreated under the same id: its old tokens are gone with
// it, and a token of the new feed works.
func TestSameIDFeedRecreated(t *testing.T) {
	l := &memLoader{rows: map[string]Stored{}}
	old := add(t, l, "t3", "grpc-2")
	c := NewCache(l, nil, nil)
	mustLoad(t, c)
	gone := recordRevokes(c)

	l.remove("t3")
	fresh := add(t, l, "t4", "grpc-2")
	mustRefresh(t, c, "grpc-2")
	if _, ok := c.Authenticate(old); ok {
		t.Error("the old feed's secret authenticated after the feed was recreated")
	}
	if tok, ok := c.Authenticate(fresh); !ok || tok.ID != "t4" {
		t.Errorf("the new feed's secret = %+v %v", tok, ok)
	}
	if got := gone(); len(got) != 1 || got[0] != "t3" {
		t.Fatalf("OnRevoke saw %v, want [t3]", got)
	}
}

func TestLoadReportsTokensGoneWhileAway(t *testing.T) {
	l := &memLoader{rows: map[string]Stored{}}
	add(t, l, "t1", "grpc-1")
	add(t, l, "t2", "grpc-1")
	c := NewCache(l, nil, nil)
	mustLoad(t, c)
	gone := recordRevokes(c)
	l.remove("t2")
	mustLoad(t, c) // a reconnect's reload
	if got := gone(); len(got) != 1 || got[0] != "t2" {
		t.Fatalf("reload reported %v, want [t2]", got)
	}
}

func TestLoaderErrorLeavesTheCacheUnchanged(t *testing.T) {
	ctx := context.Background()
	l := &memLoader{rows: map[string]Stored{}}
	s1 := add(t, l, "t1", "grpc-1")
	c := NewCache(l, nil, nil)
	mustLoad(t, c)
	gone := recordRevokes(c)

	boom := errors.New("database down")
	l.setErr(boom)
	l.remove("t1") // would drop the token if the failed read were applied
	if err := c.Load(ctx); !errors.Is(err, boom) {
		t.Fatalf("Load = %v, want %v", err, boom)
	}
	if err := c.RefreshFeed(ctx, "grpc-1"); !errors.Is(err, boom) {
		t.Fatalf("RefreshFeed = %v, want %v", err, boom)
	}
	if _, ok := c.Authenticate(s1); !ok {
		t.Error("a failed read dropped a token")
	}
	if got := gone(); len(got) != 0 {
		t.Fatalf("a failed read fired OnRevoke for %v", got)
	}
}

// A Load whose read went stale while a revocation was refreshed must not
// bring the revoked token back.
func TestLoadCannotUndoAConcurrentRefresh(t *testing.T) {
	ctx := context.Background()
	l := &memLoader{rows: map[string]Stored{}}
	secret := add(t, l, "t1", "grpc-1")
	c := NewCache(l, nil, nil)
	mustLoad(t, c)
	gone := recordRevokes(c)

	snapped := make(chan struct{})
	release := make(chan struct{})
	l.mu.Lock()
	l.afterSnapshot = func() { close(snapped); <-release }
	l.mu.Unlock()

	loadDone := make(chan error, 1)
	go func() { loadDone <- c.Load(ctx) }()
	<-snapped // the Load holds a snapshot that still shows t1 usable

	now := time.Now()
	l.mutate("t1", func(s *Stored) { s.RevokedAt = &now })
	refreshDone := make(chan error, 1)
	go func() { refreshDone <- c.RefreshFeed(ctx, "grpc-1") }()
	time.Sleep(50 * time.Millisecond) // let the refresh run, or queue behind the Load

	close(release)
	if err := <-loadDone; err != nil {
		t.Fatal(err)
	}
	if err := <-refreshDone; err != nil {
		t.Fatal(err)
	}
	if _, ok := c.Authenticate(secret); ok {
		t.Error("a stale Load brought a revoked token back")
	}
	if got := gone(); len(got) != 1 || got[0] != "t1" {
		t.Fatalf("OnRevoke saw %v, want [t1] once", got)
	}
}

func TestFlushUsedKeepsUsesWhenTheWriteFails(t *testing.T) {
	ctx := context.Background()
	l := &memLoader{rows: map[string]Stored{}}
	secret := add(t, l, "t1", "grpc-1")
	c := NewCache(l, nil, nil)
	mustLoad(t, c)

	if _, ok := c.Authenticate(secret); !ok {
		t.Fatal("did not authenticate")
	}
	l.setErr(errors.New("database down"))
	if err := c.FlushUsed(ctx); err == nil {
		t.Fatal("FlushUsed hid the failure")
	}
	l.setErr(nil)
	if _, ok := c.Authenticate(secret); !ok { // a later use of the same token
		t.Fatal("did not authenticate")
	}
	if err := c.FlushUsed(ctx); err != nil {
		t.Fatal(err)
	}
	if _, ok := l.used["t1"]; !ok {
		t.Fatal("the use was lost with the failed flush")
	}

	// The later time wins when a failed flush is merged back over a use
	// recorded while the flush was under way.
	early, late := time.Unix(100, 0), time.Unix(200, 0)
	c.usedMu.Lock()
	c.used["t1"] = late
	c.usedMu.Unlock()
	l.mu.Lock()
	l.err = errors.New("database down")
	l.duringMarkUsed = func() {
		c.usedMu.Lock()
		c.used["t1"] = early
		c.usedMu.Unlock()
	}
	l.mu.Unlock()
	if err := c.FlushUsed(ctx); err == nil {
		t.Fatal("FlushUsed hid the failure")
	}
	c.usedMu.Lock()
	got := c.used["t1"]
	c.usedMu.Unlock()
	if !got.Equal(late) {
		t.Fatalf("merged use = %v, want %v", got, late)
	}
}

// With no notice at all, the timer alone notices a revoked token.
func TestReconcileNoticesARevocationWithoutNotices(t *testing.T) {
	l := &memLoader{rows: map[string]Stored{}}
	secret := add(t, l, "t1", "grpc-1")
	c := NewCache(l, nil, nil)
	c.reconcileEvery = 10 * time.Millisecond
	mustLoad(t, c)
	gone := recordRevokes(c)

	ctx, stop := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.reconcile(ctx) }()

	if _, ok := c.Authenticate(secret); !ok {
		t.Fatal("the token did not authenticate before it was revoked")
	}
	now := time.Now()
	l.mutate("t1", func(s *Stored) { s.RevokedAt = &now })
	within(t, "the reconcile to drop the revoked token", func() bool { _, ok := c.Authenticate(secret); return !ok })
	time.Sleep(60 * time.Millisecond) // several more reconciles must not fire again
	stop()
	if err := <-done; err != nil {
		t.Fatalf("reconcile after cancel = %v", err)
	}
	if got := gone(); len(got) != 1 || got[0] != "t1" {
		t.Fatalf("OnRevoke saw %v, want [t1] once", got)
	}
}

// A failing reload ends the reconcile so the connection is rebuilt.
func TestReconcileReturnsAReloadError(t *testing.T) {
	l := &memLoader{rows: map[string]Stored{}}
	c := NewCache(l, nil, nil)
	c.reconcileEvery = 10 * time.Millisecond
	mustLoad(t, c)
	boom := errors.New("boom")
	l.setErr(boom)
	done := make(chan error, 1)
	go func() { done <- c.reconcile(context.Background()) }()
	select {
	case err := <-done:
		if !errors.Is(err, boom) {
			t.Fatalf("reconcile = %v, want boom", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("reconcile ignored a failing reload")
	}
}

func isClosed(ch <-chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

func TestReady(t *testing.T) {
	l := &memLoader{rows: map[string]Stored{}}
	c := NewCache(l, nil, nil)
	if isClosed(c.Ready()) {
		t.Fatal("Ready is closed before any Load")
	}
	l.setErr(errors.New("down"))
	if err := c.Load(context.Background()); err == nil {
		t.Fatal("Load should fail")
	}
	if isClosed(c.Ready()) {
		t.Fatal("Ready closed after a failed Load")
	}
	l.setErr(nil)
	mustLoad(t, c)
	if !isClosed(c.Ready()) {
		t.Fatal("Ready is still open after a successful Load")
	}
	mustLoad(t, c) // closing twice would panic
	if !isClosed(c.Ready()) {
		t.Fatal("Ready reopened")
	}
}

// A failed reconcile must wake a notice loop that is blocked waiting, so the
// connection ends and Run reconnects; the reconcile error is what is returned.
func TestTogetherEndsABlockedFollowWhenReconcileFails(t *testing.T) {
	boom := errors.New("reload failed")
	done := make(chan error, 1)
	go func() {
		done <- together(context.Background(),
			func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() },
			func(context.Context) error { return boom })
	}()
	select {
	case err := <-done:
		if !errors.Is(err, boom) {
			t.Fatalf("together = %v, want the reconcile error", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a failed reconcile left the notice loop blocked")
	}
}

// A notice loop that fails stops the reconciler and its error is returned.
func TestTogetherStopsReconcileWhenFollowFails(t *testing.T) {
	boom := errors.New("connection lost")
	stopped := make(chan struct{})
	err := together(context.Background(),
		func(context.Context) error { return boom },
		func(ctx context.Context) error { <-ctx.Done(); close(stopped); return nil })
	if !errors.Is(err, boom) {
		t.Fatalf("together = %v, want the follow error", err)
	}
	select {
	case <-stopped:
	default:
		t.Fatal("together returned before the reconciler stopped")
	}
}
