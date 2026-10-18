package stream

import (
	"math"
	"sort"
)

// IngestBox is a box that never crosses the antimeridian, as ingest-service
// expects it.
type IngestBox struct {
	North float64 `json:"north"`
	South float64 `json:"south"`
	East  float64 `json:"east"`
	West  float64 `json:"west"`
}

// IngestViewport is the area pushed to ingest-service: the padded bounding
// box of every client viewport, split at the antimeridian when needed.
type IngestViewport struct {
	Active  bool        `json:"active"`
	Clients int         `json:"clients"`
	Boxes   []IngestBox `json:"boxes"`

	// Union is the padded covering extent that was pushed (West > East when
	// it wraps); RawUnion is the same extent without padding. ShouldPush
	// compares a new raw extent against the previously pushed padded one, so
	// the padding doubles as hysteresis.
	Union        Bounds  `json:"-"`
	AreaSqDeg    float64 `json:"-"`
	RawUnion     Bounds  `json:"-"`
	RawAreaSqDeg float64 `json:"-"`
}

// Aggregation tuning: padding gives small pans hysteresis, and a view this
// close to the whole globe is cheaper to poll without a bounding box.
const (
	defaultPadFrac   = 0.10
	defaultPadMinDeg = 0.25
	globalLatSpan    = 162.0 // 90 % of 180
	globalLonSpan    = 324.0 // 90 % of 360
)

var globalIngestBox = IngestBox{North: 90, South: -90, East: 180, West: -180}

// lonArc is a longitude interval on the circle: start in [0, 360), length in (0, 360].
type lonArc struct {
	start, length float64
}

// Aggregate computes the ingest viewport for a set of client viewports.
// Each viewport is padded by padFrac of its span (at least padMinDeg); the
// result is the smallest box covering all of them, with the longitude union
// taken on the circle so viewports near the antimeridian stay compact.
func Aggregate(vps []Bounds, padFrac, padMinDeg float64) IngestViewport {
	if len(vps) == 0 {
		return IngestViewport{Active: false, Boxes: []IngestBox{}}
	}

	out := IngestViewport{Active: true, Clients: len(vps)}
	out.RawUnion, out.RawAreaSqDeg = unionOf(vps, 0, 0)
	union, area := unionOf(vps, padFrac, padMinDeg)

	if union.North-union.South >= globalLatSpan && lonSpanOf(union) >= globalLonSpan {
		out.Boxes = []IngestBox{globalIngestBox}
		out.Union = GlobalBounds
		out.AreaSqDeg = 180 * 360
		return out
	}

	out.Union, out.AreaSqDeg = union, area
	if union.West <= union.East {
		out.Boxes = []IngestBox{{North: union.North, South: union.South, East: union.East, West: union.West}}
	} else {
		out.Boxes = []IngestBox{
			{North: union.North, South: union.South, East: 180, West: union.West},
			{North: union.North, South: union.South, East: union.East, West: -180},
		}
	}
	return out
}

// unionOf returns the smallest extent covering every viewport after padding
// each one, and its area in square degrees.
func unionOf(vps []Bounds, padFrac, padMinDeg float64) (Bounds, float64) {
	minS, maxN := 90.0, -90.0
	arcs := make([]lonArc, 0, len(vps))
	fullLon := false
	for _, vp := range vps {
		latSpan := vp.North - vp.South
		lonSpan := lonSpanOf(vp)
		dLat := math.Max(padFrac*latSpan, padMinDeg)
		dLon := math.Max(padFrac*lonSpan, padMinDeg)

		minS = math.Min(minS, math.Max(-90, vp.South-dLat))
		maxN = math.Max(maxN, math.Min(90, vp.North+dLat))

		paddedLon := lonSpan + 2*dLon
		if paddedLon >= 360 {
			fullLon = true
			continue
		}
		arcs = append(arcs, lonArc{start: norm360(vp.West - dLon), length: paddedLon})
	}

	west, east, lonSpan := -180.0, 180.0, 360.0
	if !fullLon {
		west, east, lonSpan = coveringArc(arcs)
	}
	return Bounds{North: maxN, South: minS, East: east, West: west}, (maxN - minS) * lonSpan
}

// coveringArc returns the smallest arc (as west, east in [-180, 180] and its
// length) that covers every input arc. It is the complement of the largest
// uncovered gap on the circle.
func coveringArc(arcs []lonArc) (west, east, length float64) {
	if len(arcs) == 0 {
		return -180, 180, 360
	}
	// Unroll onto [0, 720) so every circular gap appears as a linear gap.
	type seg struct{ a, b float64 }
	segs := make([]seg, 0, 2*len(arcs))
	for _, arc := range arcs {
		segs = append(segs, seg{arc.start, arc.start + arc.length}, seg{arc.start + 360, arc.start + 360 + arc.length})
	}
	sort.Slice(segs, func(i, j int) bool { return segs[i].a < segs[j].a })

	merged := []seg{segs[0]}
	for _, s := range segs[1:] {
		last := &merged[len(merged)-1]
		if s.a <= last.b {
			if s.b > last.b {
				last.b = s.b
			}
		} else {
			merged = append(merged, s)
		}
	}

	bestGap, bestStart := 0.0, 0.0
	for i := 0; i+1 < len(merged); i++ {
		gap := merged[i+1].a - merged[i].b
		if gap > bestGap && gap < 360 {
			bestGap, bestStart = gap, merged[i].b
		}
	}
	if bestGap <= 1e-9 {
		return -180, 180, 360
	}
	length = 360 - bestGap
	west = norm180(bestStart + bestGap) // gap end
	east = norm180(bestStart)           // gap start
	return west, east, length
}

func norm360(lon float64) float64 {
	lon = math.Mod(lon, 360)
	if lon < 0 {
		lon += 360
	}
	return lon
}

func norm180(lon float64) float64 {
	lon = norm360(lon)
	if lon > 180 {
		lon -= 360
	}
	return lon
}

func lonSpanOf(b Bounds) float64 {
	if b.West <= b.East {
		return b.East - b.West
	}
	return b.East - b.West + 360
}

// boundsCover reports whether outer contains inner, on the circle.
func boundsCover(outer, inner Bounds) bool {
	if inner.South < outer.South || inner.North > outer.North {
		return false
	}
	if lonSpanOf(inner) > lonSpanOf(outer) {
		return false
	}
	mid := (inner.North + inner.South) / 2
	return outer.Contains(mid, inner.West) && outer.Contains(mid, inner.East)
}

// shrinkPushRatio: a next area below this fraction of the previous one is
// worth a push even though it is still covered, because a smaller box costs
// fewer OpenSky credits and returns fewer aircraft.
const shrinkPushRatio = 0.7

// ShouldPush decides whether next differs enough from the last pushed
// viewport to be worth another request to ingest-service.
func ShouldPush(prev *IngestViewport, next IngestViewport) bool {
	if prev == nil {
		return true
	}
	if prev.Active != next.Active {
		return true
	}
	if !next.Active {
		return false
	}
	if len(prev.Boxes) != len(next.Boxes) {
		return true
	}
	if !boundsCover(prev.Union, next.RawUnion) {
		return true
	}
	return next.RawAreaSqDeg < shrinkPushRatio*prev.RawAreaSqDeg
}

// BoundsAreaSqDeg is a viewport's area in square degrees, across the
// antimeridian when it wraps.
func BoundsAreaSqDeg(b Bounds) float64 {
	return math.Max(0, b.North-b.South) * lonSpanOf(b)
}
