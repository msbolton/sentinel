package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// CustomFeed represents a user-created data feed configuration persisted in Postgres.
type CustomFeed struct {
	ID            uuid.UUID       `json:"id"`
	Name          string          `json:"name"`
	ConnectorType string          `json:"connector_type"` // mqtt, stomp, tcp
	Format        string          `json:"format"`         // json, nmea, cot, ais, adsb, link16
	Config        json.RawMessage `json:"config"`
	Enabled       bool            `json:"enabled"`
	CreatedAt     time.Time       `json:"created_at"`
	UpdatedAt     time.Time       `json:"updated_at"`
}

// Store provides CRUD operations for custom feed configurations backed by Postgres.
type Store struct {
	pool *pgxpool.Pool
}

// New opens a pgxpool connection to the given database URL.
func New(ctx context.Context, databaseURL string) (*Store, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("connecting to postgres: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("pinging postgres: %w", err)
	}
	return &Store{pool: pool}, nil
}

// Migrate creates the custom_feeds table if it does not exist.
func (s *Store) Migrate(ctx context.Context) error {
	ddl := `
		CREATE TABLE IF NOT EXISTS custom_feeds (
			id UUID PRIMARY KEY,
			name VARCHAR(255) NOT NULL,
			connector_type VARCHAR(10) NOT NULL CHECK (connector_type IN ('mqtt', 'stomp', 'tcp')),
			format VARCHAR(10) NOT NULL CHECK (format IN ('json', 'nmea', 'cot', 'ais', 'adsb', 'link16')),
			config JSONB NOT NULL DEFAULT '{}',
			enabled BOOLEAN NOT NULL DEFAULT true,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);`
	_, err := s.pool.Exec(ctx, ddl)
	if err != nil {
		return fmt.Errorf("migrating custom_feeds table: %w", err)
	}
	return nil
}

// Create inserts a new custom feed.
func (s *Store) Create(ctx context.Context, feed *CustomFeed) error {
	query := `
		INSERT INTO custom_feeds (id, name, connector_type, format, config, enabled, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`
	_, err := s.pool.Exec(ctx, query,
		feed.ID, feed.Name, feed.ConnectorType, feed.Format,
		feed.Config, feed.Enabled, feed.CreatedAt, feed.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("inserting custom feed: %w", err)
	}
	return nil
}

// Update modifies a custom feed's name, format, and config.
func (s *Store) Update(ctx context.Context, id uuid.UUID, name, format string, config json.RawMessage) error {
	query := `
		UPDATE custom_feeds
		SET name = $2, format = $3, config = $4, updated_at = NOW()
		WHERE id = $1`
	tag, err := s.pool.Exec(ctx, query, id, name, format, config)
	if err != nil {
		return fmt.Errorf("updating custom feed: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("custom feed %s not found", id)
	}
	return nil
}

// SetEnabled toggles a custom feed's enabled state.
func (s *Store) SetEnabled(ctx context.Context, id uuid.UUID, enabled bool) error {
	query := `UPDATE custom_feeds SET enabled = $2, updated_at = NOW() WHERE id = $1`
	tag, err := s.pool.Exec(ctx, query, id, enabled)
	if err != nil {
		return fmt.Errorf("toggling custom feed: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("custom feed %s not found", id)
	}
	return nil
}

// Delete removes a custom feed by ID.
func (s *Store) Delete(ctx context.Context, id uuid.UUID) error {
	query := `DELETE FROM custom_feeds WHERE id = $1`
	tag, err := s.pool.Exec(ctx, query, id)
	if err != nil {
		return fmt.Errorf("deleting custom feed: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("custom feed %s not found", id)
	}
	return nil
}

// List returns all custom feeds ordered by creation time.
func (s *Store) List(ctx context.Context) ([]CustomFeed, error) {
	query := `
		SELECT id, name, connector_type, format, config, enabled, created_at, updated_at
		FROM custom_feeds
		ORDER BY created_at ASC`
	rows, err := s.pool.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("listing custom feeds: %w", err)
	}
	defer rows.Close()

	var feeds []CustomFeed
	for rows.Next() {
		var f CustomFeed
		if err := rows.Scan(&f.ID, &f.Name, &f.ConnectorType, &f.Format,
			&f.Config, &f.Enabled, &f.CreatedAt, &f.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scanning custom feed row: %w", err)
		}
		feeds = append(feeds, f)
	}
	return feeds, rows.Err()
}

// Close releases the connection pool.
func (s *Store) Close() {
	s.pool.Close()
}
