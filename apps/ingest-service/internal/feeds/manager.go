package feeds

import (
	"fmt"
	"sync"

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

// feed is the internal bookkeeping for a single registered feed.
type feed struct {
	status   FeedStatus
	factory  func() (sources.Listener, error)
	listener sources.Listener // nil when disabled
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
