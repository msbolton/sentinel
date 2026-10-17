// Package job runs the periodic baseline recomputation: every active
// entity's last window of track points becomes a fresh baseline. One
// Postgres advisory lock keeps two replicas from doing the work twice.
package job

import (
	"context"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/sentinel/analytics-service/internal/baseline"
	"github.com/sentinel/analytics-service/internal/store"
)

// LockKey names the advisory lock the job takes.
const LockKey = "sentinel.analytics.baselines"

// Entities lists what to recompute.
type Entities interface {
	ListActive(ctx context.Context, since time.Time) ([]store.EntityRef, error)
}

// Tracks loads points and stores baselines.
type Tracks interface {
	LoadTrack(ctx context.Context, entityID string, from, to time.Time, thin time.Duration) ([]baseline.Point, error)
	Upsert(ctx context.Context, b *baseline.Baseline) error
	NamePlace(ctx context.Context, lat, lon, radiusM float64) (name, locationID *string, err error)
}

// Counters receives the run's outcome.
type Counters interface {
	BaselineRun(at time.Time, took time.Duration, computed, skipped int)
}

// Options tunes the job.
type Options struct {
	Window       time.Duration
	Interval     time.Duration
	ActiveWindow time.Duration
	Thin         time.Duration
	Workers      int
	InitialDelay time.Duration
	Builder      baseline.Config
	// SlowKnots overrides Builder.SlowKnots per entity type when set.
	SlowKnots interface{ For(entityType string) float64 }
}

// Job recomputes baselines on a schedule.
type Job struct {
	pool     *pgxpool.Pool
	entities Entities
	tracks   Tracks
	cache    *baseline.Cache
	counters Counters
	opts     Options
	logger   *zap.Logger
	// After runs after each successful pass (pruning, closing stale findings).
	After []func(ctx context.Context) error
}

// New creates a job.
func New(pool *pgxpool.Pool, entities Entities, tracks Tracks, cache *baseline.Cache, counters Counters, opts Options, logger *zap.Logger) *Job {
	if opts.Workers <= 0 {
		opts.Workers = 1
	}
	if opts.InitialDelay <= 0 {
		opts.InitialDelay = 30 * time.Second
	}
	return &Job{pool: pool, entities: entities, tracks: tracks, cache: cache, counters: counters, opts: opts, logger: logger}
}

// Run blocks until ctx is done, running a pass after the initial delay and
// then every interval.
func (j *Job) Run(ctx context.Context) {
	timer := time.NewTimer(j.opts.InitialDelay)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		if err := j.RunOnce(ctx); err != nil && ctx.Err() == nil {
			j.logger.Error("baseline run failed", zap.Error(err))
		}
		timer.Reset(j.opts.Interval)
	}
}

// RunOnce performs one pass if the lock is free.
func (j *Job) RunOnce(ctx context.Context) error {
	conn, err := j.pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	locked, err := TryLock(ctx, conn)
	if err != nil {
		return err
	}
	if !locked {
		j.logger.Info("baseline run skipped: another replica holds the lock")
		return nil
	}
	defer func() {
		_, _ = conn.Exec(context.Background(), `SELECT pg_advisory_unlock(hashtext($1))`, LockKey)
	}()

	started := time.Now()
	now := started.UTC()
	refs, err := j.entities.ListActive(ctx, now.Add(-j.opts.ActiveWindow))
	if err != nil {
		return err
	}
	var (
		wg               sync.WaitGroup
		mu               sync.Mutex
		computed, failed int
	)
	work := make(chan store.EntityRef)
	for i := 0; i < j.opts.Workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ref := range work {
				if err := j.compute(ctx, ref, now); err != nil {
					j.logger.Warn("baseline not computed", zap.String("entityId", ref.ID), zap.Error(err))
					mu.Lock()
					failed++
					mu.Unlock()
					continue
				}
				mu.Lock()
				computed++
				mu.Unlock()
			}
		}()
	}
dispatch:
	for _, ref := range refs {
		select {
		case <-ctx.Done():
			break dispatch
		case work <- ref:
		}
	}
	close(work)
	wg.Wait()

	for _, after := range j.After {
		if err := after(ctx); err != nil {
			j.logger.Warn("post-run step failed", zap.Error(err))
		}
	}
	took := time.Since(started)
	if j.counters != nil {
		j.counters.BaselineRun(now, took, computed, failed)
	}
	j.logger.Info("baseline run complete", zap.Int("entities", len(refs)), zap.Int("computed", computed), zap.Int("failed", failed), zap.Duration("took", took))
	return ctx.Err()
}

func (j *Job) compute(ctx context.Context, ref store.EntityRef, now time.Time) error {
	from := now.Add(-j.opts.Window)
	points, err := j.tracks.LoadTrack(ctx, ref.ID, from, now, j.opts.Thin)
	if err != nil {
		return err
	}
	cfg := j.opts.Builder
	if j.opts.SlowKnots != nil {
		cfg.SlowKnots = j.opts.SlowKnots.For(ref.EntityType)
	}
	b := baseline.Build(ref.ID, ref.EntityType, points, from, now, cfg)
	for i := range b.Places {
		name, locID, err := j.tracks.NamePlace(ctx, b.Places[i].Lat, b.Places[i].Lon, b.Places[i].RadiusM)
		if err != nil {
			return err
		}
		b.Places[i].Name, b.Places[i].LocationID = name, locID
	}
	if err := j.tracks.Upsert(ctx, b); err != nil {
		return err
	}
	if j.cache != nil {
		j.cache.Put(b)
	}
	return nil
}

// Querier is the part of a connection TryLock needs; *pgxpool.Conn and
// *pgx.Conn satisfy it.
type Querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// TryLock takes the job's session-level advisory lock on a connection and
// reports whether it got it. The lock lives as long as the session, so the
// caller must hold the connection and unlock on the same one.
func TryLock(ctx context.Context, conn Querier) (bool, error) {
	var locked bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock(hashtext($1))`, LockKey).Scan(&locked); err != nil {
		return false, err
	}
	return locked, nil
}
