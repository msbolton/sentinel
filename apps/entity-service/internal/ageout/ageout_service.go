package ageout

import (
	"context"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/sentinel/entity-service/internal/events"
)

// thresholdSQL resolves the stale or ageout threshold for an entity: the
// largest threshold among the feeds that contributed to it (so one healthy
// feed keeps a multi-feed track live), falling back to the entity's own
// feed and source for rows that predate entity_sources, then the global
// default. %s is the threshold column.
const thresholdSQL = `
	COALESCE(
		(SELECT MAX(COALESCE(fc.%[1]s, sc.%[1]s, dc.%[1]s))
		   FROM sentinel.entity_sources s
		   LEFT JOIN sentinel.feed_ageout_config fc ON fc."feedId" = s."feedId" AND fc."sourceType" = s.source
		   LEFT JOIN sentinel.feed_ageout_config sc ON sc."feedId" IS NULL AND sc."sourceType" = s.source
		   LEFT JOIN sentinel.feed_ageout_config dc ON dc."feedId" IS NULL AND dc."sourceType" IS NULL
		  WHERE s."entityId" = e.id),
		feed_cfg.%[1]s, source_cfg.%[1]s, default_cfg.%[1]s)`

// Service runs the ageout state machine on a 15-second interval.
type Service struct {
	pool    *pgxpool.Pool
	events  *events.Publisher
	logger  *zap.Logger
	done    chan struct{}
	running sync.Mutex
}

// NewService creates a new ageout service.
func NewService(pool *pgxpool.Pool, publisher *events.Publisher, logger *zap.Logger) *Service {
	return &Service{pool: pool, events: publisher, logger: logger, done: make(chan struct{})}
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
	staleCount := s.transition(ctx, "LIVE", "STALE", `"staleThresholdMs"`)
	agedOutCount := s.transition(ctx, "STALE", "AGED_OUT", `"ageoutThresholdMs"`)

	if staleCount > 0 || agedOutCount > 0 {
		s.logger.Info("ageout cycle complete", zap.Int("stale", staleCount), zap.Int("aged_out", agedOutCount))
	}
}

// transition moves up to 1000 entities from one state to the next when
// their last report is older than the resolved threshold.
func (s *Service) transition(ctx context.Context, from, to, column string) int {
	threshold := sprintf(thresholdSQL, column)
	rows, err := s.pool.Query(ctx, `
		WITH candidates AS (
			SELECT e.id, e."entityType", e.source, COALESCE(e."lastFeedId", e."feedId") AS feed_id, e."lastSeenAt", `+threshold+` AS threshold_ms
			  FROM sentinel.entities e
			  LEFT JOIN sentinel.feed_ageout_config feed_cfg
				ON feed_cfg."feedId" = e."feedId" AND feed_cfg."sourceType" = e.source::varchar
			  LEFT JOIN sentinel.feed_ageout_config source_cfg
				ON source_cfg."feedId" IS NULL AND source_cfg."sourceType" = e.source::varchar
			  LEFT JOIN sentinel.feed_ageout_config default_cfg
				ON default_cfg."feedId" IS NULL AND default_cfg."sourceType" IS NULL
			 WHERE e."ageoutState" = $1 AND e.deleted = false AND e."lastSeenAt" IS NOT NULL
		), due AS (
			SELECT * FROM candidates
			 WHERE threshold_ms IS NOT NULL
			   AND EXTRACT(EPOCH FROM (NOW() - "lastSeenAt")) * 1000 > threshold_ms
			 LIMIT 1000
		)
		UPDATE sentinel.entities SET "ageoutState" = $2
		  FROM due WHERE sentinel.entities.id = due.id
		RETURNING due.id, due."entityType", due.source, COALESCE(due.feed_id, ''), due."lastSeenAt", due.threshold_ms`, from, to)
	if err != nil {
		s.logger.Error("ageout transition failed", zap.String("from", from), zap.String("to", to), zap.Error(err))
		return 0
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var id, entityType, source, feedID string
		var lastSeen *time.Time
		var thresholdMs *int64
		if err := rows.Scan(&id, &entityType, &source, &feedID, &lastSeen, &thresholdMs); err != nil {
			s.logger.Warn("scan ageout row failed", zap.Error(err))
			continue
		}
		a := events.Ageout{EntityType: entityType, Source: source, FeedID: feedID, PreviousState: from}
		if lastSeen != nil {
			a.LastSeenAt = *lastSeen
		}
		if thresholdMs != nil {
			a.ThresholdMs = *thresholdMs
		}
		if to == "STALE" {
			s.events.Stale(id, a)
		} else {
			s.events.AgedOut(id, a)
		}
		count++
	}
	return count
}

// Stop stops the ageout service.
func (s *Service) Stop() {
	close(s.done)
}
