package detect

import (
	"context"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/sentinel/analytics-service/internal/baseline"
	"github.com/sentinel/analytics-service/internal/config"
	"github.com/sentinel/analytics-service/internal/geo"
	"github.com/sentinel/analytics-service/internal/model"
	"github.com/sentinel/analytics-service/internal/store"
)

// Findings is the part of the finding store the engine uses.
type Findings interface {
	UpsertOpen(ctx context.Context, p store.OpenParams) (*store.Finding, bool, error)
	CloseOpen(ctx context.Context, entityID, kind string) error
	CloseStale(ctx context.Context, before time.Time) (int64, error)
	SetAlertID(ctx context.Context, id, alertID string) error
}

// Coverage answers whether other feeds still report around a position.
type Coverage interface {
	HasReportsSince(ctx context.Context, cell, exceptFeed string, since time.Time) (bool, error)
}

// Publisher turns a finding into an alert.
type Publisher interface {
	PublishFinding(ctx context.Context, f *store.Finding) error
}

// Counters receives what the engine did.
type Counters interface {
	FindingHit()
	FindingOpened()
	AlertPublished()
}

// Engine runs the detectors over fixes and ticks.
type Engine struct {
	baselines *baseline.Cache
	findings  Findings
	coverage  Coverage
	publisher Publisher
	counters  Counters
	cfg       config.DetectorConfig
	logger    *zap.Logger
	now       func() time.Time

	mu   sync.Mutex
	last map[string]*LastFix
}

// NewEngine creates an engine.
func NewEngine(baselines *baseline.Cache, findings Findings, coverage Coverage, publisher Publisher, counters Counters, cfg config.DetectorConfig, logger *zap.Logger) *Engine {
	return &Engine{baselines: baselines, findings: findings, coverage: coverage, publisher: publisher, counters: counters, cfg: cfg, logger: logger, now: time.Now, last: map[string]*LastFix{}}
}

// Seed primes the remembered last fixes from the entity table so the dark
// detector does not forget everything on a restart.
func (e *Engine) Seed(fixes []store.LastFix) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, f := range fixes {
		if _, ok := e.last[f.EntityID]; ok {
			continue
		}
		e.last[f.EntityID] = &LastFix{Fix: model.Fix{EntityID: f.EntityID, EntityType: f.EntityType, FeedID: f.FeedID, Lat: f.Lat, Lon: f.Lon, SpeedKnots: f.SpeedKnots, At: f.At, Classification: "UNCLASSIFIED"}}
	}
}

// Tracked is the number of entities the engine remembers.
func (e *Engine) Tracked() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.last)
}

// OnFix judges one fix. It never fails: a store or publish error is
// logged, because the next fix brings fresh evidence.
func (e *Engine) OnFix(ctx context.Context, f model.Fix) {
	e.mu.Lock()
	prev := e.last[f.EntityID]
	var prevCopy *LastFix
	if prev != nil {
		c := *prev
		prevCopy = &c
	}
	e.last[f.EntityID] = &LastFix{Fix: f}
	e.mu.Unlock()

	if e.cfg.SkipTypes[f.EntityType] {
		return
	}
	b, err := e.baselines.Get(ctx, f.EntityID)
	if err != nil {
		e.logger.Warn("baseline lookup failed", zap.String("entityId", f.EntityID), zap.Error(err))
		return
	}
	if prevCopy != nil && prevCopy.Dark {
		if h := Reappeared(b, f, *prevCopy); h != nil {
			e.record(ctx, f, h)
		}
		if err := e.findings.CloseOpen(ctx, f.EntityID, KindWentDark); err != nil {
			e.logger.Warn("dark finding not closed", zap.Error(err))
		}
	}
	var prevFix *model.Fix
	if prevCopy != nil && !prevCopy.Fix.At.After(f.At) {
		prevFix = &prevCopy.Fix
	}
	if h := UnusualMovement(b, f, prevFix, e.cfg); h != nil {
		e.record(ctx, f, h)
	}
	if h := UnusualPlace(b, f, e.cfg); h != nil {
		e.record(ctx, f, h)
	}
	if h := UnusualTime(b, f, e.cfg); h != nil {
		e.record(ctx, f, h)
	}
}

