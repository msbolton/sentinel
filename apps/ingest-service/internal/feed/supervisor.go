package feed

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/sentinel/ingest-service/internal/aoi"
	"github.com/sentinel/ingest-service/internal/metrics"
	"github.com/sentinel/ingest-service/internal/registry"
	"github.com/sentinel/ingest-service/internal/source/driver"
)

// Registry is the subset of the registry store the supervisor needs.
type Registry interface {
	List(ctx context.Context) ([]registry.Feed, error)
	GetArea(ctx context.Context, id string) (registry.Area, error)
	UpsertStatus(ctx context.Context, st registry.Status) error
	ReleaseStatuses(ctx context.Context, replica string) error
	Watch(ctx context.Context) <-chan registry.Change
}

// Factory builds a driver for a feed; driver.New by default.
type Factory func(spec driver.Spec, deps driver.Deps) (driver.Driver, error)

// Options tune the supervisor. Zero values take the defaults.
type Options struct {
	ReplicaID      string
	Poll           time.Duration // reconcile even without notifications (5 s)
	StatusInterval time.Duration // feed_status refresh period (5 s)
	RetryMin       time.Duration // first restart delay after a failure (5 s)
	RetryMax       time.Duration // cap on the restart delay (60 s)
	StaleWarn      time.Duration // default health thresholds
	StaleCritical  time.Duration
	Secrets        registry.Lookup
	Factory        Factory
	RawMax         int
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
	if o.Factory == nil {
		o.Factory = driver.New
	}
	return o
}

// instance is one feed this replica runs (or failed to start).
type instance struct {
	spec   registry.Feed
	runner *Runner // nil while in error
	cancel context.CancelFunc
	done   chan struct{}
	state  string
	// lastErr and errorsTotal cover start failures and fatal run errors;
	// decode errors are counted by the runner.
	lastErr     string
	errorsTotal int64
	startedAt   time.Time

	warn, critical time.Duration
	nextRetry      time.Time
	retryDelay     time.Duration
}

// Supervisor owns this replica's feed runners.
type Supervisor struct {
	reg     Registry
	pub     Publisher
	logger  *zap.Logger
	metrics *metrics.Metrics
	opts    Options

	mu      sync.Mutex
	running map[string]*instance
	stopped bool
}

// New creates a supervisor; call Run to start reconciling.
func New(reg Registry, pub Publisher, logger *zap.Logger, m *metrics.Metrics, opts Options) *Supervisor {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Supervisor{reg: reg, pub: pub, logger: logger, metrics: m, opts: opts.withDefaults(), running: map[string]*instance{}}
}

// Run reconciles until ctx is cancelled, then stops every feed and
// releases this replica's status rows.
func (s *Supervisor) Run(ctx context.Context) {
	changes := s.reg.Watch(ctx)
	poll := time.NewTicker(s.opts.Poll)
	defer poll.Stop()
	status := time.NewTicker(s.opts.StatusInterval)
	defer status.Stop()

	s.Reconcile(ctx)
	s.flushStatus(ctx)

	for {
		select {
		case <-ctx.Done():
			s.shutdown()
			return
		case c, ok := <-changes:
			if !ok {
				changes = nil
				continue
			}
			if c.Channel == registry.ChannelAreas {
				s.applyArea(ctx, c.FeedID)
			} else {
				s.Reconcile(ctx)
				s.flushStatus(ctx)
			}
		case <-poll.C:
			s.Reconcile(ctx)
		case <-status.C:
			s.flushStatus(ctx)
		}
	}
}

