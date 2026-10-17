// Package baseline builds and holds what is normal for an entity: where it
// dwells, how far it ranges, when it is active, how it moves and how often
// it reports. A baseline is computed from the entity's track points and
// consulted by the detectors on every new fix.
package baseline

import (
	"math"
	"time"

	"github.com/sentinel/analytics-service/internal/geo"
)

// Slots is the number of hour-of-week slots.
const Slots = 7 * 24

// Point is one track fix as the builder sees it.
type Point struct {
	Lat, Lon   float64
	AltitudeM  *float64
	SpeedKnots *float64
	At         time.Time
}

// Place is a dwell cluster: somewhere the entity habitually stops.
type Place struct {
	ClusterIndex   int       `json:"clusterIndex"`
	Name           *string   `json:"name,omitempty"`
	LocationID     *string   `json:"locationId,omitempty"`
	Lat            float64   `json:"lat"`
	Lon            float64   `json:"lon"`
	RadiusM        float64   `json:"radiusM"`
	Visits         int       `json:"visits"`
	MedianDwellSec float64   `json:"medianDwellSec"`
	PointShare     float64   `json:"pointShare"`
	FirstSeen      time.Time `json:"firstSeen"`
	LastSeen       time.Time `json:"lastSeen"`
}

// Dist summarises a distribution of a kinematic value.
type Dist struct {
	Mean   float64 `json:"mean"`
	Stddev float64 `json:"stddev"`
	P95    float64 `json:"p95"`
	Max    float64 `json:"max"`
	N      int     `json:"n"`
}

// Stats is the numeric part of a baseline, stored as JSON.
type Stats struct {
	// Range is the distance outside the nearest place's radius, over every fix.
	Range struct {
		P50M float64 `json:"p50M"`
		P95M float64 `json:"p95M"`
	} `json:"range"`
	Speed    Dist `json:"speed"`    // knots
	Altitude Dist `json:"altitude"` // metres
	// Gap is the time between consecutive fixes.
	Gap struct {
		P50Sec float64 `json:"p50Sec"`
		P95Sec float64 `json:"p95Sec"`
	} `json:"gap"`
	Params Params `json:"params"`
}

// Params records what the baseline was built with.
type Params struct {
	WindowDays       int     `json:"windowDays"`
	ThinSec          int     `json:"thinSec"`
	SlowKnots        float64 `json:"slowKnots"`
	EpsM             float64 `json:"epsM"`
	MinPts           int     `json:"minPts"`
	SolarOffsetHours int     `json:"solarOffsetHours"`
}

// Baseline is what is normal for one entity.
type Baseline struct {
	EntityID    string    `json:"entityId"`
	EntityType  string    `json:"entityType"`
	ComputedAt  time.Time `json:"computedAt"`
	WindowStart time.Time `json:"windowStart"`
	WindowEnd   time.Time `json:"windowEnd"`
	PointCount  int       `json:"pointCount"`
	Days        int       `json:"days"`
	Mature      bool      `json:"mature"`
	Stats       Stats     `json:"stats"`
	Hours       []int16   `json:"hours"` // Slots entries: weekday*24 + hour, local solar time
	Places      []Place   `json:"places"`
}

// Slot is the hour-of-week slot of a time in this baseline's local time.
func (b *Baseline) Slot(t time.Time) int {
	return HourOfWeek(t, b.Stats.Params.SolarOffsetHours)
}

// SlotShare is the share of the entity's activity that falls in a slot.
func (b *Baseline) SlotShare(slot int) float64 {
	if b.PointCount == 0 || slot < 0 || slot >= len(b.Hours) {
		return 0
	}
	return float64(b.Hours[slot]) / float64(b.PointCount)
}

// MaxSlotShare is the share of the busiest slot.
func (b *Baseline) MaxSlotShare() float64 {
	var max int16
	for _, h := range b.Hours {
		if h > max {
			max = h
		}
	}
	if b.PointCount == 0 {
		return 0
	}
	return float64(max) / float64(b.PointCount)
}

// NearestPlace returns the place nearest a position and the distance
// outside its radius (zero when inside). ok is false without places.
func (b *Baseline) NearestPlace(lat, lon float64) (Place, float64, bool) {
	best, bestD, ok := Place{}, math.Inf(1), false
	for _, p := range b.Places {
		d := geo.HaversineM(lat, lon, p.Lat, p.Lon) - p.RadiusM
		if d < bestD {
			best, bestD, ok = p, d, true
		}
	}
	if bestD < 0 {
		bestD = 0
	}
	return best, bestD, ok
}

// HourOfWeek is the slot of a time shifted by a whole-hour solar offset:
// weekday*24 + hour, Sunday = 0.
func HourOfWeek(t time.Time, offsetHours int) int {
	local := t.UTC().Add(time.Duration(offsetHours) * time.Hour)
	return int(local.Weekday())*24 + local.Hour()
}
