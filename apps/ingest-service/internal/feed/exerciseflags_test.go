package feed

import (
	"context"
	"encoding/json"
	"sort"
	"sync"
	"testing"

	"go.uber.org/zap"

	"github.com/sentinel/ingest-service/internal/metrics"
	"github.com/sentinel/ingest-service/internal/registry"
	"github.com/sentinel/ingest-service/internal/source/driver"
)

func exerciseSpec(id string, version int, enabled, exercise bool) registry.Feed {
	f := feedSpec(id, version, enabled)
	if exercise {
		f.Config = json.RawMessage(`{"exercise": true}`)
	}
	return f
}

type changeLog struct {
	mu  sync.Mutex
	ids []string
}

func (c *changeLog) add(id string) { c.mu.Lock(); c.ids = append(c.ids, id); c.mu.Unlock() }
func (c *changeLog) take() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := append([]string(nil), c.ids...)
	c.ids = nil
	sort.Strings(out)
	return out
}

// The supervisor tells the drivers each listed connection's exercise marking,
// disabled ones included, and tells them when it changes, without a restart of
// the drivers that ask.
func TestSupervisorFollowsConnectionExerciseFlags(t *testing.T) {
	reg := newFakeRegistry()
	var (
		mu    sync.Mutex
		feeds driver.FeedSource
	)
	sup := New(reg, &memPublisher{}, zap.NewNop(), metrics.New(nil), Options{
		ReplicaID: "r1",
		Factory: func(_ driver.Spec, deps driver.Deps) (driver.Driver, error) {
			mu.Lock()
			feeds = deps.Feeds
			mu.Unlock()
			return newMockDriver(), nil
		},
	})
	ctx := context.Background()
	reg.set(exerciseSpec("ex", 1, true, true), exerciseSpec("off", 1, false, true), exerciseSpec("real", 1, true, false))
	sup.Reconcile(ctx)
	mu.Lock()
	got := feeds
	mu.Unlock()
	if got == nil {
		t.Fatal("a driver got no connection source")
	}
	for id, want := range map[string]struct{ exercise, known bool }{
		"ex": {true, true}, "off": {true, true}, "real": {false, true}, "none": {false, false},
	} {
		if ex, known := got.Exercise(id); ex != want.exercise || known != want.known {
			t.Errorf("%s: exercise %v known %v, want %v %v", id, ex, known, want.exercise, want.known)
		}
	}

	log := &changeLog{}
	cancel := got.OnExerciseChange(log.add)
	defer cancel()
	sup.Reconcile(ctx) // nothing changed
	if ids := log.take(); len(ids) != 0 {
		t.Fatalf("unchanged listing notified %v", ids)
	}
	// real is marked, ex is unmarked, off is deleted, a new exercise one is added.
	reg.set(exerciseSpec("ex", 2, true, false), exerciseSpec("real", 2, true, true), exerciseSpec("new", 1, true, true))
	sup.Reconcile(ctx)
	if ids := log.take(); len(ids) != 4 || ids[0] != "ex" || ids[1] != "new" || ids[2] != "off" || ids[3] != "real" {
		t.Errorf("notified %v, want ex new off real", ids)
	}
	if ex, known := got.Exercise("real"); !ex || !known {
		t.Errorf("real after edit: %v %v", ex, known)
	}
	if ex, known := got.Exercise("off"); ex || known {
		t.Errorf("off after delete: %v %v", ex, known)
	}
	cancel()
	reg.set(exerciseSpec("real", 3, true, false))
	sup.Reconcile(ctx)
	if ids := log.take(); len(ids) != 0 {
		t.Errorf("notified after cancel: %v", ids)
	}
}
