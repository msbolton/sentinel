package emplacement

import (
	"encoding/json"
	"sync"
	"time"
)

// HeartbeatState is what an emplacement's heartbeats say of it.
type HeartbeatState string

const (
	// HeartbeatOnline: a heartbeat within three of its intervals.
	HeartbeatOnline HeartbeatState = "online"
	// HeartbeatOffline: heard from, but not within three intervals.
	HeartbeatOffline HeartbeatState = "offline"
	// HeartbeatNever: no heartbeat recorded, in memory or in the row, past
	// the startup grace.
	HeartbeatNever HeartbeatState = "never"
	// HeartbeatPending: not heard from yet, within the startup grace.
	HeartbeatPending HeartbeatState = "pending"
)

const (
	// DefaultHeartbeatIntervalSec is the interval of a heartbeat that
	// declares none (heartbeat_interval_sec 0).
	DefaultHeartbeatIntervalSec = 10
	// MaxHeartbeatIntervalSec is the longest interval a heartbeat may declare.
	MaxHeartbeatIntervalSec = 300
	// MissedHeartbeats is how many intervals pass without one before an
	// emplacement is offline.
	MissedHeartbeats = 3
	// MinStartupGrace is the least time after a connection starts during
	// which an emplacement not heard from yet is pending, not offline.
	MinStartupGrace = 30 * time.Second
)

// IntervalOf is the interval a heartbeat declared, in seconds: 0 is the
// default (10). ok is false above MaxHeartbeatIntervalSec.
func IntervalOf(sec uint32) (int, bool) {
	switch {
	case sec == 0:
		return DefaultHeartbeatIntervalSec, true
	case sec > MaxHeartbeatIntervalSec:
		return 0, false
	}
	return int(sec), true
}

// RowIntervalSec is the interval e's row last recorded, else the default.
func RowIntervalSec(e Emplacement) int {
	if s := e.HeartbeatIntervalSec; s != nil && *s > 0 && *s <= MaxHeartbeatIntervalSec {
		return *s
	}
	return DefaultHeartbeatIntervalSec
}

// Lapse is how long an emplacement stays online after a heartbeat.
func Lapse(intervalSec int) time.Duration {
	return time.Duration(MissedHeartbeats*intervalSec) * time.Second
}

// Liveness is one emplacement's heartbeat state as its connection's status
// reports it (feed_status.detail.emplacements). Classification lets the
// management API leave out an entry above the caller's clearance.
type Liveness struct {
	ID              string         `json:"id"`
	Name            string         `json:"name"`
	Classification  string         `json:"classification"`
	State           HeartbeatState `json:"state"`
	LastHeartbeatAt *time.Time     `json:"lastHeartbeatAt,omitempty"`
	IntervalSec     int            `json:"intervalSec"`
}

type beat struct {
	at       time.Time
	interval int
	// seeded: taken from a row when the run started, not heard in this run.
	seeded bool
}

// Tracker is one connection run's heartbeats, in memory. Liveness truth is
// the replica that runs the connection; the row's lastHeartbeatAt only seeds
// a new run. Every method takes the tracker's own mutex and nothing else, so
// it is safe under the supervisor's lock (statusOf calls Health).
type Tracker struct {
	started time.Time

	mu    sync.Mutex
	beats map[string]beat
}

// NewTracker starts a run's tracker at started (the startup grace counts
// from it).
func NewTracker(started time.Time) *Tracker {
	return &Tracker{started: started, beats: map[string]beat{}}
}

// Started is when the run started.
func (t *Tracker) Started() time.Time { return t.started }

// Heartbeat records a heartbeat of id at the server's time at, with the
// interval it declared. An older one than the last recorded is ignored.
func (t *Tracker) Heartbeat(id string, intervalSec int, at time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if b, ok := t.beats[id]; ok && b.at.After(at) {
		return
	}
	t.beats[id] = beat{at: at, interval: intervalSec}
}

// Forget drops id's heartbeats: it left the connection, or was deactivated.
func (t *Tracker) Forget(id string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.beats, id)
}