// Reconcile makes the running set match the enabled feeds: stops feeds
// that were disabled, deleted or changed (version bump), starts missing
// ones, and restarts failed ones whose retry delay has passed.
func (s *Supervisor) Reconcile(ctx context.Context) {
	feeds, err := s.reg.List(ctx)
	if err != nil {
		s.logger.Warn("reconcile: listing feeds failed", zap.Error(err))
		return
	}
	desired := map[string]registry.Feed{}
	for _, f := range feeds {
		if f.Enabled {
			desired[f.ID] = f
		}
	}

	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return
	}
	var toStop []*instance
	for id, inst := range s.running {
		spec, want := desired[id]
		if !want || spec.Version != inst.spec.Version {
			toStop = append(toStop, inst)
			delete(s.running, id)
		}
	}
	s.mu.Unlock()
	for _, inst := range toStop {
		s.stopInstance(inst, "disabled or changed")
	}

	now := time.Now()
	for id, spec := range desired {
		s.mu.Lock()
		inst, exists := s.running[id]
		if exists {
			s.noteExit(inst)
		}
		if exists && inst.state != registry.StateError {
			s.mu.Unlock()
			continue
		}
		if exists && now.Before(inst.nextRetry) {
			s.mu.Unlock()
			continue
		}
		if !exists {
			inst = &instance{spec: spec, state: registry.StateStarting}
			s.running[id] = inst
		} else {
			inst.spec = spec
			inst.state = registry.StateStarting
			if s.metrics != nil {
				s.metrics.FeedRestarts.WithLabelValues(id).Inc()
			}
		}
		s.mu.Unlock()
		s.startInstance(ctx, inst)
	}
}

// noteExit moves a running instance whose driver returned into the error
// state; call with s.mu held.
func (s *Supervisor) noteExit(inst *instance) {
	if inst.done == nil || inst.state == registry.StateError {
		return
	}
	select {
	case <-inst.done:
	default:
		return
	}
	inst.state = registry.StateError
	if inst.lastErr == "" {
		inst.lastErr = "driver stopped unexpectedly"
	}
	inst.done = nil
	inst.runner = nil
	s.scheduleRetry(inst)
	if s.metrics != nil {
		s.metrics.FeedsRunning.WithLabelValues(inst.spec.Driver).Dec()
	}
}

// scheduleRetry bumps the backoff; call with s.mu held.
func (s *Supervisor) scheduleRetry(inst *instance) {
	if inst.retryDelay == 0 {
		inst.retryDelay = s.opts.RetryMin
	} else if inst.retryDelay *= 2; inst.retryDelay > s.opts.RetryMax {
		inst.retryDelay = s.opts.RetryMax
	}
	inst.nextRetry = time.Now().Add(inst.retryDelay)
}

func (s *Supervisor) startInstance(ctx context.Context, inst *instance) {
	spec := inst.spec
	fail := func(err error) {
		s.mu.Lock()
		inst.state = registry.StateError
		inst.lastErr = err.Error()
		inst.errorsTotal++
		s.scheduleRetry(inst)
		delay := inst.retryDelay
		s.mu.Unlock()
		if s.metrics != nil {
			s.metrics.FeedErrorsTotal.WithLabelValues(spec.ID).Inc()
		}
		s.logger.Error("feed failed to start", zap.String("feed", spec.ID), zap.String("driver", spec.Driver), zap.Error(err), zap.Duration("retry_in", delay))
	}

	secrets, err := registry.ResolveSecrets(spec.SecretRefs, s.opts.Secrets)
	if err != nil {
		fail(err)
		return
	}
	common := spec.Common()
	logger := s.logger.With(zap.String("feed", spec.ID), zap.String("driver", spec.Driver))
	drv, err := s.opts.Factory(driver.Spec{FeedID: spec.ID, Driver: spec.Driver, Config: spec.Config, Secrets: secrets, Backpressure: common.Backpressure}, driver.Deps{Logger: logger})
	if err != nil {
		fail(fmt.Errorf("building driver: %w", err))
		return
	}
	runner := NewRunner(spec.ID, spec.Driver, drv, s.pub, s.metrics, logger, RunnerOptions{RawPolicy: common.RawPayload, RawMax: s.opts.RawMax})

	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	s.mu.Lock()
	inst.runner = runner
	inst.cancel = cancel
	inst.done = done
	inst.state = registry.StateRunning
	inst.lastErr = ""
	inst.retryDelay = 0
	inst.startedAt = time.Now()
	inst.warn = secondsOr(common.StaleWarnSec, s.opts.StaleWarn)
	inst.critical = secondsOr(common.StaleCriticalSec, s.opts.StaleCritical)
	s.mu.Unlock()
	if s.metrics != nil {
		s.metrics.FeedsRunning.WithLabelValues(spec.Driver).Inc()
	}
	s.logger.Info("feed started", zap.String("feed", spec.ID), zap.String("driver", spec.Driver), zap.Int("version", spec.Version))

	go func() {
		defer close(done)
		err := runner.Run(runCtx)
		if err != nil && runCtx.Err() == nil {
			runner.Failed(err)
			s.mu.Lock()
			inst.lastErr = err.Error()
			inst.errorsTotal++
			s.mu.Unlock()
			s.logger.Error("feed stopped with an error", zap.String("feed", spec.ID), zap.Error(err))
		}
	}()

	if _, aware := drv.(aoi.AreaAware); aware {
		s.applyArea(ctx, spec.ID)
	}
}

