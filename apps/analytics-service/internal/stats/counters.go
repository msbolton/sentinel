// Package stats keeps the service's operational counters, exposed both as
// Prometheus metrics (through the shared health handler's /metrics) and as
// the JSON the /api/v1/analytics/stats endpoint returns.
package stats

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Counters is safe for concurrent use.
type Counters struct {
	startedAt time.Time

	positions      atomic.Int64
	coverageWrites atomic.Int64
	findingsOpened atomic.Int64
	findingsHits   atomic.Int64
	alerts         atomic.Int64

	mu                sync.Mutex
	lastBaselineRun   time.Time
	lastBaselineTook  time.Duration
	baselinesComputed int64
	baselinesSkipped  int64
}

var (
	positionsTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "analytics_positions_total", Help: "Position events processed.",
	})
	coverageWritesTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "analytics_coverage_writes_total", Help: "Coverage cell upserts.",
	})
	findingsOpenedTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "analytics_findings_opened_total", Help: "Findings opened.",
	})
	findingsHitsTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "analytics_findings_hits_total", Help: "Detector hits, including those absorbed by an open finding.",
	})
	alertsTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "analytics_alerts_published_total", Help: "Findings published as alerts.",
	})
	baselinesComputedTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "analytics_baselines_computed_total", Help: "Baselines recomputed by the hourly job.",
	})
	baselineRunSeconds = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "analytics_baseline_run_seconds", Help: "Duration of the last baseline run.",
	})
)

// New returns zeroed counters.
func New() *Counters {
	return &Counters{startedAt: time.Now()}
}

// Positions records n processed position events.
func (c *Counters) Positions(n int) {
	c.positions.Add(int64(n))
	positionsTotal.Add(float64(n))
}

// CoverageWrites records n coverage cell upserts.
func (c *Counters) CoverageWrites(n int) {
	c.coverageWrites.Add(int64(n))
	coverageWritesTotal.Add(float64(n))
}

// FindingOpened records a new finding.
func (c *Counters) FindingOpened() {
	c.findingsOpened.Add(1)
	findingsOpenedTotal.Inc()
}

// FindingHit records a detector hit.
func (c *Counters) FindingHit() {
	c.findingsHits.Add(1)
	findingsHitsTotal.Inc()
}

// AlertPublished records a finding published as an alert.
func (c *Counters) AlertPublished() {
	c.alerts.Add(1)
	alertsTotal.Inc()
}

// BaselineRun records the outcome of one baseline job run.
func (c *Counters) BaselineRun(at time.Time, took time.Duration, computed, skipped int) {
	c.mu.Lock()
	c.lastBaselineRun, c.lastBaselineTook = at, took
	c.baselinesComputed += int64(computed)
	c.baselinesSkipped += int64(skipped)
	c.mu.Unlock()
	baselinesComputedTotal.Add(float64(computed))
	baselineRunSeconds.Set(took.Seconds())
}

// Snapshot is the JSON shape of the stats endpoint.
func (c *Counters) Snapshot() map[string]any {
	c.mu.Lock()
	lastRun, took, computed, skipped := c.lastBaselineRun, c.lastBaselineTook, c.baselinesComputed, c.baselinesSkipped
	c.mu.Unlock()
	out := map[string]any{
		"uptimeSec":         int64(time.Since(c.startedAt).Seconds()),
		"positions":         c.positions.Load(),
		"coverageWrites":    c.coverageWrites.Load(),
		"findingsOpened":    c.findingsOpened.Load(),
		"findingsHits":      c.findingsHits.Load(),
		"alertsPublished":   c.alerts.Load(),
		"baselinesComputed": computed,
		"baselinesSkipped":  skipped,
	}
	if !lastRun.IsZero() {
		out["lastBaselineRunAt"] = lastRun.UTC().Format(time.RFC3339)
		out["lastBaselineRunSec"] = took.Seconds()
	}
	return out
}
