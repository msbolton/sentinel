package feed

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/sentinel/ingest-service/internal/emplacement"
	"github.com/sentinel/ingest-service/internal/registry"
	"github.com/sentinel/ingest-service/internal/source/driver"
)

func livenessOf(states ...emplacement.HeartbeatState) []emplacement.Liveness {
	out := make([]emplacement.Liveness, len(states))
	for i, s := range states {
		out[i] = emplacement.Liveness{ID: string(rune('a' + i)), State: s}
	}
	return out
}

func TestHeartbeatRollup(t *testing.T) {
	on, off, never, pend := emplacement.HeartbeatOnline, emplacement.HeartbeatOffline, emplacement.HeartbeatNever, emplacement.HeartbeatPending
	for _, c := range []struct {
		name          string
		list          []emplacement.Liveness
		health, state string
		pending       bool
	}{
		{"all online", livenessOf(on, on), registry.HealthHealthy, registry.StateRunning, false},
		{"some online", livenessOf(on, off), registry.HealthWarn, registry.StateDegraded, false},
		{"none online", livenessOf(off, never), registry.HealthCritical, registry.StateDegraded, false},
		{"all pending", livenessOf(pend, pend), registry.HealthHealthy, registry.StateRunning, true},
		{"online and pending", livenessOf(on, pend), registry.HealthHealthy, registry.StateRunning, true},
		{"pending and offline", livenessOf(pend, off), registry.HealthWarn, registry.StateDegraded, false},
	} {
		h, s, p := heartbeatRollup(c.list)
		if h != c.health || s != c.state || p != c.pending {
			t.Errorf("%s: %s %s %v; want %s %s %v", c.name, h, s, p, c.health, c.state, c.pending)
		}
	}
}

// livenessDriver reports the emplacements a test sets.
type livenessDriver struct {
	*mockDriver
	mu   sync.Mutex
	list []emplacement.Liveness
}

func (d *livenessDriver) set(list []emplacement.Liveness) { d.mu.Lock(); d.list = list; d.mu.Unlock() }
func (d *livenessDriver) Health() driver.Health {
	d.mu.Lock()
	defer d.mu.Unlock()
	return driver.Health{Detail: map[string]any{}, Emplacements: append([]emplacement.Liveness(nil), d.list...)}
}

// A connection with emplacements is as healthy as their heartbeats, whatever
// its report age says, and its status carries them.
func TestStatusRollsUpFromEmplacements(t *testing.T) {
	reg := newFakeRegistry()
	d := &livenessDriver{mockDriver: newMockDriver()}
	sup := newSupervisor(reg, func(driver.Spec) (driver.Driver, error) { return d, nil })
	reg.set(feedSpec("a", 1, true))
	sup.Reconcile(context.Background())
	if !waitFor(func() bool { return d.started.Load() == 1 }, time.Second) {
		t.Fatal("not started")
	}
	snap := func() (registry.Status, map[string]json.RawMessage) {
		st, _ := sup.Snapshot("a")
		var detail map[string]json.RawMessage
		_ = json.Unmarshal(st.Detail, &detail)
		return st, detail
	}
	d.set(livenessOf(emplacement.HeartbeatOnline, emplacement.HeartbeatOffline))
	st, detail := snap()
	if st.Health != registry.HealthWarn || st.State != registry.StateDegraded {
		t.Fatalf("one of two online: %s %s", st.Health, st.State)
	}
	var list []emplacement.Liveness
	if err := json.Unmarshal(detail["emplacements"], &list); err != nil || len(list) != 2 {
		t.Fatalf("detail.emplacements = %s", detail["emplacements"])
	}
	d.set(livenessOf(emplacement.HeartbeatOffline))
	if st, _ := snap(); st.Health != registry.HealthCritical || st.State != registry.StateDegraded {
		t.Fatalf("none online: %s %s", st.Health, st.State)
	}
	d.set(livenessOf(emplacement.HeartbeatPending))
	st, detail = snap()
	if st.Health != registry.HealthHealthy || st.State != registry.StateRunning || string(detail["heartbeat"]) != `"pending"` {
		t.Fatalf("pending: %s %s %s", st.Health, st.State, detail["heartbeat"])
	}
	d.set(livenessOf(emplacement.HeartbeatOnline))
	if st, _ := snap(); st.Health != registry.HealthHealthy || st.State != registry.StateRunning {
		t.Fatalf("all online: %s %s", st.Health, st.State)
	}
}
