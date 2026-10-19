package emplacement

import (
	"context"
	"slices"
	"sync"
	"testing"
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
