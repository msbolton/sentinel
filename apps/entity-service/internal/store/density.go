package store

import (
	"context"
	"fmt"

	"github.com/sentinel/go-common/classification"
)

// DensityParams select the area and grid of a density query.
type DensityParams struct {
	North, South, East, West float64 // East < West wraps across the antimeridian
	CellDegrees              float64
	Ceiling                  string // the caller's clearance
}

// DensityCell is how many live entities of one type sit in one grid cell;
// Lat and Lon are their mean position, so a disc lands where they are.
type DensityCell struct {
	EntityType string
	Lat, Lon   float64
	Count      int
}

// densityArea is the WHERE fragment for the box: one envelope, two when
// the box wraps across the antimeridian, none for the whole globe.
// Placeholders start at $first.
func densityArea(p DensityParams, first int) (string, []any) {
	if p.North-p.South >= 180 && (p.West <= -180 && p.East >= 180) {
		return "", nil
	}
	env := func(i int) string {
		return fmt.Sprintf(`e.position && ST_MakeEnvelope($%d, $%d, $%d, $%d, 4326)`, i, i+1, i+2, i+3)
	}
	if p.East >= p.West {
		return env(first), []any{p.West, p.South, p.East, p.North}
	}
	return fmt.Sprintf(`(%s OR %s)`, env(first), env(first+4)),
		[]any{p.West, p.South, 180.0, p.North, -180.0, p.South, p.East, p.North}
}

// Density counts live, positioned entities the caller may see per type and
// grid cell inside the box: what the map draws for views too wide to show
// objects one by one.
func (s *EntityStore) Density(ctx context.Context, p DensityParams) ([]DensityCell, error) {
	args := []any{p.CellDegrees, classification.Rank(classification.Normalize(p.Ceiling))}
	where := `e.deleted = false AND e."ageoutState" != 'AGED_OUT' AND e.position IS NOT NULL
		AND sentinel.classification_rank(e.classification) <= $2`
	if frag, more := densityArea(p, 3); frag != "" {
		where += " AND " + frag
		args = append(args, more...)
	}
	rows, err := s.pool.Query(ctx, fmt.Sprintf(`
		SELECT e."entityType", COUNT(*), AVG(ST_Y(e.position)), AVG(ST_X(e.position))
		FROM sentinel.entities e
		WHERE %s
		GROUP BY e."entityType", FLOOR(ST_Y(e.position) / $1), FLOOR(ST_X(e.position) / $1)`, where), args...)
	if err != nil {
		return nil, fmt.Errorf("density: %w", err)
	}
	defer rows.Close()
	var out []DensityCell
	for rows.Next() {
		var c DensityCell
		if err := rows.Scan(&c.EntityType, &c.Count, &c.Lat, &c.Lon); err != nil {
			return nil, fmt.Errorf("scan density: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
