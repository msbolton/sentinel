package client

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strconv"
	"sync/atomic"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	ingestv1 "github.com/sentinel/proto-gen/sentinel/ingest/v1"
	sentinelv1 "github.com/sentinel/proto-gen/sentinel/v1"
)

// Ack reasons the client acts on.
const (
	reasonRateLimited        = "rate_limited"
	reasonUnknownEmplacement = "unknown_emplacement"
	reasonNoHeartbeat        = "no_heartbeat"
)

type reportStream = grpc.BidiStreamingClient[ingestv1.StreamReportsRequest, ingestv1.StreamReportsResponse]

// activeStream is one open StreamReports call. Only the loop goroutine
// touches it, except gotOK, which its ack reader sets.
type activeStream struct {
	gen    uint64
	stream reportStream
	cancel context.CancelFunc
	done   chan error // the ack reader's final error; buffered, sent once
	opened time.Time
	gotOK  atomic.Bool
	broken bool // a Send failed: wait for done, send nothing more
}

// sim is the simulated sensor: each scan is the scenario truth at the wall
// clock (so separate radar and camera processes see the same targets) put
// through the detector.
type sim struct {
	src   Source
	empID string
	hbSec uint32
}

// scan returns the reports for one scan at now. ctx bounds its terrain work.
func (s *sim) scan(ctx context.Context, now time.Time) ([]*ingestv1.Report, error) {
	truth, err := s.src.Truth(ctx, now)
	if err != nil {
		return nil, err
	}
	obs := s.src.Detector.Detect(ctx, now, truth)
	out := make([]*ingestv1.Report, len(obs))
	for i, o := range obs {
		out[i] = &ingestv1.Report{Body: &ingestv1.Report_Observation{Observation: o}}
	}
	return out, nil
}

// state is the emplacement's heartbeat. A fixed site's position is ignored by
// the server; it is sent for the record.
func (s *sim) state() *ingestv1.Report {
	p := s.src.Site
	st := &sentinelv1.EmplacementState{
		EmplacementId:        s.empID,
		Position:             &sentinelv1.Coordinate{Latitude: p.Lat, Longitude: p.Lon, AltitudeMeters: proto.Float64(p.Alt)},
		HeartbeatIntervalSec: s.hbSec,
	}
	return &ingestv1.Report{Body: &ingestv1.Report_EmplacementState{EmplacementState: st}}
}

// loop is the single goroutine that owns the simulation and every Send.
type loop struct {
	c        *Client
	api      ingestv1.IngestServiceClient
	sim      sim
	maxBytes int
	bo       *backoff

	cur  *activeStream
	gen  uint64
	refs uint64
	// scanFailing is true from a skipped scan to the next good one, so a run
	// of skipped scans warns once.
	scanFailing bool

	reconnectT *time.Timer
	reconnectC <-chan time.Time
	retryT     *time.Timer
	retryC     <-chan time.Time
}

func (c *Client) newLoop(api ingestv1.IngestServiceClient, sess *ingestv1.GetSessionResponse, emp *sentinelv1.Emplacement) (*loop, error) {
	s := c.cfg.Sensor
	c.log.Info("sensor", "kind", s.Kind, "emplacement_id", emp.GetId(),
		"lat", c.src.Site.Lat, "lon", c.src.Site.Lon, "alt", c.src.Site.Alt,
		"boresight", s.Boresight, "width", s.Width, "range_km", s.RangeKm, "min_range_km", s.MinRangeKm,
		"targets", len(c.cfg.Scenario.Targets), "rate_hz", s.Rate)
	return &loop{
		c:   c,
		api: api,
		sim: sim{
			src:   c.src,
			empID: emp.GetId(),
			hbSec: s.HeartbeatSeconds(),
		},
		maxBytes: int(sess.GetMaxMessageBytes()),
		bo:       newBackoff(c.opts.BackoffMin, c.opts.BackoffMax, c.opts.Jitter),
	}, nil
}