func (s *Supervisor) stopInstance(inst *instance, reason string) {
	s.mu.Lock()
	cancel, done, runner, state := inst.cancel, inst.done, inst.runner, inst.state
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
	if state != registry.StateError && runner != nil && s.metrics != nil {
		s.metrics.FeedsRunning.WithLabelValues(inst.spec.Driver).Dec()
	}
	s.logger.Info("feed stopped", zap.String("feed", inst.spec.ID), zap.String("reason", reason))
	var stats Stats
	if runner != nil {
		stats = runner.Stats()
	}
	ctx, cancelStatus := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelStatus()
	if err := s.reg.UpsertStatus(ctx, registry.Status{FeedID: inst.spec.ID, Owner: "", State: registry.StateStopped, Health: registry.HealthUnknown, AppliedVersion: inst.spec.Version, ErrorsTotal: inst.errorsTotal + stats.ErrorsTotal, MessagesTotal: stats.Observations, Detail: json.RawMessage("{}")}); err != nil {
		s.logger.Warn("status update failed", zap.String("feed", inst.spec.ID), zap.Error(err))
	}
}

// areaAware returns the running feed's driver when it takes an area.
func (s *Supervisor) areaAware(id string) aoi.AreaAware {
	s.mu.Lock()
	defer s.mu.Unlock()
	inst, ok := s.running[id]
	if !ok || inst.runner == nil {
		return nil
	}
	aa, _ := inst.runner.Driver().(aoi.AreaAware)
	return aa
}

// applyArea pushes the stored area to the driver when it is area-aware.
func (s *Supervisor) applyArea(ctx context.Context, id string) {
	aa := s.areaAware(id)
	if aa == nil {
		return
	}
	area, err := s.reg.GetArea(ctx, id)
	if err != nil {
		s.logger.Warn("reading area failed", zap.String("feed", id), zap.Error(err))
		return
	}
	aa.SetAreaOfInterest(toArea(area))
}

// SetArea applies an area to a feed running on this replica and returns
// the driver's status. ok is false when the feed is not running here or
// its driver does not take an area.
func (s *Supervisor) SetArea(id string, area registry.Area) (aoi.Status, bool) {
	aa := s.areaAware(id)
	if aa == nil {
		return aoi.Status{}, false
	}
	return aa.SetAreaOfInterest(toArea(area)), true
}

// AreaStatus reports the live area status of a feed running here.
func (s *Supervisor) AreaStatus(id string) (aoi.Status, bool) {
	aa := s.areaAware(id)
	if aa == nil {
		return aoi.Status{}, false
	}
	return aa.AreaStatus(), true
}

// Running reports whether this replica runs the feed.
func (s *Supervisor) Running(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	inst, ok := s.running[id]
	if ok {
		s.noteExit(inst)
	}
	return ok && inst.state == registry.StateRunning
}

// Snapshot returns the status this replica would report for a feed.
func (s *Supervisor) Snapshot(id string) (registry.Status, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	inst, ok := s.running[id]
	if !ok {
		return registry.Status{}, false
	}
	s.noteExit(inst)
	return s.statusOf(inst, time.Now()), true
}

