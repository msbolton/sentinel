package feed

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/sentinel/ingest-service/internal/aoi"
	"github.com/sentinel/ingest-service/internal/metrics"
	"github.com/sentinel/ingest-service/internal/registry"
	"github.com/sentinel/ingest-service/internal/source/driver"
)

type fakeRegistry struct {
	mu       sync.Mutex
	feeds    []registry.Feed
	areas    map[string]registry.Area
	statuses map[string]registry.Status
	released []string
	changes  chan registry.Change
}

func newFakeRegistry() *fakeRegistry {
	return &fakeRegistry{areas: map[string]registry.Area{}, statuses: map[string]registry.Status{}, changes: make(chan registry.Change, 8)}
}

func (f *fakeRegistry) List(context.Context) ([]registry.Feed, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]registry.Feed(nil), f.feeds...), nil
}
func (f *fakeRegistry) GetArea(_ context.Context, id string) (registry.Area, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	a, ok := f.areas[id]
	if !ok {
		return registry.Area{FeedID: id, Boxes: []registry.Box{}}, nil
	}
	return a, nil
}
func (f *fakeRegistry) UpsertStatus(_ context.Context, st registry.Status) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.statuses[st.FeedID] = st
	return nil
}
func (f *fakeRegistry) ReleaseStatuses(_ context.Context, replica string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.released = append(f.released, replica)
	return nil
}
func (f *fakeRegistry) Watch(context.Context) <-chan registry.Change { return f.changes }
func (f *fakeRegistry) set(feeds ...registry.Feed) {
	f.mu.Lock()
	f.feeds = feeds
	f.mu.Unlock()
}
func (f *fakeRegistry) status(id string) registry.Status {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.statuses[id]
}

// mockDriver blocks in Run until cancelled or told to fail.
type mockDriver struct {
	started, stopped atomic.Int32
	failRun          chan error
	mu               sync.Mutex
	area             *aoi.Area
}

func newMockDriver() *mockDriver { return &mockDriver{failRun: make(chan error, 1)} }

func (m *mockDriver) Run(ctx context.Context, sink driver.Sink) error {
	m.started.Add(1)
	defer m.stopped.Add(1)
	sink.Succeeded(1)
	select {
	case <-ctx.Done():
		return nil
	case err := <-m.failRun:
		return err
	}
}
func (m *mockDriver) Health() driver.Health {
	return driver.Health{Detail: map[string]any{"mock": true}}
}

type areaDriver struct{ *mockDriver }

func (a *areaDriver) SetAreaOfInterest(v aoi.Area) aoi.Status {
	a.mu.Lock()
	a.area = &v
	a.mu.Unlock()
	return aoi.Status{Active: v.Active, Boxes: v.Boxes, Idle: !v.Active, CreditsRemaining: -1}
}
func (a *areaDriver) AreaStatus() aoi.Status {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.area == nil {
		return aoi.Status{Idle: true, CreditsRemaining: -1}
	}
	return aoi.Status{Active: a.area.Active, Boxes: a.area.Boxes, Idle: !a.area.Active, CreditsRemaining: -1}
}

func feedSpec(id string, version int, enabled bool) registry.Feed {
	return registry.Feed{ID: id, Name: id, Driver: "mock", Config: json.RawMessage(`{"staleWarnSec": 1}`), SecretRefs: map[string]string{}, Enabled: enabled, Version: version}
}

func newSupervisor(reg *fakeRegistry, factory func(driver.Spec) (driver.Driver, error)) *Supervisor {
	return New(reg, &memPublisher{}, zap.NewNop(), metrics.New(nil), Options{
		ReplicaID: "r1", RetryMin: 10 * time.Millisecond, RetryMax: 20 * time.Millisecond,
		Factory: func(spec driver.Spec, _ driver.Deps) (driver.Driver, error) { return factory(spec) },
	})
}

func waitFor(cond func() bool, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return cond()
}

