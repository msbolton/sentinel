// Package client is the ew-sensor's gRPC side: it opens a session with the
// ingest service, resolves the emplacement it reports as, and streams the
// simulated sensor's reports, reconnecting with backoff and keeping stats.
//
// Concurrency: one loop goroutine owns the simulation and every
// stream.Send; one goroutine per open stream reads its acks. They share
// the stats, the rate-limit hold, the upsert grace window and the retry
// queue under Client.mu.
package client

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/grpc"

	ingestv1 "github.com/sentinel/proto-gen/sentinel/ingest/v1"

	"github.com/sentinel/ew-sensor/internal/config"
)

// Exit codes (ruling R13).
const (
	// ExitOK is a normal end: --duration elapsed, a signal, or --help.
	ExitOK = 0
	// ExitFatal is a configuration or usage error, or any other fatal error.
	ExitFatal = 1
	// ExitAuth is Unauthenticated, PermissionDenied, or the connection's
	// sender-managed emplacement cap.
	ExitAuth = 2
)

// ExitError is a fatal error with the process exit code it maps to.
type ExitError struct {
	Code int
	Err  error
}

func (e *ExitError) Error() string { return e.Err.Error() }
func (e *ExitError) Unwrap() error { return e.Err }

// ExitCode maps an error from Run or New to the process exit code.
func ExitCode(err error) int {
	if err == nil {
		return ExitOK
	}
	var ee *ExitError
	if errors.As(err, &ee) {
		return ee.Code
	}
	return ExitFatal
}

func fatalf(code int, format string, args ...any) error {
	return &ExitError{Code: code, Err: fmt.Errorf(format, args...)}
}

// Options tune the client. The zero value gives the production behaviour;
// tests shorten the intervals and inject the clock.
type Options struct {
	Logger *slog.Logger
	// DialOptions are added after the credential and keepalive options
	// (tests add an in-memory dialer).
	DialOptions []grpc.DialOption
	// Now is the clock for the rate-limit hold, the upsert grace window,
	// stream health and the simulation start. Default time.Now.
	Now func() time.Time
	// Jitter returns a number in [0,1) for the backoff. Default rand.Float64.
	Jitter func() float64

	BackoffMin    time.Duration // default 500ms
	BackoffMax    time.Duration // default 30s
	HealthyAfter  time.Duration // a stream open this long resets the backoff; default 30s
	StatsEvery    time.Duration // default 10s
	StateEvery    time.Duration // mobile EmplacementState period; default 1s
	RateLimitHold time.Duration // a rate_limited ack halves the rate this long; default 10s
	GraceWindow   time.Duration // unknown_emplacement retry window after an upsert; default 5s
	RetryDelay    time.Duration // delay before that retry; default 250ms
	RPCTimeout    time.Duration // per unary call; default 30s

	// OnAck, when set, is called by the ack reader after each ack's effects
	// (stats, rate limit, retry) are applied. Tests use it to synchronise.
	OnAck func(*ingestv1.Ack)
}

func (o *Options) defaults() {
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Jitter == nil {
		o.Jitter = rand.Float64
	}
	set := func(d *time.Duration, v time.Duration) {
		if *d <= 0 {
			*d = v
		}
	}
	set(&o.BackoffMin, 500*time.Millisecond)
	set(&o.BackoffMax, 30*time.Second)
	set(&o.HealthyAfter, 30*time.Second)
	set(&o.StatsEvery, 10*time.Second)
	set(&o.StateEvery, time.Second)
	set(&o.RateLimitHold, 10*time.Second)
	set(&o.GraceWindow, 5*time.Second)
	set(&o.RetryDelay, 250*time.Millisecond)
	set(&o.RPCTimeout, 30*time.Second)
}

// Stats counts what the client did.
type Stats struct {
	Sent            uint64            // reports written to a stream
	OK              uint64            // ACK_STATUS_OK
	Rejected        map[string]uint64 // ACK_STATUS_REJECTED by reason (retried ones excluded)
	DeadLettered    uint64            // ACK_STATUS_DEAD_LETTERED
	Retried         uint64            // unknown_emplacement re-sent in the upsert grace window
	Reconnects      uint64            // streams re-opened after one ended or failed to open
	DroppedOversize uint64            // larger than the session's max_message_bytes, never sent
	DroppedOffline  uint64            // generated while no stream was open
}

// Attrs returns the stats as slog key/value pairs.
func (s Stats) Attrs() []any {
	return []any{
		"sent", s.Sent, "ok", s.OK, "rejected", s.Rejected, "dead_lettered", s.DeadLettered,
		"retried", s.Retried, "reconnects", s.Reconnects,
		"dropped_oversize", s.DroppedOversize, "dropped_offline", s.DroppedOffline,
	}
}

// maxPending bounds the reports remembered for an unknown_emplacement retry.
const maxPending = 4096

