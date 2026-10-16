// Package feed runs feeds on this replica: a Runner wraps one driver with
// the sink that stamps provenance, suppresses duplicates and publishes;
// the Supervisor reconciles the registry's desired state against the
// runners it owns and reports status back.
package feed

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/sentinel/ingest-service/internal/dedupe"
	"github.com/sentinel/ingest-service/internal/metrics"
	"github.com/sentinel/ingest-service/internal/obs"
	"github.com/sentinel/ingest-service/internal/source/driver"
)

// Publisher is what the runner hands observations to.
type Publisher interface {
	Emit(ctx context.Context, o *obs.Observation) error
	DeadLetter(feedID, driverID, decoder string, raw []byte, reason string, cause error)
}

// Raw payload retention policies (config key rawPayload).
const (
	RawNone    = "none"
	RawBounded = "bounded"
	RawFull    = "full"
)

// RunnerOptions tune one runner.
type RunnerOptions struct {
	RawPolicy string // none|bounded|full (bounded)
	RawMax    int    // bytes kept under bounded (4096)
	Dedupe    *dedupe.Options
	// FencingToken is the lease token stamped on every observation; 0
	// when the feed runs without a lease.
	FencingToken int64
	// Classification is the feed's default level (bare Classification
	// name). Observations below it are raised to it; "" means UNCLASSIFIED.
	Classification string
}

// Stats is what a runner has counted so far.
type Stats struct {
	FramesTotal   int64
	Observations  int64
	ErrorsTotal   int64
	LastSuccessAt time.Time
	EntitiesLast  int
	LastError     string
}

// Runner runs one driver and is its sink.
type Runner struct {
	feedID   string
	driverID string
	drv      driver.Driver
	pub      Publisher
	metrics  *metrics.Metrics
	logger   *zap.Logger
	filter   *dedupe.Filter
	opts     RunnerOptions

	mu    sync.Mutex
	stats Stats
}

// NewRunner wires a driver to the publisher.
func NewRunner(feedID, driverID string, drv driver.Driver, pub Publisher, m *metrics.Metrics, logger *zap.Logger, opts RunnerOptions) *Runner {
	if logger == nil {
		logger = zap.NewNop()
	}
	if opts.RawPolicy == "" {
		opts.RawPolicy = RawBounded
	}
	if opts.RawMax <= 0 {
		opts.RawMax = 4096
	}
	var dopts dedupe.Options
	if opts.Dedupe != nil {
		dopts = *opts.Dedupe
	}
	return &Runner{feedID: feedID, driverID: driverID, drv: drv, pub: pub, metrics: m, logger: logger, filter: dedupe.New(dopts), opts: opts}
}

// Run runs the driver until ctx ends. A panic inside the driver is
// recovered and returned as an error so the supervisor can restart it.
func (r *Runner) Run(ctx context.Context) (err error) {
	defer func() {
		if rec := recover(); rec != nil {
			err = fmt.Errorf("driver panic: %v", rec)
			r.logger.Error("driver panic recovered", zap.String("feed", r.feedID), zap.Any("panic", rec))
		}
	}()
	return r.drv.Run(ctx, r)
}

// Driver returns the wrapped driver (for area pushes and health).
func (r *Runner) Driver() driver.Driver { return r.drv }

// Stats returns a copy of the counters.
func (r *Runner) Stats() Stats {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.stats
}