func TestReconcileStartsStopsAndRestartsOnVersion(t *testing.T) {
	reg := newFakeRegistry()
	var mu sync.Mutex
	var built []*mockDriver
	sup := newSupervisor(reg, func(driver.Spec) (driver.Driver, error) {
		d := newMockDriver()
		mu.Lock()
		built = append(built, d)
		mu.Unlock()
		return d, nil
	})
	ctx := context.Background()

	reg.set(feedSpec("a", 1, true), feedSpec("b", 1, false))
	sup.Reconcile(ctx)
	if !waitFor(func() bool { mu.Lock(); defer mu.Unlock(); return len(built) == 1 && built[0].started.Load() == 1 }, time.Second) || !sup.Running("a") || sup.Running("b") {
		t.Fatalf("after first reconcile: running a=%v b=%v", sup.Running("a"), sup.Running("b"))
	}
	st, _ := sup.Snapshot("a")
	if st.State != registry.StateRunning || st.Owner != "r1" || st.AppliedVersion != 1 {
		t.Errorf("snapshot = %+v", st)
	}

	// Version bump restarts with a new driver.
	reg.set(feedSpec("a", 2, true))
	sup.Reconcile(ctx)
	if !waitFor(func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(built) == 2 && built[0].stopped.Load() == 1 && built[1].started.Load() == 1
	}, time.Second) {
		t.Fatalf("version bump: built=%d stopped0=%d", len(built), built[0].stopped.Load())
	}
	// Disable stops it and writes a stopped status.
	reg.set(feedSpec("a", 2, false))
	sup.Reconcile(ctx)
	if sup.Running("a") || built[1].stopped.Load() != 1 || reg.status("a").State != registry.StateStopped {
		t.Fatalf("disable: running=%v stopped=%d status=%+v", sup.Running("a"), built[1].stopped.Load(), reg.status("a"))
	}
}

// Metrics are optional: a supervisor built without them still starts a feed
// and stops it when it loses the lease.
func TestSupervisorRunsWithoutMetrics(t *testing.T) {
	reg := newFakeRegistry()
	reg.set(feedSpec("a", 1, true))
	l := newFakeLeaser("r1")
	sup := New(reg, &memPublisher{}, zap.NewNop(), nil, Options{
		ReplicaID: "r1", Leaser: &replicaLeaser{fakeLeaser: l, me: "r1"}, Preferred: preferAlways("r1"),
		Factory: func(driver.Spec, driver.Deps) (driver.Driver, error) { return newMockDriver(), nil },
	})
	ctx := context.Background()
	sup.Reconcile(ctx)
	if !sup.Running("a") {
		t.Fatal("feed should run without metrics")
	}
	l.steal("a", "r9")
	sup.Reconcile(ctx)
	if sup.Running("a") {
		t.Fatal("a lost lease must stop the feed without metrics")
	}
}

func TestFailedStartRetriesWithBackoff(t *testing.T) {
	reg := newFakeRegistry()
	var attempts atomic.Int32
	sup := newSupervisor(reg, func(driver.Spec) (driver.Driver, error) {
		if attempts.Add(1) < 3 {
			return nil, errors.New("no broker")
		}
		return newMockDriver(), nil
	})
	ctx := context.Background()
	reg.set(feedSpec("a", 1, true))
	sup.Reconcile(ctx)
	st, _ := sup.Snapshot("a")
	if st.State != registry.StateError || st.LastError == "" || st.ErrorsTotal != 1 {
		t.Fatalf("after failed start: %+v", st)
	}
	sup.Reconcile(ctx) // too early: still in backoff
	if attempts.Load() != 1 {
		t.Fatalf("retried before the backoff elapsed: %d", attempts.Load())
	}
	time.Sleep(15 * time.Millisecond)
	sup.Reconcile(ctx)
	time.Sleep(25 * time.Millisecond)
	sup.Reconcile(ctx)
	if attempts.Load() != 3 || !sup.Running("a") {
		t.Fatalf("attempts=%d running=%v", attempts.Load(), sup.Running("a"))
	}
}

