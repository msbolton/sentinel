package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

// SourceRow is one feed's contribution to an entity in a batch.
type SourceRow struct {
	EntityID       string
	FeedID         string
	Source         string
	SourceEntityID string
	ObservedAt     time.Time
	ReceivedAt     time.Time
	Count          int64
}

// SourceStore maintains entity_sources.
type SourceStore struct {
	pool   *pgxpool.Pool
	logger *zap.Logger
}

// NewSourceStore creates a source store.
func NewSourceStore(pool *pgxpool.Pool, logger *zap.Logger) *SourceStore {
	return &SourceStore{pool: pool, logger: logger}
}

// UpsertSources folds the batch into entity_sources: one row per entity
// and feed, newest times, summed counts.
func (s *SourceStore) UpsertSources(ctx context.Context, rows []SourceRow) error {
	if len(rows) == 0 {
		return nil
	}
	type key struct{ entity, feed string }
	merged := map[key]SourceRow{}
	order := []key{}
	for _, r := range rows {
		k := key{r.EntityID, r.FeedID}
		m, ok := merged[k]
		if !ok {
			order = append(order, k)
			if r.Count == 0 {
				r.Count = 1
			}
			merged[k] = r
			continue
		}
		if r.ObservedAt.After(m.ObservedAt) {
			m.ObservedAt = r.ObservedAt
			m.Source = r.Source
			m.SourceEntityID = r.SourceEntityID
		}
		if r.ReceivedAt.After(m.ReceivedAt) {
			m.ReceivedAt = r.ReceivedAt
		}
		if r.Count == 0 {
			r.Count = 1
		}
		m.Count += r.Count
		merged[k] = m
	}
	entities, feeds, sources, sids := []string{}, []string{}, []string{}, []string{}
	observed, received := []time.Time{}, []time.Time{}
	counts := []int64{}
	for _, k := range order {
		m := merged[k]
		entities = append(entities, m.EntityID)
		feeds = append(feeds, m.FeedID)
		sources = append(sources, m.Source)
		sids = append(sids, m.SourceEntityID)
		observed = append(observed, m.ObservedAt)
		received = append(received, m.ReceivedAt)
		counts = append(counts, m.Count)
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO sentinel.entity_sources ("entityId", "feedId", source, "sourceEntityId", "lastObservedAt", "lastReceivedAt", "observationCount")
		SELECT e::uuid, f, s, sid, o, r, c
		  FROM unnest($1::text[], $2::text[], $3::text[], $4::text[], $5::timestamptz[], $6::timestamptz[], $7::bigint[]) AS b(e, f, s, sid, o, r, c)
		ON CONFLICT ("entityId", "feedId") DO UPDATE SET
		  source = EXCLUDED.source,
		  "sourceEntityId" = EXCLUDED."sourceEntityId",
		  "lastObservedAt" = GREATEST(sentinel.entity_sources."lastObservedAt", EXCLUDED."lastObservedAt"),
		  "lastReceivedAt" = GREATEST(sentinel.entity_sources."lastReceivedAt", EXCLUDED."lastReceivedAt"),
		  "observationCount" = sentinel.entity_sources."observationCount" + EXCLUDED."observationCount"`,
		entities, feeds, sources, sids, observed, received, counts)
	if err != nil {
		return fmt.Errorf("upsert entity sources: %w", err)
	}
	return nil
}
