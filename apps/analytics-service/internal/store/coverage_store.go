// Package store holds the SQL behind the analytics service: coverage cells,
// entity lookups, baselines and findings.
package store

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// CellObs is the latest time a feed reported anything in a grid cell.
type CellObs struct {
	Cell   string
	FeedID string
	At     time.Time
}

// CoverageStore keeps sentinel.coverage_cells.
type CoverageStore struct {
	pool *pgxpool.Pool
}

// NewCoverageStore creates a coverage store.
func NewCoverageStore(pool *pgxpool.Pool) *CoverageStore {
	return &CoverageStore{pool: pool}
}

// UpsertCells records the latest report time per cell and feed, never
// moving a cell's time backwards.
func (s *CoverageStore) UpsertCells(ctx context.Context, cells []CellObs) error {
	if len(cells) == 0 {
		return nil
	}
	const chunk = 1000
	for start := 0; start < len(cells); start += chunk {
		end := start + chunk
		if end > len(cells) {
			end = len(cells)
		}
		values := make([]string, 0, end-start)
		args := make([]any, 0, 3*(end-start))
		for i, c := range cells[start:end] {
			values = append(values, fmt.Sprintf("($%d, $%d, $%d)", 3*i+1, 3*i+2, 3*i+3))
			args = append(args, c.Cell, c.FeedID, c.At.UTC())
		}
		sql := `INSERT INTO sentinel.coverage_cells (cell, "feedId", "lastObservedAt") VALUES ` + strings.Join(values, ",") +
			` ON CONFLICT (cell, "feedId") DO UPDATE SET "lastObservedAt" = GREATEST(EXCLUDED."lastObservedAt", sentinel.coverage_cells."lastObservedAt")`
		if _, err := s.pool.Exec(ctx, sql, args...); err != nil {
			return fmt.Errorf("upsert coverage cells: %w", err)
		}
	}
	return nil
}

// HasReportsSince reports whether any feed other than exceptFeed has
// reported in the cell after the given time, which is what makes an
// entity's silence its own rather than its feed's.
func (s *CoverageStore) HasReportsSince(ctx context.Context, cell, exceptFeed string, since time.Time) (bool, error) {
	var found bool
	err := s.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM sentinel.coverage_cells WHERE cell = $1 AND "feedId" <> $2 AND "lastObservedAt" > $3)`,
		cell, exceptFeed, since.UTC()).Scan(&found)
	if err != nil {
		return false, fmt.Errorf("coverage lookup: %w", err)
	}
	return found, nil
}

// FeedReportedSince reports whether the entity's own feed has reported in
// the cell after the given time: the feed still covers the area.
func (s *CoverageStore) FeedReportedSince(ctx context.Context, cell, feedID string, since time.Time) (bool, error) {
	var found bool
	err := s.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM sentinel.coverage_cells WHERE cell = $1 AND "feedId" = $2 AND "lastObservedAt" > $3)`,
		cell, feedID, since.UTC()).Scan(&found)
	if err != nil {
		return false, fmt.Errorf("coverage lookup: %w", err)
	}
	return found, nil
}

// Prune drops cells nobody has reported in since the given time.
func (s *CoverageStore) Prune(ctx context.Context, olderThan time.Time) (int64, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM sentinel.coverage_cells WHERE "lastObservedAt" < $1`, olderThan.UTC())
	if err != nil {
		return 0, fmt.Errorf("prune coverage cells: %w", err)
	}
	return tag.RowsAffected(), nil
}

// Count returns the number of coverage rows.
func (s *CoverageStore) Count(ctx context.Context) (int64, error) {
	var n int64
	if err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM sentinel.coverage_cells`).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}