func TestDriverExitWithErrorRestarts(t *testing.T) {
	reg := newFakeRegistry()
	var mu sync.Mutex
	var built []*mockDriver
	sup := newSupervisor(reg, func(driver.Spec) (driver.Driver, error) {
		d := newMockDriver()
		mu.Lock()
		built = append(built, d)
		mu.Unlock()
		return d, nil
	})
	ctx := context.Background()
	reg.set(feedSpec("a", 1, true))
	sup.Reconcile(ctx)
	mu.Lock()
	first := built[0]
	mu.Unlock()
	first.failRun <- errors.New("connection lost")
	if !waitFor(func() bool { return first.stopped.Load() == 1 }, time.Second) {
		t.Fatal("driver did not exit")
	}
	if !waitFor(func() bool {
		st, _ := sup.Snapshot("a")
		return st.State == registry.StateError && st.LastError == "connection lost"
	}, time.Second) {
		st, _ := sup.Snapshot("a")
		t.Fatalf("exit not noticed: %+v", st)
	}
	time.Sleep(15 * time.Millisecond)
	sup.Reconcile(ctx)
	mu.Lock()
	n := len(built)
	mu.Unlock()
	if n != 2 || !sup.Running("a") {
		t.Fatalf("restart: built=%d running=%v", n, sup.Running("a"))
	}
}

func TestAreaAppliedOnStartAndPush(t *testing.T) {
	reg := newFakeRegistry()
	reg.areas["a"] = registry.Area{FeedID: "a", Active: true, Clients: 1, Boxes: []registry.Box{{North: 1, South: 0, East: 1, West: 0}}}
	ad := &areaDriver{newMockDriver()}
	sup := newSupervisor(reg, func(driver.Spec) (driver.Driver, error) { return ad, nil })
	ctx := context.Background()
	reg.set(feedSpec("a", 1, true))
	sup.Reconcile(ctx)
	ad.mu.Lock()
	applied := ad.area
	ad.mu.Unlock()
	if applied == nil || !applied.Active || len(applied.Boxes) != 1 {
		t.Fatalf("area not applied on start: %+v", applied)
	}
	st, ok := sup.SetArea("a", registry.Area{FeedID: "a", Active: false})
	if !ok || !st.Idle {
		t.Fatalf("SetArea = %+v %v", st, ok)
	}
	if _, ok := sup.AreaStatus("nope"); ok {
		t.Error("unknown feed must not report an area")
	}
	snap, _ := sup.Snapshot("a")
	if snap.State != registry.StateIdle {
		t.Errorf("idle driver should report idle state: %+v", snap)
	}
	var detail map[string]any
	_ = json.Unmarshal(snap.Detail, &detail)
	if detail["idle"] != true || detail["mock"] != true {
		t.Errorf("detail = %v", detail)
	}
}

func TestRunStopsEverythingOnCancel(t *testing.T) {
	reg := newFakeRegistry()
	d := newMockDriver()
	sup := newSupervisor(reg, func(driver.Spec) (driver.Driver, error) { return d, nil })
	reg.set(feedSpec("a", 1, true))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); sup.Run(ctx) }()
	if !waitFor(func() bool { return d.started.Load() == 1 }, time.Second) {
		t.Fatal("feed did not start")
	}
	reg.changes <- registry.Change{Channel: registry.ChannelFeeds, FeedID: "a"}
	time.Sleep(20 * time.Millisecond)
	cancel()
	<-done
	if d.stopped.Load() != 1 || len(reg.released) != 1 || reg.status("a").State != registry.StateStopped {
		t.Errorf("shutdown: stopped=%d released=%v status=%+v", d.stopped.Load(), reg.released, reg.status("a"))
	}
}
