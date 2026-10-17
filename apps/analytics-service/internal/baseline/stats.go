package baseline

import (
	"math"
	"sort"
	"time"
)

// Quantile returns the q-th quantile (0..1) of sorted values with linear
// interpolation between neighbours; zero for no values.
func Quantile(sorted []float64, q float64) float64 {
	n := len(sorted)
	if n == 0 {
		return 0
	}
	if n == 1 || q <= 0 {
		return sorted[0]
	}
	if q >= 1 {
		return sorted[n-1]
	}
	pos := q * float64(n-1)
	lo := int(math.Floor(pos))
	hi := lo + 1
	if hi >= n {
		return sorted[lo]
	}
	frac := pos - float64(lo)
	return sorted[lo] + (sorted[hi]-sorted[lo])*frac
}

// Median is the 0.5 quantile of unsorted values.
func Median(values []float64) float64 {
	s := append([]float64(nil), values...)
	sort.Float64s(s)
	return Quantile(s, 0.5)
}

// MeanStd returns the mean and population standard deviation.
func MeanStd(values []float64) (mean, std float64) {
	n := float64(len(values))
	if n == 0 {
		return 0, 0
	}
	for _, v := range values {
		mean += v
	}
	mean /= n
	for _, v := range values {
		d := v - mean
		std += d * d
	}
	return mean, math.Sqrt(std / n)
}

// Summarise builds a Dist from unsorted values.
func Summarise(values []float64) Dist {
	if len(values) == 0 {
		return Dist{}
	}
	s := append([]float64(nil), values...)
	sort.Float64s(s)
	mean, std := MeanStd(s)
	return Dist{Mean: mean, Stddev: std, P95: Quantile(s, 0.95), Max: s[len(s)-1], N: len(s)}
}

// Episode is one continuous stay at a place.
type Episode struct {
	Start, End time.Time
}

// Episodes splits the time-ordered points of one cluster into stays: a new
// stay begins after a gap longer than maxGap, or when the entity was seen
// at a different cluster in between (prevLabel differs from the cluster).
// labels and points are parallel and time-ordered over the whole slow set;
// cluster selects which stays to return.
func Episodes(points []Point, labels []int, cluster int, maxGap time.Duration) []Episode {
	var out []Episode
	var cur *Episode
	lastLabel := Noise
	var lastAt time.Time
	for i, p := range points {
		l := labels[i]
		if l != cluster {
			if l != Noise {
				lastLabel = l
			}
			continue
		}
		if cur == nil || lastLabel != cluster || p.At.Sub(lastAt) > maxGap {
			out = append(out, Episode{Start: p.At, End: p.At})
			cur = &out[len(out)-1]
		} else {
			cur.End = p.At
		}
		lastLabel = cluster
		lastAt = p.At
	}
	return out
}
