package feed

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"slices"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/sentinel/ingest-service/internal/aoi"
	"github.com/sentinel/ingest-service/internal/metrics"
	"github.com/sentinel/ingest-service/internal/outbound"
	"github.com/sentinel/ingest-service/internal/policy"
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

// Leaser assigns feeds to replicas (see the lease package). A nil Leaser
// runs every enabled feed on this replica.
type Leaser interface {
	Heartbeat(ctx context.Context) error
	Live(ctx context.Context) ([]string, error)
	Acquire(ctx context.Context, feedID string) (token int64, ok bool, err error)
	Renew(ctx context.Context, feedID string, token int64) (bool, error)
	Release(ctx context.Context, feedID string, token int64) error
	Leave(ctx context.Context) error
	TTL() time.Duration
}

// ListenerLister is implemented by leasers that know the listener replicas:
// all of them, and the dedicated ones among them, which run listening
// connections only.
type ListenerLister interface {
	Listeners(ctx context.Context) (all, dedicated []string, err error)
}

// Preferred picks a feed's owner from the live set; lease.Preferred by
// default (rendezvous hash, pin wins).
type Preferred func(feedID, pinned string, live []string) string

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
	// Listener means this replica takes listening connections (the ones that
	// bind a port); ListenerHost is the address senders use to reach it.
	// ListenerShared lets such a replica take its share of the ordinary feeds
	// too; without it a listener runs only listening connections and the feeds
	// pinned to it.
	Listener       bool
	ListenerHost   string
	ListenerShared bool
	// Leaser enables sharding; LeaseTick is how often leases are renewed
	// and ownership re-evaluated (2 s), RebalanceAfter how long a feed
	// keeps running here while another live replica is its preferred
	// owner before it is handed over (30 s).
	Leaser         Leaser
	Preferred      Preferred
	LeaseTick      time.Duration
	RebalanceAfter time.Duration
	// ConsumerFactory builds the Kafka consumer group of an outbound
	// connection; nil disables the outbound side (tests, or a replica
	// without Kafka consume access).
	ConsumerFactory outbound.ConsumerFactory
	// Shapes lets exporting drivers read geofences and locations.
	Shapes driver.ShapeSource
	// Density builds the density-grid publisher for one feed (nil: drivers
	// get none and keep their counts to themselves).
	Density func(feedID, classification string) driver.DensityPublisher
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
	if o.LeaseTick <= 0 {
		o.LeaseTick = 2 * time.Second
	}
	if o.RebalanceAfter <= 0 {
		o.RebalanceAfter = 30 * time.Second
	}
	if o.Preferred == nil {
		o.Preferred = defaultPreferred
	}
	return o
}

// defaultPreferred is the rendezvous hash; it mirrors lease.Preferred
// without importing it so the feed package stays free of Postgres.
func defaultPreferred(feedID, pinned string, live []string) string {
	if pinned != "" {
		return pinned
	}
	best, bestScore := "", uint64(0)
	for _, r := range live {
		h := fnv.New64a()
		_, _ = h.Write([]byte(feedID))
		_, _ = h.Write([]byte{'|'})
		_, _ = h.Write([]byte(r))
		if s := h.Sum64(); best == "" || s > bestScore || (s == bestScore && r < best) {
			best, bestScore = r, s
		}
	}
	return best
}

// listens reports whether the feed binds a port.
func listens(spec registry.Feed) bool {
	_, _, ok := driver.ListenOf(spec.Driver, spec.Config)
	return ok
}

// candidates is the replica set a feed may run on: the listener replicas for
// a listening feed, the ordinary replicas for any other. Either can be empty,
// and the feed then waits; there is no fallback onto a replica of the other
// kind. Call with s.mu held.
func (s *Supervisor) candidates(spec registry.Feed) []string {
	if listens(spec) {
		return s.listeners
	}
	return s.ordinary()
}

// ordinary is the live replicas that run ordinary feeds: all of them but the
// dedicated listeners. Call with s.mu held.
func (s *Supervisor) ordinary() []string {
	if len(s.dedicated) == 0 {
		return s.live
	}
	out := make([]string, 0, len(s.live))
	for _, r := range s.live {
		if !slices.Contains(s.dedicated, r) {
			out = append(out, r)
		}
	}
	return out
}

// Why a feed has no replica to run on: the reason label of FeedsUnplaced.
const (
	unplacedNoListener        = "no_listener"
	unplacedNoOrdinaryReplica = "no_ordinary_replica"
)

