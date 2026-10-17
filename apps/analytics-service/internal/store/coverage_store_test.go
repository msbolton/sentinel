package store

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// testPool connects to TEST_DATABASE_URL or skips the test.
func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func TestCoverageUpsertKeepsLatestTime(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	s := NewCoverageStore(pool)
	cell := "test:cov-upsert"
	t.Cleanup(func() { pool.Exec(ctx, `DELETE FROM sentinel.coverage_cells WHERE cell = $1`, cell) })

	t1 := time.Date(2026, 10, 17, 10, 0, 0, 0, time.UTC)
	if err := s.UpsertCells(ctx, []CellObs{{Cell: cell, FeedID: "feed-a", At: t1}, {Cell: cell, FeedID: "feed-b", At: t1}}); err != nil {
		t.Fatal(err)
	}
	// An older report must not move the time backwards; a newer one must.
	if err := s.UpsertCells(ctx, []CellObs{{Cell: cell, FeedID: "feed-a", At: t1.Add(-time.Hour)}, {Cell: cell, FeedID: "feed-b", At: t1.Add(time.Hour)}}); err != nil {
		t.Fatal(err)
	}
	var a, b time.Time
	if err := pool.QueryRow(ctx, `SELECT "lastObservedAt" FROM sentinel.coverage_cells WHERE cell = $1 AND "feedId" = 'feed-a'`, cell).Scan(&a); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT "lastObservedAt" FROM sentinel.coverage_cells WHERE cell = $1 AND "feedId" = 'feed-b'`, cell).Scan(&b); err != nil {
		t.Fatal(err)
	}
	if !a.Equal(t1) {
		t.Errorf("feed-a moved backwards to %v", a)
	}
	if !b.Equal(t1.Add(time.Hour)) {
		t.Errorf("feed-b = %v, want %v", b, t1.Add(time.Hour))
	}

	// Other feeds reported after t1 - 1 min; only feed-b after t1.
	if ok, _ := s.HasReportsSince(ctx, cell, "feed-a", t1.Add(-time.Minute)); !ok {
		t.Error("expected another feed's report after t1-1m")
	}
	if ok, _ := s.HasReportsSince(ctx, cell, "feed-b", t1); ok {
		t.Error("feed-a has nothing after t1, yet HasReportsSince excluding feed-b is true")
	}
	if ok, _ := s.FeedReportedSince(ctx, cell, "feed-b", t1); !ok {
		t.Error("feed-b reported after t1")
	}
	n, err := s.Prune(ctx, t1.Add(30*time.Minute))
	if err != nil || n != 1 {
		t.Errorf("prune removed %d rows (err %v), want 1", n, err)
	}
}