// scan runs one scan, bounded by the scan timeout, and sends its detections.
// A scan the terrain cannot answer in time is skipped: the download it
// started carries on, and later scans use the tile once it is in.
func (l *loop) scan(ctx context.Context) {
	sctx, cancel := context.WithTimeout(ctx, l.c.opts.ScanTimeout)
	reports, err := l.sim.scan(sctx, l.c.opts.Now())
	cancel()
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		l.c.count(func(s *Stats) { s.ScansSkipped++ })
		if !l.scanFailing {
			l.scanFailing = true
			l.c.log.Warn("scan skipped: the terrain did not answer in time (a tile may still be downloading); counting skipped scans in scans_skipped",
				"error", err, "timeout", l.c.opts.ScanTimeout)
		}
		return
	}
	if l.scanFailing {
		l.scanFailing = false
		l.c.log.Info("scanning again", "scans_skipped", l.c.Stats().ScansSkipped)
	}
	for _, r := range reports {
		l.send(r, false)
	}
}

func (l *loop) run(ctx context.Context) error {
	defer l.shutdown()
	// Open the first stream before ticking, so the first detections are not
	// generated while still connecting.
	if err := l.connect(ctx); err != nil {
		return err
	}
	interval := l.c.tickInterval()
	l.c.interval.Store(int64(interval))
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	stats := time.NewTicker(l.c.opts.StatsEvery)
	defer stats.Stop()
	// The emplacement's heartbeat: an EmplacementState every heartbeat
	// interval, declaring the configured interval.
	st := time.NewTicker(l.c.opts.HeartbeatEvery)
	defer st.Stop()
	stateC := st.C

	for {
		var doneC <-chan error
		if l.cur != nil {
			doneC = l.cur.done
		}
		select {
		case <-ctx.Done():
			return nil

		case <-ticker.C:
			l.scan(ctx)
			if next := l.c.tickInterval(); next != interval {
				interval = next
				ticker.Reset(interval)
				l.c.interval.Store(int64(interval))
				l.c.log.Info("tick rate changed", "rate_hz", l.c.Rate(), "interval", interval)
			}

		case <-stateC:
			l.send(l.sim.state(), false)

		case <-stats.C:
			l.c.logStats("stats")

		case <-l.c.wake:
			if l.c.takeStateKick() {
				l.send(l.sim.state(), false)
			}
			l.armRetry()

		case <-l.retryC:
			l.retryC = nil
			l.sendDueRetries()
			l.armRetry()

		case err := <-doneC:
			a := l.cur
			l.cur = nil
			a.cancel()
			healthy := a.gotOK.Load() || l.c.opts.Now().Sub(a.opened) >= l.c.opts.HealthyAfter
			if f := l.failed(ctx, a.gen, err, healthy); f != nil {
				return f
			}

		case <-l.reconnectC:
			l.reconnectC = nil
			if err := l.connect(ctx); err != nil {
				return err
			}
		}
	}
}

// connect opens a stream; its first report is always the emplacement's
// heartbeat (EmplacementState). A failure to open schedules a reconnect, or is fatal.
func (l *loop) connect(ctx context.Context) error {
	if ctx.Err() != nil {
		return nil
	}
	l.gen++
	sctx, cancel := context.WithCancel(ctx)
	st, err := l.api.StreamReports(sctx)
	if err != nil {
		cancel()
		return l.failed(ctx, l.gen, err, false)
	}
	a := &activeStream{gen: l.gen, stream: st, cancel: cancel, done: make(chan error, 1), opened: l.c.opts.Now()}
	l.cur = a
	go l.c.readAcks(a)
	l.c.log.Info("stream open", "stream", a.gen)
	l.send(l.sim.state(), false)
	return nil
}

