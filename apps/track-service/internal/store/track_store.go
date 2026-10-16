package store

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/sentinel/go-common/classification"
	"github.com/sentinel/track-service/internal/models"
)

// EntityClassification returns an entity's stored classification, or
// UNCLASSIFIED when the entity row is gone (its points may remain).
func (s *TrackStore) EntityClassification(ctx context.Context, entityID string) (string, error) {
	var level string
	err := s.pool.QueryRow(ctx, `SELECT classification FROM sentinel.entities WHERE id = $1`, entityID).Scan(&level)
	if err == pgx.ErrNoRows {
		return classification.Unclassified, nil
	}
	if err != nil {
		return "", fmt.Errorf("entity classification: %w", err)
	}
	return level, nil
}

// TrackStore handles all database operations for track points.
type TrackStore struct {
	pool   *pgxpool.Pool
	logger *zap.Logger
}

// NewTrackStore creates a new track store connected to PostgreSQL.
func NewTrackStore(pool *pgxpool.Pool, logger *zap.Logger) *TrackStore {
	return &TrackStore{pool: pool, logger: logger}
}

// BulkInsert performs a bulk INSERT of track points using parameterized SQL.
// paramsPerRow is the number of bind parameters one track point needs.
const paramsPerRow = 17

// maxRowsPerInsert keeps a multi-row INSERT under PostgreSQL's 65,535 bind
// parameter limit (17 parameters per row allows 3,855). A backlog replayed
// at startup can easily exceed that in one flush.
const maxRowsPerInsert = 3800

// BulkInsert writes points with as few multi-row INSERTs as the parameter
// limit allows. On error the whole batch is reported failed so the batcher
// can retry it; rows already written in earlier chunks are not rolled back
// (track points are append-only and a duplicate is harmless).
func (s *TrackStore) BulkInsert(ctx context.Context, points []models.BufferedPoint) error {
	for start := 0; start < len(points); start += maxRowsPerInsert {
		end := start + maxRowsPerInsert
		if end > len(points) {
			end = len(points)
		}
		query, args := buildInsert(points[start:end])
		if _, err := s.pool.Exec(ctx, query, args...); err != nil {
			return err
		}
	}
	return nil
}

// buildInsert renders one multi-row INSERT for up to maxRowsPerInsert points.
func buildInsert(points []models.BufferedPoint) (string, []interface{}) {
	var b strings.Builder
	b.WriteString(`INSERT INTO sentinel.track_points ("entityId", "source", "position", "heading", "speedKnots", "course", "timestamp", "altitude", "velocityNorth", "velocityEast", "velocityUp", "circularError", "feedId", "sourceEntityId", "observationId", "entityType") VALUES `)

	args := make([]interface{}, 0, len(points)*paramsPerRow)
	for i, p := range points {
		if i > 0 {
			b.WriteString(", ")
		}
		idx := i*paramsPerRow + 1
		b.WriteString(fmt.Sprintf(
			"($%d, $%d, ST_SetSRID(ST_MakePoint($%d, $%d), 4326), $%d, $%d, $%d, $%d, $%d, $%d, $%d, $%d, $%d, $%d, $%d, $%d::uuid, $%d)",
			idx, idx+1, idx+2, idx+3, idx+4, idx+5, idx+6, idx+7, idx+8, idx+9, idx+10, idx+11, idx+12, idx+13, idx+14, idx+15, idx+16,
		))
		args = append(args, p.EntityID, p.Source, p.Longitude, p.Latitude, p.Heading, p.SpeedKnots, p.Course, p.Timestamp, p.Altitude, p.VelocityNorth, p.VelocityEast, p.VelocityUp, p.CircularError,
			p.FeedID, p.SourceEntityID, p.ObservationID, p.EntityType)
	}
	return b.String(), args
}

// GetHistory queries track history for an entity with optional time range and limit.
func (s *TrackStore) GetHistory(ctx context.Context, entityID string, startTime, endTime *time.Time, maxPoints *int) ([]models.TrackPointResult, error) {
	var b strings.Builder
	args := []interface{}{entityID}
	paramIdx := 2

	b.WriteString(`SELECT id, "entityId", ST_Y(position) AS latitude, ST_X(position) AS longitude, heading, "speedKnots", course, source, timestamp FROM sentinel.track_points WHERE "entityId" = $1`)

	if startTime != nil {
		b.WriteString(fmt.Sprintf(` AND timestamp >= $%d`, paramIdx))
		args = append(args, *startTime)
		paramIdx++
	}
	if endTime != nil {
		b.WriteString(fmt.Sprintf(` AND timestamp <= $%d`, paramIdx))
		args = append(args, *endTime)
		paramIdx++
	}

	b.WriteString(` ORDER BY timestamp ASC`)

	if maxPoints != nil {
		b.WriteString(fmt.Sprintf(` LIMIT $%d`, paramIdx))
		args = append(args, *maxPoints)
	}

	rows, err := s.pool.Query(ctx, b.String(), args...)
	if err != nil {
		return nil, fmt.Errorf("querying track history: %w", err)
	}
	defer rows.Close()

	return scanTrackPoints(rows)
}

