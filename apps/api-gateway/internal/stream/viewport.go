package stream

import "errors"

// Bounds is a client's visible map extent.
type Bounds struct {
	North float64 `json:"north"`
	South float64 `json:"south"`
	East  float64 `json:"east"`
	West  float64 `json:"west"`
}

// GlobalBounds covers the whole globe; new connections start here.
var GlobalBounds = Bounds{North: 90, South: -90, East: 180, West: -180}

// ErrInvalidBounds is returned by Validate for out-of-range extents.
var ErrInvalidBounds = errors.New("Invalid viewport bounds. Expected {north, south, east, west} with valid lat/lng ranges.")

// Validate checks latitude and longitude ranges and that north >= south.
func (b Bounds) Validate() error {
	if b.North < -90 || b.North > 90 || b.South < -90 || b.South > 90 ||
		b.East < -180 || b.East > 180 || b.West < -180 || b.West > 180 ||
		b.North < b.South {
		return ErrInvalidBounds
	}
	return nil
}

// Contains reports whether the point lies within the bounds. When west > east
// the extent crosses the antimeridian and wraps around.
func (b Bounds) Contains(lat, lng float64) bool {
	if lat < b.South || lat > b.North {
		return false
	}
	if b.West <= b.East {
		return lng >= b.West && lng <= b.East
	}
	return lng >= b.West || lng <= b.East
}
