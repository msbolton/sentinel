// Package runtime runs the feeds the registry says should run on this
// replica. It reconciles desired state (feeds rows) against the listeners it
// owns, applies pushed areas of interest, and reports observed state back to
// feed_status. Reconciliation runs on one goroutine, so a slow listener
// start never blocks the management API.
package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/sentinel/ingest-service/internal/metrics"
	"github.com/sentinel/ingest-service/internal/models"
	"github.com/sentinel/ingest-service/internal/registry"
	"github.com/sentinel/ingest-service/internal/sources"
)

// Registry is the subset of the registry store the runtime needs.
type Registry interface {
	List(ctx context.Context) ([]registry.Feed, error)
	GetArea(ctx context.Context, id string) (registry.Area, error)
	UpsertStatus(ctx context.Context, st registry.Status) error
	ReleaseStatuses(ctx context.Context, replica string) error
	Watch(ctx context.Context) <-chan registry.Change
}

// Deps is what a Factory gets to build a listener for one feed. The
// callbacks are already bound to the feed id.
type Deps struct {
	Input     chan<- *models.IngestMessage
	Logger    *zap.Logger
	Metrics   *metrics.Metrics
	OnSuccess func(count int, at time.Time)
	OnError   func()
}

// Factory builds a listener for a feed spec. Secrets are the resolved
// secret references.
type Factory func(spec registry.Feed, secrets map[string]string, deps Deps) (sources.Listener, error)

// Options tunes the runtime. Zero values take the defaults.
type Options struct {
	ReplicaID      string
	Poll           time.Duration // reconcile even without notifications (5 s)
	StatusInterval time.Duration // feed_status refresh period (5 s)
	RetryMin       time.Duration // first restart delay after a failed start (5 s)
	RetryMax       time.Duration // cap on the restart delay (60 s)
	StaleWarn      time.Duration // default health thresholds
	StaleCritical  time.Duration
	Secrets        registry.Lookup
}

func (o Options) withDefaults() Options {
	if o.ReplicaID == "" {
		o.ReplicaID = "local"
	}
	if o.Poll <= 0 {
		o.Poll = 5 * time.Second
	}
	if o.StatusInterval <= 0 {
		o.StatusInterval = 5 * time.Second
	}
	if o.RetryMin <= 0 {
		o.RetryMin = 5 * time.Second
	}
	if o.RetryMax <= 0 {
		o.RetryMax = time.Minute
	}
	if o.StaleWarn <= 0 {
		o.StaleWarn = 120 * time.Second
	}
	if o.StaleCritical <= 0 {
		o.StaleCritical = 300 * time.Second
	}
	return o
}

// instance is one feed this replica runs (or failed to start).
type instance struct {
	spec     registry.Feed
	listener sources.Listener // nil while in error
	state    string
	lastErr  string

	lastSuccessAt time.Time
	entitiesLast  int
	errorsTotal   int64
	messagesTotal int64

	warn, critical time.Duration
	nextRetry      time.Time
	retryDelay     time.Duration
}

// Runtime owns this replica's listeners.
type Runtime struct {
	reg       Registry
	factories map[string]Factory
	input     chan<- *models.IngestMessage
	logger    *zap.Logger
	metrics   *metrics.Metrics
	opts      Options

	mu      sync.Mutex
	running map[string]*instance
	stopped bool
}

// New creates a runtime; call Run to start reconciling.
func New(reg Registry, factories map[string]Factory, input chan<- *models.IngestMessage, logger *zap.Logger, m *metrics.Metrics, opts Options) *Runtime {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Runtime{
		reg:       reg,
		factories: factories,
		input:     input,
		logger:    logger,
		metrics:   m,
		opts:      opts.withDefaults(),
		running:   map[string]*instance{},
	}
}