// unplacedReasons are the reasons in the order noteUnplaced reports them, each
// with the text that tells an operator what to do about it.
var unplacedReasons = []struct{ reason, detail string }{
	{unplacedNoListener, registry.NoListenerError},
	{unplacedNoOrdinaryReplica, registry.NoOrdinaryReplicaError},
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

	token          int64     // lease fencing token; 0 without a leaser
	elsewhereSince time.Time // when another live replica became the preferred owner
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
	// waitingSince records when a feed another replica should own was
	// first seen free, so this replica only takes it after the preferred
	// owner had a lease TTL to claim it.
	waitingSince map[string]time.Time
	live         []string
	listeners    []string // the live replicas that take listening connections
	dedicated    []string // the listeners among them that take nothing else
	// unplaced is, per reason, how many feeds had no replica at the last
	// reconcile; noteUnplaced compares against it to log only the changes.
	unplaced map[string]int
	// connections is the set of feed ids that send (direction out or both),
	// from the last listing; outbound policies use it to avoid re-exporting
	// one connection's contributions on another.
	connections map[string]bool
}

// driverDirection is the registered driver's direction, or in when the
// driver is unknown (tests with ad-hoc factories).
func driverDirection(id string) driver.Direction {
	if d, ok := driver.Lookup(id); ok {
		return d.Direction.Normalize()
	}
	return driver.DirectionIn
}

// outboundTuning mirrors config.outbound.
type outboundTuning struct {
	QueueMax           int     `json:"queueMax"`
	PerEntityMinSec    float64 `json:"perEntityMinSec"`
	MaxEventsPerSec    int     `json:"maxEventsPerSec"`
	MaxLagSec          int     `json:"maxLagSec"`
	PolicyExitGraceSec *int    `json:"policyExitGraceSec"`
}

// buildPump creates the outbound half of a connection, or nil when the
// feed does not send or the driver cannot consume.
func (s *Supervisor) buildPump(spec registry.Feed, common registry.CommonOptions, drv driver.Driver, logger *zap.Logger) (*outbound.Pump, error) {
	consumer, ok := drv.(driver.Consumer)
	if !ok || s.opts.ConsumerFactory == nil || !spec.Direction(driverDirection(spec.Driver)).Outbound() {
		return nil, nil
	}
	pol, err := policy.Parse(common.Policy)
	if err != nil {
		return nil, err
	}
	var tune outboundTuning
	if len(common.Outbound) > 0 {
		if err := json.Unmarshal(common.Outbound, &tune); err != nil {
			return nil, fmt.Errorf("outbound: %w", err)
		}
	}
	s.mu.Lock()
	conns := make(map[string]bool, len(s.connections)+1)
	for id := range s.connections {
		conns[id] = true
	}
	s.mu.Unlock()
	conns[spec.ID] = true
	o := outbound.Options{
		FeedID: spec.ID, ReplicaID: s.opts.ReplicaID, Policy: pol,
		Context:         policy.Context{SelfFeedID: spec.ID, ConnectionFeedIDs: conns},
		QueueMax:        tune.QueueMax,
		PerEntityMin:    time.Duration(tune.PerEntityMinSec * float64(time.Second)),
		MaxEventsPerSec: tune.MaxEventsPerSec,
		MaxLag:          time.Duration(tune.MaxLagSec) * time.Second,
		ExitGrace:       30 * time.Second,
	}
	if tune.PerEntityMinSec == 0 && !bytesHas(common.Outbound, "perEntityMinSec") {
		o.PerEntityMin = 5 * time.Second
	}
	if tune.PolicyExitGraceSec != nil {
		o.ExitGrace = time.Duration(*tune.PolicyExitGraceSec) * time.Second
	}
	return outbound.New(o, consumer, s.opts.ConsumerFactory, s.metrics, logger), nil
}

func bytesHas(raw json.RawMessage, key string) bool {
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil {
		return false
	}
	_, ok := m[key]
	return ok
}

// New creates a supervisor; call Run to start reconciling.
func New(reg Registry, pub Publisher, logger *zap.Logger, m *metrics.Metrics, opts Options) *Supervisor {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Supervisor{reg: reg, pub: pub, logger: logger, metrics: m, opts: opts.withDefaults(), running: map[string]*instance{}, waitingSince: map[string]time.Time{}, unplaced: map[string]int{}}
}