// GetLatestPositions returns the most recent track point for each entity ID.
func (s *TrackStore) GetLatestPositions(ctx context.Context, entityIDs []string) ([]models.TrackPointResult, error) {
	if len(entityIDs) == 0 {
		return nil, nil
	}

	sql := `
		SELECT DISTINCT ON ("entityId")
			id, "entityId", ST_Y(position) AS latitude, ST_X(position) AS longitude,
			heading, "speedKnots", course, source, timestamp
		FROM sentinel.track_points
		WHERE "entityId" = ANY($1)
		ORDER BY "entityId", timestamp DESC
	`

	rows, err := s.pool.Query(ctx, sql, entityIDs)
	if err != nil {
		return nil, fmt.Errorf("querying latest positions: %w", err)
	}
	defer rows.Close()

	return scanTrackPoints(rows)
}

// Ping checks database connectivity.
func (s *TrackStore) Ping(ctx context.Context) error {
	return s.pool.Ping(ctx)
}

func scanTrackPoints(rows pgx.Rows) ([]models.TrackPointResult, error) {
	var results []models.TrackPointResult
	for rows.Next() {
		var tp models.TrackPointResult
		if err := rows.Scan(
			&tp.ID, &tp.EntityID, &tp.Latitude, &tp.Longitude,
			&tp.Heading, &tp.SpeedKnots, &tp.Course, &tp.Source, &tp.Timestamp,
		); err != nil {
			return nil, fmt.Errorf("scanning track point: %w", err)
		}
		results = append(results, tp)
	}
	return results, rows.Err()
}

// TimelineBucket is one time slice of track activity.
type TimelineBucket struct {
	Timestamp   time.Time      `json:"timestamp"`
	Count       int64          `json:"count"`
	EntityTypes map[string]int `json:"entityTypes"`
}

// GetTimeline counts track points in `buckets` equal slices of [start, end),
// with a per-entity-type breakdown. Empty slices are returned with zero counts.
// Only points of entities at or below the caller's clearance are counted;
// points whose entity is gone count as UNCLASSIFIED.
func (s *TrackStore) GetTimeline(ctx context.Context, start, end time.Time, buckets int, ceiling string) ([]TimelineBucket, error) {
	rows, err := s.pool.Query(ctx, `
		WITH counts AS (
			SELECT width_bucket(EXTRACT(EPOCH FROM t."timestamp"), EXTRACT(EPOCH FROM $1::timestamptz), EXTRACT(EPOCH FROM $2::timestamptz), $3::int) - 1 AS i,
			       COALESCE(e."entityType", 'UNKNOWN') AS entity_type,
			       COUNT(*) AS cnt
			FROM sentinel.track_points t
			LEFT JOIN sentinel.entities e ON e.id = t."entityId"
			WHERE t."timestamp" >= $1 AND t."timestamp" < $2
			  AND sentinel.classification_rank(COALESCE(e.classification, 'UNCLASSIFIED')) <= $4
			GROUP BY 1, 2
		)
		SELECT $1::timestamptz + (($2::timestamptz - $1::timestamptz) / $3::int) * g.i AS bucket_start,
		       COALESCE(SUM(c.cnt), 0)::bigint AS total,
		       COALESCE(jsonb_object_agg(c.entity_type, c.cnt) FILTER (WHERE c.entity_type IS NOT NULL), '{}'::jsonb) AS entity_types
		FROM generate_series(0, $3::int - 1) AS g(i)
		LEFT JOIN counts c ON c.i = g.i
		GROUP BY g.i
		ORDER BY g.i
	`, start, end, buckets, classification.Rank(ceiling))
	if err != nil {
		return nil, fmt.Errorf("querying timeline: %w", err)
	}
	defer rows.Close()

	out := make([]TimelineBucket, 0, buckets)
	for rows.Next() {
		var b TimelineBucket
		if err := rows.Scan(&b.Timestamp, &b.Count, &b.EntityTypes); err != nil {
			return nil, fmt.Errorf("scanning timeline bucket: %w", err)
		}
		if b.EntityTypes == nil {
			b.EntityTypes = map[string]int{}
		}
		out = append(out, b)
	}
	return out, rows.Err()
}
