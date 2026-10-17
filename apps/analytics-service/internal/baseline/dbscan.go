package baseline

import (
	"math"

	"github.com/sentinel/analytics-service/internal/geo"
)

// XY is a position in local metres.
type XY struct{ X, Y float64 }

// Project maps positions to local metres with an equirectangular projection
// around their mean, which is accurate enough for the footprint of one
// entity's dwell points.
func Project(points []Point) []XY {
	if len(points) == 0 {
		return nil
	}
	var lat0 float64
	for _, p := range points {
		lat0 += p.Lat
	}
	lat0 /= float64(len(points))
	lon0 := MeanLon(points)
	cos := math.Cos(lat0 * math.Pi / 180)
	const mPerDeg = geo.EarthRadiusM * math.Pi / 180
	out := make([]XY, len(points))
	for i, p := range points {
		dLon := p.Lon - lon0
		// Keep the projection continuous across the antimeridian.
		if dLon > 180 {
			dLon -= 360
		} else if dLon < -180 {
			dLon += 360
		}
		out[i] = XY{X: dLon * cos * mPerDeg, Y: (p.Lat - lat0) * mPerDeg}
	}
	return out
}

// MeanLon is the circular mean of the points' longitudes, so a footprint
// straddling the antimeridian averages to about 180, not 0.
func MeanLon(points []Point) float64 {
	var sx, sy float64
	for _, p := range points {
		r := p.Lon * math.Pi / 180
		sx += math.Cos(r)
		sy += math.Sin(r)
	}
	if sx == 0 && sy == 0 {
		return 0
	}
	return math.Atan2(sy, sx) * 180 / math.Pi
}

// Noise is the label of a point that belongs to no cluster.
const Noise = -1

// DBSCAN clusters points within eps metres of each other, a cluster needing
// at least minPts points in some neighbourhood. Labels are 0..n-1 or Noise.
// Neighbour search uses a grid of eps-sized buckets, so the cost is linear
// in the number of points for the densities a track produces, and the
// expansion uses an explicit queue so deep clusters cannot overflow the
// stack. The result is deterministic for a given input order.
func DBSCAN(xy []XY, eps float64, minPts int) (labels []int, clusters int) {
	n := len(xy)
	labels = make([]int, n)
	for i := range labels {
		labels[i] = Noise
	}
	if n == 0 || eps <= 0 || minPts <= 0 {
		return labels, 0
	}
	grid := newGrid(xy, eps)
	visited := make([]bool, n)
	eps2 := eps * eps
	queue := make([]int, 0, 64)
	for i := 0; i < n; i++ {
		if visited[i] {
			continue
		}
		visited[i] = true
		neighbours := grid.query(xy, i, eps2)
		if len(neighbours) < minPts {
			continue // noise for now; may be claimed as a border point later
		}
		labels[i] = clusters
		queue = append(queue[:0], neighbours...)
		for len(queue) > 0 {
			j := queue[0]
			queue = queue[1:]
			if labels[j] == Noise {
				labels[j] = clusters // border point
			}
			if visited[j] {
				continue
			}
			visited[j] = true
			labels[j] = clusters
			more := grid.query(xy, j, eps2)
			if len(more) >= minPts {
				queue = append(queue, more...)
			}
		}
		clusters++
	}
	return labels, clusters
}

type cellKey [2]int

type grid struct {
	size  float64
	cells map[cellKey][]int
}

func newGrid(xy []XY, size float64) *grid {
	g := &grid{size: size, cells: make(map[cellKey][]int, len(xy)/4+1)}
	for i, p := range xy {
		k := g.key(p)
		g.cells[k] = append(g.cells[k], i)
	}
	return g
}

func (g *grid) key(p XY) cellKey {
	return cellKey{int(math.Floor(p.X / g.size)), int(math.Floor(p.Y / g.size))}
}

// query returns every index within sqrt(eps2) of point i, including i.
func (g *grid) query(xy []XY, i int, eps2 float64) []int {
	p := xy[i]
	k := g.key(p)
	var out []int
	for dx := -1; dx <= 1; dx++ {
		for dy := -1; dy <= 1; dy++ {
			for _, j := range g.cells[cellKey{k[0] + dx, k[1] + dy}] {
				q := xy[j]
				ddx, ddy := q.X-p.X, q.Y-p.Y
				if ddx*ddx+ddy*ddy <= eps2 {
					out = append(out, j)
				}
			}
		}
	}
	return out
}
