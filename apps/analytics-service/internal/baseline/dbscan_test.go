package baseline

import (
	"math"
	"math/rand"
	"testing"
)

func blob(r *rand.Rand, cx, cy, sigma float64, n int) []XY {
	out := make([]XY, n)
	for i := range out {
		out[i] = XY{X: cx + r.NormFloat64()*sigma, Y: cy + r.NormFloat64()*sigma}
	}
	return out
}

func TestDBSCANFindsBlobsAndNoise(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	var pts []XY
	pts = append(pts, blob(r, 0, 0, 20, 200)...)
	pts = append(pts, blob(r, 5000, 5000, 20, 150)...)
	// Sparse noise far from everything.
	for i := 0; i < 30; i++ {
		pts = append(pts, XY{X: 20000 + r.Float64()*100000, Y: -50000 + r.Float64()*100000})
	}
	labels, n := DBSCAN(pts, 150, 10)
	if n != 2 {
		t.Fatalf("clusters = %d, want 2", n)
	}
	first := labels[0]
	for i := 0; i < 200; i++ {
		if labels[i] != first {
			t.Fatalf("blob 1 point %d has label %d, want %d", i, labels[i], first)
		}
	}
	second := labels[200]
	if second == first {
		t.Fatal("both blobs share a label")
	}
	for i := 200; i < 350; i++ {
		if labels[i] != second {
			t.Fatalf("blob 2 point %d has label %d, want %d", i, labels[i], second)
		}
	}
	for i := 350; i < len(pts); i++ {
		if labels[i] != Noise {
			t.Errorf("noise point %d labelled %d", i, labels[i])
		}
	}
}

// naiveDBSCAN is the textbook O(n²) algorithm, used to check the grid.
func naiveDBSCAN(xy []XY, eps float64, minPts int) ([]int, int) {
	n := len(xy)
	labels := make([]int, n)
	for i := range labels {
		labels[i] = Noise
	}
	visited := make([]bool, n)
	region := func(i int) []int {
		var out []int
		for j := range xy {
			if math.Hypot(xy[j].X-xy[i].X, xy[j].Y-xy[i].Y) <= eps {
				out = append(out, j)
			}
		}
		return out
	}
	clusters := 0
	for i := 0; i < n; i++ {
		if visited[i] {
			continue
		}
		visited[i] = true
		nb := region(i)
		if len(nb) < minPts {
			continue
		}
		labels[i] = clusters
		queue := append([]int(nil), nb...)
		for len(queue) > 0 {
			j := queue[0]
			queue = queue[1:]
			if labels[j] == Noise {
				labels[j] = clusters
			}
			if visited[j] {
				continue
			}
			visited[j] = true
			labels[j] = clusters
			if more := region(j); len(more) >= minPts {
				queue = append(queue, more...)
			}
		}
		clusters++
	}
	return labels, clusters
}

func TestDBSCANMatchesNaiveOnRandomData(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	var pts []XY
	for c := 0; c < 6; c++ {
		pts = append(pts, blob(r, r.Float64()*4000, r.Float64()*4000, 30+r.Float64()*60, 150+r.Intn(200))...)
	}
	for i := 0; i < 400; i++ {
		pts = append(pts, XY{X: r.Float64() * 4000, Y: r.Float64() * 4000})
	}
	fast, nf := DBSCAN(pts, 120, 8)
	slow, ns := naiveDBSCAN(pts, 120, 8)
	if nf != ns {
		t.Fatalf("grid found %d clusters, naive %d", nf, ns)
	}
	// Labels may be numbered differently; compare the partition.
	for i := range pts {
		if (fast[i] == Noise) != (slow[i] == Noise) {
			t.Fatalf("point %d: grid noise=%v naive noise=%v", i, fast[i] == Noise, slow[i] == Noise)
		}
	}
	mapping := map[int]int{}
	for i := range pts {
		if fast[i] == Noise {
			continue
		}
		if m, ok := mapping[fast[i]]; ok {
			if m != slow[i] {
				t.Fatalf("point %d: grid cluster %d maps to naive %d and %d", i, fast[i], m, slow[i])
			}
		} else {
			mapping[fast[i]] = slow[i]
		}
	}
}

func TestDBSCANEdgeCases(t *testing.T) {
	if labels, n := DBSCAN(nil, 10, 3); n != 0 || len(labels) != 0 {
		t.Error("empty input")
	}
	labels, n := DBSCAN([]XY{{0, 0}, {1, 1}}, 10, 5)
	if n != 0 || labels[0] != Noise || labels[1] != Noise {
		t.Error("too few points must all be noise")
	}
}

func TestProjectAcrossAntimeridian(t *testing.T) {
	pts := []Point{{Lat: 0, Lon: 179.9}, {Lat: 0, Lon: -179.9}}
	xy := Project(pts)
	if d := math.Abs(xy[0].X - xy[1].X); d < 20000 || d > 25000 {
		t.Errorf("projected separation %.0f m, want about 22 km", d)
	}
}
