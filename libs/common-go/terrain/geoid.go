// Package terrain provides ground heights and the vertical datum conversion
// that goes with them.
//
// Sentinel keeps every altitude as metres above the WGS84 ellipsoid (HAE).
// Terrain models such as Copernicus GLO-30 give heights above mean sea level,
// so this package converts between the two with the EGM96 geoid.
//
// Copernicus tiles are PixelIsPoint: a tile's pixel centres stop one pixel
// (about 30 m) short of its south and east edges, where the neighbouring
// tile's first row and column lie. A height in that strip is interpolated
// with the neighbour only if the neighbour is already in memory or on disk.
// Otherwise the neighbour's download starts in the background and the tile's
// own edge value is returned meanwhile, so the same point can read
// differently once the neighbour arrives; the change is at most the local
// slope times one pixel.
package terrain

import (
	_ "embed"
	"encoding/binary"
	"math"
	"sync"
)

// The geoid is NGA's EGM96 15-arc-minute undulation grid, WW15MGH.GRD:
//
//	source:  https://earth-info.nga.mil/php/download.php?file=egm-96interpolation
//	         (EGM96_Interpolation_Grid.zip, 2,870,765 bytes)
//	zip SHA-256:          f051316cdf794a87e8291bcb7aee82d0e49a99c35301b6b82462fbe5078b7216
//	WW15MGH.GRD SHA-256:  4bcc2c8b2314528530de6d1dccbd0ff26cd15912c892d48a6ad38b3ed79bdbae
//
// egm96_15.bin is that grid converted by internal/cmd/geoidgen: int16
// centimetres, little endian, row-major, 721 rows from 90N to 90S by 1441
// columns from 0E to 360E (the last column repeats the first), 2,077,922
// bytes. The raw grid is not kept in the repository. The conversion rounds
// each value from millimetres to the nearest centimetre.
//
//go:embed egm96_15.bin
var egm96Raw []byte

const (
	geoidRows = 721
	geoidCols = 1441
	geoidStep = 0.25 // degrees
)

var (
	geoidOnce sync.Once
	geoidGrid []int16
)

func geoid() []int16 {
	geoidOnce.Do(func() {
		if len(egm96Raw) != geoidRows*geoidCols*2 {
			panic("terrain: egm96_15.bin has the wrong size")
		}
		g := make([]int16, geoidRows*geoidCols)
		for i := range g {
			g[i] = int16(binary.LittleEndian.Uint16(egm96Raw[2*i:]))
		}
		geoidGrid = g
	})
	return geoidGrid
}

// GeoidHeight returns the EGM96 geoid undulation N in metres at a point,
// interpolated bilinearly on the 15-arc-minute grid. N is the height of mean
// sea level above the WGS84 ellipsoid, so ellipsoidal height = MSL + N.
//
// Latitude is clamped to [-90, 90]; longitude may be in any range and wraps.
// A NaN or infinite input gives NaN.
func GeoidHeight(lat, lon float64) float64 {
	if math.IsNaN(lat) || math.IsNaN(lon) || math.IsInf(lat, 0) || math.IsInf(lon, 0) {
		return math.NaN()
	}
	lat = math.Max(-90, math.Min(90, lat))
	lon = math.Mod(lon, 360)
	if lon < 0 {
		lon += 360
	}
	if lon >= 360 { // a tiny negative longitude rounds up to exactly 360
		lon = 0
	}

	row := (90 - lat) / geoidStep
	col := lon / geoidStep
	r0 := int(row)
	if r0 > geoidRows-2 { // lat == -90 sits on the last row
		r0 = geoidRows - 2
	}
	c0 := int(col) // < 1440, so c0+1 is a valid column
	fr := row - float64(r0)
	fc := col - float64(c0)

	g := geoid()
	at := func(r, c int) float64 { return float64(g[r*geoidCols+c]) }
	top := at(r0, c0)*(1-fc) + at(r0, c0+1)*fc
	bot := at(r0+1, c0)*(1-fc) + at(r0+1, c0+1)*fc
	return (top*(1-fr) + bot*fr) / 100
}

// MSLToHAE converts a height above mean sea level to a height above the WGS84
// ellipsoid at the given point.
func MSLToHAE(lat, lon, msl float64) float64 { return msl + GeoidHeight(lat, lon) }

// HAEToMSL converts a height above the WGS84 ellipsoid to a height above mean
// sea level at the given point.
func HAEToMSL(lat, lon, hae float64) float64 { return hae - GeoidHeight(lat, lon) }