// Run reconciles until ctx is cancelled, then stops every listener and
// releases this replica's status rows.
func (r *Runtime) Run(ctx context.Context) {
	changes := r.reg.Watch(ctx)
	poll := time.NewTicker(r.opts.Poll)
	defer poll.Stop()
	status := time.NewTicker(r.opts.StatusInterval)
	defer status.Stop()

	r.Reconcile(ctx)
	r.flushStatus(ctx)

	for {
		select {
		case <-ctx.Done():
			r.shutdown()
			return
		case c, ok := <-changes:
			if !ok {
				changes = nil
				continue
			}
			if c.Channel == registry.ChannelAreas {
				r.applyArea(ctx, c.FeedID)
			} else {
				r.Reconcile(ctx)
				r.flushStatus(ctx)
			}
		case <-poll.C:
			r.Reconcile(ctx)
		case <-status.C:
			r.flushStatus(ctx)
		}
	}
}

// Reconcile makes the running set match the enabled feeds.
func (r *Runtime) Reconcile(ctx context.Context) {
	feeds, err := r.reg.List(ctx)
	if err != nil {
		r.logger.Warn("reconcile: listing feeds failed", zap.Error(err))
		return
	}
	desired := map[string]registry.Feed{}
	for _, f := range feeds {
		if f.Enabled {
			desired[f.ID] = f
		}
	}

	r.mu.Lock()
	if r.stopped {
		r.mu.Unlock()
		return
	}
	var toStop []*instance
	for id, inst := range r.running {
		spec, want := desired[id]
		if !want || spec.Version != inst.spec.Version {
			toStop = append(toStop, inst)
			delete(r.running, id)
		}
	}
	r.mu.Unlock()

	for _, inst := range toStop {
		r.stopInstance(inst, "disabled or changed")
	}

	now := time.Now()
	for id, spec := range desired {
		r.mu.Lock()
		inst, exists := r.running[id]
		if exists && inst.state != registry.StateError {
			r.mu.Unlock()
			continue
		}
		if exists && now.Before(inst.nextRetry) {
			r.mu.Unlock()
			continue
		}
		if !exists {
			inst = &instance{spec: spec, state: registry.StateStarting}
			r.running[id] = inst
		} else {
			inst.spec = spec
			inst.state = registry.StateStarting
		}
		r.mu.Unlock()
		r.startInstance(ctx, inst)
	}
}

func (r *Runtime) startInstance(ctx context.Context, inst *instance) {
	spec := inst.spec
	fail := func(err error) {
		r.mu.Lock()
		inst.state = registry.StateError
		inst.lastErr = err.Error()
		inst.errorsTotal++
		if inst.retryDelay == 0 {
			inst.retryDelay = r.opts.RetryMin
		} else if inst.retryDelay *= 2; inst.retryDelay > r.opts.RetryMax {
			inst.retryDelay = r.opts.RetryMax
		}
		inst.nextRetry = time.Now().Add(inst.retryDelay)
		r.mu.Unlock()
		r.logger.Error("feed failed to start", zap.String("feed", spec.ID), zap.String("driver", spec.Driver), zap.Error(err), zap.Duration("retry_in", inst.retryDelay))
	}

	factory, ok := r.factories[spec.Driver]
	if !ok {
		fail(fmt.Errorf("no factory for driver %q", spec.Driver))
		return
	}
	secrets, err := registry.ResolveSecrets(spec.SecretRefs, r.opts.Secrets)
	if err != nil {
		fail(err)
		return
	}
	common := spec.Common()
	deps := Deps{
		Input:   r.input,
		Logger:  r.logger.With(zap.String("feed", spec.ID)),
		Metrics: r.metrics,
		OnSuccess: func(count int, at time.Time) {
			r.mu.Lock()
			inst.lastSuccessAt = at
			inst.entitiesLast = count
			inst.messagesTotal += int64(count)
			r.mu.Unlock()
			if r.metrics != nil {
				r.metrics.FeedLastSuccess.WithLabelValues(spec.ID).Set(float64(at.Unix()))
				r.metrics.FeedEntityCount.WithLabelValues(spec.ID).Set(float64(count))
			}
		},
		OnError: func() {
			r.mu.Lock()
			inst.errorsTotal++
			r.mu.Unlock()
			if r.metrics != nil {
				r.metrics.FeedErrorsTotal.WithLabelValues(spec.ID).Inc()
			}
		},
	}
	listener, err := factory(spec, secrets, deps)
	if err != nil {
		fail(fmt.Errorf("building listener: %w", err))
		return
	}
	if err := listener.Start(); err != nil {
		fail(fmt.Errorf("starting listener: %w", err))
		return
	}

	r.mu.Lock()
	inst.listener = listener
	inst.state = registry.StateRunning
	inst.lastErr = ""
	inst.retryDelay = 0
	inst.warn = secondsOr(common.StaleWarnSec, r.opts.StaleWarn)
	inst.critical = secondsOr(common.StaleCriticalSec, r.opts.StaleCritical)
	r.mu.Unlock()
	r.logger.Info("feed started", zap.String("feed", spec.ID), zap.String("driver", spec.Driver), zap.Int("version", spec.Version))

	if _, aware := listener.(sources.ViewportAware); aware {
		r.applyArea(ctx, spec.ID)
	}
}

