package job

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/sentinel/analytics-service/internal/baseline"
	"github.com/sentinel/analytics-service/internal/store"
)

func TestTryLockIsExclusiveAcrossSessions(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	a, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Release()
	b, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Release()

	if got, err := TryLock(ctx, a); err != nil || !got {
		t.Fatalf("first lock: %v %v", got, err)
	}
	if got, err := TryLock(ctx, b); err != nil || got {
		t.Fatalf("second session got the lock too: %v %v", got, err)
	}
	if _, err := a.Exec(ctx, `SELECT pg_advisory_unlock(hashtext($1))`, LockKey); err != nil {
		t.Fatal(err)
	}
	if got, err := TryLock(ctx, b); err != nil || !got {
		t.Fatalf("lock not released: %v %v", got, err)
	}
	b.Exec(ctx, `SELECT pg_advisory_unlock(hashtext($1))`, LockKey)
}

// Fakes for the pass itself.
type fakeEntities struct{ refs []store.EntityRef }

func (f fakeEntities) ListActive(context.Context, time.Time) ([]store.EntityRef, error) {
	return f.refs, nil
}

type fakeTracks struct {
	mu     sync.Mutex
	points map[string][]baseline.Point
	stored []*baseline.Baseline
}

func (f *fakeTracks) LoadTrack(_ context.Context, id string, _, _ time.Time, _ time.Duration) ([]baseline.Point, error) {
	return f.points[id], nil
}
func (f *fakeTracks) Upsert(_ context.Context, b *baseline.Baseline) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stored = append(f.stored, b)
	return nil
}
func (f *fakeTracks) NamePlace(context.Context, float64, float64, float64) (*string, *string, error) {
	return nil, nil, nil
}

type fakeCounters struct{ computed, failed int }

func (f *fakeCounters) BaselineRun(_ time.Time, _ time.Duration, computed, skipped int) {
	f.computed, f.failed = computed, skipped
}

func TestRunOncePassComputesEveryActiveEntity(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	t0 := time.Now().Add(-time.Hour)
	var pts []baseline.Point
	speed := 0.0
	for i := 0; i < 20; i++ {
		pts = append(pts, baseline.Point{Lat: 40, Lon: -74, SpeedKnots: &speed, At: t0.Add(time.Duration(i) * time.Minute)})
	}
	tracks := &fakeTracks{points: map[string][]baseline.Point{"a": pts, "b": nil}}
	cache := baseline.NewCache(tracksGetter{}, time.Minute)
	counters := &fakeCounters{}
	after := 0
	j := New(pool, fakeEntities{refs: []store.EntityRef{{ID: "a", EntityType: "VEHICLE"}, {ID: "b", EntityType: "VESSEL"}}},
		tracks, cache, counters, Options{Window: 24 * time.Hour, Interval: time.Hour, ActiveWindow: time.Hour, Workers: 2,
			Builder: baseline.Config{SlowKnots: 2, EpsM: 150, MinPts: 10, MinPoints: 200, MinDays: 3, MaxPlaces: 20}}, zap.NewNop())
	j.After = append(j.After, func(context.Context) error { after++; return nil })

	if err := j.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if len(tracks.stored) != 2 || counters.computed != 2 || counters.failed != 0 || after != 1 {
		t.Errorf("stored=%d computed=%d failed=%d after=%d", len(tracks.stored), counters.computed, counters.failed, after)
	}
	if b, _ := cache.Get(ctx, "a"); b == nil || len(b.Places) != 1 {
		t.Errorf("cache not primed with entity a's baseline: %+v", b)
	}
}

type tracksGetter struct{}

func (tracksGetter) Get(context.Context, string) (*baseline.Baseline, error) { return nil, nil }