// failed handles a stream that ended or could not open: Unauthenticated,
// PermissionDenied and Unimplemented are fatal; anything else (Unavailable,
// transport errors, ResourceExhausted after an oversized report, a clean
// close) is logged and reconnected with backoff. Unacked reports are never
// replayed: detections are perishable.
func (l *loop) failed(ctx context.Context, gen uint64, err error, healthy bool) error {
	if ctx.Err() != nil {
		return nil
	}
	l.c.clearRetries()
	code, msg := codes.OK, "the server closed the stream"
	if !errors.Is(err, io.EOF) {
		st := status.Convert(err)
		code, msg = st.Code(), st.Message()
	}
	switch code {
	case codes.Unauthenticated, codes.PermissionDenied:
		l.c.log.Error("stream refused", "stream", gen, "code", code.String(), "message", msg)
		return fatalf(ExitAuth, "StreamReports refused (%s): %s", code, msg)
	case codes.Unimplemented:
		l.c.log.Error("stream refused", "stream", gen, "code", code.String(), "message", msg)
		return fatalf(ExitFatal, "StreamReports failed (%s): %s; is %s a Sentinel ingest service?", code, msg, l.c.cfg.Connection.Addr)
	}
	if healthy {
		l.bo.reset()
	}
	d := l.bo.next()
	l.c.count(func(s *Stats) { s.Reconnects++ })
	attrs := []any{"stream", gen, "code", code.String(), "message", msg, "backoff", d}
	if code == codes.ResourceExhausted {
		attrs = append(attrs, "note", "a report was too large for the service; it is dropped, not resent")
	}
	l.c.log.Warn("stream ended; reconnecting", attrs...)
	l.reconnectT = time.NewTimer(d)
	l.reconnectC = l.reconnectT.C
	return nil
}

// send stamps r with a fresh client_ref and the emplacement id and writes
// it to the open stream. Nothing is queued: with no usable stream, or when
// r exceeds the session's max_message_bytes, r is dropped and counted.
func (l *loop) send(r *ingestv1.Report, retried bool) {
	a := l.cur
	if a == nil || a.broken {
		l.c.count(func(s *Stats) { s.DroppedOffline++ })
		return
	}
	l.refs++
	r.ClientRef = "sim-" + strconv.FormatUint(l.refs, 10)
	r.EmplacementId = l.sim.empID
	req := &ingestv1.StreamReportsRequest{Report: r}
	if l.maxBytes > 0 {
		if n := proto.Size(req); n > l.maxBytes {
			l.c.count(func(s *Stats) { s.DroppedOversize++ })
			l.c.log.Warn("report dropped: larger than the session's max_message_bytes",
				"client_ref", r.ClientRef, "bytes", n, "max_message_bytes", l.maxBytes)
			return
		}
	}
	l.c.track(a.gen, r, retried)
	if l.c.log.Enabled(context.Background(), slog.LevelDebug) {
		kind := "observation"
		if r.GetEmplacementState() != nil {
			kind = "heartbeat"
		}
		l.c.log.Debug("send", "client_ref", r.ClientRef, "kind", kind,
			"emplacement", r.EmplacementId, "retried", retried,
			"body", protojson.MarshalOptions{}.Format(r))
	}
	if err := a.stream.Send(req); err != nil {
		// The stream's status comes from its ack reader (Send returns io.EOF).
		a.broken = true
		l.c.untrack(r.ClientRef)
		l.c.log.Debug("send failed; waiting for the stream's status", "stream", a.gen, "error", err)
		return
	}
	l.c.count(func(s *Stats) { s.Sent++ })
}

func (l *loop) armRetry() {
	if l.retryC != nil {
		return
	}
	due, ok := l.c.nextRetry()
	if !ok {
		return
	}
	l.retryT = time.NewTimer(max(time.Until(due), 0))
	l.retryC = l.retryT.C
}

func (l *loop) sendDueRetries() {
	for _, it := range l.c.takeDueRetries(time.Now()) {
		if l.cur == nil || it.gen != l.cur.gen {
			continue // never across streams
		}
		r := proto.Clone(it.report).(*ingestv1.Report)
		l.send(r, true)
	}
}

// shutdown ends the open stream and waits for its ack reader.
func (l *loop) shutdown() {
	if l.reconnectT != nil {
		l.reconnectT.Stop()
	}
	if l.retryT != nil {
		l.retryT.Stop()
	}
	if a := l.cur; a != nil {
		l.cur = nil
		a.cancel()
		<-a.done
	}
	l.c.interval.Store(0)
}

// ---------------------------------------------------------------- ack reader

// readAcks is a stream's only reader. It ends when the stream does, and its
// last act is to hand the stream's error to the loop.
func (c *Client) readAcks(a *activeStream) {
	for {
		resp, err := a.stream.Recv()
		if err != nil {
			a.done <- err
			return
		}
		c.handleAck(a, resp.GetAck())
	}
}

