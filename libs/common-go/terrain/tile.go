package terrain

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// tileID names a Copernicus 1° × 1° tile by its south-west corner.
type tileID struct{ lat, lon int }

// tileOf returns the tile holding a point. Latitude 90 belongs to the top row
// of tiles; longitude wraps into [-180, 180).
func tileOf(lat, lon float64) tileID {
	la := int(math.Floor(lat))
	if la > 89 {
		la = 89
	}
	if la < -90 {
		la = -90
	}
	return tileID{lat: la, lon: int(math.Floor(wrapLon(lon)))}
}

// wrapLon maps a longitude into [-180, 180).
func wrapLon(lon float64) float64 {
	lon = math.Mod(lon+180, 360)
	if lon < 0 {
		lon += 360
	}
	if lon >= 360 { // a tiny negative remainder rounds up to 360
		lon = 0
	}
	return lon - 180
}

// key is the tile's name in the Copernicus bucket, for example
// Copernicus_DSM_COG_10_N38_00_W078_00_DEM.
func (id tileID) key() string {
	ns, ew := 'N', 'E'
	la, lo := id.lat, id.lon
	if la < 0 {
		ns, la = 'S', -la
	}
	if lo < 0 {
		ew, lo = 'W', -lo
	}
	return fmt.Sprintf("Copernicus_DSM_COG_10_%c%02d_00_%c%03d_00_DEM", ns, la, ew, lo)
}

func (id tileID) String() string { return id.key() }

// TileKey returns the Copernicus GLO-30 tile name holding a point, e.g.
// "Copernicus_DSM_COG_10_N38_00_W078_00_DEM" for (38.5, -77.5).
func TileKey(lat, lon float64) string { return tileOf(lat, lon).key() }

// parseTileKey is the inverse of tileID.key.
func parseTileKey(key string) (tileID, bool) {
	rest, ok := strings.CutPrefix(key, "Copernicus_DSM_COG_10_")
	if !ok {
		return tileID{}, false
	}
	rest, ok = strings.CutSuffix(rest, "_DEM")
	// What is left looks like N38_00_W078_00.
	if !ok || len(rest) != 14 || rest[3:7] != "_00_" || rest[11:] != "_00" {
		return tileID{}, false
	}
	la, err1 := strconv.Atoi(rest[1:3])
	lo, err2 := strconv.Atoi(rest[8:11])
	if err1 != nil || err2 != nil {
		return tileID{}, false
	}
	switch rest[0] {
	case 'N':
	case 'S':
		la = -la
	default:
		return tileID{}, false
	}
	switch rest[7] {
	case 'E':
	case 'W':
		lo = -lo
	default:
		return tileID{}, false
	}
	if la < -90 || la > 89 || lo < -180 || lo > 179 {
		return tileID{}, false
	}
	return tileID{lat: la, lon: lo}, true
}

// tilesInBox lists the tiles a box touches. A box whose west edge is east of
// its east edge crosses the antimeridian. An edge on a whole degree does not
// pull in the tile beyond it, so 38..40 × -78..-76 is four tiles.
func tilesInBox(south, west, north, east float64) []tileID {
	if south > north {
		south, north = north, south
	}
	south = math.Max(-90, math.Min(90, south))
	north = math.Max(-90, math.Min(90, north))
	// lastIndex is the last whole degree a box edge reaches into.
	lastIndex := func(edge float64, first int) int {
		return max(int(math.Ceil(edge))-1, first)
	}
	lat0 := tileOf(south, 0).lat
	lat1 := min(lastIndex(north, lat0), 89)

	west, east = wrapLon(west), wrapLon(east)
	lon0 := int(math.Floor(west))
	span := east - west
	if span < 0 {
		span += 360
	}
	lon1 := lastIndex(west+span, lon0) // may run past 180; wrapped below

	var ids []tileID
	for la := lat0; la <= lat1; la++ {
		for lo := lon0; lo <= lon1 && lo < lon0+360; lo++ {
			ids = append(ids, tileID{lat: la, lon: int(math.Floor(wrapLon(float64(lo))))})
		}
	}
	return ids
}

// step returns the tile dLat, dLon tiles away, wrapping in longitude. There
// is no tile beyond a pole.
func (id tileID) step(dLat, dLon int) (tileID, bool) {
	la := id.lat + dLat
	if la < -90 || la > 89 {
		return tileID{}, false
	}
	lo := (id.lon + dLon + 180) % 360
	if lo < 0 {
		lo += 360
	}
	return tileID{lat: la, lon: lo - 180}, true
}

// tileGetter returns a neighbouring tile's raster, or nil if it has none.
type tileGetter func(tileID) *raster