// Emit stamps provenance, drops repeated fixes and publishes.
func (r *Runner) Emit(ctx context.Context, o *obs.Observation) error {
	r.mu.Lock()
	r.stats.FramesTotal++
	r.mu.Unlock()
	if r.metrics != nil {
		r.metrics.FramesTotal.WithLabelValues(r.feedID, r.driverID).Inc()
	}
	o.FeedID = r.feedID
	o.Driver = r.driverID
	o.FencingToken = r.opts.FencingToken
	if o.ReceivedAt.IsZero() {
		o.ReceivedAt = time.Now().UTC()
	}
	if o.EntityType == "" {
		o.EntityType = obs.EntityUnknown
	}
	if o.TrackEnvironment == "" {
		o.TrackEnvironment = obs.EnvUnknown
	}
	if obs.ClassificationRank(o.Classification) < obs.ClassificationRank(r.opts.Classification) {
		o.Classification = r.opts.Classification
	}
	if o.Classification == "" {
		o.Classification = obs.ClassUnclassified
	}
	o.EnsureIdentity()
	r.boundRaw(o)
	if !r.filter.Allow(o, time.Now()) {
		if r.metrics != nil {
			r.metrics.DuplicatesTotal.WithLabelValues(r.feedID).Inc()
		}
		return nil
	}
	o.ID = o.ComputeID()
	if err := r.pub.Emit(ctx, o); err != nil {
		if !errors.Is(err, context.Canceled) {
			r.logger.Warn("observation not published", zap.String("feed", r.feedID), zap.Error(err))
		}
		return err
	}
	r.mu.Lock()
	r.stats.Observations++
	r.mu.Unlock()
	if r.metrics != nil {
		r.metrics.ObservationsTotal.WithLabelValues(r.feedID, r.driverID).Inc()
	}
	return nil
}

func (r *Runner) boundRaw(o *obs.Observation) {
	switch r.opts.RawPolicy {
	case RawNone:
		o.Raw, o.RawTruncated, o.RawContentType = nil, false, ""
	case RawFull:
		if len(o.Raw) > 0 {
			o.Raw = append([]byte(nil), o.Raw...)
		}
	default:
		if len(o.Raw) > r.opts.RawMax {
			o.Raw = append([]byte(nil), o.Raw[:r.opts.RawMax]...)
			o.RawTruncated = true
		} else if len(o.Raw) > 0 {
			o.Raw = append([]byte(nil), o.Raw...)
		}
	}
	if len(o.Raw) == 0 {
		o.RawContentType = ""
	}
}

// DeadLetter forwards an undecodable payload.
func (r *Runner) DeadLetter(raw []byte, reason string, cause error) {
	r.mu.Lock()
	r.stats.ErrorsTotal++
	if cause != nil {
		r.stats.LastError = cause.Error()
	}
	r.mu.Unlock()
	if r.metrics != nil {
		r.metrics.DecodeErrorsTotal.WithLabelValues(r.feedID, r.driverID, reason).Inc()
	}
	r.logger.Debug("frame dead-lettered", zap.String("feed", r.feedID), zap.String("reason", reason), zap.Error(cause))
	r.pub.DeadLetter(r.feedID, r.driverID, "", raw, reason, cause)
}

// Skipped counts a frame that produced nothing.
func (r *Runner) Skipped(reason string) {
	if r.metrics == nil {
		return
	}
	switch reason {
	case "dropped_backpressure":
		r.metrics.FramesDroppedTotal.WithLabelValues(r.feedID, "backpressure").Inc()
	case "nacked_backpressure":
		r.metrics.NackedTotal.WithLabelValues(r.feedID).Inc()
	default:
		r.metrics.SkippedTotal.WithLabelValues(r.feedID, r.driverID, reason).Inc()
	}
}

// Succeeded records a completed poll or decode round.
func (r *Runner) Succeeded(count int) {
	now := time.Now()
	r.mu.Lock()
	r.stats.LastSuccessAt = now
	r.stats.EntitiesLast = count
	r.mu.Unlock()
	if r.metrics != nil {
		r.metrics.FeedLastSuccess.WithLabelValues(r.feedID).Set(float64(now.Unix()))
		r.metrics.FeedEntityCount.WithLabelValues(r.feedID).Set(float64(count))
	}
}

// Blocked accounts backpressure wait time.
func (r *Runner) Blocked(d time.Duration) {
	if r.metrics != nil {
		r.metrics.BlockedSeconds.WithLabelValues(r.feedID).Add(d.Seconds())
	}
}

// Failed records a driver-level error (start failure, fatal run error).
func (r *Runner) Failed(err error) {
	r.mu.Lock()
	r.stats.ErrorsTotal++
	r.stats.LastError = err.Error()
	r.mu.Unlock()
	if r.metrics != nil {
		r.metrics.FeedErrorsTotal.WithLabelValues(r.feedID).Inc()
	}
}