// Tick runs the time-based detectors over every remembered entity, closes
// findings whose evidence has stopped, and forgets entities silent for a
// day.
func (e *Engine) Tick(ctx context.Context) {
	now := e.now().UTC()
	e.mu.Lock()
	snapshot := make([]LastFix, 0, len(e.last))
	for id, lf := range e.last {
		if now.Sub(lf.Fix.At) > 24*time.Hour {
			delete(e.last, id)
			continue
		}
		snapshot = append(snapshot, *lf)
	}
	e.mu.Unlock()

	for _, lf := range snapshot {
		if ctx.Err() != nil {
			return
		}
		if e.cfg.SkipTypes[lf.Fix.EntityType] {
			continue
		}
		b, err := e.baselines.Get(ctx, lf.Fix.EntityID)
		if err != nil || b == nil {
			continue
		}
		if !lf.Dark {
			if threshold := DarkThreshold(b, e.cfg); threshold > 0 && now.Sub(lf.Fix.At) > threshold {
				covered, err := e.coverage.HasReportsSince(ctx, geo.Cell(lf.Fix.Lat, lf.Fix.Lon, e.cfg.CoverageCellDeg), lf.Fix.FeedID, lf.Fix.At)
				if err != nil {
					e.logger.Warn("coverage lookup failed", zap.Error(err))
					continue
				}
				if h := WentDark(b, lf, now, covered, e.cfg); h != nil {
					e.record(ctx, lf.Fix, h)
					e.markDark(lf.Fix.EntityID, lf.Fix.At, now)
					continue
				}
			}
			if h := QuietSlot(b, lf, now, e.cfg); h != nil {
				e.record(ctx, lf.Fix, h)
			}
		} else if h := WentDark(b, lf, now, true, e.cfg); h != nil {
			// Still dark: keep the open finding's evidence and severity current.
			e.record(ctx, lf.Fix, h)
		}
	}
	if n, err := e.findings.CloseStale(ctx, now.Add(-e.cfg.AbsorbWindow)); err != nil {
		e.logger.Warn("stale findings not closed", zap.Error(err))
	} else if n > 0 {
		e.logger.Info("findings closed", zap.Int64("count", n))
	}
}

// RunTicker calls Tick every cfg.Tick until ctx is done.
func (e *Engine) RunTicker(ctx context.Context) {
	t := time.NewTicker(e.cfg.Tick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			e.Tick(ctx)
		}
	}
}

func (e *Engine) markDark(entityID string, lastAt, now time.Time) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if lf, ok := e.last[entityID]; ok && lf.Fix.At.Equal(lastAt) {
		lf.Dark = true
		lf.DarkSince = now
	}
}

// record stores a hit as a finding (or absorbs it into the open one) and
// publishes it as an alert when it has reached the alerting severity.
func (e *Engine) record(ctx context.Context, f model.Fix, h *Hit) {
	if e.counters != nil {
		e.counters.FindingHit()
	}
	p := store.OpenParams{
		EntityID:       f.EntityID,
		Kind:           h.Kind,
		Severity:       h.Severity,
		Score:          h.Score,
		Summary:        h.Summary,
		Expected:       h.Expected,
		Actual:         h.Actual,
		Thresholds:     e.cfg.Thresholds(),
		EvidenceStart:  h.EvidenceStart,
		EvidenceEnd:    h.EvidenceEnd,
		Classification: f.Classification,
	}
	if h.HasPosition {
		p.Position = &store.GeoPoint{Lat: h.Lat, Lon: h.Lon}
	}
	finding, created, err := e.findings.UpsertOpen(ctx, p)
	if err != nil {
		e.logger.Warn("finding not recorded", zap.String("entityId", f.EntityID), zap.String("kind", h.Kind), zap.Error(err))
		return
	}
	if created && e.counters != nil {
		e.counters.FindingOpened()
	}
	if finding.AlertID == nil && SeverityRank(finding.Severity) >= SeverityRank(e.cfg.HighSeverity) {
		e.Publish(ctx, finding)
	}
}

// Publish turns a finding into an alert once; the finding's own id is the
// alert id, so a repeat is a no-op downstream.
func (e *Engine) Publish(ctx context.Context, finding *store.Finding) {
	if e.publisher == nil || finding.AlertID != nil {
		return
	}
	if err := e.publisher.PublishFinding(ctx, finding); err != nil {
		e.logger.Warn("finding not published", zap.String("findingId", finding.ID), zap.Error(err))
		return
	}
	if err := e.findings.SetAlertID(ctx, finding.ID, finding.ID); err != nil {
		e.logger.Warn("alert id not recorded", zap.String("findingId", finding.ID), zap.Error(err))
	}
	id := finding.ID
	finding.AlertID = &id
	if e.counters != nil {
		e.counters.AlertPublished()
	}
}
