package grpcin

import (
	"encoding/json"
	"slices"
	"strconv"
	"time"

	"github.com/sentinel/ingest-service/internal/emplacement"
	"github.com/sentinel/ingest-service/internal/obs"
)

// maxBearingsPerSite is the most bearings a site carries: the newest win.
const maxBearingsPerSite = 50

// The bearing time to live: how long a line stays on its site without a
// newer detection (bearingTtlSec).
const (
	defaultBearingTTLSec = 10
	minBearingTTLSec     = 1
	maxBearingTTLSec     = 300
)

// heldBearing is a bearing as a site keeps it.
type heldBearing struct {
	b bearing
	// base is what its time to live runs from: when the server took it
	// (ReceivedAt), so a sender's clock, fast or slow, neither keeps a line
	// up past the ttl nor drops it on arrival.
	base time.Time
	// seq orders bearings observed at the same instant: the later taken first.
	seq uint64
	key string
}

// siteBearings are one site's bearings.
type siteBearings struct {
	byKey map[string]heldBearing
	// dirty is set when a bearing is added and cleared when the site is
	// published; published is how many went out with that publish, so that
	// one expiring is noticed (bearingsChanged).
	dirty     bool
	published int
}

// addBearing records b on the emplacement's site, keyed by its track id (a
// track's newer bearing replaces its older one) or under a fresh key when it
// has none. It marks the site dirty, so the ticker publishes it, and counts
// the bearing under its reason.
func (d *Driver) addBearing(emplacementID string, b bearing) {
	now := d.now().UTC()
	if b.ReceivedAt.IsZero() || b.ReceivedAt.After(now) {
		b.ReceivedAt = now
	}
	base := b.ReceivedAt
	d.bearingsMu.Lock()
	d.bearingSeq++
	seq := d.bearingSeq
	key := "\x00" + strconv.FormatUint(seq, 10)
	if b.TrackID != "" {
		key = "t:" + b.TrackID
	}
	s := d.bearings[emplacementID]
	if s == nil {
		s = &siteBearings{byKey: map[string]heldBearing{}}
		d.bearings[emplacementID] = s
	}
	if old, ok := s.byKey[key]; !ok || !b.ObservedAt.Before(old.b.ObservedAt) {
		s.byKey[key] = heldBearing{b: b, base: base, seq: seq, key: key}
		s.dirty = true
	}
	d.pruneBearings(s, now)
	d.bearingsMu.Unlock()
	d.countBearing(b.Reason)
}

// countBearing counts a bearing under reason in detail.bearings: the reason
// it was recorded for, or why it was withheld.
func (d *Driver) countBearing(reason string) {
	d.bearingCountMu.Lock()
	d.bearingCounts[reason]++
	d.bearingCountMu.Unlock()
}

// pruneBearings drops s's expired bearings, and then its oldest past the
// cap, so a site never holds more than it can publish. The caller holds
// bearingsMu.
func (d *Driver) pruneBearings(s *siteBearings, now time.Time) []heldBearing {
	held := make([]heldBearing, 0, len(s.byKey))
	for k, h := range s.byKey {
		if h.base.Add(d.bearingTTL).Before(now) {
			delete(s.byKey, k)
			continue
		}
		held = append(held, h)
	}
	slices.SortFunc(held, func(a, b heldBearing) int {
		if c := b.b.ObservedAt.Compare(a.b.ObservedAt); c != 0 {
			return c
		}
		switch {
		case a.seq > b.seq:
			return -1
		case a.seq < b.seq:
			return 1
		}
		return 0
	})
	if len(held) > maxBearingsPerSite {
		for _, h := range held[maxBearingsPerSite:] {
			delete(s.byKey, h.key)
		}
		held = held[:maxBearingsPerSite]
	}
	return held
}

// activeBearings are the emplacement's unexpired bearings at now (expired
// once ReceivedAt + ttl is before now), newest first, at most
// maxBearingsPerSite.
func (d *Driver) activeBearings(emplacementID string, now time.Time) []bearing {
	d.bearingsMu.Lock()
	defer d.bearingsMu.Unlock()
	return d.activeLocked(emplacementID, now)
}

// activeLocked is activeBearings; the caller holds bearingsMu.
func (d *Driver) activeLocked(emplacementID string, now time.Time) []bearing {
	s := d.bearings[emplacementID]
	if s == nil {
		return nil
	}
	held := d.pruneBearings(s, now)
	out := make([]bearing, len(held))
	for i, h := range held {
		out[i] = h.b
	}
	return out
}

// bearingsChanged reports whether the site's bearings differ from its last
// publish: one was added, or one has expired since.
func (d *Driver) bearingsChanged(emplacementID string, now time.Time) bool {
	d.bearingsMu.Lock()
	defer d.bearingsMu.Unlock()
	s := d.bearings[emplacementID]
	if s == nil {
		return false
	}
	return s.dirty || len(d.pruneBearings(s, now)) < s.published
}

// clearBearings forgets the emplacement's bearings: its site went offline.
func (d *Driver) clearBearings(emplacementID string) {
	d.bearingsMu.Lock()
	defer d.bearingsMu.Unlock()
	delete(d.bearings, emplacementID)
}

// markBearings writes the site's active bearings onto its observation, after
// MarkSite (which replaces Generic): FieldBearings as a JSON array, newest
// first, and FieldBearingTTLSec, when it has any. It records them as
// published.
func (d *Driver) markBearings(o *obs.Observation, emplacementID string, now time.Time) {
	d.bearingsMu.Lock()
	active := d.activeLocked(emplacementID, now)
	if s := d.bearings[emplacementID]; s != nil {
		s.dirty, s.published = false, len(active)
		if len(s.byKey) == 0 {
			delete(d.bearings, emplacementID)
		}
	}
	d.bearingsMu.Unlock()
	if len(active) == 0 {
		return
	}
	raw, err := json.Marshal(active)
	if err != nil {
		return
	}
	if o.Generic == nil {
		o.Generic = map[string]string{}
	}
	o.Generic[emplacement.FieldBearings] = string(raw)
	o.Generic[emplacement.FieldBearingTTLSec] = strconv.Itoa(int(d.bearingTTL / time.Second))
}
