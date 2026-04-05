package store

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/sentinel/track-service/internal/models"
)

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
func (s *TrackStore) BulkInsert(ctx context.Context, points []models.BufferedPoint) error {
	if len(points) == 0 {
		return nil
	}

	var b strings.Builder
	b.WriteString(`INSERT INTO sentinel.track_points ("entityId", "source", "position", "heading", "speedKnots", "course", "timestamp", "altitude", "velocityNorth", "velocityEast", "velocityUp", "circularError") VALUES `)

	const paramsPerRow = 13
	args := make([]interface{}, 0, len(points)*paramsPerRow)
	for i, p := range points {
		if i > 0 {
			b.WriteString(", ")
		}
		idx := i*paramsPerRow + 1
		b.WriteString(fmt.Sprintf(
			"($%d, $%d, ST_SetSRID(ST_MakePoint($%d, $%d), 4326), $%d, $%d, $%d, $%d, $%d, $%d, $%d, $%d, $%d)",
			idx, idx+1, idx+2, idx+3, idx+4, idx+5, idx+6, idx+7, idx+8, idx+9, idx+10, idx+11, idx+12,
		))
		args = append(args, p.EntityID, p.Source, p.Longitude, p.Latitude, p.Heading, p.SpeedKnots, p.Course, p.Timestamp, p.Altitude, p.VelocityNorth, p.VelocityEast, p.VelocityUp, p.CircularError)
	}

	_, err := s.pool.Exec(ctx, b.String(), args...)
	return err
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
