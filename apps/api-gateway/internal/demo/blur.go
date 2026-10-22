// Package demo holds the pieces of the public demo: visitor position
// blurring, visitor IP extraction and the guest-facing limits built on them.
package demo

import "math"

// BlurCellDeg is the side of the grid cell, in degrees, a visitor's position
// is snapped to. 0.1 degrees is roughly 11 km of latitude.
const BlurCellDeg = 0.1

// maxLat is the centre of the northernmost cell, so a blurred latitude never
// leaves [-90, 90].
const maxLat = 90 - BlurCellDeg/2

// Blur snaps a position to the centre of its BlurCellDeg grid cell. Every
// point in a cell maps to the same output, so the exact position cannot be
// recovered from what the server stores or publishes.
func Blur(lat, lon float64) (float64, float64) {
	lat = math.Floor(lat/BlurCellDeg)*BlurCellDeg + BlurCellDeg/2
	lon = math.Floor(lon/BlurCellDeg)*BlurCellDeg + BlurCellDeg/2

	lat = math.Max(-maxLat, math.Min(maxLat, lat))

	// Wrap into [-180, 180).
	lon = math.Mod(lon+180, 360)
	if lon < 0 {
		lon += 360
	}
	lon -= 180
	return lat, lon
}