func noNeighbours(tileID) *raster { return nil }

// seaRaster stands in for an unlisted tile: 0 m everywhere, with samples on
// the tile's edges so interpolation from a coastal tile runs down to sea
// level at the shared edge.
func seaRaster(id tileID) *raster {
	return &raster{width: 2, height: 2, data: make([]float32, 4),
		lon0: float64(id.lon), lat0: float64(id.lat + 1), dLon: 1, dLat: 1}
}

func (r *raster) px(col, row int) float64 { return float64(r.data[row*r.width+col]) }
func (r *raster) rowLat(row int) float64  { return r.lat0 - float64(row)*r.dLat }
func (r *raster) colLon(col int) float64  { return r.lon0 + float64(col)*r.dLon }

// rowFor returns the row nearest a latitude.
func (r *raster) rowFor(lat float64) int {
	return max(0, min(r.height-1, int(math.Round((r.lat0-lat)/r.dLat))))
}

// interior reports whether a point lies within the tile's outermost pixel
// centres, where bilinear interpolation needs no other tile.
func (r *raster) interior(lat, lon float64) bool {
	x := (wrapLon(lon) - r.lon0) / r.dLon
	y := (r.lat0 - lat) / r.dLat
	return x >= 0 && y >= 0 && x <= float64(r.width-1) && y <= float64(r.height-1)
}

// at interpolates within this raster alone, taking the edge value past the
// outermost pixel centres.
func (r *raster) at(lat, lon float64) float64 {
	return heightAt(tileID{}, r, lat, lon, noNeighbours)
}

// between interpolates linearly from (a, va) to (b, vb) at x.
func between(x, a, va, b, vb float64) float64 {
	if a == b {
		return va
	}
	return va + (vb-va)*(x-a)/(b-a)
}

// heightAt interpolates bilinearly at a point in tile id, whose raster is r.
//
// Copernicus tiles are PixelIsPoint: pixel centres run from the tile's
// north-west corner and stop one pixel short of its south and east edges,
// where the neighbouring tiles' first row and column lie. A point in that
// last strip is interpolated between this tile's outermost pixels and the
// neighbour's, so heights are continuous across tile edges. (A PixelIsArea
// tile has a half-pixel strip on every side and is handled the same way.)
// A neighbour get cannot supply is replaced by this tile's edge value.
func heightAt(id tileID, r *raster, lat, lon float64, get tileGetter) float64 {
	lon = wrapLon(lon)
	y := (r.lat0 - lat) / r.dLat
	last := float64(r.height - 1)
	switch {
	case y < 0:
		if nid, ok := id.step(1, 0); ok {
			if n := get(nid); n != nil {
				return between(lat,
					n.rowLat(n.height-1), rowValue(nid, n, n.height-1, lon, get),
					r.rowLat(0), rowValue(id, r, 0, lon, get))
			}
		}
		y = 0
	case y > last:
		if sid, ok := id.step(-1, 0); ok {
			if s := get(sid); s != nil {
				return between(lat,
					r.rowLat(r.height-1), rowValue(id, r, r.height-1, lon, get),
					s.rowLat(0), rowValue(sid, s, 0, lon, get))
			}
		}
		y = last
	}
	r0 := min(int(y), r.height-2)
	fy := y - float64(r0)
	return rowValue(id, r, r0, lon, get)*(1-fy) + rowValue(id, r, r0+1, lon, get)*fy
}

// rowValue interpolates along one row of a tile at a longitude, reaching
// into the east or west neighbour past the outermost columns.
func rowValue(id tileID, r *raster, row int, lon float64, get tileGetter) float64 {
	x := (lon - r.lon0) / r.dLon
	last := float64(r.width - 1)
	switch {
	case x < 0:
		if wid, ok := id.step(0, -1); ok {
			if w := get(wid); w != nil {
				lonW := w.colLon(w.width - 1)
				if lonW > r.lon0 { // across the antimeridian
					lonW -= 360
				}
				return between(lon, lonW, w.px(w.width-1, w.rowFor(r.rowLat(row))), r.lon0, r.px(0, row))
			}
		}
		x = 0
	case x > last:
		if eid, ok := id.step(0, 1); ok {
			if e := get(eid); e != nil {
				lonE := e.lon0
				if lonE < r.colLon(r.width-1) { // across the antimeridian
					lonE += 360
				}
				return between(lon, r.colLon(r.width-1), r.px(r.width-1, row), lonE, e.px(0, e.rowFor(r.rowLat(row))))
			}
		}
		x = last
	}
	c0 := min(int(x), r.width-2)
	fx := x - float64(c0)
	return r.px(c0, row)*(1-fx) + r.px(c0+1, row)*fx
}
