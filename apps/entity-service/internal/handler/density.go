package handler

import (
	"net/url"
	"strconv"
	"time"

	"github.com/sentinel/entity-service/internal/store"
)

const (
	defaultDensityCell = 5.0
	minDensityCell     = 0.5
	maxDensityCell     = 30.0
)

// parseDensityQuery reads the box and cell size; it returns a problem
// message for a bad request.
func parseDensityQuery(q url.Values) (store.DensityParams, string) {
	p := store.DensityParams{CellDegrees: defaultDensityCell}
	read := func(name string, lo, hi float64, dst *float64) string {
		v, err := strconv.ParseFloat(q.Get(name), 64)
		if err != nil || v < lo || v > hi {
			return name + " must be a number from " + strconv.FormatFloat(lo, 'f', -1, 64) + " to " + strconv.FormatFloat(hi, 'f', -1, 64)
		}
		*dst = v
		return ""
	}
	for _, f := range []struct {
		name   string
		lo, hi float64
		dst    *float64
	}{
		{"north", -90, 90, &p.North}, {"south", -90, 90, &p.South},
		{"east", -180, 180, &p.East}, {"west", -180, 180, &p.West},
	} {
		if problem := read(f.name, f.lo, f.hi, f.dst); problem != "" {
			return p, problem
		}
	}
	if p.South > p.North {
		return p, "south must not be north of north"
	}
	if q.Get("cell") != "" {
		if problem := read("cell", minDensityCell, maxDensityCell, &p.CellDegrees); problem != "" {
			return p, problem
		}
	}
	return p, ""
}

// densityResponse is the JSON body: cells as [entityType, lat, lon, count].
func densityResponse(cell float64, cells []store.DensityCell) map[string]any {
	out := make([][4]any, 0, len(cells))
	total := 0
	for _, c := range cells {
		out = append(out, [4]any{c.EntityType, c.Lat, c.Lon, c.Count})
		total += c.Count
	}
	return map[string]any{
		"cellDegrees": cell,
		"generatedAt": time.Now().UTC().Format(time.RFC3339),
		"total":       total,
		"cells":       out,
	}
}