func (r *Runtime) stopInstance(inst *instance, reason string) {
	if inst.listener != nil {
		inst.listener.Stop()
	}
	r.logger.Info("feed stopped", zap.String("feed", inst.spec.ID), zap.String("reason", reason))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := r.reg.UpsertStatus(ctx, registry.Status{FeedID: inst.spec.ID, Owner: "", State: registry.StateStopped, Health: registry.HealthUnknown, AppliedVersion: inst.spec.Version, ErrorsTotal: inst.errorsTotal, MessagesTotal: inst.messagesTotal}); err != nil {
		r.logger.Warn("status update failed", zap.String("feed", inst.spec.ID), zap.Error(err))
	}
}

// applyArea pushes the stored area to the listener when it is area-aware.
func (r *Runtime) applyArea(ctx context.Context, id string) {
	r.mu.Lock()
	inst, ok := r.running[id]
	var va sources.ViewportAware
	if ok && inst.listener != nil {
		va, _ = inst.listener.(sources.ViewportAware)
	}
	r.mu.Unlock()
	if va == nil {
		return
	}
	area, err := r.reg.GetArea(ctx, id)
	if err != nil {
		r.logger.Warn("reading area failed", zap.String("feed", id), zap.Error(err))
		return
	}
	if area.Expired(time.Now()) {
		area.Active = false
		area.Boxes = nil
	}
	va.SetViewport(toViewport(area))
}

// SetArea applies an area to a feed running on this replica and returns the
// listener's status. ok is false when the feed is not running here or its
// listener is not area-aware.
func (r *Runtime) SetArea(id string, area registry.Area) (sources.ViewportStatus, bool) {
	r.mu.Lock()
	inst, ok := r.running[id]
	var va sources.ViewportAware
	if ok && inst.listener != nil {
		va, _ = inst.listener.(sources.ViewportAware)
	}
	r.mu.Unlock()
	if va == nil {
		return sources.ViewportStatus{}, false
	}
	return va.SetViewport(toViewport(area)), true
}

// AreaStatus reports the live area status of a feed running here.
func (r *Runtime) AreaStatus(id string) (sources.ViewportStatus, bool) {
	r.mu.Lock()
	inst, ok := r.running[id]
	var va sources.ViewportAware
	if ok && inst.listener != nil {
		va, _ = inst.listener.(sources.ViewportAware)
	}
	r.mu.Unlock()
	if va == nil {
		return sources.ViewportStatus{}, false
	}
	return va.ViewportStatus(), true
}

// Running reports whether this replica runs the feed.
func (r *Runtime) Running(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	inst, ok := r.running[id]
	return ok && inst.state == registry.StateRunning
}

