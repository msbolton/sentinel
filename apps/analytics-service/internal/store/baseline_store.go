package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sentinel/analytics-service/internal/baseline"
)

// BaselineStore keeps sentinel.entity_baselines and sentinel.entity_places.
type BaselineStore struct {
	pool *pgxpool.Pool
}

// NewBaselineStore creates a baseline store.
func NewBaselineStore(pool *pgxpool.Pool) *BaselineStore {
	return &BaselineStore{pool: pool}
}

// LoadTrack returns an entity's track points in [from, to), oldest first,
// keeping at most one point per thin interval so a high-rate feed does not
// swamp the builder. Points without a position are skipped by the query.
func (s *BaselineStore) LoadTrack(ctx context.Context, entityID string, from, to time.Time, thin time.Duration) ([]baseline.Point, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT ST_Y(position), ST_X(position), altitude, "speedKnots", "timestamp"
		 FROM sentinel.track_points
		 WHERE "entityId" = $1 AND "timestamp" >= $2 AND "timestamp" < $3
		 ORDER BY "timestamp"`, entityID, from.UTC(), to.UTC())
	if err != nil {
		return nil, fmt.Errorf("load track: %w", err)
	}
	defer rows.Close()
	var out []baseline.Point
	var lastKept time.Time
	for rows.Next() {
		var p baseline.Point
		if err := rows.Scan(&p.Lat, &p.Lon, &p.AltitudeM, &p.SpeedKnots, &p.At); err != nil {
			return nil, err
		}
		if thin > 0 && !lastKept.IsZero() && p.At.Sub(lastKept) < thin {
			continue
		}
		lastKept = p.At
		out = append(out, p)
	}
	return out, rows.Err()
}

// Upsert stores a baseline and replaces its places in one transaction.
func (s *BaselineStore) Upsert(ctx context.Context, b *baseline.Baseline) error {
	stats, err := json.Marshal(b.Stats)
	if err != nil {
		return fmt.Errorf("encode stats: %w", err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	_, err = tx.Exec(ctx, `
		INSERT INTO sentinel.entity_baselines ("entityId", "entityType", "computedAt", "windowStart", "windowEnd", "pointCount", days, mature, stats, hours)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9::jsonb, $10)
		ON CONFLICT ("entityId") DO UPDATE SET
			"entityType" = EXCLUDED."entityType", "computedAt" = EXCLUDED."computedAt",
			"windowStart" = EXCLUDED."windowStart", "windowEnd" = EXCLUDED."windowEnd",
			"pointCount" = EXCLUDED."pointCount", days = EXCLUDED.days, mature = EXCLUDED.mature,
			stats = EXCLUDED.stats, hours = EXCLUDED.hours`,
		b.EntityID, b.EntityType, b.ComputedAt.UTC(), b.WindowStart.UTC(), b.WindowEnd.UTC(),
		b.PointCount, b.Days, b.Mature, string(stats), b.Hours)
	if err != nil {
		return fmt.Errorf("upsert baseline: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM sentinel.entity_places WHERE "entityId" = $1`, b.EntityID); err != nil {
		return fmt.Errorf("clear places: %w", err)
	}
	if len(b.Places) > 0 {
		values := make([]string, 0, len(b.Places))
		args := make([]any, 0, 12*len(b.Places))
		for i, p := range b.Places {
			base := 12 * i
			values = append(values, fmt.Sprintf("($%d, $%d, $%d, $%d, ST_SetSRID(ST_MakePoint($%d, $%d), 4326), $%d, $%d, $%d, $%d, $%d, $%d)",
				base+1, base+2, base+3, base+4, base+5, base+6, base+7, base+8, base+9, base+10, base+11, base+12))
			args = append(args, b.EntityID, p.ClusterIndex, p.Name, p.LocationID, p.Lon, p.Lat, p.RadiusM, p.Visits, p.MedianDwellSec, p.PointShare, p.FirstSeen.UTC(), p.LastSeen.UTC())
		}
		_, err = tx.Exec(ctx, `INSERT INTO sentinel.entity_places ("entityId", "clusterIndex", name, "locationId", centroid, "radiusM", visits, "medianDwellSec", "pointShare", "firstSeen", "lastSeen") VALUES `+strings.Join(values, ","), args...)
		if err != nil {
			return fmt.Errorf("insert places: %w", err)
		}
	}
	return tx.Commit(ctx)
}

// Get loads a baseline with its places; nil, nil when the entity has none.
func (s *BaselineStore) Get(ctx context.Context, entityID string) (*baseline.Baseline, error) {
	b := &baseline.Baseline{}
	var stats []byte
	err := s.pool.QueryRow(ctx,
		`SELECT "entityId", "entityType", "computedAt", "windowStart", "windowEnd", "pointCount", days, mature, stats, hours
		 FROM sentinel.entity_baselines WHERE "entityId" = $1`, entityID).
		Scan(&b.EntityID, &b.EntityType, &b.ComputedAt, &b.WindowStart, &b.WindowEnd, &b.PointCount, &b.Days, &b.Mature, &stats, &b.Hours)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get baseline: %w", err)
	}
	if err := json.Unmarshal(stats, &b.Stats); err != nil {
		return nil, fmt.Errorf("decode stats: %w", err)
	}
	rows, err := s.pool.Query(ctx,
		`SELECT "clusterIndex", name, "locationId"::text, ST_Y(centroid), ST_X(centroid), "radiusM", visits, "medianDwellSec", "pointShare", "firstSeen", "lastSeen"
		 FROM sentinel.entity_places WHERE "entityId" = $1 ORDER BY "pointShare" DESC`, entityID)
	if err != nil {
		return nil, fmt.Errorf("get places: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var p baseline.Place
		if err := rows.Scan(&p.ClusterIndex, &p.Name, &p.LocationID, &p.Lat, &p.Lon, &p.RadiusM, &p.Visits, &p.MedianDwellSec, &p.PointShare, &p.FirstSeen, &p.LastSeen); err != nil {
			return nil, err
		}
		b.Places = append(b.Places, p)
	}
	return b, rows.Err()
}

// NamePlace finds the nearest saved location within the larger of the
// place's radius and the location's own range, so a dwell cluster at a
// known site carries its name.
func (s *BaselineStore) NamePlace(ctx context.Context, lat, lon, radiusM float64) (name, locationID *string, err error) {
	var n, id string
	err = s.pool.QueryRow(ctx,
		`SELECT name, id::text FROM sentinel.locations
		 WHERE ST_DistanceSphere(ST_MakePoint(longitude, latitude), ST_MakePoint($1, $2)) <= GREATEST($3, "range")
		 ORDER BY ST_DistanceSphere(ST_MakePoint(longitude, latitude), ST_MakePoint($1, $2))
		 LIMIT 1`, lon, lat, radiusM).Scan(&n, &id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("name place: %w", err)
	}
	return &n, &id, nil
}

// Count returns the number of baselines.
func (s *BaselineStore) Count(ctx context.Context) (int64, error) {
	var n int64
	err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM sentinel.entity_baselines`).Scan(&n)
	return n, err
}
