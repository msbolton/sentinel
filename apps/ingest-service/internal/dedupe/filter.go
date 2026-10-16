// Package dedupe suppresses repeated fixes within one feed: a report that
// repeats the last forwarded position for the same object within a second
// is dropped, except that one is let through every few seconds so
// downstream last-seen times stay fresh. It never merges objects.
package dedupe

import (
	"math"
	"sync"
	"time"

	"github.com/sentinel/ingest-service/internal/obs"
)

// Options tune the filter. Zero values take the defaults.
type Options struct {
	TimeWindow      time.Duration // reports closer than this are candidates (1 s)
	DistanceMeters  float64       // and closer than this in space are duplicates (10 m)
	ForwardInterval time.Duration // one duplicate is forwarded at least this often (5 s)
	Expiry          time.Duration // forget objects not seen for this long (15 min)
}

func (o Options) withDefaults() Options {
	if o.TimeWindow <= 0 {
		o.TimeWindow = time.Second
	}
	if o.DistanceMeters <= 0 {
		o.DistanceMeters = 10
	}
	if o.ForwardInterval <= 0 {
		o.ForwardInterval = 5 * time.Second
	}
	if o.Expiry <= 0 {
		o.Expiry = 15 * time.Minute
	}
	return o
}

type entry struct {
	lat, lon      float64
	sourceTime    time.Time
	lastForwarded time.Time
}

// Filter is one feed's duplicate suppressor; safe for concurrent use.
type Filter struct {
	opts    Options
	mu      sync.Mutex
	last    map[string]*entry
	sweepAt time.Time
}

// New builds a filter.
func New(opts Options) *Filter {
	return &Filter{opts: opts.withDefaults(), last: map[string]*entry{}}
}

// Allow reports whether o should be forwarded and records it when so.
// Observations without a position always pass.
func (f *Filter) Allow(o *obs.Observation, now time.Time) bool {
	if !o.HasPosition || o.SourceEntityID == "" {
		return true
	}
	ts := o.SourceTimestamp
	if ts.IsZero() {
		ts = now
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sweep(now)
	e, ok := f.last[o.SourceEntityID]
	if ok && f.duplicate(e, o, ts, now) {
		return false
	}
	if !ok {
		e = &entry{}
		f.last[o.SourceEntityID] = e
	}
	e.lat, e.lon, e.sourceTime, e.lastForwarded = o.Latitude, o.Longitude, ts, now
	return true
}

func (f *Filter) duplicate(e *entry, o *obs.Observation, ts, now time.Time) bool {
	if ts.Sub(e.sourceTime) > f.opts.TimeWindow {
		return false
	}
	if now.Sub(e.lastForwarded) >= f.opts.ForwardInterval {
		return false
	}
	return haversineMeters(o.Latitude, o.Longitude, e.lat, e.lon) < f.opts.DistanceMeters
}

func (f *Filter) sweep(now time.Time) {
	if now.Sub(f.sweepAt) < time.Minute {
		return
	}
	f.sweepAt = now
	cutoff := now.Add(-f.opts.Expiry)
	for k, e := range f.last {
		if e.lastForwarded.Before(cutoff) {
			delete(f.last, k)
		}
	}
}

// Size returns the number of tracked objects.
func (f *Filter) Size() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.last)
}

func haversineMeters(lat1, lon1, lat2, lon2 float64) float64 {
	const r = 6371000.0
	dLat := (lat2 - lat1) * math.Pi / 180
	dLon := (lon2 - lon1) * math.Pi / 180
	a := math.Sin(dLat/2)*math.Sin(dLat/2) + math.Cos(lat1*math.Pi/180)*math.Cos(lat2*math.Pi/180)*math.Sin(dLon/2)*math.Sin(dLon/2)
	return r * 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
}
