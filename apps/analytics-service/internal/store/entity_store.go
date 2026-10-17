package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// EntityRef is what the baseline job needs to know about an entity.
type EntityRef struct {
	ID             string
	EntityType     string
	Classification string
}

// LastFix is an entity's last known position, used to seed the dark
// detector after a restart.
type LastFix struct {
	EntityID   string
	EntityType string
	FeedID     string
	Lat, Lon   float64
	SpeedKnots *float64
	At         time.Time
}

// EntityStore reads sentinel.entities.
type EntityStore struct {
	pool *pgxpool.Pool
}

// NewEntityStore creates an entity store.
func NewEntityStore(pool *pgxpool.Pool) *EntityStore {
	return &EntityStore{pool: pool}
}

// Ping checks the database connection.
func (s *EntityStore) Ping(ctx context.Context) error {
	return s.pool.Ping(ctx)
}

// Classification returns an entity's stored level; an unknown entity reads
// as UNCLASSIFIED so a missing row never widens what a caller may see.
func (s *EntityStore) Classification(ctx context.Context, entityID string) (string, error) {
	var level string
	err := s.pool.QueryRow(ctx, `SELECT classification FROM sentinel.entities WHERE id = $1`, entityID).Scan(&level)
	if errors.Is(err, pgx.ErrNoRows) {
		return "UNCLASSIFIED", nil
	}
	if err != nil {
		return "", fmt.Errorf("entity classification: %w", err)
	}
	return level, nil
}

// ListActive returns the live entities with a fix since the given time.
func (s *EntityStore) ListActive(ctx context.Context, since time.Time) ([]EntityRef, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, "entityType", classification FROM sentinel.entities
		 WHERE deleted = false AND "lastSeenAt" > $1`, since.UTC())
	if err != nil {
		return nil, fmt.Errorf("list active entities: %w", err)
	}
	defer rows.Close()
	var out []EntityRef
	for rows.Next() {
		var e EntityRef
		if err := rows.Scan(&e.ID, &e.EntityType, &e.Classification); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// LastFixes returns the last known position of every live entity seen
// since the given time.
func (s *EntityStore) LastFixes(ctx context.Context, since time.Time) ([]LastFix, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, "entityType", COALESCE("lastFeedId", "feedId", ''), ST_Y(position), ST_X(position), "speedKnots",
		        COALESCE("lastObservedAt", "lastSeenAt")
		 FROM sentinel.entities
		 WHERE deleted = false AND position IS NOT NULL AND "lastSeenAt" > $1`, since.UTC())
	if err != nil {
		return nil, fmt.Errorf("last fixes: %w", err)
	}
	defer rows.Close()
	var out []LastFix
	for rows.Next() {
		var f LastFix
		if err := rows.Scan(&f.EntityID, &f.EntityType, &f.FeedID, &f.Lat, &f.Lon, &f.SpeedKnots, &f.At); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}
