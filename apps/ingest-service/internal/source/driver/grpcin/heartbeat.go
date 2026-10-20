package grpcin

import (
	"context"
	"time"

	"go.uber.org/zap"

	"github.com/sentinel/ingest-service/internal/emplacement"
	"github.com/sentinel/ingest-service/internal/obs"
	"github.com/sentinel/ingest-service/internal/source/driver"
)

const (
	// republishTick is how often the bound sites are checked for a publish.
	republishTick = time.Second
	// maxRepublish is the longest a bound site goes unpublished.
	maxRepublish = 60 * time.Second
	// stopPublishTimeout bounds the offline publish when the run ends.
	stopPublishTimeout = 2 * time.Second
	// beatChangeEvery is the most often a changed interval is written: a
	// sender alternating intervals must not force a write per message.
	beatChangeEvery = time.Second
)

// beatTouch is the last heartbeat written to a row.
type beatTouch struct {
	at       time.Time
	interval int
}

// liveness is the run's heartbeat tracker; nil before the first Run.
func (d *Driver) liveness() *emplacement.Tracker { return d.live.Load() }

// bound lists the active emplacements bound to this connection (the cache's
// in-memory view: no DB wait, safe under the supervisor's lock).
func (d *Driver) bound() []emplacement.Emplacement {
	var out []emplacement.Emplacement
	for _, e := range d.emps.ByFeed(d.feedID) {
		if e.Active {
			out = append(out, e)
		}
	}
	return out
}

// startLiveness makes the run's tracker and seeds it from the rows: from the
// store when there is one, since a Heartbeat write sends no notice and the
// cache's copies of lastHeartbeatAt are as old as its last load.
func (d *Driver) startLiveness(ctx context.Context) *emplacement.Tracker {
	now := d.now().UTC()
	t := emplacement.NewTracker(now)
	rows := d.emps.ByFeed(d.feedID)
	if d.writer != nil {
		lctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		fresh, err := d.writer.List(lctx, emplacement.Filter{FeedID: d.feedID})
		cancel()
		if err == nil {
			rows = fresh
		} else {
			d.logger.Warn("reading the emplacements' last heartbeats failed; they start from none", zap.Error(err))
		}
	}
	t.Seed(rows, now)
	d.live.Store(t)
	return t
}

// watchBindings forgets the heartbeats of an emplacement that leaves the
// connection or is deactivated: if it comes back, it starts from none.
func (d *Driver) watchBindings(t *emplacement.Tracker) (cancel func()) {
	return d.emps.OnChange(func(id string) {
		if e, ok := d.emps.Get(id); !ok || !e.Active || e.FeedID == nil || *e.FeedID != d.feedID {
			t.Forget(id)
		}
	})
}

// beat records a heartbeat of id at the server's time now, and writes it to
// the row at most every min(interval, touchEvery), apart from the report
// touches, and sooner when the interval changes, but never twice within
// beatChangeEvery. A failed write is logged.
func (d *Driver) beat(ctx context.Context, id string, intervalSec int, now time.Time) {
	if t := d.liveness(); t != nil {
		t.Heartbeat(id, intervalSec, now)
	}
	if d.writer == nil {
		return
	}
	every := min(time.Duration(intervalSec)*time.Second, touchEvery)
	d.touchMu.Lock()
	last, seen := d.beatTouched[id]
	if seen {
		within := func(w time.Duration) bool { return now.Sub(last.at) < w && last.at.Sub(now) < w }
		if within(every) && (last.interval == intervalSec || within(beatChangeEvery)) {
			d.touchMu.Unlock()
			return
		}
	}
	d.beatTouched[id] = beatTouch{at: now, interval: intervalSec}
	d.touchMu.Unlock()
	tctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := d.writer.Heartbeat(tctx, id, intervalSec, now); err != nil {
		d.logger.Warn("recording an emplacement's heartbeat failed", zap.String("emplacement", id), zap.Error(err))
	}
}

