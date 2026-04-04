package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

// AgeoutConfigRecord represents a feed ageout config row.
type AgeoutConfigRecord struct {
	ID                string  `json:"id"`
	FeedID            *string `json:"feedId,omitempty"`
	SourceType        *string `json:"sourceType,omitempty"`
	StaleThresholdMs  int     `json:"staleThresholdMs"`
	AgeoutThresholdMs int     `json:"ageoutThresholdMs"`
	CreatedAt         string  `json:"createdAt"`
	UpdatedAt         string  `json:"updatedAt"`
}

// AgeoutConfigStore handles database operations for ageout configs.
type AgeoutConfigStore struct {
	pool   *pgxpool.Pool
	logger *zap.Logger
}

// NewAgeoutConfigStore creates a new ageout config store.
func NewAgeoutConfigStore(pool *pgxpool.Pool, logger *zap.Logger) *AgeoutConfigStore {
	return &AgeoutConfigStore{pool: pool, logger: logger}
}

// FindAll returns all ageout configs.
func (s *AgeoutConfigStore) FindAll(ctx context.Context) ([]AgeoutConfigRecord, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, "feedId", "sourceType", "staleThresholdMs", "ageoutThresholdMs",
			"createdAt"::text, "updatedAt"::text
		FROM sentinel.feed_ageout_config
		ORDER BY "createdAt" DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("find all ageout configs: %w", err)
	}
	defer rows.Close()

	var configs []AgeoutConfigRecord
	for rows.Next() {
		var c AgeoutConfigRecord
		if err := rows.Scan(&c.ID, &c.FeedID, &c.SourceType, &c.StaleThresholdMs, &c.AgeoutThresholdMs, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan ageout config: %w", err)
		}
		configs = append(configs, c)
	}
	return configs, nil
}

// FindBySourceType returns a config by source type and optional feed ID.
func (s *AgeoutConfigStore) FindBySourceType(ctx context.Context, sourceType string, feedID *string) (*AgeoutConfigRecord, error) {
	var row = s.pool.QueryRow(ctx, `
		SELECT id, "feedId", "sourceType", "staleThresholdMs", "ageoutThresholdMs",
			"createdAt"::text, "updatedAt"::text
		FROM sentinel.feed_ageout_config
		WHERE "sourceType" = $1 AND ("feedId" = $2 OR ($2 IS NULL AND "feedId" IS NULL))
	`, sourceType, feedID)

	var c AgeoutConfigRecord
	if err := row.Scan(&c.ID, &c.FeedID, &c.SourceType, &c.StaleThresholdMs, &c.AgeoutThresholdMs, &c.CreatedAt, &c.UpdatedAt); err != nil {
		return nil, fmt.Errorf("find ageout config: %w", err)
	}
	return &c, nil
}

// Upsert creates or updates an ageout config.
func (s *AgeoutConfigStore) Upsert(ctx context.Context, feedID *string, sourceType *string, staleMs, ageoutMs int) (*AgeoutConfigRecord, error) {
	row := s.pool.QueryRow(ctx, `
		INSERT INTO sentinel.feed_ageout_config ("feedId", "sourceType", "staleThresholdMs", "ageoutThresholdMs")
		VALUES ($1, $2, $3, $4)
		ON CONFLICT ("feedId", "sourceType") WHERE "feedId" IS NOT NULL AND "sourceType" IS NOT NULL
		DO UPDATE SET "staleThresholdMs" = $3, "ageoutThresholdMs" = $4, "updatedAt" = NOW()
		RETURNING id, "feedId", "sourceType", "staleThresholdMs", "ageoutThresholdMs",
			"createdAt"::text, "updatedAt"::text
	`, feedID, sourceType, staleMs, ageoutMs)

	var c AgeoutConfigRecord
	if err := row.Scan(&c.ID, &c.FeedID, &c.SourceType, &c.StaleThresholdMs, &c.AgeoutThresholdMs, &c.CreatedAt, &c.UpdatedAt); err != nil {
		return nil, fmt.Errorf("upsert ageout config: %w", err)
	}
	return &c, nil
}

// Delete removes an ageout config by ID.
func (s *AgeoutConfigStore) Delete(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM sentinel.feed_ageout_config WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete ageout config: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("ageout config not found: %s", id)
	}
	return nil
}

// SeedDefaults inserts default ageout configs if they don't exist.
func (s *AgeoutConfigStore) SeedDefaults(ctx context.Context) error {
	defaults := []struct {
		sourceType *string
		staleMs    int
		ageoutMs   int
	}{
		{strPtr("ADS_B"), 60000, 300000},
		{strPtr("OPENSKY"), 60000, 300000},
		{strPtr("ADSB_LOL"), 60000, 300000},
		{strPtr("AIS"), 600000, 1800000},
		{strPtr("CELESTRAK"), 86400000, 604800000},
		{strPtr("LINK16"), 30000, 120000},
		{strPtr("RADAR"), 30000, 120000},
		{nil, 300000, 1800000}, // Global default
	}

	for _, d := range defaults {
		_, err := s.pool.Exec(ctx, `
			INSERT INTO sentinel.feed_ageout_config ("feedId", "sourceType", "staleThresholdMs", "ageoutThresholdMs")
			VALUES (NULL, $1, $2, $3)
			ON CONFLICT DO NOTHING
		`, d.sourceType, d.staleMs, d.ageoutMs)
		if err != nil {
			s.logger.Warn("failed to seed ageout config", zap.Error(err))
		}
	}

	return nil
}

func strPtr(s string) *string { return &s }
