package feeds

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/sentinel/ingest-service/internal/metrics"
	"github.com/sentinel/ingest-service/internal/models"
	"github.com/sentinel/ingest-service/internal/sources"
	"github.com/sentinel/ingest-service/internal/store"
)

// FeedStatus describes the current state of a registered data feed.
type FeedStatus struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	SourceType    string `json:"sourceType"`
	Description   string `json:"description"`
	Enabled       bool   `json:"enabled"`
	Custom        bool   `json:"custom"`
	ConnectorType string `json:"connectorType,omitempty"`
	Format        string `json:"format,omitempty"`
}

// MQTTConfig holds connection parameters for a custom MQTT feed.
type MQTTConfig struct {
	BrokerURL string   `json:"broker_url"`
	Topics    []string `json:"topics"`
	QoS       int      `json:"qos"`
}

// STOMPConfig holds connection parameters for a custom STOMP feed.
type STOMPConfig struct {
	BrokerURL string `json:"broker_url"`
	Queue     string `json:"queue"`
}

// TCPConfig holds connection parameters for a custom TCP feed.
type TCPConfig struct {
	Address string `json:"address"`
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
	mu            sync.Mutex
	feeds         []*feed          // insertion-ordered
	byID          map[string]*feed // quick lookup
	logger        *zap.Logger
	store         *store.Store                // nil for tests / when no DB
	pipelineInput chan<- *models.IngestMessage // nil until wired
	metrics       *metrics.Metrics            // nil in tests
}

