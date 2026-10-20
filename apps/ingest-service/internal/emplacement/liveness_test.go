package emplacement

import (
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"
)

var t0 = time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)

func TestIntervalOf(t *testing.T) {
	for _, c := range []struct {
		in   uint32
		want int
		ok   bool
	}{{0, 10, true}, {1, 1, true}, {300, 300, true}, {301, 0, false}, {100000, 0, false}} {
		if got, ok := IntervalOf(c.in); got != c.want || ok != c.ok {
			t.Errorf("IntervalOf(%d) = %d, %v; want %d, %v", c.in, got, ok, c.want, c.ok)
		}
	}
}

// Online for three intervals after a heartbeat, offline from then on.
func TestTrackerOnlineForThreeIntervals(t *testing.T) {
	tr := NewTracker(t0)
	tr.Heartbeat("a", 5, t0)
	if s := tr.State("a", 0, t0.Add(15*time.Second-time.Millisecond)); s != HeartbeatOnline {
		t.Errorf("just inside three intervals: %s", s)
	}
	if s := tr.State("a", 0, t0.Add(15*time.Second)); s != HeartbeatOffline {
		t.Errorf("three intervals later: %s", s)
	}
	if tr.Online("a", t0.Add(16*time.Second)) {
		t.Error("Online after the lapse")
	}
}

// Without a heartbeat an emplacement is pending through the startup grace,
// max(three of its intervals, 30 s), and never after it.
func TestTrackerNeverAndPendingGrace(t *testing.T) {
	tr := NewTracker(t0)
	if s := tr.State("a", 0, t0.Add(29*time.Second)); s != HeartbeatPending {
		t.Errorf("29 s after the start: %s", s)
	}
	if s := tr.State("a", 0, t0.Add(30*time.Second)); s != HeartbeatNever {
		t.Errorf("30 s after the start: %s", s)
	}
	if s := tr.State("b", 20, t0.Add(59*time.Second)); s != HeartbeatPending {
		t.Errorf("a 20 s interval waits 60 s: %s", s)
	}
	if tr.Online("a", t0.Add(time.Second)) {
		t.Error("pending counted as online")
	}
}

func TestTrackerKeepsTheNewerHeartbeat(t *testing.T) {
	tr := NewTracker(t0)
	tr.Heartbeat("a", 5, t0.Add(10*time.Second))
	tr.Heartbeat("a", 7, t0)
	at, sec, ok := tr.Last("a")
	if !ok || !at.Equal(t0.Add(10*time.Second)) || sec != 5 {
		t.Fatalf("Last = %v %d %v; the older heartbeat won", at, sec, ok)
	}
	tr.Forget("a")
	if _, _, ok := tr.Last("a"); ok {
		t.Fatal("Forget kept the heartbeat")
	}
}

// A replica that takes the connection over starts from the rows: those that
// heartbeat within three of their intervals are online at once.
func TestTrackerSeedsFromRecentRows(t *testing.T) {
	sec := 5
	recent, old := t0.Add(-10*time.Second), t0.Add(-20*time.Second)
	rows := []Emplacement{
		{ID: "recent", LastHeartbeatAt: &recent, HeartbeatIntervalSec: &sec},
		{ID: "old", LastHeartbeatAt: &old, HeartbeatIntervalSec: &sec},
		{ID: "none"},
	}
	tr := NewTracker(t0)
	tr.Seed(rows, t0)
	if s := tr.State("recent", sec, t0); s != HeartbeatOnline {
		t.Errorf("recent: %s", s)
	}
	if s := tr.State("old", sec, t0); s != HeartbeatOffline {
		t.Errorf("old: %s, want offline (seeded stale)", s)
	}
	if s := tr.State("none", 0, t0); s != HeartbeatPending {
		t.Errorf("none: %s", s)
	}
	tr.Heartbeat("old", sec, t0)
	if s := tr.State("old", sec, t0); s != HeartbeatOnline {
		t.Errorf("old after a real heartbeat: %s", s)
	}
}

func TestSnapshotOnePerRow(t *testing.T) {
	sec := 20
	tr := NewTracker(t0)
	tr.Heartbeat("a", 5, t0)
	got := tr.Snapshot([]Emplacement{
		{ID: "a", Name: "Coastal", Classification: "SECRET"},
		{ID: "b", Name: "Mast", Classification: "UNCLASSIFIED", HeartbeatIntervalSec: &sec},
	}, t0.Add(time.Second))
	if len(got) != 2 {
		t.Fatalf("%d entries, want 2", len(got))
	}
	if a := got[0]; a.ID != "a" || a.Name != "Coastal" || a.Classification != "SECRET" || a.State != HeartbeatOnline ||
		a.IntervalSec != 5 || a.LastHeartbeatAt == nil || !a.LastHeartbeatAt.Equal(t0) {
		t.Errorf("a = %+v", a)
	}
	if b := got[1]; b.State != HeartbeatPending || b.IntervalSec != 20 || b.LastHeartbeatAt != nil {
		t.Errorf("b = %+v", b)
	}
}

// Health reads the snapshot under the supervisor's lock while senders
// heartbeat: the tracker's own mutex is all it takes (run with -race).
func TestTrackerIsSafeForConcurrentUse(t *testing.T) {
	tr := NewTracker(t0)
	rows := make([]Emplacement, 8)
	for i := range rows {
		rows[i] = Emplacement{ID: fmt.Sprintf("e-%d", i)}
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				tr.Heartbeat(rows[i].ID, 5, t0.Add(time.Duration(j)*time.Millisecond))
			}
		}(i)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				_ = tr.Snapshot(rows, t0)
			}
		}()
	}
	wg.Wait()
}

func TestLivenessFromDetail(t *testing.T) {
	list, ok := LivenessFromDetail(json.RawMessage(`{"reportsOk":1,"emplacements":[{"id":"a","name":"A","classification":"UNCLASSIFIED","state":"offline","lastHeartbeatAt":"2026-10-20T12:00:00Z","intervalSec":5}]}`))
	if !ok || len(list) != 1 || list[0].State != HeartbeatOffline || list[0].LastHeartbeatAt == nil || !list[0].LastHeartbeatAt.Equal(t0) {
		t.Fatalf("list = %+v %v", list, ok)
	}
	if _, ok := LivenessFromDetail(json.RawMessage(`{"reportsOk":1}`)); ok {
		t.Fatal("a detail without emplacements read as some")
	}
}

func TestRowState(t *testing.T) {
	sec := 5
	recent, old := t0.Add(-10*time.Second), t0.Add(-15*time.Second)
	if s := RowState(Emplacement{LastHeartbeatAt: &recent, HeartbeatIntervalSec: &sec}, t0); s != HeartbeatOnline {
		t.Errorf("recent: %s", s)
	}
	if s := RowState(Emplacement{LastHeartbeatAt: &old, HeartbeatIntervalSec: &sec}, t0); s != HeartbeatOffline {
		t.Errorf("three intervals old: %s", s)
	}
	if s := RowState(Emplacement{}, t0); s != HeartbeatNever {
		t.Errorf("none: %s", s)
	}
}