// ReplicaID returns this replica's id.
func (s *Supervisor) ReplicaID() string { return s.opts.ReplicaID }

// Run reconciles until ctx is cancelled, then stops every feed and
// releases this replica's status rows.
func (s *Supervisor) Run(ctx context.Context) {
	changes := s.reg.Watch(ctx)
	pollEvery := s.opts.Poll
	if s.opts.Leaser != nil && s.opts.LeaseTick < pollEvery {
		pollEvery = s.opts.LeaseTick
	}
	poll := time.NewTicker(pollEvery)
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

// renewLeases heartbeats this replica and renews every held lease; a
// lease that cannot be renewed stops its feed at once, without touching
// the status row the new owner now writes.
func (s *Supervisor) renewLeases(ctx context.Context) {
	if err := s.opts.Leaser.Heartbeat(ctx); err != nil {
		s.logger.Warn("replica heartbeat failed", zap.Error(err))
	}
	s.mu.Lock()
	held := make([]*instance, 0, len(s.running))
	for _, inst := range s.running {
		if inst.token > 0 && inst.state != registry.StateError {
			held = append(held, inst)
		}
	}
	s.mu.Unlock()
	for _, inst := range held {
		ok, err := s.opts.Leaser.Renew(ctx, inst.spec.ID, inst.token)
		if err != nil {
			s.logger.Warn("lease renew failed", zap.String("feed", inst.spec.ID), zap.Error(err))
			continue
		}
		if !ok {
			s.mu.Lock()
			delete(s.running, inst.spec.ID)
			s.mu.Unlock()
			s.logger.Warn("lease lost; stopping feed", zap.String("feed", inst.spec.ID), zap.Int64("token", inst.token))
			if s.metrics != nil {
				s.metrics.LeasesLost.WithLabelValues(inst.spec.ID).Inc()
			}
			s.stopInstance(inst, "lease lost", false)
		}
	}
}

// owns reports whether this replica should run the feed now and, when it
// should, takes the lease. ok false means skip the feed this round.
func (s *Supervisor) owns(ctx context.Context, spec registry.Feed, now time.Time) (token int64, ok bool) {
	if s.opts.Leaser == nil {
		return 0, true
	}
	pinned := spec.Common().PinnedReplica
	if pinned == "" {
		listening := listens(spec)
		if listening && !s.opts.Listener {
			return 0, false // listening connections run only on listener replicas
		}
		if !listening && s.opts.Listener && !s.opts.ListenerShared {
			return 0, false // a dedicated listener never steps in for an ordinary feed
		}
	}
	preferred := s.opts.Preferred(spec.ID, pinned, s.candidates(spec))
	if pinned != "" && pinned != s.opts.ReplicaID {
		return 0, false
	}
	if preferred != s.opts.ReplicaID {
		// Give the preferred owner a lease TTL to claim it before stepping in.
		since, seen := s.waitingSince[spec.ID]
		if !seen {
			s.waitingSince[spec.ID] = now
			return 0, false
		}
		if now.Sub(since) < s.opts.Leaser.TTL() {
			return 0, false
		}
	}
	token, got, err := s.opts.Leaser.Acquire(ctx, spec.ID)
	if err != nil {
		s.logger.Warn("lease acquire failed", zap.String("feed", spec.ID), zap.Error(err))
		return 0, false
	}
	if !got {
		return 0, false
	}
	delete(s.waitingSince, spec.ID)
	return token, true
}

// rebalance hands a running feed over when another live replica has been
// its preferred owner for RebalanceAfter; call with s.mu held. It returns
// true when the feed was removed from the running set.
func (s *Supervisor) rebalance(inst *instance, now time.Time) bool {
	if s.opts.Leaser == nil || inst.token == 0 {
		return false
	}
	preferred := s.opts.Preferred(inst.spec.ID, inst.spec.Common().PinnedReplica, s.candidates(inst.spec))
	if preferred == "" || preferred == s.opts.ReplicaID {
		inst.elsewhereSince = time.Time{}
		return false
	}
	if inst.elsewhereSince.IsZero() {
		inst.elsewhereSince = now
		return false
	}
	if now.Sub(inst.elsewhereSince) < s.opts.RebalanceAfter {
		return false
	}
	delete(s.running, inst.spec.ID)
	s.logger.Info("handing feed over", zap.String("feed", inst.spec.ID), zap.String("to", preferred))
	if s.metrics != nil {
		s.metrics.LeaseHandovers.WithLabelValues(inst.spec.ID).Inc()
	}
	return true
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
	connections := map[string]bool{}
	for _, f := range feeds {
		if f.Enabled {
			desired[f.ID] = f
		}
		if f.Direction(driverDirection(f.Driver)).Outbound() {
			connections[f.ID] = true
		}
	}
	s.mu.Lock()
	s.connections = connections
	s.mu.Unlock()
	now := time.Now()
	if s.opts.Leaser != nil {
		s.renewLeases(ctx)
		live, err := s.opts.Leaser.Live(ctx)
		if err != nil {
			s.logger.Warn("reconcile: listing replicas failed", zap.Error(err))
		} else {
			s.mu.Lock()
			s.live = live
			s.mu.Unlock()
		}
		if ll, ok := s.opts.Leaser.(ListenerLister); ok {
			listeners, dedicated, err := ll.Listeners(ctx)
			if err != nil {
				s.logger.Warn("reconcile: listing listener replicas failed", zap.Error(err))
			} else {
				s.mu.Lock()
				s.listeners, s.dedicated = listeners, dedicated
				s.mu.Unlock()
			}
		}
	}

	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return
	}
	if s.opts.Leaser != nil {
		s.noteUnplaced(desired)
	}
	var toStop []*instance
	var handover []*instance
	for id, inst := range s.running {
		spec, want := desired[id]
		if !want || spec.Version != inst.spec.Version {
			toStop = append(toStop, inst)
			delete(s.running, id)
			continue
		}
		if pinned := spec.Common().PinnedReplica; pinned != "" && pinned != s.opts.ReplicaID {
			toStop = append(toStop, inst)
			delete(s.running, id)
			continue
		}
		if inst.state != registry.StateError && s.rebalance(inst, now) {
			handover = append(handover, inst)
		}
	}
	for id := range s.waitingSince {
		if _, want := desired[id]; !want {
			delete(s.waitingSince, id)
		}
	}
	s.mu.Unlock()
	for _, inst := range toStop {
		s.stopInstance(inst, "disabled or changed", true)
	}
	for _, inst := range handover {
		s.stopInstance(inst, "handed over", true)
	}

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
		token := int64(0)
		if !exists {
			var ok bool
			if token, ok = s.owns(ctx, spec, now); !ok {
				s.mu.Unlock()
				continue
			}
			inst = &instance{spec: spec, state: registry.StateStarting, token: token}
			if s.opts.Leaser != nil {
				// Start the handover clock when we take a feed another live
				// replica should own (it did not claim it within a TTL).
				if p := s.opts.Preferred(spec.ID, spec.Common().PinnedReplica, s.candidates(spec)); p != "" && p != s.opts.ReplicaID {
					inst.elsewhereSince = now
				}
			}
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

// noteUnplaced counts the enabled feeds without a pin that no live replica may
// run, by reason, and sets the FeedsUnplaced gauge from the counts. The log
// says when feeds start to lack a replica and when none do, not on every
// reconcile. These feeds wait until a replica that can run them is live: a
// listener never fails over for an ordinary feed. Call with s.mu held.
func (s *Supervisor) noteUnplaced(desired map[string]registry.Feed) {
	counts := map[string]int{}
	ordinary := len(s.ordinary())
	for _, spec := range desired {
		switch {
		case spec.Common().PinnedReplica != "":
			// A pin runs the feed on that replica or on none.
		case listens(spec):
			if len(s.listeners) == 0 {
				counts[unplacedNoListener]++
			}
		case ordinary == 0:
			counts[unplacedNoOrdinaryReplica]++
		}
	}
	for _, r := range unplacedReasons {
		n, before := counts[r.reason], s.unplaced[r.reason]
		s.unplaced[r.reason] = n
		if s.metrics != nil {
			s.metrics.FeedsUnplaced.WithLabelValues(r.reason).Set(float64(n))
		}
		switch {
		case before == 0 && n > 0:
			s.logger.Warn("feeds have no replica to run on", zap.String("reason", r.reason), zap.Int("feeds", n), zap.String("detail", r.detail))
		case before > 0 && n == 0:
			s.logger.Info("no feed is left without a replica to run on", zap.String("reason", r.reason))
		}
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
	deps := driver.Deps{Logger: logger, Shapes: s.opts.Shapes, ListenerHost: s.opts.ListenerHost}
	if s.opts.Density != nil {
		deps.Density = s.opts.Density(spec.ID, common.Classification)
	}
	drv, err := s.opts.Factory(driver.Spec{FeedID: spec.ID, Driver: spec.Driver, Config: spec.Config, Secrets: secrets, Backpressure: common.Backpressure}, deps)
	if err != nil {
		fail(fmt.Errorf("building driver: %w", err))
		return
	}
	pump, err := s.buildPump(spec, common, drv, logger)
	if err != nil {
		fail(fmt.Errorf("building outbound pump: %w", err))
		return
	}
	runner := NewRunner(spec.ID, spec.Driver, drv, s.pub, s.metrics, logger, RunnerOptions{RawPolicy: common.RawPayload, RawMax: s.opts.RawMax, FencingToken: inst.token, Classification: common.Classification, Pump: pump})

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
	s.logger.Info("feed started", zap.String("feed", spec.ID), zap.String("driver", spec.Driver), zap.Int("version", spec.Version), zap.Int64("lease_token", inst.token))

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

// stopInstance stops a feed. With held true the replica still owns the
// lease: it writes the stopped status (fenced) and releases the lease;
// with held false the lease is already someone else's and nothing is
// written.
func (s *Supervisor) stopInstance(inst *instance, reason string, held bool) {
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
	if !held {
		return
	}
	var stats Stats
	if runner != nil {
		stats = runner.Stats()
	}
	ctx, cancelStatus := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelStatus()
	st := registry.Status{FeedID: inst.spec.ID, Owner: "", State: registry.StateStopped, Health: registry.HealthUnknown, AppliedVersion: inst.spec.Version, ErrorsTotal: inst.errorsTotal + stats.ErrorsTotal, MessagesTotal: stats.Observations, Detail: json.RawMessage("{}"), FencedBy: s.opts.ReplicaID, FencingToken: inst.token}
	if err := s.reg.UpsertStatus(ctx, st); err != nil && !errors.Is(err, registry.ErrFenced) {
		s.logger.Warn("status update failed", zap.String("feed", inst.spec.ID), zap.Error(err))
	}
	if s.opts.Leaser != nil && inst.token > 0 {
		if err := s.opts.Leaser.Release(ctx, inst.spec.ID, inst.token); err != nil {
			s.logger.Warn("lease release failed", zap.String("feed", inst.spec.ID), zap.Error(err))
		}
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
		FencedBy: s.opts.ReplicaID, FencingToken: inst.token,
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
	lastSuccess := stats.LastSuccessAt
	if ob := stats.Outbound; ob != nil {
		st.MessagesTotal += ob.Delivered
		st.ErrorsTotal += ob.DeliveryErrors
		if st.LastError == "" {
			st.LastError = ob.LastError
		}
		if ob.LastDeliveredAt.After(lastSuccess) {
			lastSuccess = ob.LastDeliveredAt
		}
		stats.LastSuccessAt = lastSuccess
	}
	if !lastSuccess.IsZero() {
		t := lastSuccess
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
	if inst.token > 0 {
		detail["leaseToken"] = inst.token
	}
	detail["direction"] = string(inst.spec.Direction(driverDirection(inst.spec.Driver)))
	if ob := stats.Outbound; ob != nil {
		out := map[string]any{
			"delivered": ob.Delivered, "deliveryErrors": ob.DeliveryErrors, "denied": ob.Denied,
			"coalesced": ob.Coalesced, "dropped": ob.Dropped, "queueDepth": ob.QueueDepth, "tracked": ob.Tracked,
			"consumerGroup": outbound.GroupID(inst.spec.ID),
		}
		if !ob.LastDeliveredAt.IsZero() {
			out["lastDeliveredAt"] = ob.LastDeliveredAt.UTC().Format(time.RFC3339)
		}
		detail["outbound"] = out
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
		if err := s.reg.UpsertStatus(ctx, st); err != nil && !errors.Is(err, registry.ErrFenced) {
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
			s.stopInstance(inst, "shutdown", true)
		}(inst)
	}
	wg.Wait()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.reg.ReleaseStatuses(ctx, s.opts.ReplicaID); err != nil {
		s.logger.Warn("releasing statuses failed", zap.Error(err))
	}
	if s.opts.Leaser != nil {
		if err := s.opts.Leaser.Leave(ctx); err != nil {
			s.logger.Warn("leaving the replica set failed", zap.Error(err))
		}
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
