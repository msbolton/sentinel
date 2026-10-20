package grpcin

import (
	"context"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/sentinel/go-common/classification"
	"github.com/sentinel/ingest-service/internal/emplacement"
	"github.com/sentinel/ingest-service/internal/obs"
)

// touchEvery is how often a report writes its emplacement's lastReportAt
// (and a mobile one's position): sensors report every second.
const touchEvery = 10 * time.Second

// claimOf is an emplacement id a sender claims, as the cache keys it: ids
// are lower-case UUIDs, and senders print them in either case.
func claimOf(id string) string { return strings.ToLower(strings.TrimSpace(id)) }

// resolve finds the emplacement a report comes from among those bound to
// this connection that are active and the token may see. An empty claim
// means the connection's only such emplacement. A connection with no
// emplacements at all (of any kind: inactive and above-the-token ones
// count) takes reports from none (ok false, no reason); an operator who
// deactivates or rebinds its only emplacement must not turn its sender's
// reports into unbound ones, losing the emplacement's level and exercise
// flag. Any other miss is unknown_emplacement, whether the emplacement is
// another connection's, above the token, inactive or does not exist: the
// answer is the same.
func (s *service) resolve(a authed, claim string) (emplacement.Emplacement, bool, string) {
	ceiling := classification.Rank(a.tok.Classification)
	usable := func(e emplacement.Emplacement) bool {
		return e.Active && e.FeedID != nil && *e.FeedID == s.d.feedID && classification.Rank(e.Classification) <= ceiling
	}
	if claim = claimOf(claim); claim != "" {
		if e, ok := s.d.emps.Get(claim); ok && usable(e) {
			return e, true, ""
		}
		return emplacement.Emplacement{}, false, "unknown_emplacement"
	}
	all := s.d.emps.ByFeed(s.d.feedID)
	if len(all) == 0 {
		return emplacement.Emplacement{}, false, ""
	}
	var found []emplacement.Emplacement
	for _, e := range all {
		if usable(e) {
			found = append(found, e)
		}
	}
	if len(found) == 1 {
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

// touch records that an emplacement reported, with the position when it is
// a mobile one's own report. A report-only touch (pos nil) and a position
// touch are throttled apart, each at most once per touchEvery for each
// emplacement: a sender streaming observations as well as states must not
// have the observations' touches use up the slot its positions need. A
// position touch also writes lastReportAt, so it counts for both.
// The throttle runs on now, the server's clock, and the row records reportAt:
// a sender's clock may be far off, and must not stall or flood the writes.
// A failed write is logged, never the report's failure.
func (d *Driver) touch(ctx context.Context, id string, pos *emplacement.Point, alt *float64, now, reportAt time.Time) {
	if d.writer == nil {
		return
	}
	d.touchMu.Lock()
	throttle := d.touched
	if pos != nil {
		throttle = d.touchedPos
	}
	last, seen := throttle[id]
	if seen && now.Sub(last) < touchEvery && last.Sub(now) < touchEvery {
		d.touchMu.Unlock()
		return
	}
	throttle[id] = now
	if pos != nil {
		d.touched[id] = now
	}
	d.touchMu.Unlock()
	tctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := d.writer.Touch(tctx, id, pos, alt, reportAt); err != nil {
		d.logger.Warn("recording an emplacement's report failed", zap.String("emplacement", id), zap.Error(err))
	}
}
