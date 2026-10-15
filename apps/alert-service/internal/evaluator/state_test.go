package evaluator

import (
	"testing"
	"time"
)

func TestGeofenceTransition(t *testing.T) {
	in, out := true, false
	cases := []struct {
		name string
		prev *bool
		cur  bool
		mode string
		want string
	}{
		{"first seen inside, entry mode", nil, true, "ENTRY", "GEOFENCE_ENTRY"},
		{"first seen outside raises nothing", nil, false, "BOTH", ""},
		{"stays inside raises nothing", &in, true, "ENTRY", ""},
		{"stays outside raises nothing", &out, false, "BOTH", ""},
		{"enters", &out, true, "BOTH", "GEOFENCE_ENTRY"},
		{"exits", &in, false, "BOTH", "GEOFENCE_EXIT"},
		{"exits but mode is entry only", &in, false, "ENTRY", ""},
		{"enters but mode is exit only", &out, true, "EXIT", ""},
	}
	for _, c := range cases {
		if got := geofenceTransition(c.prev, c.cur, c.mode); got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
}

func TestRuleStateTracker_FiresOnEdgesWithCooldown(t *testing.T) {
	tr := newRuleStateTracker()
	t0 := time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)
	entry := func(prev *bool, cur bool) string { return geofenceTransition(prev, cur, "BOTH") }

	if got := tr.transition("r", "e", true, t0, entry); got != "GEOFENCE_ENTRY" {
		t.Fatalf("first fix inside should fire entry, got %q", got)
	}
	for i := 1; i <= 5; i++ {
		if got := tr.transition("r", "e", true, t0.Add(time.Duration(i)*10*time.Second), entry); got != "" {
			t.Fatalf("fix %d inside must not re-fire, got %q", i, got)
		}
	}
	if got := tr.transition("r", "e", false, t0.Add(time.Minute), entry); got != "GEOFENCE_EXIT" {
		t.Fatalf("leaving should fire exit, got %q", got)
	}
	// Re-entering within the cooldown is suppressed; after it, it fires again.
	if got := tr.transition("r", "e", true, t0.Add(2*time.Minute), entry); got != "" {
		t.Fatalf("re-entry inside the cooldown must be suppressed, got %q", got)
	}
	if got := tr.transition("r", "e", false, t0.Add(3*time.Minute), entry); got != "" {
		t.Fatalf("re-exit inside the cooldown must be suppressed, got %q", got)
	}
	if got := tr.transition("r", "e", true, t0.Add(15*time.Minute), entry); got != "GEOFENCE_ENTRY" {
		t.Fatalf("entry after the cooldown should fire, got %q", got)
	}
	// Other rules and entities are independent.
	if got := tr.transition("r2", "e", true, t0.Add(15*time.Minute), entry); got != "GEOFENCE_ENTRY" {
		t.Errorf("second rule should track separately, got %q", got)
	}
	if got := tr.transition("r", "e2", true, t0.Add(15*time.Minute), entry); got != "GEOFENCE_ENTRY" {
		t.Errorf("second entity should track separately, got %q", got)
	}
}

func TestRuleStateTracker_SweepsStaleEntries(t *testing.T) {
	tr := newRuleStateTracker()
	t0 := time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)
	noop := func(*bool, bool) string { return "" }
	tr.transition("r", "old", true, t0, noop)
	for i := 0; i < 999; i++ { // the 1000th evaluation sweeps
		tr.transition("r", "fresh", true, t0.Add(3*time.Hour), noop)
	}
	if _, ok := tr.states["r|old"]; ok {
		t.Error("entry older than the TTL should have been swept")
	}
	if _, ok := tr.states["r|fresh"]; !ok {
		t.Error("fresh entry must survive the sweep")
	}
}
