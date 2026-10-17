// Package geo holds the small amount of geometry the analytics service
// needs: great-circle distance, the coverage grid, and local solar time.
package geo

import (
	"fmt"
	"math"
)

// EarthRadiusM is the mean Earth radius in metres.
const EarthRadiusM = 6371008.8

// HaversineM returns the great-circle distance in metres between two
// WGS84 positions.
func HaversineM(lat1, lon1, lat2, lon2 float64) float64 {
	const d = math.Pi / 180
	phi1, phi2 := lat1*d, lat2*d
	dPhi := (lat2 - lat1) * d
	dLambda := (lon2 - lon1) * d
	a := math.Sin(dPhi/2)*math.Sin(dPhi/2) + math.Cos(phi1)*math.Cos(phi2)*math.Sin(dLambda/2)*math.Sin(dLambda/2)
	return 2 * EarthRadiusM * math.Asin(math.Min(1, math.Sqrt(a)))
}

// Cell is the coverage grid cell a position falls in, for cells of cellDeg
// degrees: "<row>:<col>" from the floor of lat and lon divided by the cell
// size, so cells are stable across calls and tile without gaps.
func Cell(lat, lon, cellDeg float64) string {
	if cellDeg <= 0 {
		cellDeg = 0.05
	}
	return fmt.Sprintf("%d:%d", int(math.Floor(lat/cellDeg)), int(math.Floor(lon/cellDeg)))
}

// SolarOffsetHours is the whole-hour offset of local solar time from UTC
// at a longitude: 15 degrees per hour, rounded, so an entity's hour-of-week
// histogram has its night where the sun is down.
func SolarOffsetHours(lon float64) int {
	return int(math.Round(lon / 15))
}

// KnotsPerMPS converts metres per second to knots when multiplied.
const KnotsPerMPS = 1.943844
