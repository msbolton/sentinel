package aisstream

import (
	"time"

	"github.com/sentinel/ingest-service/internal/obs"
)

// thinner keeps at most one position per vessel per interval: one for
// moving vessels, a longer one for vessels that are moored, at anchor,
// aground or slower than the stationary speed, and the background interval
// for vessels nobody is watching (o.Background). Reports without a position
// (static and voyage data) always pass. It is used from one goroutine.
type thinner struct {
	moving, stationary, background time.Duration
	knots                          float64
	last                           map[string]time.Time
	lastSweep                      time.Time
}

func newThinner(moving, stationary, background time.Duration, knots float64) *thinner {
	return &thinner{moving: moving, stationary: stationary, background: background, knots: knots, last: map[string]time.Time{}}
}

// allow reports whether the observation should be stored, and if so
// remembers when.
func (t *thinner) allow(o *obs.Observation, now time.Time) bool {
	if !o.HasPosition {
		return true
	}
	interval := t.moving
	switch {
	case o.Background:
		interval = t.background
	case t.isStationary(o):
		interval = t.stationary
	}
	if interval <= 0 {
		return true
	}
	t.sweep(now)
	if last, ok := t.last[o.SourceEntityID]; ok && now.Sub(last) < interval {
		return false
	}
	t.last[o.SourceEntityID] = now
	return true
}

func (t *thinner) isStationary(o *obs.Observation) bool {
	switch o.OperationalStatus {
	case "AT_ANCHOR", "MOORED", "AGROUND":
		return true
	}
	return o.SpeedKnots != nil && *o.SpeedKnots < t.knots
}

// sweep forgets vessels not stored for longer than any interval, at most
// once a minute.
func (t *thinner) sweep(now time.Time) {
	if now.Sub(t.lastSweep) < time.Minute {
		return
	}
	t.lastSweep = now
	keep := max(t.moving, t.stationary, t.background) + time.Minute
	for id, at := range t.last {
		if now.Sub(at) > keep {
			delete(t.last, id)
		}
	}
}
