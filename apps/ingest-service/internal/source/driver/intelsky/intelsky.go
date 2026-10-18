// Package intelsky polls IntelSky (intelsky.org), a public open-source
// intelligence site for military and government aircraft, for every such
// aircraft it currently sees worldwide.
//
// The live feed is the readsb aircraft.json document with "now" in
// milliseconds and dbFlags a flag emoji naming the operator's country. It
// has no area filter and IntelSky allows 60 requests a minute per IP, so
// the driver polls the whole feed every 10 s whether or not anyone is
// looking: continuous tracks feed the behavioural analysis and alerts.
// Failures back off from 1 s to 60 s (or Retry-After) but never poll
// faster than the interval.
package intelsky

import (
	"context"
	_ "embed"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/sentinel/ingest-service/internal/source/decoder/readsb"
	"github.com/sentinel/ingest-service/internal/source/driver"
	"github.com/sentinel/ingest-service/internal/source/httppoll"
)

// ID is the driver id.
const ID = "intelsky"

const (
	defaultURL      = "https://intelsky.org/api/"
	defaultInterval = 10 * time.Second
	minInterval     = 8 * time.Second
	maxBackoff      = time.Minute
	maxRetryAfter   = 10 * time.Minute
	userAgent       = "Sentinel ingest (intelsky driver)"
)

//go:embed schema.json
var schema []byte

func init() {
	driver.Register(driver.Definition{
		ID: ID, Name: "IntelSky military aircraft", Source: "INTELSKY",
		Description:  "Military and government aircraft worldwide from IntelSky's live feed (intelsky.org). No API key.",
		Capabilities: []string{driver.CapabilityPoll},
		ConfigSchema: schema,
		New:          New,
	})
}

// Config is the feed configuration.
type Config struct {
	IntervalSec int    `json:"intervalSec"`
	URL         string `json:"url"`
}

// Driver is one IntelSky feed.
type Driver struct {
	logger   *zap.Logger
	url      string
	interval time.Duration
	client   *httppoll.Client
	now      func() time.Time

	mu        sync.Mutex
	aircraft  int
	polls     int64
	errors    int64
	failures  int // consecutive
	status    int
	lastErr   string
	fetchedAt time.Time
	nextAt    time.Time
}

// New validates the config and builds the driver.
func New(spec driver.Spec, deps driver.Deps) (driver.Driver, error) {
	var cfg Config
	if err := driver.DecodeConfig(spec, &cfg); err != nil {
		return nil, err
	}
	interval := driver.Seconds(cfg.IntervalSec, defaultInterval)
	if interval < minInterval {
		interval = minInterval
	}
	logger := deps.Logger
	if logger == nil {
		logger = zap.NewNop()
	}
	client := httppoll.NewClient(20*time.Second, httppoll.Auth{})
	client.UserAgent = userAgent
	d := &Driver{logger: logger, url: defaultURL, interval: interval, client: client, now: time.Now}
	if cfg.URL != "" {
		d.url = cfg.URL
	}
	return d, nil
}

// Run polls until ctx ends.
func (d *Driver) Run(ctx context.Context, sink driver.Sink) error {
	d.logger.Info("intelsky started", zap.String("url", d.url), zap.Duration("interval", d.interval))
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
			wait := d.poll(ctx, sink)
			if ctx.Err() != nil {
				return nil
			}
			d.mu.Lock()
			d.nextAt = d.now().Add(wait)
			d.mu.Unlock()
			timer.Reset(wait)
		}
	}
}

// poll fetches the feed once and returns how long to wait before the next.
func (d *Driver) poll(ctx context.Context, sink driver.Sink) time.Duration {
	resp, err := d.client.Get(ctx, d.url)
	if err != nil {
		return d.fail(0, err.Error(), 0)
	}
	if resp.Status != http.StatusOK {
		body := strings.TrimSpace(string(resp.Body))
		if len(body) > 120 {
			body = body[:120]
		}
		return d.fail(resp.Status, fmt.Sprintf("intelsky returned %d: %s", resp.Status, body), parseRetryAfter(resp.Header.Get("Retry-After"), d.now()))
	}
	out, err := readsb.Decode(resp.Body, d.now().UTC(), readsb.Options{Decoder: ID, Source: "INTELSKY"})
	if err != nil {
		sink.DeadLetter(resp.Body, "bad_response", err)
		return d.fail(resp.Status, err.Error(), 0)
	}
	for _, o := range out {
		if err := sink.Emit(ctx, o); err != nil {
			return d.interval
		}
	}
	sink.Succeeded(len(out))
	d.mu.Lock()
	d.aircraft, d.status, d.lastErr, d.failures = len(out), resp.Status, "", 0
	d.fetchedAt = d.now()
	d.polls++
	d.mu.Unlock()
	return d.interval
}

// fail records a failed poll and picks the wait: Retry-After when given,
// else backoff doubling from 1 s to 60 s, never shorter than the interval.
func (d *Driver) fail(status int, msg string, retryAfter time.Duration) time.Duration {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.polls++
	d.errors++
	d.failures++
	d.status, d.lastErr = status, msg
	wait := retryAfter
	if wait == 0 {
		wait = time.Second << min(d.failures-1, 6)
		if wait > maxBackoff {
			wait = maxBackoff
		}
	}
	if wait < d.interval {
		wait = d.interval
	}
	d.logger.Warn("intelsky poll failed", zap.Int("status", status), zap.String("error", msg), zap.Duration("retry_in", wait))
	return wait
}

// Health reports the last poll.
func (d *Driver) Health() driver.Health {
	d.mu.Lock()
	defer d.mu.Unlock()
	detail := map[string]any{
		"aircraft": d.aircraft,
		"polls":    d.polls,
		"errors":   d.errors,
	}
	if d.status != 0 {
		detail["lastStatus"] = d.status
	}
	if !d.fetchedAt.IsZero() {
		detail["fetchedAt"] = d.fetchedAt.UTC().Format(time.RFC3339)
	}
	if !d.nextAt.IsZero() {
		detail["nextFetchAt"] = d.nextAt.UTC().Format(time.RFC3339)
	}
	if d.failures > 0 {
		detail["consecutiveFailures"] = d.failures
	}
	return driver.Health{Connected: d.failures == 0 && !d.fetchedAt.IsZero(), LastError: d.lastErr, Detail: detail}
}

func parseRetryAfter(v string, now time.Time) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	var wait time.Duration
	if secs, err := strconv.Atoi(v); err == nil {
		wait = time.Duration(secs) * time.Second
	} else if t, err := http.ParseTime(v); err == nil {
		wait = t.Sub(now)
	}
	if wait <= 0 {
		return 0
	}
	return min(wait, maxRetryAfter)
}