// statusOf builds the status row; call with s.mu held.
func (s *Supervisor) statusOf(inst *instance, now time.Time) registry.Status {
	st := registry.Status{
		FeedID: inst.spec.ID, Owner: s.opts.ReplicaID, State: inst.state, Health: registry.HealthUnknown,
		LastError: inst.lastErr, ErrorsTotal: inst.errorsTotal, AppliedVersion: inst.spec.Version, Detail: json.RawMessage("{}"),
	}
	if inst.runner == nil {
		return st
	}
	stats := inst.runner.Stats()
	st.MessagesTotal = stats.Observations
	st.EntitiesLast = stats.EntitiesLast
	st.ErrorsTotal += stats.ErrorsTotal
	if st.LastError == "" {
		st.LastError = stats.LastError
	}
	if !stats.LastSuccessAt.IsZero() {
		t := stats.LastSuccessAt
		st.LastSuccessAt = &t
		st.LastMessageAt = &t
	}
	health := inst.runner.Driver().Health()
	if health.LastError != "" && st.LastError == "" {
		st.LastError = health.LastError
	}
	idle := health.Idle
	aa, areaAware := inst.runner.Driver().(aoi.AreaAware)
	var as aoi.Status
	if areaAware {
		as = aa.AreaStatus()
		idle = idle || as.Idle
	}
	if inst.state == registry.StateRunning {
		switch {
		case idle:
			st.State = registry.StateIdle
		case !stats.LastSuccessAt.IsZero():
			age := now.Sub(stats.LastSuccessAt)
			switch {
			case inst.critical > 0 && age >= inst.critical:
				st.Health = registry.HealthCritical
				st.State = registry.StateDegraded
			case inst.warn > 0 && age >= inst.warn:
				st.Health = registry.HealthWarn
			default:
				st.Health = registry.HealthHealthy
			}
		case inst.critical > 0 && now.Sub(inst.startedAt) >= inst.critical:
			st.Health = registry.HealthCritical
			st.State = registry.StateDegraded
		case inst.warn > 0 && now.Sub(inst.startedAt) >= inst.warn:
			st.Health = registry.HealthWarn
		}
	}
	detail := map[string]any{}
	for k, v := range health.Detail {
		detail[k] = v
	}
	if areaAware {
		detail["idle"] = as.Idle
		detail["active"] = as.Active
		detail["areaSqDeg"] = as.AreaSqDeg
		detail["effectiveIntervalSec"] = int(as.EffectiveInterval / time.Second)
		detail["lowCredits"] = as.LowCredits
		if as.CreditsRemaining >= 0 {
			detail["creditsRemaining"] = as.CreditsRemaining
		}
	}
	if b, err := json.Marshal(detail); err == nil {
		st.Detail = b
	}
	return st
}

func (s *Supervisor) flushStatus(ctx context.Context) {
	now := time.Now()
	s.mu.Lock()
	rows := make([]registry.Status, 0, len(s.running))
	for _, inst := range s.running {
		s.noteExit(inst)
		rows = append(rows, s.statusOf(inst, now))
	}
	s.mu.Unlock()
	for _, st := range rows {
		if err := s.reg.UpsertStatus(ctx, st); err != nil {
			s.logger.Warn("status update failed", zap.String("feed", st.FeedID), zap.Error(err))
		}
	}
}

func (s *Supervisor) shutdown() {
	s.mu.Lock()
	s.stopped = true
	insts := make([]*instance, 0, len(s.running))
	for id, inst := range s.running {
		insts = append(insts, inst)
		delete(s.running, id)
	}
	s.mu.Unlock()
	var wg sync.WaitGroup
	for _, inst := range insts {
		wg.Add(1)
		go func(inst *instance) {
			defer wg.Done()
			s.stopInstance(inst, "shutdown")
		}(inst)
	}
	wg.Wait()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.reg.ReleaseStatuses(ctx, s.opts.ReplicaID); err != nil {
		s.logger.Warn("releasing statuses failed", zap.Error(err))
	}
}

func toArea(a registry.Area) aoi.Area {
	out := aoi.Area{Active: a.Active, Clients: a.Clients, Boxes: make([]aoi.BBox, 0, len(a.Boxes))}
	for _, b := range a.Boxes {
		out.Boxes = append(out.Boxes, aoi.BBox{North: b.North, South: b.South, East: b.East, West: b.West})
	}
	if a.ExpiresAt != nil {
		out.ExpiresAt = *a.ExpiresAt
	}
	return out
}

func secondsOr(sec int, def time.Duration) time.Duration {
	if sec > 0 {
		return time.Duration(sec) * time.Second
	}
	return def
}