func (c *Client) handleAck(a *activeStream, ack *ingestv1.Ack) {
	now := c.opts.Now()
	ref, reason := ack.GetClientRef(), ack.GetReason()
	var warn string
	c.mu.Lock()
	p, tracked := c.pending[ref]
	delete(c.pending, ref)
	switch ack.GetStatus() {
	case ingestv1.AckStatus_ACK_STATUS_OK:
		c.stats.OK++
		a.gotOK.Store(true)
	case ingestv1.AckStatus_ACK_STATUS_REJECTED:
		switch {
		case reason == reasonRateLimited:
			if !now.Before(c.limitedUntil) {
				warn = "rate limited by the service: halving the tick rate"
			}
			c.limitedUntil = now.Add(c.opts.RateLimitHold)
			c.stats.Rejected[reason]++
		case reason == reasonUnknownEmplacement && tracked && !p.retried && p.gen == a.gen && now.Before(c.graceUntil):
			// The service's cache may not have caught up with the upsert yet.
			c.stats.Retried++
			c.retries = append(c.retries, retryItem{gen: a.gen, report: p.report, due: time.Now().Add(c.opts.RetryDelay)})
			select {
			case c.wake <- struct{}{}:
			default:
			}
		case reason == reasonNoHeartbeat:
			// The server holds no heartbeat for the emplacement (its connection
			// moved replica, or a state was lost): heartbeat now, debounced,
			// and send the detection again once, after it.
			c.stats.Rejected[reason]++
			if now.Sub(c.lastKick) >= c.opts.HeartbeatKickMin {
				c.lastKick, c.stateKick = now, true
			}
			if tracked && !p.retried && p.gen == a.gen && p.report.GetObservation() != nil {
				c.stats.Retried++
				c.retries = append(c.retries, retryItem{gen: a.gen, report: p.report, due: time.Now().Add(c.opts.RetryDelay)})
			}
			if !c.seenReason[reason] {
				c.seenReason[reason] = true
				warn = "report rejected: the service has no heartbeat from the emplacement; sending one"
			}
			select {
			case c.wake <- struct{}{}:
			default:
			}
		default:
			c.stats.Rejected[reason]++
			if !c.seenReason[reason] {
				c.seenReason[reason] = true
				warn = "report rejected"
			}
		}
	case ingestv1.AckStatus_ACK_STATUS_DEAD_LETTERED:
		c.stats.DeadLettered++
		warn = "report dead-lettered"
	default:
		c.stats.Rejected["unspecified"]++
	}
	c.mu.Unlock()
	if warn != "" {
		c.log.Warn(warn, "client_ref", ref, "status", ack.GetStatus().String(), "reason", reason)
	}
	c.log.Debug("ack", "client_ref", ref, "status", ack.GetStatus().String(), "reason", reason,
		"emplacement", ack.GetEmplacementId())
	if c.opts.OnAck != nil {
		c.opts.OnAck(ack)
	}
}

// track remembers a sent report until its ack, so that an unknown_emplacement
// in the upsert grace window, or a no_heartbeat at any time, can retry it.
func (c *Client) track(gen uint64, r *ingestv1.Report, retried bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.pending) >= maxPending {
		return
	}
	c.pending[r.ClientRef] = pendingReport{gen: gen, report: r, retried: retried}
}

// takeStateKick reports whether a no_heartbeat ack asked for a heartbeat, and clears the request.
func (c *Client) takeStateKick() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	kick := c.stateKick
	c.stateKick = false
	return kick
}

func (c *Client) untrack(ref string) {
	c.mu.Lock()
	delete(c.pending, ref)
	c.mu.Unlock()
}

// clearRetries forgets every unacked report: nothing is replayed on a new
// stream.
func (c *Client) clearRetries() {
	c.mu.Lock()
	clear(c.pending)
	c.retries = nil
	c.mu.Unlock()
}

func (c *Client) nextRetry() (time.Time, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.retries) == 0 {
		return time.Time{}, false
	}
	return c.retries[0].due, true
}

func (c *Client) takeDueRetries(now time.Time) []retryItem {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for n < len(c.retries) && !c.retries[n].due.After(now) {
		n++
	}
	due := c.retries[:n:n]
	c.retries = append([]retryItem(nil), c.retries[n:]...)
	return due
}