// Snapshot returns the status this replica would report for a feed.
func (r *Runtime) Snapshot(id string) (registry.Status, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	inst, ok := r.running[id]
	if !ok {
		return registry.Status{}, false
	}
	return r.statusOf(inst, time.Now()), true
}

// statusOf builds the status row; call with r.mu held.
func (r *Runtime) statusOf(inst *instance, now time.Time) registry.Status {
	st := registry.Status{
		FeedID:         inst.spec.ID,
		Owner:          r.opts.ReplicaID,
		State:          inst.state,
		Health:         registry.HealthUnknown,
		LastError:      inst.lastErr,
		MessagesTotal:  inst.messagesTotal,
		EntitiesLast:   inst.entitiesLast,
		ErrorsTotal:    inst.errorsTotal,
		AppliedVersion: inst.spec.Version,
		Detail:         json.RawMessage("{}"),
	}
	if !inst.lastSuccessAt.IsZero() {
		t := inst.lastSuccessAt
		st.LastSuccessAt = &t
		st.LastMessageAt = &t
	}
	if inst.state == registry.StateRunning && !inst.lastSuccessAt.IsZero() {
		age := now.Sub(inst.lastSuccessAt)
		switch {
		case inst.critical > 0 && age >= inst.critical:
			st.Health = registry.HealthCritical
		case inst.warn > 0 && age >= inst.warn:
			st.Health = registry.HealthWarn
		default:
			st.Health = registry.HealthHealthy
		}
	}
	if inst.listener != nil {
		if va, ok := inst.listener.(sources.ViewportAware); ok {
			vs := va.ViewportStatus()
			if vs.Idle && inst.state == registry.StateRunning {
				st.State = registry.StateIdle
			}
			detail := map[string]any{
				"idle":                 vs.Idle,
				"active":               vs.Active,
				"areaSqDeg":            vs.AreaSqDeg,
				"effectiveIntervalSec": int(vs.EffectiveInterval / time.Second),
				"lowCredits":           vs.LowCredits,
			}
			if vs.CreditsRemaining >= 0 {
				detail["creditsRemaining"] = vs.CreditsRemaining
			}
			if b, err := json.Marshal(detail); err == nil {
				st.Detail = b
			}
		}
	}
	return st
}

func (r *Runtime) flushStatus(ctx context.Context) {
	now := time.Now()
	r.mu.Lock()
	rows := make([]registry.Status, 0, len(r.running))
	for _, inst := range r.running {
		rows = append(rows, r.statusOf(inst, now))
	}
	r.mu.Unlock()
	for _, st := range rows {
		if err := r.reg.UpsertStatus(ctx, st); err != nil {
			r.logger.Warn("status update failed", zap.String("feed", st.FeedID), zap.Error(err))
		}
	}
}

func (r *Runtime) shutdown() {
	r.mu.Lock()
	r.stopped = true
	insts := make([]*instance, 0, len(r.running))
	for id, inst := range r.running {
		insts = append(insts, inst)
		delete(r.running, id)
	}
	r.mu.Unlock()
	for _, inst := range insts {
		r.stopInstance(inst, "shutdown")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := r.reg.ReleaseStatuses(ctx, r.opts.ReplicaID); err != nil {
		r.logger.Warn("releasing statuses failed", zap.Error(err))
	}
}

func toViewport(a registry.Area) sources.Viewport {
	v := sources.Viewport{Active: a.Active, Clients: a.Clients, Boxes: make([]sources.BBox, 0, len(a.Boxes))}
	for _, b := range a.Boxes {
		v.Boxes = append(v.Boxes, sources.BBox{North: b.North, South: b.South, East: b.East, West: b.West})
	}
	return v
}

func secondsOr(sec int, def time.Duration) time.Duration {
	if sec > 0 {
		return time.Duration(sec) * time.Second
	}
	return def
}
