package sources

import (
	"fmt"
	"time"
)

// Listener is the common interface for all ingest data-feed listeners.
// All existing types (MQTTListener, STOMPListener, TCPListener, OpenSkyListener)
// satisfy this implicitly.
type Listener interface {
	Start() error
	Stop()
}

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

// GlobalArea is the area of the whole globe in square degrees.
const GlobalArea = 180.0 * 360.0

// Viewport is the area a feed should ingest, as pushed by the gateway.
// Active false means no client is looking at the map.
type Viewport struct {
	Active  bool   `json:"active"`
	Clients int    `json:"clients"`
	Boxes   []BBox `json:"boxes"`
}

// Equal compares viewports ignoring box order and the client count.
func (v Viewport) Equal(o Viewport) bool {
	if v.Active != o.Active || len(v.Boxes) != len(o.Boxes) {
		return false
	}
	for _, b := range v.Boxes {
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

// Area returns the summed area of the boxes; no boxes means the globe.
func (v Viewport) Area() float64 {
	if len(v.Boxes) == 0 {
		return GlobalArea
	}
	total := 0.0
	for _, b := range v.Boxes {
		if b.IsGlobal() {
			return GlobalArea
		}
		total += b.Area()
	}
	return total
}

// ViewportStatus is what a viewport-aware listener reports back.
type ViewportStatus struct {
	Active            bool
	Clients           int
	Boxes             []BBox
	AreaSqDeg         float64
	EffectiveInterval time.Duration
	LastPollAt        time.Time // zero until the first successful poll
	NextPollIn        time.Duration
	ExpiresAt         time.Time // zero when the lease never expires (legacy mode)
	Idle              bool      // true when the listener is not polling
	CreditsRemaining  int       // OpenSky daily credits left per its last response; -1 when unknown
	LowCredits        bool      // true when CreditsRemaining is below the configured floor
}

// ViewportAware is implemented by listeners whose polling area can be
// changed at runtime.
type ViewportAware interface {
	SetViewport(Viewport) ViewportStatus
	ViewportStatus() ViewportStatus
}
