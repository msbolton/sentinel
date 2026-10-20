package grpcin

import (
	"context"
	"time"

	"go.uber.org/zap"

	"github.com/sentinel/go-common/classification"
	"github.com/sentinel/ingest-service/internal/emplacement"
	"github.com/sentinel/ingest-service/internal/obs"
)

// touchEvery is how often a report writes its emplacement's lastReportAt
// (and a mobile one's position): sensors report every second.
const touchEvery = 10 * time.Second

// resolve finds the emplacement a report comes from among those bound to
// this connection that are active and the token may see. An empty claim
// means the connection's only such emplacement; a connection with none
// takes reports from no emplacement (ok false, no reason). Any other miss is
// unknown_emplacement, whether the emplacement is another connection's,
// above the token, inactive or does not exist: the answer is the same.
func (s *service) resolve(a authed, claim string) (emplacement.Emplacement, bool, string) {
	ceiling := classification.Rank(a.tok.Classification)
	usable := func(e emplacement.Emplacement) bool {
		return e.Active && e.FeedID != nil && *e.FeedID == s.d.feedID && classification.Rank(e.Classification) <= ceiling
	}
	if claim != "" {
		if e, ok := s.d.emps.Get(claim); ok && usable(e) {
			return e, true, ""
		}
		return emplacement.Emplacement{}, false, "unknown_emplacement"
	}
	var found []emplacement.Emplacement
	for _, e := range s.d.emps.ByFeed(s.d.feedID) {
		if usable(e) {
			found = append(found, e)
		}
	}
	switch len(found) {
	case 0:
		return emplacement.Emplacement{}, false, ""
	case 1:
		return found[0], true, ""
	}
	return emplacement.Emplacement{}, false, "unknown_emplacement"
}

// stripEmplacementIdentities removes every EMPLACEMENT identity from a
// target's observation: only an emplacement's own report is its entity, and
// a target carrying one would fuse into the sensor that saw it.
func stripEmplacementIdentities(o *obs.Observation) {
	kept := o.Identities[:0]
	for _, id := range o.Identities {
		if id.Type != obs.IdentityEmplacement {
			kept = append(kept, id)
		}
	}
	o.Identities = kept
}

// touch records that an emplacement reported, at most once per touchEvery
// for each one, with the position when it is a mobile one's own report.
// The throttle runs on now, the server's clock, and the row records reportAt:
// a sender's clock may be far off, and must not stall or flood the writes.
// A failed write is logged, never the report's failure.
func (d *Driver) touch(ctx context.Context, id string, pos *emplacement.Point, alt *float64, now, reportAt time.Time) {
	if d.writer == nil {
		return
	}
	d.touchMu.Lock()
	last, seen := d.touched[id]
	if seen && now.Sub(last) < touchEvery && last.Sub(now) < touchEvery {
		d.touchMu.Unlock()
		return
	}
	d.touched[id] = now
	d.touchMu.Unlock()
	tctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := d.writer.Touch(tctx, id, pos, alt, reportAt); err != nil {
		d.logger.Warn("recording an emplacement's report failed", zap.String("emplacement", id), zap.Error(err))
	}
}
