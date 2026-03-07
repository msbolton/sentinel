package feeds

import (
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/sentinel/ingest-service/internal/sources"
)

// FeedStatus describes the current state of a registered data feed.
type FeedStatus struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	SourceType  string `json:"sourceType"`
	Description string `json:"description"`
	Enabled     bool   `json:"enabled"`
}

// FeedHealth holds runtime health metrics for a feed.
type FeedHealth struct {
	LastSuccessAt time.Time `json:"lastSuccessAt"`
	EntitiesCount int       `json:"entitiesCount"`
	ErrorCount    int64     `json:"errorCount"`
	Status        string    `json:"status"`
}

// FeedStatusWithHealth extends FeedStatus with optional health information.
type FeedStatusWithHealth struct {
	FeedStatus
	Health *FeedHealth `json:"health"`
}

// feed is the internal bookkeeping for a single registered feed.
type feed struct {
	status      FeedStatus
	factory     func() (sources.Listener, error)
	listener    sources.Listener // nil when disabled
	health      FeedHealth
	warnSec     int
	criticalSec int
}

// Manager tracks all data-feed listeners and exposes runtime start/stop.
type Manager struct {
	mu     sync.Mutex
	feeds  []*feed          // insertion-ordered
	byID   map[string]*feed // quick lookup
	logger *zap.Logger
}

// NewManager creates a new feed manager.
func NewManager(logger *zap.Logger) *Manager {
	return &Manager{
		byID:   make(map[string]*feed),
		logger: logger,
	}
}

// Register adds a feed to the manager. If startNow is true the listener is
// started immediately. The factory is called each time the feed is enabled
// because most listeners use sync.Once on Stop() and are therefore single-use.
func (m *Manager) Register(id, name, sourceType, description string, factory func() (sources.Listener, error), startNow bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.byID[id]; exists {
		return fmt.Errorf("feed %q already registered", id)
	}

	f := &feed{
		status: FeedStatus{
			ID:          id,
			Name:        name,
			SourceType:  sourceType,
			Description: description,
			Enabled:     false,
		},
		factory: factory,
	}

	if startNow {
		l, err := factory()
		if err != nil {
			m.logger.Error("failed to create listener for feed", zap.String("feed", id), zap.Error(err))
			return fmt.Errorf("create listener for %q: %w", id, err)
		}
		if err := l.Start(); err != nil {
			m.logger.Error("failed to start feed", zap.String("feed", id), zap.Error(err))
			// Register as disabled — the feed exists but isn't running.
		} else {
			f.listener = l
			f.status.Enabled = true
		}
	}

	m.feeds = append(m.feeds, f)
	m.byID[id] = f

	m.logger.Info("feed registered",
		zap.String("id", id),
		zap.String("name", name),
		zap.Bool("enabled", f.status.Enabled),
	)

	return nil
}

// List returns a snapshot of all feed statuses in registration order.
func (m *Manager) List() []FeedStatus {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := make([]FeedStatus, len(m.feeds))
	for i, f := range m.feeds {
		out[i] = f.status
	}
	return out
}

// SetEnabled starts or stops a feed by ID. A fresh listener is created via the
// factory on each enable because listener instances are single-use.
func (m *Manager) SetEnabled(id string, enabled bool) (FeedStatus, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	f, ok := m.byID[id]
	if !ok {
		return FeedStatus{}, fmt.Errorf("unknown feed %q", id)
	}

	if f.status.Enabled == enabled {
		return f.status, nil // already in desired state
	}

	if enabled {
		l, err := f.factory()
		if err != nil {
			return f.status, fmt.Errorf("create listener for %q: %w", id, err)
		}
		if err := l.Start(); err != nil {
			return f.status, fmt.Errorf("start listener for %q: %w", id, err)
		}
		f.listener = l
		f.status.Enabled = true
		m.logger.Info("feed enabled", zap.String("id", id))
	} else {
		if f.listener != nil {
			f.listener.Stop()
			f.listener = nil
		}
		f.status.Enabled = false
		m.logger.Info("feed disabled", zap.String("id", id))
	}

	return f.status, nil
}

// StopAll gracefully stops every running feed. Intended for shutdown.
func (m *Manager) StopAll() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, f := range m.feeds {
		if f.listener != nil {
			m.logger.Info("stopping feed", zap.String("id", f.status.ID))
			f.listener.Stop()
			f.listener = nil
			f.status.Enabled = false
		}
	}
}

// SetStaleThresholds configures the warn and critical staleness thresholds
// (in seconds) for a feed.
func (m *Manager) SetStaleThresholds(id string, warnSec, criticalSec int) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if f, ok := m.byID[id]; ok {
		f.warnSec = warnSec
		f.criticalSec = criticalSec
	}
}

// RecordSuccess updates the health of a feed after a successful poll.
func (m *Manager) RecordSuccess(id string, entitiesCount int, at time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if f, ok := m.byID[id]; ok {
		f.health.LastSuccessAt = at
		f.health.EntitiesCount = entitiesCount
	}
}

// RecordError increments the error count for a feed.
func (m *Manager) RecordError(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if f, ok := m.byID[id]; ok {
		f.health.ErrorCount++
	}
}

// GetHealth returns a snapshot of the feed's health with a computed Status.
func (m *Manager) GetHealth(id string) *FeedHealth {
	m.mu.Lock()
	defer m.mu.Unlock()

	f, ok := m.byID[id]
	if !ok {
		return nil
	}

	h := f.health
	h.Status = m.computeStatus(f)
	return &h
}

// ListWithHealth returns a snapshot of all feeds with their health status.
func (m *Manager) ListWithHealth() []FeedStatusWithHealth {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := make([]FeedStatusWithHealth, len(m.feeds))
	for i, f := range m.feeds {
		h := f.health
		h.Status = m.computeStatus(f)
		out[i] = FeedStatusWithHealth{
			FeedStatus: f.status,
			Health:     &h,
		}
	}
	return out
}

// computeStatus derives the health status string for a feed. Must be called
// under m.mu.
func (m *Manager) computeStatus(f *feed) string {
	if !f.status.Enabled || f.health.LastSuccessAt.IsZero() {
		return "unknown"
	}

	age := int(time.Since(f.health.LastSuccessAt).Seconds())

	if f.criticalSec > 0 && age >= f.criticalSec {
		return "critical"
	}
	if f.warnSec > 0 && age >= f.warnSec {
		return "warn"
	}
	return "healthy"
}