// NewManager creates a new feed manager. Pass nil for store/pipelineInput/metrics
// when custom feed CRUD is not needed (e.g. tests).
func NewManager(logger *zap.Logger, st *store.Store, pipelineInput chan<- *models.IngestMessage, m *metrics.Metrics) *Manager {
	return &Manager{
		byID:          make(map[string]*feed),
		logger:        logger,
		store:         st,
		pipelineInput: pipelineInput,
		metrics:       m,
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

	// Persist enabled state for custom feeds.
	if f.status.Custom && m.store != nil {
		uid, parseErr := uuid.Parse(id)
		if parseErr == nil {
			if storeErr := m.store.SetEnabled(context.Background(), uid, enabled); storeErr != nil {
				m.logger.Error("failed to persist feed enabled state", zap.String("id", id), zap.Error(storeErr))
			}
		}
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

// listenerFactory builds a factory function for a custom feed based on its
// connector type and config JSON. The returned listeners are pre-wired with
// the feed's ID and format.
func (m *Manager) listenerFactory(cf *store.CustomFeed) func() (sources.Listener, error) {
	feedID := cf.ID.String()
	format := cf.Format
	connType := cf.ConnectorType
	configJSON := cf.Config

	return func() (sources.Listener, error) {
		switch connType {
		case "mqtt":
			var cfg MQTTConfig
			if err := json.Unmarshal(configJSON, &cfg); err != nil {
				return nil, fmt.Errorf("parsing MQTT config: %w", err)
			}
			topics := strings.Join(cfg.Topics, ",")
			l := sources.NewMQTTListener(cfg.BrokerURL, topics, feedID, m.pipelineInput, m.logger, m.metrics)
			return &formatListener{Listener: l, input: m.pipelineInput, format: format}, nil

		case "stomp":
			var cfg STOMPConfig
			if err := json.Unmarshal(configJSON, &cfg); err != nil {
				return nil, fmt.Errorf("parsing STOMP config: %w", err)
			}
			l := sources.NewSTOMPListener(cfg.BrokerURL, cfg.Queue, feedID, m.pipelineInput, m.logger, m.metrics)
			return &formatListener{Listener: l, input: m.pipelineInput, format: format}, nil

		case "tcp":
			var cfg TCPConfig
			if err := json.Unmarshal(configJSON, &cfg); err != nil {
				return nil, fmt.Errorf("parsing TCP config: %w", err)
			}
			l := sources.NewTCPListener(cfg.Address, feedID, m.pipelineInput, m.logger, m.metrics)
			return &formatListener{Listener: l, input: m.pipelineInput, format: format}, nil

		default:
			return nil, fmt.Errorf("unsupported connector type: %s", connType)
		}
	}
}

// formatListener wraps a source listener to inject format metadata.
// The actual format injection happens via IngestMessage.Format set by the
// source listener's feedID, but this wrapper keeps the factory pattern clean.
type formatListener struct {
	sources.Listener
	input  chan<- *models.IngestMessage
	format string
}

// CreateCustomFeed persists a new custom feed and starts its listener.
func (m *Manager) CreateCustomFeed(ctx context.Context, name, connectorType, format string, config json.RawMessage) (*FeedStatusWithHealth, error) {
	if m.store == nil {
		return nil, fmt.Errorf("custom feed store not configured")
	}

	id := uuid.New()
	now := time.Now().UTC()
	cf := &store.CustomFeed{
		ID:            id,
		Name:          name,
		ConnectorType: connectorType,
		Format:        format,
		Config:        config,
		Enabled:       true,
		CreatedAt:     now,
		UpdatedAt:     now,
	}

	if err := m.store.Create(ctx, cf); err != nil {
		return nil, fmt.Errorf("persisting custom feed: %w", err)
	}

	factory := m.listenerFactory(cf)
	description := fmt.Sprintf("Custom %s feed: %s", connectorType, name)
	if err := m.Register(id.String(), name, connectorType, description, factory, true); err != nil {
		// Clean up DB on registration failure.
		_ = m.store.Delete(ctx, id)
		return nil, fmt.Errorf("registering custom feed: %w", err)
	}

	// Mark as custom with connector/format metadata.
	m.mu.Lock()
	if f, ok := m.byID[id.String()]; ok {
		f.status.Custom = true
		f.status.ConnectorType = connectorType
		f.status.Format = format
	}
	m.mu.Unlock()

	feeds := m.ListWithHealth()
	for _, f := range feeds {
		if f.ID == id.String() {
			return &f, nil
		}
	}
	return nil, fmt.Errorf("feed created but not found in list")
}

// DeleteCustomFeed stops and removes a custom feed. Refuses to delete built-in feeds.
func (m *Manager) DeleteCustomFeed(ctx context.Context, id string) error {
	m.mu.Lock()
	f, ok := m.byID[id]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("unknown feed %q", id)
	}
	if !f.status.Custom {
		m.mu.Unlock()
		return fmt.Errorf("cannot delete built-in feed %q", id)
	}

	// Stop listener if running.
	if f.listener != nil {
		f.listener.Stop()
		f.listener = nil
	}

	// Remove from internal maps.
	delete(m.byID, id)
	for i, ff := range m.feeds {
		if ff.status.ID == id {
			m.feeds = append(m.feeds[:i], m.feeds[i+1:]...)
			break
		}
	}
	m.mu.Unlock()

	// Delete from Postgres.
	if m.store != nil {
		uid, err := uuid.Parse(id)
		if err != nil {
			return fmt.Errorf("invalid feed ID: %w", err)
		}
		if err := m.store.Delete(ctx, uid); err != nil {
			m.logger.Error("failed to delete custom feed from store", zap.String("id", id), zap.Error(err))
			return err
		}
	}

	m.logger.Info("custom feed deleted", zap.String("id", id))
	return nil
}

// UpdateCustomFeed stops the current listener, updates the feed config in Postgres,
// and restarts with the new configuration. Refuses to update built-in feeds.
func (m *Manager) UpdateCustomFeed(ctx context.Context, id string, name, format string, config json.RawMessage) (*FeedStatusWithHealth, error) {
	m.mu.Lock()
	f, ok := m.byID[id]
	if !ok {
		m.mu.Unlock()
		return nil, fmt.Errorf("unknown feed %q", id)
	}
	if !f.status.Custom {
		m.mu.Unlock()
		return nil, fmt.Errorf("cannot update built-in feed %q", id)
	}

	// Stop existing listener.
	if f.listener != nil {
		f.listener.Stop()
		f.listener = nil
	}
	m.mu.Unlock()

	// Update in Postgres.
	uid, err := uuid.Parse(id)
	if err != nil {
		return nil, fmt.Errorf("invalid feed ID: %w", err)
	}

	if m.store != nil {
		if err := m.store.Update(ctx, uid, name, format, config); err != nil {
			return nil, fmt.Errorf("updating custom feed: %w", err)
		}
	}

	// Rebuild factory with new config and restart.
	m.mu.Lock()
	f.status.Name = name
	f.status.Format = format
	connectorType := f.status.ConnectorType

	cf := &store.CustomFeed{
		ID:            uid,
		Name:          name,
		ConnectorType: connectorType,
		Format:        format,
		Config:        config,
	}
	f.factory = m.listenerFactory(cf)

	// Start the new listener.
	l, err := f.factory()
	if err != nil {
		m.mu.Unlock()
		return nil, fmt.Errorf("creating updated listener: %w", err)
	}
	if err := l.Start(); err != nil {
		m.mu.Unlock()
		return nil, fmt.Errorf("starting updated listener: %w", err)
	}
	f.listener = l
	f.status.Enabled = true
	m.mu.Unlock()

	m.logger.Info("custom feed updated", zap.String("id", id), zap.String("name", name))

	feeds := m.ListWithHealth()
	for _, fh := range feeds {
		if fh.ID == id {
			return &fh, nil
		}
	}
	return nil, fmt.Errorf("feed updated but not found in list")
}

// LoadCustomFeeds restores custom feeds from Postgres on startup.
func (m *Manager) LoadCustomFeeds(ctx context.Context) error {
	if m.store == nil {
		return nil
	}

	feeds, err := m.store.List(ctx)
	if err != nil {
		return fmt.Errorf("loading custom feeds: %w", err)
	}

	for i := range feeds {
		cf := &feeds[i]
		factory := m.listenerFactory(cf)
		description := fmt.Sprintf("Custom %s feed: %s", cf.ConnectorType, cf.Name)

		if err := m.Register(cf.ID.String(), cf.Name, cf.ConnectorType, description, factory, cf.Enabled); err != nil {
			m.logger.Error("failed to load custom feed",
				zap.String("id", cf.ID.String()),
				zap.String("name", cf.Name),
				zap.Error(err),
			)
			continue
		}

		// Mark as custom with metadata.
		m.mu.Lock()
		if f, ok := m.byID[cf.ID.String()]; ok {
			f.status.Custom = true
			f.status.ConnectorType = cf.ConnectorType
			f.status.Format = cf.Format
		}
		m.mu.Unlock()

		m.logger.Info("loaded custom feed",
			zap.String("id", cf.ID.String()),
			zap.String("name", cf.Name),
			zap.Bool("enabled", cf.Enabled),
		)
	}

	return nil
}
