package feeds

import (
	"errors"
	"sync/atomic"
	"testing"

	"go.uber.org/zap"

	"github.com/sentinel/ingest-service/internal/sources"
)

// mockListener is a test double that tracks Start/Stop calls.
type mockListener struct {
	started atomic.Int32
	stopped atomic.Int32
	startFn func() error // optional override
}

func (m *mockListener) Start() error {
	m.started.Add(1)
	if m.startFn != nil {
		return m.startFn()
	}
	return nil
}

func (m *mockListener) Stop() {
	m.stopped.Add(1)
}

func newMockFactory(ml *mockListener) func() (sources.Listener, error) {
	return func() (sources.Listener, error) {
		return ml, nil
	}
}

func testLogger() *zap.Logger {
	return zap.NewNop()
}

func TestRegisterAndList(t *testing.T) {
	mgr := NewManager(testLogger())

	err := mgr.Register("feed1", "Feed One", "test", "description", newMockFactory(&mockListener{}), false)
	if err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	err = mgr.Register("feed2", "Feed Two", "test", "description 2", newMockFactory(&mockListener{}), false)
	if err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	feeds := mgr.List()
	if len(feeds) != 2 {
		t.Fatalf("expected 2 feeds, got %d", len(feeds))
	}
	if feeds[0].ID != "feed1" || feeds[1].ID != "feed2" {
		t.Errorf("unexpected feed order: %v", feeds)
	}
	if feeds[0].Enabled || feeds[1].Enabled {
		t.Error("expected feeds to be disabled")
	}
}

func TestRegisterDuplicate(t *testing.T) {
	mgr := NewManager(testLogger())

	_ = mgr.Register("dup", "Dup", "test", "d", newMockFactory(&mockListener{}), false)
	err := mgr.Register("dup", "Dup Again", "test", "d", newMockFactory(&mockListener{}), false)
	if err == nil {
		t.Fatal("expected error registering duplicate feed")
	}
}

func TestRegisterWithStartNow(t *testing.T) {
	ml := &mockListener{}
	mgr := NewManager(testLogger())

	err := mgr.Register("auto", "Auto Start", "test", "d", newMockFactory(ml), true)
	if err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	feeds := mgr.List()
	if !feeds[0].Enabled {
		t.Error("expected feed to be enabled after startNow=true")
	}
	if ml.started.Load() != 1 {
		t.Errorf("expected 1 Start call, got %d", ml.started.Load())
	}
}

func TestSetEnabled(t *testing.T) {
	ml := &mockListener{}
	mgr := NewManager(testLogger())

	// Use a factory that returns a new mock each time but tracks via shared pointer.
	callCount := &atomic.Int32{}
	factory := func() (sources.Listener, error) {
		callCount.Add(1)
		return ml, nil
	}

	_ = mgr.Register("toggle", "Toggle", "test", "d", factory, false)

	// Enable.
	status, err := mgr.SetEnabled("toggle", true)
	if err != nil {
		t.Fatalf("SetEnabled(true) failed: %v", err)
	}
	if !status.Enabled {
		t.Error("expected enabled=true")
	}
	if ml.started.Load() != 1 {
		t.Errorf("expected 1 Start call, got %d", ml.started.Load())
	}

	// Disable.
	status, err = mgr.SetEnabled("toggle", false)
	if err != nil {
		t.Fatalf("SetEnabled(false) failed: %v", err)
	}
	if status.Enabled {
		t.Error("expected enabled=false")
	}
	if ml.stopped.Load() != 1 {
		t.Errorf("expected 1 Stop call, got %d", ml.stopped.Load())
	}

	// Idempotent — setting same state is a no-op.
	status, err = mgr.SetEnabled("toggle", false)
	if err != nil {
		t.Fatalf("idempotent SetEnabled failed: %v", err)
	}
	if ml.stopped.Load() != 1 {
		t.Errorf("expected no additional Stop call, got %d", ml.stopped.Load())
	}
}

func TestSetEnabledUnknownFeed(t *testing.T) {
	mgr := NewManager(testLogger())

	_, err := mgr.SetEnabled("nonexistent", true)
	if err == nil {
		t.Fatal("expected error for unknown feed")
	}
}

func TestSetEnabledStartError(t *testing.T) {
	ml := &mockListener{startFn: func() error { return errors.New("connection refused") }}
	mgr := NewManager(testLogger())

	_ = mgr.Register("fail", "Fail Feed", "test", "d", newMockFactory(ml), false)

	_, err := mgr.SetEnabled("fail", true)
	if err == nil {
		t.Fatal("expected error when Start fails")
	}

	feeds := mgr.List()
	if feeds[0].Enabled {
		t.Error("feed should remain disabled after failed start")
	}
}

func TestStopAll(t *testing.T) {
	ml1 := &mockListener{}
	ml2 := &mockListener{}
	mgr := NewManager(testLogger())

	_ = mgr.Register("a", "A", "test", "d", newMockFactory(ml1), true)
	_ = mgr.Register("b", "B", "test", "d", newMockFactory(ml2), true)

	mgr.StopAll()

	if ml1.stopped.Load() != 1 {
		t.Errorf("expected feed a to be stopped")
	}
	if ml2.stopped.Load() != 1 {
		t.Errorf("expected feed b to be stopped")
	}

	feeds := mgr.List()
	for _, f := range feeds {
		if f.Enabled {
			t.Errorf("expected feed %s to be disabled after StopAll", f.ID)
		}
	}
}
