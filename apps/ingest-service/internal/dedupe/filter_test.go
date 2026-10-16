package dedupe

import (
	"testing"
	"time"

	"github.com/sentinel/ingest-service/internal/obs"
)

func fix(id string, lat, lon float64, ts time.Time) *obs.Observation {
	o := &obs.Observation{SourceEntityID: id, SourceTimestamp: ts}
	o.SetPosition(lat, lon)
	return o
}

func TestSuppressesRepeatedFixBriefly(t *testing.T) {
	f := New(Options{})
	t0 := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	if !f.Allow(fix("a", 40, -80, t0), t0) {
		t.Fatal("first fix must pass")
	}
	if f.Allow(fix("a", 40, -80, t0.Add(500*time.Millisecond)), t0.Add(500*time.Millisecond)) {
		t.Error("same fix half a second later is a duplicate")
	}
	if !f.Allow(fix("a", 40.001, -80, t0.Add(500*time.Millisecond)), t0.Add(500*time.Millisecond)) {
		t.Error("a fix 100 m away is not a duplicate")
	}
	if !f.Allow(fix("a", 40.001, -80, t0.Add(3*time.Second)), t0.Add(3*time.Second)) {
		t.Error("a fix outside the time window passes")
	}
}

func TestForwardsParkedObjectPeriodically(t *testing.T) {
	f := New(Options{})
	t0 := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	f.Allow(fix("a", 40, -80, t0), t0)
	// Feed keeps repeating the same source time (a cached snapshot).
	if f.Allow(fix("a", 40, -80, t0), t0.Add(2*time.Second)) {
		t.Error("duplicate within the forward interval must be suppressed")
	}
	if !f.Allow(fix("a", 40, -80, t0), t0.Add(6*time.Second)) {
		t.Error("after the forward interval one duplicate must pass")
	}
}

func TestObjectsAreIndependentAndPositionlessPass(t *testing.T) {
	f := New(Options{})
	t0 := time.Now()
	f.Allow(fix("a", 40, -80, t0), t0)
	if !f.Allow(fix("b", 40, -80, t0), t0) {
		t.Error("another object at the same place is not a duplicate")
	}
	o := &obs.Observation{SourceEntityID: "a"}
	if !f.Allow(o, t0) || !f.Allow(o, t0) {
		t.Error("positionless reports always pass")
	}
	if f.Size() != 2 {
		t.Errorf("size = %d", f.Size())
	}
}

func TestExpiry(t *testing.T) {
	f := New(Options{Expiry: time.Minute})
	t0 := time.Now()
	f.Allow(fix("a", 1, 1, t0), t0)
	f.Allow(fix("b", 1, 1, t0.Add(3*time.Minute)), t0.Add(3*time.Minute))
	if f.Size() != 1 {
		t.Errorf("stale object should be swept, size = %d", f.Size())
	}
}
