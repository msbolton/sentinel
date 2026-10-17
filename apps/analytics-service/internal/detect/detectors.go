package detect

import (
	"fmt"
	"math"
	"time"

	"github.com/sentinel/analytics-service/internal/baseline"
	"github.com/sentinel/analytics-service/internal/config"
	"github.com/sentinel/analytics-service/internal/geo"
	"github.com/sentinel/analytics-service/internal/model"
)

// baselineSummary is attached to every hit's expected values so a reviewer
// can see how much history the judgement rests on.
func baselineSummary(b *baseline.Baseline) map[string]any {
	if b == nil {
		return map[string]any{"mature": false, "pointCount": 0, "days": 0}
	}
	return map[string]any{"mature": b.Mature, "pointCount": b.PointCount, "days": b.Days, "computedAt": b.ComputedAt}
}

func placeLabel(p baseline.Place) string {
	if p.Name != nil && *p.Name != "" {
		return *p.Name
	}
	return fmt.Sprintf("place %d", p.ClusterIndex+1)
}

func km(m float64) string {
	if m >= 1000 {
		return fmt.Sprintf("%.1f km", m/1000)
	}
	return fmt.Sprintf("%.0f m", m)
}

// UnusualPlace fires when a fix is farther outside the entity's nearest
// usual place than its range allows.
func UnusualPlace(b *baseline.Baseline, cur model.Fix, cfg config.DetectorConfig) *Hit {
	if b == nil || !b.Mature || len(b.Places) == 0 {
		return nil
	}
	place, d, ok := b.NearestPlace(cur.Lat, cur.Lon)
	if !ok {
		return nil
	}
	threshold := math.Max(cfg.PlaceRangeFactor*b.Stats.Range.P95M, cfg.PlaceMinM.For(cur.EntityType))
	if threshold <= 0 || d <= threshold {
		return nil
	}
	sev := SeverityMedium
	if d > 2*threshold {
		sev = SeverityHigh
	}
	return &Hit{
		Kind:     KindUnusualPlace,
		Severity: sev,
		Score:    d / threshold,
		Summary:  fmt.Sprintf("%s from %s, which it usually stays within %s of", km(d), placeLabel(place), km(b.Stats.Range.P95M)),
		Expected: map[string]any{
			"nearestPlace": map[string]any{"name": placeLabel(place), "lat": place.Lat, "lon": place.Lon, "radiusM": place.RadiusM},
			"rangeP95M":    b.Stats.Range.P95M,
			"thresholdM":   threshold,
			"baseline":     baselineSummary(b),
		},
		Actual:        map[string]any{"distanceM": d, "lat": cur.Lat, "lon": cur.Lon, "at": cur.At},
		EvidenceStart: cur.At,
		EvidenceEnd:   cur.At,
		Lat:           cur.Lat, Lon: cur.Lon, HasPosition: true,
	}
}

// UnusualTime fires when the entity is active in an hour-of-week slot that
// holds almost none of its history.
func UnusualTime(b *baseline.Baseline, cur model.Fix, cfg config.DetectorConfig) *Hit {
	if b == nil || !b.Mature || b.Days < cfg.TimeMinDays {
		return nil
	}
	slot := b.Slot(cur.At)
	share := b.SlotShare(slot)
	if share >= cfg.TimeMinShare {
		return nil
	}
	sev := SeverityLow
	if share == 0 {
		sev = SeverityMedium
	}
	score := 1.0
	if share > 0 {
		score = cfg.TimeMinShare / share
	} else {
		score = 2 * cfg.TimeMinShare / 0.001
	}
	local := cur.At.UTC().Add(time.Duration(b.Stats.Params.SolarOffsetHours) * time.Hour)
	return &Hit{
		Kind:     KindUnusualTime,
		Severity: sev,
		Score:    score,
		Summary:  fmt.Sprintf("active %s %02d:00 local, when it is normally quiet (%.1f%% of its activity)", local.Weekday(), local.Hour(), share*100),
		Expected: map[string]any{
			"slot": slot, "slotShare": share, "minShare": cfg.TimeMinShare, "busiestShare": b.MaxSlotShare(),
			"solarOffsetHours": b.Stats.Params.SolarOffsetHours, "baseline": baselineSummary(b),
		},
		Actual:        map[string]any{"at": cur.At, "localWeekday": local.Weekday().String(), "localHour": local.Hour()},
		EvidenceStart: cur.At,
		EvidenceEnd:   cur.At,
		Lat:           cur.Lat, Lon: cur.Lon, HasPosition: true,
	}
}