// Seed records the heartbeats rows hold, stale ones too, marked as seeded.
// One still inside its window reads online. One already lapsed reads pending
// through the startup grace (its sender may simply not have reconnected to
// the new run yet) and offline after it: heard from once, never "never".
// Rows with no heartbeat are left out. A real heartbeat newer than the seed
// replaces it.
func (t *Tracker) Seed(rows []Emplacement) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, e := range rows {
		if e.LastHeartbeatAt == nil {
			continue
		}
		if b, ok := t.beats[e.ID]; ok && !b.at.Before(*e.LastHeartbeatAt) {
			continue
		}
		t.beats[e.ID] = beat{at: *e.LastHeartbeatAt, interval: RowIntervalSec(e), seeded: true}
	}
}

// Last is id's last heartbeat and its interval.
func (t *Tracker) Last(id string) (time.Time, int, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	b, ok := t.beats[id]
	return b.at, b.interval, ok
}

// State is id's heartbeat state at now. declaredSec is the interval its row
// records (0 for the default); it sets the startup grace of one not heard
// from yet.
func (t *Tracker) State(id string, declaredSec int, now time.Time) HeartbeatState {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.stateLocked(id, declaredSec, now)
}

func (t *Tracker) stateLocked(id string, declaredSec int, now time.Time) HeartbeatState {
	if b, ok := t.beats[id]; ok {
		switch {
		case now.Before(b.at.Add(Lapse(b.interval))):
			return HeartbeatOnline
		case b.seeded && t.inGrace(b.interval, now):
			return HeartbeatPending
		}
		return HeartbeatOffline
	}
	if declaredSec <= 0 {
		declaredSec = DefaultHeartbeatIntervalSec
	}
	if t.inGrace(declaredSec, now) {
		return HeartbeatPending
	}
	return HeartbeatNever
}

// inGrace reports whether now is within the startup grace of an emplacement
// with intervalSec: max(three intervals, MinStartupGrace) from the start.
func (t *Tracker) inGrace(intervalSec int, now time.Time) bool {
	return now.Sub(t.started) < max(Lapse(intervalSec), MinStartupGrace)
}

// Online reports whether id heartbeat within three of its intervals.
func (t *Tracker) Online(id string, now time.Time) bool {
	return t.State(id, 0, now) == HeartbeatOnline
}

// Snapshot is the state of each of rows at now, in their order.
func (t *Tracker) Snapshot(rows []Emplacement, now time.Time) []Liveness {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]Liveness, 0, len(rows))
	for _, e := range rows {
		sec := RowIntervalSec(e)
		l := Liveness{ID: e.ID, Name: e.Name, Classification: e.Classification, IntervalSec: sec}
		if b, ok := t.beats[e.ID]; ok {
			at := b.at
			l.LastHeartbeatAt, l.IntervalSec = &at, b.interval
		}
		l.State = t.stateLocked(e.ID, sec, now)
		out = append(out, l)
	}
	return out
}

// LivenessFromDetail reads detail.emplacements from a connection's status,
// as Supervisor.statusOf writes it. ok is false when the detail has none.
func LivenessFromDetail(detail json.RawMessage) ([]Liveness, bool) {
	var d struct {
		Emplacements *[]Liveness `json:"emplacements"`
	}
	if json.Unmarshal(detail, &d) != nil || d.Emplacements == nil {
		return nil, false
	}
	return *d.Emplacements, true
}

// RowState is an emplacement's heartbeat state from its row alone, for when
// no replica reports its connection: online within three intervals of the
// last heartbeat the row recorded, offline after, never without one. The row
// is written at most every interval and every 10 s, so it lags by up to that.
func RowState(e Emplacement, now time.Time) HeartbeatState {
	if e.LastHeartbeatAt == nil {
		return HeartbeatNever
	}
	if now.Before(e.LastHeartbeatAt.Add(Lapse(RowIntervalSec(e)))) {
		return HeartbeatOnline
	}
	return HeartbeatOffline
}
