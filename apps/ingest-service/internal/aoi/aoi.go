// Package aoi describes the area of interest an area-aware driver polls:
// the boxes connected map clients can see, pushed by the API gateway and
// stored in feed_areas, plus the status such a driver reports back.
package aoi

import (
	"fmt"
	"time"
)

// BBox is a latitude/longitude box that never crosses the antimeridian:
// South <= North and West <= East. The gateway splits a viewport that wraps
// around 180° into two boxes before pushing it.
type BBox struct {
	North float64 `json:"north"`
	South float64 `json:"south"`
	East  float64 `json:"east"`
	West  float64 `json:"west"`
}

// Validate checks ranges and ordering.
func (b BBox) Validate() error {
	if b.North < -90 || b.North > 90 || b.South < -90 || b.South > 90 {
		return fmt.Errorf("latitude out of range: south=%v north=%v", b.South, b.North)
	}
	if b.East < -180 || b.East > 180 || b.West < -180 || b.West > 180 {
		return fmt.Errorf("longitude out of range: west=%v east=%v", b.West, b.East)
	}
	if b.South > b.North {
		return fmt.Errorf("south (%v) is north of north (%v)", b.South, b.North)
	}
	if b.West > b.East {
		return fmt.Errorf("west (%v) is east of east (%v); split boxes at the antimeridian", b.West, b.East)
	}
	return nil
}

// Area returns the box area in square degrees, the unit OpenSky prices
// bounding-box queries in.
func (b BBox) Area() float64 {
	return (b.North - b.South) * (b.East - b.West)
}

// IsGlobal reports whether the box covers at least 90 % of both spans, in
// which case polling without a bounding box is cheaper and equivalent.
func (b BBox) IsGlobal() bool {
	return b.North-b.South >= 162 && b.East-b.West >= 324
}

// Contains reports whether a position lies in the box (edges included); a
// global box takes everything, whatever its nominal span.
func (b BBox) Contains(lat, lon float64) bool {
	if b.IsGlobal() {
		return true
	}
	return lat >= b.South && lat <= b.North && lon >= b.West && lon <= b.East
}

// Center returns the box centre.
func (b BBox) Center() (lat, lon float64) {
	return (b.North + b.South) / 2, (b.East + b.West) / 2
}

// GlobalArea is the area of the whole globe in square degrees.
const GlobalArea = 180.0 * 360.0

// Area is the area a feed should ingest. Active false means no client is
// looking at the map. ExpiresAt is the lease end stored in feed_areas; zero
// means the area never expires.
type Area struct {
	Active    bool
	Clients   int
	Boxes     []BBox
	ExpiresAt time.Time
}

// Equal compares areas ignoring box order, the client count and expiry.
func (a Area) Equal(o Area) bool {
	if a.Active != o.Active || len(a.Boxes) != len(o.Boxes) {
		return false
	}
	for _, b := range a.Boxes {
		found := false
		for _, c := range o.Boxes {
			if b == c {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// Expired reports whether the lease has lapsed at now.
func (a Area) Expired(now time.Time) bool {
	return !a.ExpiresAt.IsZero() && now.After(a.ExpiresAt)
}

// SqDeg returns the summed area of the boxes; no boxes means the globe.
func (a Area) SqDeg() float64 {
	if len(a.Boxes) == 0 {
		return GlobalArea
	}
	total := 0.0
	for _, b := range a.Boxes {
		if b.IsGlobal() {
			return GlobalArea
		}
		total += b.Area()
	}
	return total
}

// Status is what an area-aware driver reports back.
type Status struct {
	Active            bool
	Clients           int
	Boxes             []BBox
	AreaSqDeg         float64
	EffectiveInterval time.Duration
	LastPollAt        time.Time // zero until the first successful poll
	NextPollIn        time.Duration
	ExpiresAt         time.Time // zero when the lease never expires
	Idle              bool      // true when the driver is not polling
	CreditsRemaining  int       // remote quota left per its last response; -1 when unknown
	LowCredits        bool      // true when CreditsRemaining is below the configured floor
}

// AreaAware is implemented by drivers whose polling area can be changed at
// runtime.
type AreaAware interface {
	SetAreaOfInterest(Area) Status
	AreaStatus() Status
}
