package stream

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"
)

type push struct {
	feed string
	v    IngestViewport
}

type fakeSink struct {
	mu     sync.Mutex
	pushes []push
	fail   bool
	gone   map[string]bool
}

func (f *fakeSink) Push(_ context.Context, feedID string, v IngestViewport) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail {
		return errors.New("boom")
	}
	if f.gone[feedID] {
		return ErrFeedGone
	}
	f.pushes = append(f.pushes, push{feed: feedID, v: v})
	return nil
}

func (f *fakeSink) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.pushes)
}

func (f *fakeSink) last() IngestViewport {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.pushes[len(f.pushes)-1].v
}

func (f *fakeSink) feeds() map[string]int {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[string]int{}
	for _, p := range f.pushes {
		out[p.feed]++
	}
	return out
}

type fakeDirectory struct {
	mu          sync.Mutex
	ids         []string
	invalidated int
}

func (d *fakeDirectory) AreaFeeds(context.Context) ([]string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.ids...), nil
}

func (d *fakeDirectory) Invalidate() {
	d.mu.Lock()
	d.invalidated++
	d.mu.Unlock()
}

func (f *fakeSink) setFail(v bool) {
	f.mu.Lock()
	f.fail = v
	f.mu.Unlock()
}

func waitCount(t *testing.T, s *fakeSink, n int, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if s.count() >= n {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return s.count() >= n
}

func newSyncUnderTest(t *testing.T) (*Hub, *fakeSink, *ViewportSync) {
	t.Helper()
	hub := NewHub(zap.NewNop())
	sink := &fakeSink{}
	sync := NewViewportSync(hub, sink, nil, ViewportSyncOptions{
		Debounce:   10 * time.Millisecond,
		MinSpacing: 40 * time.Millisecond,
		Heartbeat:  80 * time.Millisecond,
		MaxBackoff: 20 * time.Millisecond,
	}, zap.NewNop())
	hub.SetViewportListener(sync.Notify)
	sync.Start()
	t.Cleanup(sync.Stop)
	return hub, sink, sync
}

func TestViewportSync_BurstBecomesOnePush(t *testing.T) {
	hub, sink, _ := newSyncUnderTest(t)
	c := newClient("c1", hub, nil, zap.NewNop())
	hub.register(c)

	// A client without a viewport does not trigger a push on its own.
	time.Sleep(30 * time.Millisecond)
	if sink.count() != 0 {
		t.Fatalf("register alone pushed %d times", sink.count())
	}

	for i := 0; i < 5; i++ {
		c.setViewport(Bounds{North: 45, South: 40, East: -80, West: -90})
	}
	if !waitCount(t, sink, 1, time.Second) {
		t.Fatal("expected a push after the debounce")
	}
	time.Sleep(30 * time.Millisecond)
	if sink.count() != 1 {
		t.Errorf("burst should coalesce into one push, got %d", sink.count())
	}
	got := sink.last()
	if !got.Active || got.Clients != 1 || len(got.Boxes) != 1 || got.Boxes[0].North != 45.5 {
		t.Errorf("unexpected push %+v", got)
	}
}

func TestViewportSync_SmallPanDoesNotPushButHeartbeatDoes(t *testing.T) {
	hub, sink, _ := newSyncUnderTest(t)
	c := newClient("c1", hub, nil, zap.NewNop())
	hub.register(c)
	c.setViewport(Bounds{North: 45, South: 40, East: -80, West: -90})
	if !waitCount(t, sink, 1, time.Second) {
		t.Fatal("expected the first push")
	}

	c.setViewport(Bounds{North: 45.1, South: 40.1, East: -79.9, West: -89.9})
	time.Sleep(30 * time.Millisecond)
	if sink.count() != 1 {
		t.Errorf("a pan inside the padding must not push, got %d", sink.count())
	}

	// The heartbeat re-pushes while a client is connected.
	if !waitCount(t, sink, 2, time.Second) {
		t.Fatal("expected a heartbeat push")
	}
}

func TestViewportSync_LastClientLeavingPushesInactiveOnce(t *testing.T) {
	hub, sink, _ := newSyncUnderTest(t)
	c := newClient("c1", hub, nil, zap.NewNop())
	hub.register(c)
	c.setViewport(Bounds{North: 45, South: 40, East: -80, West: -90})
	if !waitCount(t, sink, 1, time.Second) {
		t.Fatal("expected the first push")
	}

	hub.unregister(c)
	if !waitCount(t, sink, 2, time.Second) {
		t.Fatal("expected an inactive push")
	}
	if got := sink.last(); got.Active || got.Clients != 0 || len(got.Boxes) != 0 {
		t.Errorf("expected inactive push, got %+v", got)
	}
	// No heartbeat for an idle state.
	time.Sleep(200 * time.Millisecond)
	if sink.count() != 2 {
		t.Errorf("idle state must not be re-pushed, got %d pushes", sink.count())
	}
}

func TestViewportSync_RetriesAfterFailure(t *testing.T) {
	hub, sink, _ := newSyncUnderTest(t)
	sink.setFail(true)
	c := newClient("c1", hub, nil, zap.NewNop())
	hub.register(c)
	c.setViewport(Bounds{North: 45, South: 40, East: -80, West: -90})

	time.Sleep(50 * time.Millisecond)
	if sink.count() != 0 {
		t.Fatalf("failing sink recorded %d pushes", sink.count())
	}
	sink.setFail(false)
	if !waitCount(t, sink, 1, time.Second) {
		t.Fatal("expected a retry to succeed")
	}
}

func TestViewportSync_StopPushesInactive(t *testing.T) {
	hub := NewHub(zap.NewNop())
	sink := &fakeSink{}
	sync := NewViewportSync(hub, sink, nil, ViewportSyncOptions{Debounce: 10 * time.Millisecond}, zap.NewNop())
	hub.SetViewportListener(sync.Notify)
	sync.Start()
	c := newClient("c1", hub, nil, zap.NewNop())
	hub.register(c)
	c.setViewport(Bounds{North: 45, South: 40, East: -80, West: -90})
	if !waitCount(t, sink, 1, time.Second) {
		t.Fatal("expected the first push")
	}
	sync.Stop()
	if sink.count() != 2 || sink.last().Active {
		t.Errorf("Stop should push inactive once, got %d pushes, last %+v", sink.count(), sink.last())
	}
}

func TestHub_ViewportsOnlyIncludeClientsThatSentOne(t *testing.T) {
	hub := NewHub(zap.NewNop())
	calls := 0
	hub.SetViewportListener(func() { calls++ })
	a := newClient("a", hub, nil, zap.NewNop())
	b := newClient("b", hub, nil, zap.NewNop())
	hub.register(a)
	hub.register(b)
	if got := hub.Viewports(); len(got) != 0 {
		t.Errorf("fresh clients must not report a viewport, got %v", got)
	}
	a.handleInbound([]byte(`{"type":"viewport:update","data":{"north":10,"south":0,"east":10,"west":0}}`))
	if got := hub.Viewports(); len(got) != 1 || got[0].North != 10 {
		t.Errorf("expected a's viewport, got %v", got)
	}
	if calls != 1 {
		t.Errorf("listener calls after one viewport = %d", calls)
	}
	hub.unregister(a)
	if calls != 2 {
		t.Errorf("unregister must notify, calls = %d", calls)
	}
	hub.unregister(a) // already gone: no extra notification
	if calls != 2 {
		t.Errorf("double unregister must not notify, calls = %d", calls)
	}
}

func TestViewportSync_PushesToEveryListedFeed(t *testing.T) {
	hub := NewHub(zap.NewNop())
	sink := &fakeSink{}
	dir := &fakeDirectory{ids: []string{"opensky", "adsb-lol"}}
	sync := NewViewportSync(hub, sink, dir, ViewportSyncOptions{Debounce: 10 * time.Millisecond, MinSpacing: 20 * time.Millisecond}, zap.NewNop())
	hub.SetViewportListener(sync.Notify)
	sync.Start()
	t.Cleanup(sync.Stop)
	c := newClient("c1", hub, nil, zap.NewNop())
	hub.register(c)
	c.setViewport(Bounds{North: 45, South: 40, East: -80, West: -90})
	if !waitCount(t, sink, 2, time.Second) {
		t.Fatal("expected one push per feed")
	}
	if got := sink.feeds(); got["opensky"] != 1 || got["adsb-lol"] != 1 {
		t.Errorf("pushes per feed = %v", got)
	}
	if v := sync.LastPushed("adsb-lol"); v == nil || !v.Active {
		t.Errorf("LastPushed(adsb-lol) = %+v", v)
	}
}

func TestViewportSync_GoneFeedIsForgottenAndDirectoryInvalidated(t *testing.T) {
	hub := NewHub(zap.NewNop())
	sink := &fakeSink{gone: map[string]bool{"adsb-lol": true}}
	dir := &fakeDirectory{ids: []string{"opensky", "adsb-lol"}}
	sync := NewViewportSync(hub, sink, dir, ViewportSyncOptions{Debounce: 10 * time.Millisecond}, zap.NewNop())
	hub.SetViewportListener(sync.Notify)
	sync.Start()
	t.Cleanup(sync.Stop)
	c := newClient("c1", hub, nil, zap.NewNop())
	hub.register(c)
	c.setViewport(Bounds{North: 45, South: 40, East: -80, West: -90})
	if !waitCount(t, sink, 1, time.Second) {
		t.Fatal("expected the opensky push")
	}
	time.Sleep(30 * time.Millisecond)
	dir.mu.Lock()
	inv := dir.invalidated
	dir.mu.Unlock()
	if inv == 0 {
		t.Error("a gone feed must invalidate the directory")
	}
	if sync.LastPushed("adsb-lol") != nil {
		t.Error("a gone feed must not be remembered")
	}
	if sink.count() != 1 {
		t.Errorf("a gone feed must not be retried as an error, pushes = %d", sink.count())
	}
}