type pendingReport struct {
	gen     uint64
	report  *ingestv1.Report
	retried bool
}

type retryItem struct {
	gen    uint64
	report *ingestv1.Report
	due    time.Time // wall clock (time.Now), for the retry timer
}

// Client streams one simulated sensor to the ingest service.
type Client struct {
	cfg  config.Config
	opts Options
	log  *slog.Logger
	dial []grpc.DialOption

	interval atomic.Int64 // the loop's current tick interval, ns
	wake     chan struct{}

	mu           sync.Mutex
	stats        Stats
	limitedUntil time.Time
	graceUntil   time.Time
	pending      map[string]pendingReport
	retries      []retryItem
	seenReason   map[string]bool
}

// New builds a client for cfg. It fails (exit 1) when the credentials
// cannot be loaded.
func New(cfg config.Config, opts Options) (*Client, error) {
	opts.defaults()
	creds, err := DialOptions(cfg.Connection, opts.Logger)
	if err != nil {
		return nil, &ExitError{Code: ExitFatal, Err: err}
	}
	dial := append(creds, grpc.WithKeepaliveParams(KeepaliveParams()))
	dial = append(dial, opts.DialOptions...)
	return &Client{
		cfg:        cfg,
		opts:       opts,
		log:        opts.Logger,
		dial:       dial,
		wake:       make(chan struct{}, 1),
		stats:      Stats{Rejected: map[string]uint64{}},
		pending:    map[string]pendingReport{},
		seenReason: map[string]bool{},
	}, nil
}

// Run connects and streams until ctx ends (nil) or a fatal error (an
// *ExitError). It returns only once every goroutine it started has ended.
func (c *Client) Run(ctx context.Context) error {
	err := c.run(ctx)
	var ee *ExitError
	if err != nil && ctx.Err() != nil && !errors.As(err, &ee) {
		return nil // the run was stopped; whatever failed failed because of it
	}
	return err
}

func (c *Client) run(ctx context.Context) error {
	addr := c.cfg.Connection.Addr
	conn, err := grpc.NewClient(addr, c.dial...)
	if err != nil {
		return fatalf(ExitFatal, "connecting to %s: %w", addr, err)
	}
	defer conn.Close()
	api := ingestv1.NewIngestServiceClient(conn)
	c.log.Info("connecting", "addr", addr, "tls", c.cfg.Connection.TLS, "token", c.cfg.Connection.RedactedToken())

	sess, err := c.session(ctx, api)
	if sess == nil || err != nil {
		return err
	}
	emp, err := c.emplacement(ctx, api, sess)
	if emp == nil || err != nil {
		return err
	}
	l, err := c.newLoop(api, sess, emp)
	if err != nil {
		return err
	}
	return l.run(ctx)
}

// Rate is the current tick rate in Hz: the configured rate, halved while a
// rate_limited ack's hold lasts.
func (c *Client) Rate() float64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.rateLocked(c.opts.Now())
}

func (c *Client) rateLocked(now time.Time) float64 {
	r := c.cfg.Sensor.Rate
	if !(r > 0) {
		r = 1
	}
	if now.Before(c.limitedUntil) {
		r /= 2
	}
	return r
}

func (c *Client) tickInterval() time.Duration {
	return time.Duration(float64(time.Second) / c.Rate())
}

// TickInterval is the interval the stream loop is ticking at.
func (c *Client) TickInterval() time.Duration { return time.Duration(c.interval.Load()) }

// Stats returns a copy of the counters.
func (c *Client) Stats() Stats {
	c.mu.Lock()
	defer c.mu.Unlock()
	s := c.stats
	s.Rejected = maps.Clone(c.stats.Rejected)
	return s
}

func (c *Client) logStats(msg string) { c.log.Info(msg, c.Stats().Attrs()...) }

func (c *Client) count(f func(*Stats)) {
	c.mu.Lock()
	f(&c.stats)
	c.mu.Unlock()
}

// ---------------------------------------------------------------- backoff

// backoff is jittered exponential backoff: min·2^n capped at max, times a
// jitter factor in [0.8,1.2), clamped to [min,max].
type backoff struct {
	min, max time.Duration
	n        int
	jitter   func() float64
}

func newBackoff(min, max time.Duration, jitter func() float64) *backoff {
	return &backoff{min: min, max: max, jitter: jitter}
}

func (b *backoff) next() time.Duration {
	d := b.min
	for i := 0; i < b.n && d < b.max; i++ {
		d *= 2
	}
	if d > b.max {
		d = b.max
	}
	if b.n < 64 {
		b.n++
	}
	d = time.Duration(float64(d) * (0.8 + 0.4*b.jitter()))
	return min(max(d, b.min), b.max)
}

func (b *backoff) reset() { b.n = 0 }

// sleep waits d or until ctx ends, reporting whether it waited the full d.
func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
