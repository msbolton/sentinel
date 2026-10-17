package baseline

import (
	"sort"
	"time"

	"github.com/sentinel/analytics-service/internal/geo"
)

// Config tunes the builder.
type Config struct {
	WindowDays    int
	ThinSec       int
	SlowKnots     float64 // fixes at or below this speed are dwelling
	EpsM          float64 // DBSCAN neighbourhood
	MinPts        int     // DBSCAN core threshold
	MaxSlowPoints int     // stride-sample the slow set to this many points
	MinPoints     int     // maturity gate
	MinDays       int     // maturity gate
	MaxPlaces     int
}

// Build computes a baseline from an entity's points in a window. Points
// need not be sorted. A track with no slow fixes still yields a baseline
// (hours, movement and gaps) with no places, so the time and movement
// detectors can work while the place detector stays off.
func Build(entityID, entityType string, points []Point, windowStart, windowEnd time.Time, c Config) *Baseline {
	pts := append([]Point(nil), points...)
	sort.SliceStable(pts, func(i, j int) bool { return pts[i].At.Before(pts[j].At) })

	b := &Baseline{
		EntityID:    entityID,
		EntityType:  entityType,
		ComputedAt:  time.Now().UTC(),
		WindowStart: windowStart,
		WindowEnd:   windowEnd,
		PointCount:  len(pts),
		Hours:       make([]int16, Slots),
	}
	b.Stats.Params = Params{WindowDays: c.WindowDays, ThinSec: c.ThinSec, SlowKnots: c.SlowKnots, EpsM: c.EpsM, MinPts: c.MinPts}
	if len(pts) == 0 {
		return b
	}

	// Local solar time from the mean longitude.
	b.Stats.Params.SolarOffsetHours = geo.SolarOffsetHours(MeanLon(pts))

	// Days covered and the hour-of-week histogram, both in local time.
	days := map[string]bool{}
	offset := time.Duration(b.Stats.Params.SolarOffsetHours) * time.Hour
	for _, p := range pts {
		local := p.At.UTC().Add(offset)
		days[local.Format("2006-01-02")] = true
		b.Hours[HourOfWeek(p.At, b.Stats.Params.SolarOffsetHours)]++
	}
	b.Days = len(days)
	b.Mature = b.PointCount >= c.MinPoints && b.Days >= c.MinDays

	// Movement statistics.
	var speeds, alts, gaps []float64
	for i, p := range pts {
		if p.SpeedKnots != nil {
			speeds = append(speeds, *p.SpeedKnots)
		}
		if p.AltitudeM != nil {
			alts = append(alts, *p.AltitudeM)
		}
		if i > 0 {
			if g := p.At.Sub(pts[i-1].At).Seconds(); g > 0 {
				gaps = append(gaps, g)
			}
		}
	}
	b.Stats.Speed = Summarise(speeds)
	b.Stats.Altitude = Summarise(alts)
	if len(gaps) > 0 {
		sort.Float64s(gaps)
		b.Stats.Gap.P50Sec = Quantile(gaps, 0.5)
		b.Stats.Gap.P95Sec = Quantile(gaps, 0.95)
	}

	// Places from the slow fixes.
	slow := slowPoints(pts, c.SlowKnots)
	if c.MaxSlowPoints > 0 && len(slow) > c.MaxSlowPoints {
		stride := (len(slow) + c.MaxSlowPoints - 1) / c.MaxSlowPoints
		sampled := make([]Point, 0, c.MaxSlowPoints)
		for i := 0; i < len(slow); i += stride {
			sampled = append(sampled, slow[i])
		}
		slow = sampled
	}
	if len(slow) >= c.MinPts {
		labels, n := DBSCAN(Project(slow), c.EpsM, c.MinPts)
		b.Places = BuildPlaces(slow, labels, n, len(pts), c.MaxPlaces)
	}

	// Range: distance outside the nearest place, over every fix.
	if len(b.Places) > 0 {
		ds := make([]float64, 0, len(pts))
		for _, p := range pts {
			_, d, _ := b.NearestPlace(p.Lat, p.Lon)
			ds = append(ds, d)
		}
		sort.Float64s(ds)
		b.Stats.Range.P50M = Quantile(ds, 0.5)
		b.Stats.Range.P95M = Quantile(ds, 0.95)
	}
	return b
}

// slowPoints keeps the fixes that count as dwelling: reported speed at or
// below the threshold, or, without a speed, an implied speed from the
// previous fix at or below it.
func slowPoints(sorted []Point, slowKnots float64) []Point {
	var out []Point
	for i, p := range sorted {
		switch {
		case p.SpeedKnots != nil:
			if *p.SpeedKnots <= slowKnots {
				out = append(out, p)
			}
		case i > 0:
			dt := p.At.Sub(sorted[i-1].At).Seconds()
			if dt <= 0 {
				continue
			}
			mps := geo.HaversineM(sorted[i-1].Lat, sorted[i-1].Lon, p.Lat, p.Lon) / dt
			if mps*geo.KnotsPerMPS <= slowKnots {
				out = append(out, p)
			}
		}
	}
	return out
}
