package outbound

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sentinel/ingest-service/internal/source/driver"
)

// ShapeStore reads geofence rules and locations from the shared schema for
// drivers that export them. The tables belong to other services; a missing
// table yields no shapes rather than an error, so a connection still runs
// on a database without them.
type ShapeStore struct{ pool *pgxpool.Pool }

// NewShapeStore wraps a pool.
func NewShapeStore(pool *pgxpool.Pool) *ShapeStore { return &ShapeStore{pool: pool} }

// Geofences lists enabled GEOFENCE rules with a usable polygon.
func (s *ShapeStore) Geofences(ctx context.Context) ([]driver.Geofence, error) {
	rows, err := s.pool.Query(ctx, `SELECT id::text, name, config FROM sentinel.alert_rules WHERE enabled AND "ruleType" = 'GEOFENCE'`)
	if err != nil {
		if missingTable(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("geofences: %w", err)
	}
	defer rows.Close()
	var out []driver.Geofence
	for rows.Next() {
		var g driver.Geofence
		var cfg []byte
		if err := rows.Scan(&g.ID, &g.Name, &cfg); err != nil {
			return nil, fmt.Errorf("geofences: %w", err)
		}
		var c struct {
			Polygon json.RawMessage `json:"polygon"`
		}
		if json.Unmarshal(cfg, &c) != nil {
			continue
		}
		if pts, ok := ParsePolygon(c.Polygon); ok {
			g.Points = pts
			out = append(out, g)
		}
	}
	return out, rows.Err()
}

// Locations lists every location.
func (s *ShapeStore) Locations(ctx context.Context) ([]driver.Location, error) {
	rows, err := s.pool.Query(ctx, `SELECT id::text, name, COALESCE(description, ''), latitude, longitude FROM sentinel.locations`)
	if err != nil {
		if missingTable(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("locations: %w", err)
	}
	defer rows.Close()
	var out []driver.Location
	for rows.Next() {
		var l driver.Location
		if err := rows.Scan(&l.ID, &l.Name, &l.Description, &l.Lat, &l.Lon); err != nil {
			return nil, fmt.Errorf("locations: %w", err)
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

func missingTable(err error) bool {
	// 42P01 undefined_table; pgx wraps it as *pgconn.PgError but a string
	// check keeps this independent of the driver's error types.
	return err != nil && (contains(err.Error(), "42P01") || contains(err.Error(), "does not exist"))
}

func contains(s, sub string) bool { return len(sub) > 0 && len(s) >= len(sub) && indexOf(s, sub) >= 0 }

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// ParsePolygon accepts the two polygon shapes used across the platform,
// [[lng, lat], ...] or [{"latitude":..,"longitude":..}, ...], and returns
// (lat, lon) points, at least three, or ok=false.
func ParsePolygon(raw json.RawMessage) ([][2]float64, bool) {
	if len(raw) == 0 {
		return nil, false
	}
	var pairs [][]float64
	if err := json.Unmarshal(raw, &pairs); err == nil {
		var out [][2]float64
		for _, p := range pairs {
			if len(p) >= 2 {
				out = append(out, [2]float64{p[1], p[0]})
			}
		}
		return out, len(out) >= 3
	}
	var objs []struct {
		Latitude  *float64 `json:"latitude"`
		Longitude *float64 `json:"longitude"`
		Lat       *float64 `json:"lat"`
		Lng       *float64 `json:"lng"`
		Lon       *float64 `json:"lon"`
	}
	if err := json.Unmarshal(raw, &objs); err != nil {
		return nil, false
	}
	var out [][2]float64
	for _, o := range objs {
		lat, lng := o.Latitude, o.Longitude
		if lat == nil {
			lat = o.Lat
		}
		if lng == nil {
			if o.Lng != nil {
				lng = o.Lng
			} else {
				lng = o.Lon
			}
		}
		if lat != nil && lng != nil {
			out = append(out, [2]float64{*lat, *lng})
		}
	}
	return out, len(out) >= 3
}
