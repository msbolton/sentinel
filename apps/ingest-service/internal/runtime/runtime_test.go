package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/sentinel/ingest-service/internal/registry"
	"github.com/sentinel/ingest-service/internal/sources"
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

type mockListener struct {
	started, stopped atomic.Int32
	startErr         error
	mu               sync.Mutex
	viewport         *sources.Viewport
}

func (m *mockListener) Start() error { m.started.Add(1); return m.startErr }
func (m *mockListener) Stop()        { m.stopped.Add(1) }

type areaListener struct{ mockListener }

func (a *areaListener) SetViewport(v sources.Viewport) sources.ViewportStatus {
	a.mu.Lock()
	a.viewport = &v
	a.mu.Unlock()
	return sources.ViewportStatus{Active: v.Active, Boxes: v.Boxes, Idle: !v.Active, CreditsRemaining: -1}
}
func (a *areaListener) ViewportStatus() sources.ViewportStatus {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.viewport == nil {
		return sources.ViewportStatus{Idle: true, CreditsRemaining: -1}
	}
	return sources.ViewportStatus{Active: a.viewport.Active, Boxes: a.viewport.Boxes, Idle: !a.viewport.Active, CreditsRemaining: -1}
}

func feed(id string, version int, enabled bool) registry.Feed {
	return registry.Feed{ID: id, Name: id, Driver: "mock", Config: json.RawMessage(`{"staleWarnSec": 1}`), SecretRefs: map[string]string{}, Enabled: enabled, Version: version}
}

func newRuntime(reg *fakeRegistry, make func(registry.Feed, Deps) (sources.Listener, error)) *Runtime {
	factories := map[string]Factory{"mock": func(spec registry.Feed, _ map[string]string, deps Deps) (sources.Listener, error) {
		return make(spec, deps)
	}}
	return New(reg, factories, nil, zap.NewNop(), nil, Options{ReplicaID: "r1", RetryMin: 10 * time.Millisecond, RetryMax: 20 * time.Millisecond})
}

func TestReconcileStartsStopsAndRestartsOnVersion(t *testing.T) {
	reg := newFakeRegistry()
	var built []*mockListener
	rt := newRuntime(reg, func(registry.Feed, Deps) (sources.Listener, error) {
		l := &mockListener{}
		built = append(built, l)
		return l, nil
	})
	ctx := context.Background()

	reg.set(feed("a", 1, true), feed("b", 1, false))
	rt.Reconcile(ctx)
	if len(built) != 1 || built[0].started.Load() != 1 || !rt.Running("a") || rt.Running("b") {
		t.Fatalf("after first reconcile: built=%d running a=%v b=%v", len(built), rt.Running("a"), rt.Running("b"))
	}
	rt.flushStatus(ctx)
	if st := reg.status("a"); st.State != registry.StateRunning || st.Owner != "r1" || st.AppliedVersion != 1 {
		t.Fatalf("status a = %+v", st)
	}

	// Same version: nothing happens.
	rt.Reconcile(ctx)
	if len(built) != 1 {
		t.Fatalf("unchanged spec rebuilt the listener")
	}

	// Version bump: stop the old, start a new one.
	reg.set(feed("a", 2, true))
	rt.Reconcile(ctx)
	if len(built) != 2 || built[0].stopped.Load() != 1 || built[1].started.Load() != 1 {
		t.Fatalf("version bump: built=%d stopped0=%d started1=%d", len(built), built[0].stopped.Load(), built[1].started.Load())
	}

	// Disabled: stopped and status cleared.
	reg.set(feed("a", 3, false))
	rt.Reconcile(ctx)
	if built[1].stopped.Load() != 1 || rt.Running("a") {
		t.Fatalf("disable did not stop the listener")
	}
	if st := reg.status("a"); st.State != registry.StateStopped || st.Owner != "" {
		t.Fatalf("stopped status = %+v", st)
	}
}