// newestFix is a mobile emplacement's last fix, when it has one.
func (d *Driver) newestFix(id string) (fix, bool) {
	d.tracksMu.Lock()
	defer d.tracksMu.Unlock()
	t := d.tracks[id]
	if t == nil || len(t.fixes) == 0 {
		return fix{}, false
	}
	return t.fixes[len(t.fixes)-1], true
}

// siteFor is e's SENSOR observation for a publish in state. A fixed one is
// stamped with e's last heartbeat (the run's start when it has had none),
// never with now, so that a re-publish of a silent site does not look like a
// fresh report. A mobile one is placed at its newest fix and stamped with
// that fix's time (no later than now), the clock its online publishes in
// state use, so a re-publish never outdates the sender's next move; with no
// fix it falls back to the heartbeat stamp. ok is false when there is
// nowhere to place it.
func (d *Driver) siteFor(e emplacement.Emplacement, state emplacement.HeartbeatState, now time.Time) (*obs.Observation, bool) {
	interval := emplacement.RowIntervalSec(e)
	var last time.Time
	at := now
	if t := d.liveness(); t != nil {
		at = t.Started()
		if l, sec, ok := t.Last(e.ID); ok {
			last, interval, at = l, sec, l
		}
	}
	if e.Mobility == emplacement.MobilityMobile {
		if f, ok := d.newestFix(e.ID); ok {
			alt := f.pos.Alt
			e.Position, e.Altitude = &emplacement.Point{Lat: f.pos.Lat, Lon: f.pos.Lon}, &alt
			at = f.at
			if at.After(now) {
				at = now
			}
		}
	}
	if e.Position == nil {
		return nil, false
	}
	o := emplacement.Observation(e, at)
	o.Decoder, o.ReceivedAt = Decoder, now
	emplacement.MarkSite(o, state, last, interval)
	return o, true
}

// siteDue reports whether a site's publish is due: its heartbeat state has
// changed since the last one went out, or every has passed. It marks it
// emitted when it is. It shares sensorDue's clock, so the two never publish
// the same state twice within a second.
func (d *Driver) siteDue(id string, state emplacement.HeartbeatState, every time.Duration, now time.Time) bool {
	d.tracksMu.Lock()
	defer d.tracksMu.Unlock()
	last, seen := d.emittedAt[id]
	if seen && d.emittedState[id] == state && now.Sub(last) < every && last.Sub(now) < every {
		return false
	}
	d.emittedAt[id], d.emittedState[id] = now, state
	return true
}

// republish publishes every bound site whatever its state: at once when the
// state changes, else every min(interval, 60 s). That way an offline site
// stays searchable and anchors geofences, and turns grey without waiting for
// a report.
func (d *Driver) republish(ctx context.Context, sink driver.Sink) {
	tick := time.NewTicker(d.republishTick)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		t := d.liveness()
		if t == nil {
			continue
		}
		now := d.now().UTC()
		for _, e := range d.bound() {
			declared := emplacement.RowIntervalSec(e)
			state := t.State(e.ID, declared, now)
			interval := declared
			if _, sec, ok := t.Last(e.ID); ok {
				interval = sec
			}
			every := min(time.Duration(interval)*time.Second, maxRepublish)
			if !d.siteDue(e.ID, state, every, now) {
				continue
			}
			o, ok := d.siteFor(e, state, now)
			if !ok {
				continue
			}
			if err := sink.Emit(ctx, o); err != nil {
				return
			}
		}
	}
}

// publishOffline publishes every bound site once as offline when the run
// ends (a disable, a delete, a lease move), so the map greys them now. A new
// owner's publishes carry newer times and win.
func (d *Driver) publishOffline(sink driver.Sink) {
	ctx, cancel := context.WithTimeout(context.Background(), stopPublishTimeout)
	defer cancel()
	now := d.now().UTC()
	for _, e := range d.bound() {
		if o, ok := d.siteFor(e, emplacement.HeartbeatOffline, now); ok {
			if err := sink.Emit(ctx, o); err != nil {
				return
			}
		}
	}
}