// UnusualMovement fires on a speed or altitude far outside the entity's
// own statistics, or on an impossible move between consecutive fixes,
// which needs no baseline and also catches two objects sharing an
// identity.
func UnusualMovement(b *baseline.Baseline, cur model.Fix, prev *model.Fix, cfg config.DetectorConfig) *Hit {
	// Impossible move first: it is the strongest signal and needs no history.
	if prev != nil {
		dt := cur.At.Sub(prev.At)
		if dt >= cfg.MoveMinGap {
			dist := geo.HaversineM(prev.Lat, prev.Lon, cur.Lat, cur.Lon)
			implied := dist / dt.Seconds() * geo.KnotsPerMPS
			if max := cfg.MoveMaxKnots.For(cur.EntityType); max > 0 && implied > max {
				return &Hit{
					Kind:     KindUnusualMovement,
					Severity: SeverityHigh,
					Score:    implied / max,
					Summary:  fmt.Sprintf("moved %s in %s, an implied %.0f kn against a physical limit of %.0f kn for a %s", km(dist), dt.Round(time.Second), implied, max, cur.EntityType),
					Expected: map[string]any{"maxKnots": max, "baseline": baselineSummary(b)},
					Actual: map[string]any{"impliedKnots": implied, "distanceM": dist, "gapSec": dt.Seconds(),
						"from": map[string]any{"lat": prev.Lat, "lon": prev.Lon, "at": prev.At}, "to": map[string]any{"lat": cur.Lat, "lon": cur.Lon, "at": cur.At}},
					EvidenceStart: prev.At,
					EvidenceEnd:   cur.At,
					Lat:           cur.Lat, Lon: cur.Lon, HasPosition: true,
				}
			}
		}
	}
	if b == nil || !b.Mature {
		return nil
	}
	if h := zHit(b, cur, "speed", cur.SpeedKnots, b.Stats.Speed, "kn", cfg); h != nil {
		return h
	}
	return zHit(b, cur, "altitude", cur.AltitudeM, b.Stats.Altitude, "m", cfg)
}

func zHit(b *baseline.Baseline, cur model.Fix, what string, value *float64, dist baseline.Dist, unit string, cfg config.DetectorConfig) *Hit {
	if value == nil || dist.N < 200 || dist.Stddev <= 0 {
		return nil
	}
	z := (*value - dist.Mean) / dist.Stddev
	if math.Abs(z) <= cfg.MoveZ {
		return nil
	}
	sev := SeverityMedium
	if math.Abs(z) > 2*cfg.MoveZ {
		sev = SeverityHigh
	}
	return &Hit{
		Kind:          KindUnusualMovement,
		Severity:      sev,
		Score:         math.Abs(z) / cfg.MoveZ,
		Summary:       fmt.Sprintf("%s %.0f %s against a usual %.0f ± %.0f %s (z = %.1f)", what, *value, unit, dist.Mean, dist.Stddev, unit, z),
		Expected:      map[string]any{"measure": what, "mean": dist.Mean, "stddev": dist.Stddev, "p95": dist.P95, "max": dist.Max, "z": cfg.MoveZ, "baseline": baselineSummary(b)},
		Actual:        map[string]any{"measure": what, "value": *value, "zScore": z, "at": cur.At},
		EvidenceStart: cur.At,
		EvidenceEnd:   cur.At,
		Lat:           cur.Lat, Lon: cur.Lon, HasPosition: true,
	}
}

// DarkThreshold is how long an entity may be silent before it is dark.
func DarkThreshold(b *baseline.Baseline, cfg config.DetectorConfig) time.Duration {
	if b == nil || b.Stats.Gap.P95Sec <= 0 {
		return 0
	}
	t := time.Duration(cfg.DarkGapFactor * b.Stats.Gap.P95Sec * float64(time.Second))
	if t < cfg.DarkMinGap {
		t = cfg.DarkMinGap
	}
	return t
}

