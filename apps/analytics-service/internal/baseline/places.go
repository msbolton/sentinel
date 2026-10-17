package baseline

import (
	"sort"
	"time"

	"github.com/sentinel/analytics-service/internal/geo"
)

// MinPlaceRadiusM is the smallest radius a place is given, so a few
// identical fixes still make a place with some extent.
const MinPlaceRadiusM = 25

// VisitGap separates two stays at the same place.
const VisitGap = 30 * time.Minute

// BuildPlaces turns clustered slow points into places: centroid, radius
// (the 90th percentile of distance to the centroid), visits and median
// dwell from the stays, and the cluster's share of every fix in the
// window. Places are ordered by share, largest first, at most maxPlaces.
func BuildPlaces(slow []Point, labels []int, clusters, totalPoints, maxPlaces int) []Place {
	if clusters == 0 || totalPoints == 0 {
		return nil
	}
	type acc struct {
		lat, lon float64
		n        int
		first    time.Time
		last     time.Time
	}
	accs := make([]acc, clusters)
	for i, p := range slow {
		l := labels[i]
		if l == Noise {
			continue
		}
		a := &accs[l]
		a.lat += p.Lat
		a.lon += p.Lon
		a.n++
		if a.first.IsZero() || p.At.Before(a.first) {
			a.first = p.At
		}
		if p.At.After(a.last) {
			a.last = p.At
		}
	}
	places := make([]Place, 0, clusters)
	for c, a := range accs {
		if a.n == 0 {
			continue
		}
		lat, lon := a.lat/float64(a.n), a.lon/float64(a.n)
		dists := make([]float64, 0, a.n)
		for i, p := range slow {
			if labels[i] == c {
				dists = append(dists, geo.HaversineM(lat, lon, p.Lat, p.Lon))
			}
		}
		sort.Float64s(dists)
		radius := Quantile(dists, 0.9)
		if radius < MinPlaceRadiusM {
			radius = MinPlaceRadiusM
		}
		eps := Episodes(slow, labels, c, VisitGap)
		dwells := make([]float64, 0, len(eps))
		for _, e := range eps {
			dwells = append(dwells, e.End.Sub(e.Start).Seconds())
		}
		places = append(places, Place{
			ClusterIndex:   c,
			Lat:            lat,
			Lon:            lon,
			RadiusM:        radius,
			Visits:         len(eps),
			MedianDwellSec: Median(dwells),
			PointShare:     float64(a.n) / float64(totalPoints),
			FirstSeen:      a.first,
			LastSeen:       a.last,
		})
	}
	sort.SliceStable(places, func(i, j int) bool { return places[i].PointShare > places[j].PointShare })
	if maxPlaces > 0 && len(places) > maxPlaces {
		places = places[:maxPlaces]
	}
	return places
}