func TestStartFailureIsRetriedWithBackoff(t *testing.T) {
	reg := newFakeRegistry()
	attempts := 0
	rt := newRuntime(reg, func(registry.Feed, Deps) (sources.Listener, error) {
		attempts++
		if attempts < 3 {
			return nil, errors.New("broker down")
		}
		return &mockListener{}, nil
	})
	ctx := context.Background()
	reg.set(feed("a", 1, true))

	rt.Reconcile(ctx)
	rt.flushStatus(ctx)
	if st := reg.status("a"); st.State != registry.StateError || st.LastError == "" || st.ErrorsTotal != 1 {
		t.Fatalf("status after failure = %+v", st)
	}
	rt.Reconcile(ctx) // inside the retry delay: no attempt
	if attempts != 1 {
		t.Fatalf("retried before the backoff elapsed: %d", attempts)
	}
	time.Sleep(15 * time.Millisecond)
	rt.Reconcile(ctx)
	time.Sleep(25 * time.Millisecond)
	rt.Reconcile(ctx)
	if attempts != 3 || !rt.Running("a") {
		t.Fatalf("attempts=%d running=%v", attempts, rt.Running("a"))
	}
}

func TestHealthFromCallbacks(t *testing.T) {
	reg := newFakeRegistry()
	var deps Deps
	rt := newRuntime(reg, func(_ registry.Feed, d Deps) (sources.Listener, error) {
		deps = d
		return &mockListener{}, nil
	})
	ctx := context.Background()
	reg.set(feed("a", 1, true))
	rt.Reconcile(ctx)

	if st, _ := rt.Snapshot("a"); st.Health != registry.HealthUnknown {
		t.Fatalf("health before any success = %s", st.Health)
	}
	deps.OnSuccess(7, time.Now())
	deps.OnError()
	st, _ := rt.Snapshot("a")
	if st.Health != registry.HealthHealthy || st.EntitiesLast != 7 || st.MessagesTotal != 7 || st.ErrorsTotal != 1 || st.LastSuccessAt == nil {
		t.Fatalf("status = %+v", st)
	}
	deps.OnSuccess(1, time.Now().Add(-2*time.Second)) // staleWarnSec is 1 in the fixture
	if st, _ := rt.Snapshot("a"); st.Health != registry.HealthWarn {
		t.Fatalf("stale health = %s", st.Health)
	}
}

func TestAreaIsAppliedOnStartAndOnChange(t *testing.T) {
	reg := newFakeRegistry()
	l := &areaListener{}
	rt := newRuntime(reg, func(registry.Feed, Deps) (sources.Listener, error) { return l, nil })
	ctx := context.Background()
	reg.set(feed("a", 1, true))
	reg.areas["a"] = registry.Area{FeedID: "a", Active: true, Clients: 1, Boxes: []registry.Box{{North: 2, South: 1, East: 2, West: 1}}}

	rt.Reconcile(ctx)
	if l.viewport == nil || !l.viewport.Active || len(l.viewport.Boxes) != 1 || l.viewport.Boxes[0].North != 2 {
		t.Fatalf("area not applied on start: %+v", l.viewport)
	}

	st, ok := rt.SetArea("a", registry.Area{FeedID: "a", Active: false})
	if !ok || st.Active {
		t.Fatalf("SetArea = %+v ok=%v", st, ok)
	}
	if _, ok := rt.SetArea("nope", registry.Area{}); ok {
		t.Fatal("unknown feed must report not ok")
	}
	if snap, _ := rt.Snapshot("a"); snap.State != registry.StateIdle {
		t.Fatalf("idle listener should report state idle, got %s", snap.State)
	}

	past := time.Now().Add(-time.Minute)
	reg.areas["a"] = registry.Area{FeedID: "a", Active: true, Boxes: []registry.Box{{North: 1}}, ExpiresAt: &past}
	rt.applyArea(ctx, "a")
	if l.viewport.Active {
		t.Fatal("expired area must be applied as inactive")
	}
}

func TestRunStopsEverythingOnCancel(t *testing.T) {
	reg := newFakeRegistry()
	l := &mockListener{}
	rt := newRuntime(reg, func(registry.Feed, Deps) (sources.Listener, error) { return l, nil })
	reg.set(feed("a", 1, true))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { rt.Run(ctx); close(done) }()
	deadline := time.Now().Add(time.Second)
	for !rt.Running("a") && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	reg.changes <- registry.Change{Channel: registry.ChannelFeeds, FeedID: "a"}
	cancel()
	<-done
	if l.stopped.Load() != 1 || len(reg.released) != 1 || reg.released[0] != "r1" {
		t.Fatalf("stopped=%d released=%v", l.stopped.Load(), reg.released)
	}
}
