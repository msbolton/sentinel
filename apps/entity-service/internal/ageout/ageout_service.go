package ageout

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sentinel/go-common/kafka"
	"go.uber.org/zap"
)

// Service runs the ageout state machine on a 15-second interval.
type Service struct {
	pool     *pgxpool.Pool
	producer *kafka.Producer
	logger   *zap.Logger
	done     chan struct{}
	running  sync.Mutex
}

// NewService creates a new ageout service.
func NewService(pool *pgxpool.Pool, producer *kafka.Producer, logger *zap.Logger) *Service {
	return &Service{
		pool:     pool,
		producer: producer,
		logger:   logger,
		done:     make(chan struct{}),
	}
}

// Start begins the 15-second ageout ticker.
func (s *Service) Start() {
	go s.run()
}

func (s *Service) run() {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	s.logger.Info("ageout service started (15s interval)")

	for {
		select {
		case <-s.done:
			return
		case <-ticker.C:
			s.process()
		}
	}
}

func (s *Service) process() {
	if !s.running.TryLock() {
		return
	}
	defer s.running.Unlock()

	ctx := context.Background()

	staleCount := s.transitionToStale(ctx)
	agedOutCount := s.transitionToAgedOut(ctx)

	if staleCount > 0 || agedOutCount > 0 {
		s.logger.Info("ageout cycle complete",
			zap.Int("stale", staleCount),
			zap.Int("aged_out", agedOutCount),
		)
	}
}

func (s *Service) transitionToStale(ctx context.Context) int {
	rows, err := s.pool.Query(ctx, `
		WITH candidates AS (
			SELECT e.id, e."entityType", e.source, e."feedId", e."lastSeenAt"::text,
				COALESCE(
					feed_cfg."staleThresholdMs",
					source_cfg."staleThresholdMs",
					default_cfg."staleThresholdMs"
				) AS threshold_ms
			FROM sentinel.entities e
			LEFT JOIN sentinel.feed_ageout_config feed_cfg
				ON feed_cfg."feedId" = e."feedId" AND feed_cfg."sourceType" = e.source::varchar
			LEFT JOIN sentinel.feed_ageout_config source_cfg
				ON source_cfg."feedId" IS NULL AND source_cfg."sourceType" = e.source::varchar
			LEFT JOIN sentinel.feed_ageout_config default_cfg
				ON default_cfg."feedId" IS NULL AND default_cfg."sourceType" IS NULL
			WHERE e."ageoutState" = 'LIVE' AND e.deleted = false AND e."lastSeenAt" IS NOT NULL
				AND EXTRACT(EPOCH FROM (NOW() - e."lastSeenAt")) * 1000 > COALESCE(
					feed_cfg."staleThresholdMs",
					source_cfg."staleThresholdMs",
					default_cfg."staleThresholdMs"
				)
			LIMIT 1000
		)
		UPDATE sentinel.entities SET "ageoutState" = 'STALE'
		FROM candidates c WHERE sentinel.entities.id = c.id
		RETURNING c.id, c."entityType", c.source, c."feedId", c."lastSeenAt", c.threshold_ms
	`)
	if err != nil {
		s.logger.Error("failed to transition to stale", zap.Error(err))
		return 0
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var id, entityType, source string
		var feedID, lastSeenAt *string
		var thresholdMs *int
		if err := rows.Scan(&id, &entityType, &source, &feedID, &lastSeenAt, &thresholdMs); err != nil {
			s.logger.Warn("scan stale result failed", zap.Error(err))
			continue
		}

		payload, _ := json.Marshal(map[string]interface{}{
			"entity_id":    id,
			"entity_type":  entityType,
			"source":       source,
			"feed_id":      feedID,
			"ageout_state": "STALE",
			"last_seen_at": lastSeenAt,
			"threshold_ms": thresholdMs,
			"timestamp":    time.Now().UTC().Format(time.RFC3339),
		})
		s.producer.ProduceRaw(kafka.TopicEntityStale, []byte(id), payload, nil)
		count++
	}
	return count
}

func (s *Service) transitionToAgedOut(ctx context.Context) int {
	rows, err := s.pool.Query(ctx, `
		WITH candidates AS (
			SELECT e.id, e."entityType", e.source, e."feedId", e."lastSeenAt"::text,
				COALESCE(
					feed_cfg."ageoutThresholdMs",
					source_cfg."ageoutThresholdMs",
					default_cfg."ageoutThresholdMs"
				) AS threshold_ms
			FROM sentinel.entities e
			LEFT JOIN sentinel.feed_ageout_config feed_cfg
				ON feed_cfg."feedId" = e."feedId" AND feed_cfg."sourceType" = e.source::varchar
			LEFT JOIN sentinel.feed_ageout_config source_cfg
				ON source_cfg."feedId" IS NULL AND source_cfg."sourceType" = e.source::varchar
			LEFT JOIN sentinel.feed_ageout_config default_cfg
				ON default_cfg."feedId" IS NULL AND default_cfg."sourceType" IS NULL
			WHERE e."ageoutState" = 'STALE' AND e.deleted = false AND e."lastSeenAt" IS NOT NULL
				AND EXTRACT(EPOCH FROM (NOW() - e."lastSeenAt")) * 1000 > COALESCE(
					feed_cfg."ageoutThresholdMs",
					source_cfg."ageoutThresholdMs",
					default_cfg."ageoutThresholdMs"
				)
			LIMIT 1000
		)
		UPDATE sentinel.entities SET "ageoutState" = 'AGED_OUT'
		FROM candidates c WHERE sentinel.entities.id = c.id
		RETURNING c.id, c."entityType", c.source, c."feedId", c."lastSeenAt", c.threshold_ms
	`)
	if err != nil {
		s.logger.Error("failed to transition to aged out", zap.Error(err))
		return 0
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var id, entityType, source string
		var feedID, lastSeenAt *string
		var thresholdMs *int
		if err := rows.Scan(&id, &entityType, &source, &feedID, &lastSeenAt, &thresholdMs); err != nil {
			s.logger.Warn("scan aged out result failed", zap.Error(err))
			continue
		}

		payload, _ := json.Marshal(map[string]interface{}{
			"entity_id":    id,
			"entity_type":  entityType,
			"source":       source,
			"feed_id":      feedID,
			"ageout_state": "AGED_OUT",
			"last_seen_at": lastSeenAt,
			"threshold_ms": thresholdMs,
			"timestamp":    time.Now().UTC().Format(time.RFC3339),
		})
		s.producer.ProduceRaw(kafka.TopicEntityAgedOut, []byte(id), payload, nil)
		count++
	}
	return count
}

// Stop stops the ageout service.
func (s *Service) Stop() {
	close(s.done)
}
