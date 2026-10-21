package emplacement

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"
)

type memLoader struct {
	mu   sync.Mutex
	rows map[string]Emplacement
}

func (m *memLoader) List(context.Context, Filter) ([]Emplacement, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []Emplacement{}
	for _, e := range m.rows {
		out = append(out, e)
	}
	return out, nil
}

func (m *memLoader) Get(_ context.Context, id string) (Emplacement, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.rows[id]
	if !ok {
		return Emplacement{}, ErrNotFound
	}
	return e, nil
}

func (m *memLoader) put(e Emplacement) { m.mu.Lock(); m.rows[e.ID] = e; m.mu.Unlock() }
func (m *memLoader) drop(id string)    { m.mu.Lock(); delete(m.rows, id); m.mu.Unlock() }

func row(id, name string, version int, feed string) Emplacement {
	e := Emplacement{ID: id, Name: name, Version: version, Mobility: MobilityFixed, Active: true}
	if feed != "" {
		e.FeedID = &feed
	}
	return e
}

func TestCacheLoadRefreshAndSubscribers(t *testing.T) {
	ctx := context.Background()
	l := &memLoader{rows: map[string]Emplacement{"a": row("a", "Bravo", 1, "grpc-1"), "b": row("b", "alpha", 1, "")}}
	c := NewCache(l, nil, nil)
	var mu sync.Mutex
	var seen []string
	cancel := c.OnChange(func(id string) { mu.Lock(); seen = append(seen, id); mu.Unlock() })

	if err := c.Load(ctx); err != nil {
		t.Fatal(err)
	}
	if all := c.All(); len(all) != 2 || all[0].Name != "alpha" {
		t.Fatalf("all = %v (sorted by name, case-insensitively)", all)
	}
	if got := c.ByFeed("grpc-1"); len(got) != 1 || got[0].ID != "a" {
		t.Fatalf("by feed = %v", got)
	}

	l.put(row("a", "Bravo", 2, "grpc-1"))
	if err := c.Refresh(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	if e, _ := c.Get("a"); e.Version != 2 {
		t.Fatalf("refresh kept version %d", e.Version)
	}
	l.drop("b")
	_ = c.Refresh(ctx, "b")
	if _, ok := c.Get("b"); ok {
		t.Fatal("a deleted emplacement must leave the cache")
	}

	mu.Lock()
	got := append([]string(nil), seen...)
	mu.Unlock()
	slices.Sort(got[:2])
	if !slices.Equal(got, []string{"a", "b", "a", "b"}) {
		t.Fatalf("subscriber saw %v", got)
	}
	cancel()
	_ = c.Refresh(ctx, "a")
	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 4 {
		t.Fatalf("a cancelled subscriber was called: %v", seen)
	}
}

// Review focus: what changed while LISTEN was down is caught by the reload
// that follows every reconnect.
func TestCacheReloadReportsWhatChangedWhileAway(t *testing.T) {
	ctx := context.Background()
	l := &memLoader{rows: map[string]Emplacement{"a": row("a", "A", 1, ""), "b": row("b", "B", 1, ""), "c": row("c", "C", 1, "")}}
	c := NewCache(l, nil, nil)
	if err := c.Load(ctx); err != nil {
		t.Fatal(err)
	}
	var seen []string
	c.OnChange(func(id string) { seen = append(seen, id) })

	l.put(row("a", "A", 2, "")) // edited while away
	l.drop("b")                 // deleted while away
	l.put(row("d", "D", 1, "")) // created while away
	if err := c.Load(ctx); err != nil {
		t.Fatal(err)
	}
	slices.Sort(seen)
	if !slices.Equal(seen, []string{"a", "b", "d"}) {
		t.Fatalf("reload reported %v, want a b d (c unchanged)", seen)
	}
	if _, ok := c.Get("b"); ok {
		t.Fatal("b must be gone")
	}
}

var errTransient = errors.New("connection reset")

// getFailLoader is a memLoader whose Get fails with a transient error while fail is set.
type getFailLoader struct {
	*memLoader
	fail bool
}

func (g *getFailLoader) Get(ctx context.Context, id string) (Emplacement, error) {
	if g.fail {
		return Emplacement{}, errTransient
	}
	return g.memLoader.Get(ctx, id)
}

func TestCacheHandleReturnsRefreshFailureAndKeepsEntry(t *testing.T) {
	ctx := context.Background()
	l := &getFailLoader{memLoader: &memLoader{rows: map[string]Emplacement{"a": row("a", "A", 1, "")}}}
	c := NewCache(l, nil, nil)
	if err := c.Load(ctx); err != nil {
		t.Fatal(err)
	}
	var seen []string
	c.OnChange(func(id string) { seen = append(seen, id) })

	l.put(row("a", "A", 2, "")) // changed in the store, but the read fails
	l.fail = true
	if err := c.handle(ctx, `{"id":"a","op":"upsert"}`); !errors.Is(err, errTransient) {
		t.Fatalf("handle err = %v, want the transient Get error", err)
	}
	if e, ok := c.Get("a"); !ok || e.Version != 1 {
		t.Fatalf("entry after a failed refresh = version %d (present %v), want the old version 1", e.Version, ok)
	}
	if len(seen) != 0 {
		t.Fatalf("subscriber saw %v after a failed refresh", seen)
	}
}

func TestCacheHandleDeletedIDLeavesCache(t *testing.T) {
	ctx := context.Background()
	l := &memLoader{rows: map[string]Emplacement{"a": row("a", "A", 1, "")}}
	c := NewCache(l, nil, nil)
	if err := c.Load(ctx); err != nil {
		t.Fatal(err)
	}
	var seen []string
	c.OnChange(func(id string) { seen = append(seen, id) })

	l.drop("a")
	if err := c.handle(ctx, `{"id":"a","op":"delete"}`); err != nil {
		t.Fatalf("handle of a deleted id = %v, want nil", err)
	}
	if _, ok := c.Get("a"); ok {
		t.Fatal("a deleted emplacement must leave the cache")
	}
	if !slices.Equal(seen, []string{"a"}) {
		t.Fatalf("subscriber saw %v, want [a]", seen)
	}
}

func TestCacheHandleIgnoresPayloadWithoutID(t *testing.T) {
	ctx := context.Background()
	c := NewCache(&memLoader{rows: map[string]Emplacement{}}, nil, nil)
	var seen []string
	c.OnChange(func(id string) { seen = append(seen, id) })

	for _, p := range []string{"", "not json", `{"op":"delete"}`, `{"id":"","op":"upsert"}`} {
		if err := c.handle(ctx, p); err != nil {
			t.Fatalf("handle(%q) = %v, want nil", p, err)
		}
	}
	if len(seen) != 0 {
		t.Fatalf("subscriber saw %v for payloads without an id", seen)
	}
}

// Needs TEST_DATABASE_URL: a real write reaches the cache through Run's LISTEN.
func TestCacheFollowsStoreWrites(t *testing.T) {
	pool, ctx := testPool(t)
	store := NewPGStore(pool)
	c := NewCache(store, pool, nil)
	changed := make(chan string, 64)
	c.OnChange(func(id string) { changed <- id })

	runCtx, stop := context.WithCancel(ctx)
	defer stop()
	go c.Run(runCtx)

	created, err := store.Create(ctx, fixed("test-cache-follows"))
	if err != nil {
		t.Fatal(err)
	}
	timeout := time.After(10 * time.Second)
	for {
		select {
		case id := <-changed:
			if id != created.ID {
				continue
			}
			if e, ok := c.Get(created.ID); ok && e.Version == created.Version {
				return
			}
		case <-timeout:
			t.Fatal("the cache never reported the created emplacement")
		}
	}
}

type failingLoader struct {
	Loader
	fail bool
}

func (f *failingLoader) List(ctx context.Context, fl Filter) ([]Emplacement, error) {
	if f.fail {
		return nil, errors.New("database down")
	}
	return f.Loader.List(ctx, fl)
}

func TestCacheReadyClosesAfterTheFirstSuccessfulLoad(t *testing.T) {
	ctx := context.Background()
	l := &failingLoader{Loader: &memLoader{rows: map[string]Emplacement{}}, fail: true}
	c := NewCache(l, nil, nil)
	isReady := func() bool {
		select {
		case <-c.Ready():
			return true
		default:
			return false
		}
	}
	if isReady() {
		t.Fatal("ready before any load")
	}
	if err := c.Load(ctx); err == nil {
		t.Fatal("load should have failed")
	}
	if isReady() {
		t.Fatal("ready after a failed load")
	}
	l.fail = false
	if err := c.Load(ctx); err != nil {
		t.Fatal(err)
	}
	if !isReady() {
		t.Fatal("not ready after a successful load")
	}
	if err := c.Load(ctx); err != nil { // closing twice would panic
		t.Fatal(err)
	}
}