// WentDark fires when an entity with a known reporting rhythm has been
// silent far longer than usual while other feeds still report around its
// last position (covered). It is evaluated on the tick, not on a fix.
func WentDark(b *baseline.Baseline, prev LastFix, now time.Time, covered bool, cfg config.DetectorConfig) *Hit {
	threshold := DarkThreshold(b, cfg)
	if threshold == 0 || !covered {
		return nil
	}
	gap := now.Sub(prev.Fix.At)
	if gap <= threshold {
		return nil
	}
	sev := SeverityMedium
	if gap > 3*threshold {
		sev = SeverityHigh
	}
	return &Hit{
		Kind:     KindWentDark,
		Severity: sev,
		Score:    gap.Seconds() / threshold.Seconds(),
		Summary:  fmt.Sprintf("silent for %s where it usually reports every %s or less, while other feeds still cover the area", gap.Round(time.Minute), (time.Duration(b.Stats.Gap.P95Sec) * time.Second).Round(time.Second)),
		Expected: map[string]any{"gapP95Sec": b.Stats.Gap.P95Sec, "thresholdSec": threshold.Seconds(), "baseline": baselineSummary(b)},
		Actual: map[string]any{"gapSec": gap.Seconds(), "lastAt": prev.Fix.At, "lastFeedId": prev.Fix.FeedID,
			"lastPosition": map[string]any{"lat": prev.Fix.Lat, "lon": prev.Fix.Lon}},
		EvidenceStart: prev.Fix.At,
		EvidenceEnd:   now,
		Lat:           prev.Fix.Lat, Lon: prev.Fix.Lon, HasPosition: true,
	}
}

// QuietSlot fires on the tick when an entity that is normally busy at this
// hour has not reported for a while: absence where presence is usual.
func QuietSlot(b *baseline.Baseline, prev LastFix, now time.Time, cfg config.DetectorConfig) *Hit {
	if b == nil || !b.Mature || b.Days < cfg.TimeMinDays || prev.Dark {
		return nil
	}
	slot := b.Slot(now)
	share := b.SlotShare(slot)
	if share <= cfg.TimeQuietShare {
		return nil
	}
	gap := now.Sub(prev.Fix.At)
	if gap <= cfg.TimeQuietGap {
		return nil
	}
	local := now.UTC().Add(time.Duration(b.Stats.Params.SolarOffsetHours) * time.Hour)
	return &Hit{
		Kind:          KindUnusualTime,
		Severity:      SeverityMedium,
		Score:         share / cfg.TimeQuietShare,
		Summary:       fmt.Sprintf("nothing for %s on %s around %02d:00 local, one of its busiest times (%.0f%% of its activity)", gap.Round(time.Minute), local.Weekday(), local.Hour(), share*100),
		Expected:      map[string]any{"slot": slot, "slotShare": share, "quietShare": cfg.TimeQuietShare, "quietGapSec": cfg.TimeQuietGap.Seconds(), "baseline": baselineSummary(b)},
		Actual:        map[string]any{"gapSec": gap.Seconds(), "lastAt": prev.Fix.At, "localWeekday": local.Weekday().String(), "localHour": local.Hour()},
		EvidenceStart: prev.Fix.At,
		EvidenceEnd:   now,
		Lat:           prev.Fix.Lat, Lon: prev.Fix.Lon, HasPosition: true,
	}
}

// Reappeared fires on the first fix after a silence that was found to be
// dark; the severity rises when the entity is farther from where it went
// silent than it could plausibly have travelled.
func Reappeared(b *baseline.Baseline, cur model.Fix, prev LastFix) *Hit {
	if !prev.Dark {
		return nil
	}
	gap := cur.At.Sub(prev.Fix.At)
	dist := geo.HaversineM(prev.Fix.Lat, prev.Fix.Lon, cur.Lat, cur.Lon)
	speed := 0.0
	if b != nil {
		speed = b.Stats.Speed.P95
	}
	if prev.Fix.SpeedKnots != nil && *prev.Fix.SpeedKnots > speed {
		speed = *prev.Fix.SpeedKnots
	}
	plausible := speed / geo.KnotsPerMPS * math.Max(gap.Seconds(), 0)
	sev := SeverityLow
	score := 1.0
	if plausible > 0 {
		score = dist / plausible
	}
	if dist > 1000 && (plausible == 0 || dist > 1.2*plausible) {
		sev = SeverityHigh
	}
	return &Hit{
		Kind:     KindReappeared,
		Severity: sev,
		Score:    score,
		Summary:  fmt.Sprintf("back after %s, %s from where it went silent (could plausibly have covered %s)", gap.Round(time.Minute), km(dist), km(plausible)),
		Expected: map[string]any{"plausibleM": plausible, "speedKnots": speed, "baseline": baselineSummary(b)},
		Actual: map[string]any{"distanceM": dist, "gapSec": gap.Seconds(), "darkSince": prev.Fix.At,
			"from": map[string]any{"lat": prev.Fix.Lat, "lon": prev.Fix.Lon}, "to": map[string]any{"lat": cur.Lat, "lon": cur.Lon, "at": cur.At}},
		EvidenceStart: prev.Fix.At,
		EvidenceEnd:   cur.At,
		Lat:           cur.Lat, Lon: cur.Lon, HasPosition: true,
	}
}
