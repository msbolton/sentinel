package demo

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	ingestv1 "github.com/sentinel/proto-gen/sentinel/ingest/v1"
	sentinelv1 "github.com/sentinel/proto-gen/sentinel/v1"
)

// visitorNamePrefix starts every visitor emplacement's name: guest-<guestID>.
const visitorNamePrefix = "guest-"

const visitorDescription = "A visitor exploring the demo"

// Visitor defaults and fixed tunings.
const (
	defaultVisitorHeartbeat   = 10 * time.Second
	defaultVisitorIntervalSec = 10
	defaultVisitorRemoveAfter = 5 * time.Minute
	defaultVisitorRemoveRetry = 30 * time.Second
	defaultVisitorRPCTimeout  = 10 * time.Second
	defaultVisitorBackoffMin  = time.Second
	defaultVisitorBackoffMax  = 30 * time.Second
	visitorHealthyAfter       = 30 * time.Second // a stream open this long resets the backoff
	visitorUpsertGrace        = 5 * time.Second  // unknown_emplacement right after an upsert is retried
	visitorAckRetryDelay      = 250 * time.Millisecond
	visitorCallRetryMin       = 250 * time.Millisecond
	visitorCallRetryMax       = 2 * time.Second
	visitorWarnEvery          = time.Minute // a refusal reason warns at most this often
	visitorRemovalQueue       = 64
	ackUnknownEmplacement     = "unknown_emplacement"
)

var errVisitorsNotRunning = errors.New("demo: visitor sites are not running")

// VisitorOptions configures Visitors. Addr and Token are required; the rest
// default to production values (tests shorten the durations).
type VisitorOptions struct {
	// Addr is the ingest service's grpc listener for the "Demo visitors"
	// connection, host:port.
	Addr string
	// Token is that connection's ingest token (si_…). It is sent on every
	// call as authorization metadata and never logged.
	Token string
	// Insecure uses plaintext h2c (in-cluster); otherwise TLS with the
	// system roots.
	Insecure bool
	// Heartbeat is how often each online guest's site heartbeats. Default 10s.
	Heartbeat time.Duration
	// HeartbeatIntervalSec is the interval each heartbeat declares to the
	// service, which marks the site offline after three of them pass without
	// one. Default 10; independent of Heartbeat so a test can tick faster
	// than the interval it declares.
	HeartbeatIntervalSec uint32
	// RemoveAfter is how long a guest's site outlives the guest's last
	// socket. Default 5m.
	RemoveAfter time.Duration
	// Now is the clock for removal deadlines. Default time.Now.
	Now func() time.Time

	// RPCTimeout bounds each unary call, retries included, and the opening
	// of the report stream. Default 10s.
	RPCTimeout time.Duration
	// RemoveRetry is the wait before a failed removal is tried again.
	// Default 30s.
	RemoveRetry time.Duration
	// BackoffMin and BackoffMax bound the report stream's reconnect
	// backoff. Defaults 1s and 30s.
	BackoffMin, BackoffMax time.Duration
	// DialOptions are added after the credential and keepalive options.
	DialOptions []grpc.DialOption
}

func (o *VisitorOptions) defaults() {
	set := func(d *time.Duration, v time.Duration) {
		if *d <= 0 {
			*d = v
		}
	}
	set(&o.Heartbeat, defaultVisitorHeartbeat)
	set(&o.RemoveAfter, defaultVisitorRemoveAfter)
	set(&o.RPCTimeout, defaultVisitorRPCTimeout)
	set(&o.RemoveRetry, defaultVisitorRemoveRetry)
	set(&o.BackoffMin, defaultVisitorBackoffMin)
	set(&o.BackoffMax, defaultVisitorBackoffMax)
	if o.BackoffMax < o.BackoffMin {
		o.BackoffMax = o.BackoffMin
	}
	if o.HeartbeatIntervalSec == 0 {
		o.HeartbeatIntervalSec = defaultVisitorIntervalSec
	}
	if o.Now == nil {
		o.Now = time.Now
	}
}

// siteState is what is known of a guest's emplacement in the ingest service.
type siteState int

const (
	siteNone     siteState = iota // none, as far as this process knows
	sitePlaced                    // upserted; empID is its id
	siteUnknown                   // an upsert failed, so it may have landed: found by name
	siteRemoving                  // a Remove failed and is retried; no heartbeats
)

// visitor is one guest's site and presence. Its fields are guarded by
// Visitors.mu; lock serialises the guest's Place, Remove and scheduled
// removal (a buffered channel, so waiting honours a context and waiters go
// in turn).
type visitor struct {
	online   bool
	site     siteState
	empID    string
	placedAt time.Time // Now() at the last successful Place
	kick     bool      // heartbeat at the next wake
	retried  bool      // the unknown_emplacement retry has been used since Place
	retryAt  time.Time // wall clock; zero: no retry due
	// removeAt is when the site is due to be removed (Now() clock); zero
	// when it is not. removeGen changes whenever removeAt does, and a
	// removal handed to the remover goes ahead only if it still matches;
	// dispatched is the removeGen last handed over, so it is handed once.
	removeAt   time.Time
	removeGen  uint64
	dispatched uint64
	ops        int // calls holding or waiting for lock; the entry stays while > 0
	lock       chan struct{}
}

func (e *visitor) schedule(at time.Time) {
	e.removeAt = at
	e.removeGen++
}

type removal struct {
	guestID string
	gen     uint64
}

// Visitors keeps each demo guest's emplacement, guest-<guestID>, on the
// "Demo visitors" connection of the ingest service, as that connection's
// grpc sender. A site heartbeats while its guest has a socket open; the
// service marks it offline after three missed intervals, and RemoveAfter
// later it is removed. It is safe for concurrent use; Online and Offline
// never block.
//
// Goroutines: one loop owns the report stream and every Send (heartbeats,
// reconnects, and handing due removals to the remover); one reader per open
// stream reads its acks; one remover carries out scheduled removals. Stop
// ends them all.
type Visitors struct {
	opts   VisitorOptions
	logger *zap.Logger

	wake     chan struct{}
	removals chan removal
	wg       sync.WaitGroup

	mu       sync.Mutex
	started  bool
	stopped  bool
	api      ingestv1.IngestServiceClient // nil unless running
	conn     *grpc.ClientConn
	cancel   context.CancelFunc
	visitors map[string]*visitor
	warned   map[string]time.Time
}

// NewVisitors creates the client; Start connects it.
func NewVisitors(opts VisitorOptions, logger *zap.Logger) *Visitors {
	opts.defaults()
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Visitors{
		opts:     opts,
		logger:   logger,
		wake:     make(chan struct{}, 1),
		removals: make(chan removal, visitorRemovalQueue),
		visitors: map[string]*visitor{},
		warned:   map[string]time.Time{},
	}
}

// Start dials the ingest service (lazily: an unreachable service is retried,
// not an error) and starts the report stream. It fails on a missing address
// or token, an address grpc cannot parse, or a second Start. The goroutines
// run until ctx ends or Stop.
func (v *Visitors) Start(ctx context.Context) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.started || v.stopped {
		return errors.New("demo: visitor sites already started")
	}
	addr := strings.TrimSpace(v.opts.Addr)
	if addr == "" {
		return errors.New("demo: visitor sites need the ingest service's grpc address")
	}
	if v.opts.Token == "" {
		return errors.New("demo: visitor sites need an ingest token")
	}
	conn, err := grpc.NewClient(addr, v.dialOptions()...)
	if err != nil {
		return fmt.Errorf("demo: connecting to %s: %w", addr, err)
	}
	runCtx, cancel := context.WithCancel(ctx)
	api := ingestv1.NewIngestServiceClient(conn)
	v.started, v.api, v.conn, v.cancel = true, api, conn, cancel
	v.wg.Add(2)
	go func() {
		defer v.wg.Done()
		v.run(runCtx, api)
	}()
	go func() {
		defer v.wg.Done()
		v.remover(runCtx, api)
	}()
	v.logger.Info("demo visitor sites connecting to ingest", zap.String("addr", addr), zap.Bool("tls", !v.opts.Insecure))
	return nil
}

// Stop ends every goroutine and closes the connection. Sites are left in
// place: Reconcile removes them at the next start. It is idempotent.
func (v *Visitors) Stop() {
	v.mu.Lock()
	if !v.started || v.stopped {
		v.stopped = true
		v.mu.Unlock()
		return
	}
	v.stopped = true
	v.api = nil
	cancel, conn := v.cancel, v.conn
	v.mu.Unlock()
	cancel()
	v.wg.Wait()
	_ = conn.Close()
}

func (v *Visitors) client() ingestv1.IngestServiceClient {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.api
}

func (v *Visitors) dialOptions() []grpc.DialOption {
	var opts []grpc.DialOption
	if v.opts.Insecure {
		opts = append(opts,
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithPerRPCCredentials(bearerToken{token: v.opts.Token}))
	} else {
		opts = append(opts,
			grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS12})),
			grpc.WithPerRPCCredentials(bearerToken{token: v.opts.Token, requireTLS: true}))
	}
	// Time stays at or above the service's enforcement MinTime (10s), or it
	// answers with GOAWAY too_many_pings.
	opts = append(opts, grpc.WithKeepaliveParams(keepalive.ClientParameters{
		Time: 20 * time.Second, Timeout: 10 * time.Second, PermitWithoutStream: true,
	}))
	return append(opts, v.opts.DialOptions...)
}

// bearerToken sends the ingest token on every call as
// `authorization: Bearer <token>`.
type bearerToken struct {
	token      string
	requireTLS bool
}

func (b bearerToken) GetRequestMetadata(context.Context, ...string) (map[string]string, error) {
	return map[string]string{"authorization": "Bearer " + b.token}, nil
}

func (b bearerToken) RequireTransportSecurity() bool { return b.requireTLS }

// String keeps the token out of anything that prints the credentials.
func (b bearerToken) String() string { return "bearerToken(redacted)" }

func visitorName(guestID string) string { return visitorNamePrefix + guestID }

// ---------------------------------------------------------------- per-guest state

// entryLocked returns guestID's entry, creating it. v.mu is held.
func (v *Visitors) entryLocked(guestID string) *visitor {
	e, ok := v.visitors[guestID]
	if !ok {
		e = &visitor{lock: make(chan struct{}, 1)}
		v.visitors[guestID] = e
	}
	return e
}

// gcLocked forgets an entry with nothing left to track. v.mu is held.
func (v *Visitors) gcLocked(guestID string, e *visitor) {
	if e.site == siteNone && !e.online && e.ops == 0 && v.visitors[guestID] == e {
		delete(v.visitors, guestID)
	}
}

// lockGuest takes guestID's operation lock, so that the guest's Place,
// Remove and scheduled removal reach the service one at a time, in turn.
func (v *Visitors) lockGuest(ctx context.Context, guestID string) (*visitor, func(), error) {
	v.mu.Lock()
	e := v.entryLocked(guestID)
	e.ops++
	v.mu.Unlock()
	release := func() {
		v.mu.Lock()
		e.ops--
		v.gcLocked(guestID, e)
		v.mu.Unlock()
	}
	select {
	case e.lock <- struct{}{}:
	case <-ctx.Done():
		release()
		return nil, nil, ctx.Err()
	}
	return e, func() {
		<-e.lock
		release()
	}, nil
}

func (v *Visitors) signal() {
	select {
	case v.wake <- struct{}{}:
	default:
	}
}

// Online is the guest's first socket opening. A placed site heartbeats at
// once and from then on every Heartbeat, and a pending removal is cancelled.
// A guest with no site yet starts heartbeating when Place succeeds. It never
// blocks.
func (v *Visitors) Online(guestID string) {
	if guestID == "" {
		return
	}
	v.mu.Lock()
	e := v.entryLocked(guestID)
	e.online = true
	if (e.site == sitePlaced || e.site == siteUnknown) && !e.removeAt.IsZero() {
		e.schedule(time.Time{})
	}
	kick := e.site == sitePlaced
	e.kick = e.kick || kick
	v.mu.Unlock()
	if kick {
		v.signal()
	}
}

// Offline is the guest's last socket closing. Heartbeats stop, so the
// service marks the site offline after three missed intervals, and the site
// is removed RemoveAfter from now unless the guest comes back. It never
// blocks.
func (v *Visitors) Offline(guestID string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	e, ok := v.visitors[guestID]
	if !ok {
		return
	}
	e.online, e.kick, e.retryAt = false, false, time.Time{}
	if e.site == sitePlaced || e.site == siteUnknown {
		e.schedule(v.opts.Now().Add(v.opts.RemoveAfter))
	}
	v.gcLocked(guestID, e)
}

// ---------------------------------------------------------------- Place, Remove, Reconcile

// Place upserts the guest's site, guest-<guestID>, FIXED and HUMAN, at
// lat/lon, which the caller has already blurred, and returns its id. The
// upsert is idempotent by name, so a second Place moves the same site. An
// online guest's site heartbeats at once; one placed while its guest has no
// socket open is removed RemoveAfter later unless the guest comes online.
func (v *Visitors) Place(ctx context.Context, guestID string, lat, lon float64) (string, error) {
	if guestID == "" {
		return "", errors.New("demo: no guest id to place")
	}
	api := v.client()
	if api == nil {
		return "", errVisitorsNotRunning
	}
	e, unlock, err := v.lockGuest(ctx, guestID)
	if err != nil {
		return "", fmt.Errorf("placing the visitor's site: %w", err)
	}
	defer unlock()

	req := &ingestv1.UpsertEmplacementRequest{Emplacement: &sentinelv1.Emplacement{
		Name:        visitorName(guestID),
		Description: visitorDescription,
		Mobility:    sentinelv1.Mobility_MOBILITY_FIXED,
		SensorType:  sentinelv1.SensorType_SENSOR_TYPE_HUMAN,
		Position:    &sentinelv1.Coordinate{Latitude: lat, Longitude: lon},
	}}
	var resp *ingestv1.UpsertEmplacementResponse
	err = v.call(ctx, func(ctx context.Context) error {
		var err error
		resp, err = api.UpsertEmplacement(ctx, req)
		return err
	})
	id := resp.GetEmplacement().GetId()
	if err == nil && id == "" {
		err = errors.New("the ingest service returned no emplacement id")
	}

	v.mu.Lock()
	now := v.opts.Now()
	if err != nil {
		// It may have landed: remember to look for it by name.
		if e.site == siteNone {
			e.site = siteUnknown
		}
		if !e.online && e.site != siteRemoving && e.removeAt.IsZero() {
			e.schedule(now.Add(v.opts.RemoveAfter))
		}
		v.mu.Unlock()
		return "", fmt.Errorf("placing the visitor's site: %w", err)
	}
	e.site, e.empID, e.placedAt = sitePlaced, id, now
	e.retried, e.retryAt = false, time.Time{}
	online := e.online
	if online {
		e.kick = true
		e.schedule(time.Time{})
	} else {
		e.schedule(now.Add(v.opts.RemoveAfter))
	}
	v.mu.Unlock()
	if online {
		v.signal()
	}
	return id, nil
}

// Remove takes the guest's site down: its heartbeats stop at once, and only
// a later Place brings it back. It is idempotent: a guest with no site, or a
// site the service no longer has, is done. A failed removal is retried every
// RemoveRetry until it succeeds or the guest is placed again.
func (v *Visitors) Remove(ctx context.Context, guestID string) {
	if guestID == "" {
		return
	}
	api := v.client()
	if api == nil {
		return
	}
	e, unlock, err := v.lockGuest(ctx, guestID)
	if err != nil {
		v.logger.Warn("demo visitor site not removed", zap.String("guestId", guestID), zap.Error(err))
		return
	}
	defer unlock()
	v.mu.Lock()
	if e.site == siteNone {
		v.mu.Unlock()
		return
	}
	empID := e.empID
	e.site, e.kick, e.retryAt = siteRemoving, false, time.Time{}
	e.schedule(time.Time{})
	v.mu.Unlock()

	err = v.removeSite(ctx, api, guestID, empID)
	v.mu.Lock()
	if err == nil {
		e.site, e.empID = siteNone, ""
		e.schedule(time.Time{})
	} else {
		e.schedule(v.opts.Now().Add(v.opts.RemoveRetry))
	}
	v.mu.Unlock()
	if err != nil {
		v.logger.Warn("demo visitor site removal failed, will retry", zap.String("guestId", guestID), zap.String("emplacementId", empID), zap.Error(err))
		return
	}
	v.logger.Debug("demo visitor site removed", zap.String("guestId", guestID), zap.String("emplacementId", empID))
}

// remover carries out the removals the loop hands it, one at a time.
func (v *Visitors) remover(ctx context.Context, api ingestv1.IngestServiceClient) {
	for {
		select {
		case <-ctx.Done():
			return
		case r := <-v.removals:
			v.removeScheduled(ctx, api, r)
		}
	}
}

// removeScheduled removes a site whose removal fell due, unless its schedule
// changed since it was handed over (the guest came back, was placed again or
// removed).
func (v *Visitors) removeScheduled(ctx context.Context, api ingestv1.IngestServiceClient, r removal) {
	e, unlock, err := v.lockGuest(ctx, r.guestID)
	if err != nil {
		return
	}
	defer unlock()
	v.mu.Lock()
	if e.removeGen != r.gen || e.removeAt.IsZero() || e.site == siteNone {
		v.mu.Unlock()
		return
	}
	empID := e.empID
	v.mu.Unlock()

	err = v.removeSite(ctx, api, r.guestID, empID)
	v.mu.Lock()
	switch {
	case err == nil:
		e.site, e.empID, e.kick = siteNone, "", false
		e.schedule(time.Time{})
	case e.removeGen == r.gen:
		e.schedule(v.opts.Now().Add(v.opts.RemoveRetry))
	}
	v.mu.Unlock()
	if err != nil {
		if ctx.Err() == nil {
			v.logger.Warn("demo visitor site removal failed, will retry", zap.String("guestId", r.guestID), zap.String("emplacementId", empID), zap.Error(err))
		}
		return
	}
	v.logger.Info("demo visitor site removed after its guest left", zap.String("guestId", r.guestID), zap.String("emplacementId", empID))
}

// removeSite removes the guest's site: by id, or, with none, the one the
// service has by the guest's name. A site the service does not have is
// success.
func (v *Visitors) removeSite(ctx context.Context, api ingestv1.IngestServiceClient, guestID, empID string) error {
	if empID == "" {
		var resp *ingestv1.ListEmplacementsResponse
		err := v.call(ctx, func(ctx context.Context) error {
			var err error
			resp, err = api.ListEmplacements(ctx, &ingestv1.ListEmplacementsRequest{})
			return err
		})
		if err != nil {
			return err
		}
		name := visitorName(guestID)
		for _, e := range resp.GetEmplacements() {
			if strings.EqualFold(strings.TrimSpace(e.GetName()), name) {
				empID = e.GetId()
				break
			}
		}
		if empID == "" {
			return nil
		}
	}
	err := v.call(ctx, func(ctx context.Context) error {
		_, err := api.RemoveEmplacement(ctx, &ingestv1.RemoveEmplacementRequest{Id: empID})
		return err
	})
	if status.Code(err) == codes.NotFound {
		return nil
	}
	return err
}

// Reconcile removes every guest-* site on the connection that this process
// does not track, such as those a previous gateway process left. Call it at
// startup, before guests are served.
func (v *Visitors) Reconcile(ctx context.Context) {
	api := v.client()
	if api == nil {
		return
	}
	var resp *ingestv1.ListEmplacementsResponse
	err := v.call(ctx, func(ctx context.Context) error {
		var err error
		resp, err = api.ListEmplacements(ctx, &ingestv1.ListEmplacementsRequest{})
		return err
	})
	if err != nil {
		v.logger.Error("failed to list leftover demo visitor sites", zap.Error(err))
		return
	}
	removed := 0
	for _, e := range resp.GetEmplacements() {
		name := strings.TrimSpace(e.GetName())
		if len(name) <= len(visitorNamePrefix) || !strings.EqualFold(name[:len(visitorNamePrefix)], visitorNamePrefix) {
			continue
		}
		if v.tracked(strings.ToLower(name[len(visitorNamePrefix):])) {
			continue
		}
		err := v.call(ctx, func(ctx context.Context) error {
			_, err := api.RemoveEmplacement(ctx, &ingestv1.RemoveEmplacementRequest{Id: e.GetId()})
			return err
		})
		if err != nil && status.Code(err) != codes.NotFound {
			v.logger.Warn("failed to remove a leftover demo visitor site", zap.String("emplacementId", e.GetId()), zap.Error(err))
			continue
		}
		removed++
	}
	if removed > 0 {
		v.logger.Info("removed leftover demo visitor sites", zap.Int("count", removed))
	}
}

func (v *Visitors) tracked(guestID string) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	e, ok := v.visitors[guestID]
	return ok && e.site != siteNone
}

// call runs one unary call within RPCTimeout, retrying while the service
// answers Unavailable (an upsert waiting for its cache, a replica that cannot
// write, a connection being made). Every call it retries is idempotent.
func (v *Visitors) call(ctx context.Context, fn func(context.Context) error) error {
	ctx, cancel := context.WithTimeout(ctx, v.opts.RPCTimeout)
	defer cancel()
	delay := visitorCallRetryMin
	for {
		err := fn(ctx)
		if status.Code(err) != codes.Unavailable {
			return err
		}
		t := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			t.Stop()
			return err
		case <-t.C:
		}
		delay = min(delay*2, visitorCallRetryMax)
	}
}

// ---------------------------------------------------------------- report stream

type visitorReportStream = grpc.BidiStreamingClient[ingestv1.StreamReportsRequest, ingestv1.StreamReportsResponse]

// visitorStream is one open StreamReports call. Only the loop touches it,
// except gotOK, which its ack reader sets.
type visitorStream struct {
	gen    uint64
	stream visitorReportStream
	cancel context.CancelFunc
	done   chan error // the ack reader's final error; buffered, sent once
	opened time.Time
	gotOK  atomic.Bool
	broken bool // a Send failed: wait for done, send nothing more
}

// visitorLoop is the single goroutine that owns the stream and every Send.
type visitorLoop struct {
	v    *Visitors
	api  ingestv1.IngestServiceClient
	bo   *visitorBackoff
	cur  *visitorStream
	gen  uint64
	refs uint64

	reconnectT *time.Timer
	reconnectC <-chan time.Time
	retryT     *time.Timer
	retryC     <-chan time.Time
}

func (v *Visitors) run(ctx context.Context, api ingestv1.IngestServiceClient) {
	l := &visitorLoop{v: v, api: api, bo: &visitorBackoff{min: v.opts.BackoffMin, max: v.opts.BackoffMax}}
	defer l.shutdown()
	ticker := time.NewTicker(v.opts.Heartbeat)
	defer ticker.Stop()
	l.connect(ctx)
	for {
		var doneC <-chan error
		if l.cur != nil {
			doneC = l.cur.done
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			l.beat(v.beats(true))
			v.dispatchRemovals()
		case <-v.wake:
			l.beat(v.beats(false))
			l.armRetry()
		case <-l.retryC:
			l.retryC = nil
			l.beat(v.dueRetries(time.Now()))
			l.armRetry()
		case err := <-doneC:
			a := l.cur
			l.cur = nil
			a.cancel()
			healthy := a.gotOK.Load() || time.Since(a.opened) >= visitorHealthyAfter
			l.failed(ctx, a.gen, err, healthy)
		case <-l.reconnectC:
			l.reconnectC = nil
			l.connect(ctx)
		}
	}
}

// connect opens a stream and heartbeats every online site on it at once.
// Failing to open schedules a reconnect.
func (l *visitorLoop) connect(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	l.gen++
	sctx, cancel := context.WithCancel(ctx)
	openT := time.AfterFunc(l.v.opts.RPCTimeout, cancel) // bounds the open, not the stream
	st, err := l.api.StreamReports(sctx)
	if !openT.Stop() && err == nil {
		err = status.Error(codes.DeadlineExceeded, "opening the report stream timed out")
	}
	if err != nil {
		cancel()
		l.failed(ctx, l.gen, err, false)
		return
	}
	a := &visitorStream{gen: l.gen, stream: st, cancel: cancel, done: make(chan error, 1), opened: time.Now()}
	l.cur = a
	go l.v.readAcks(a)
	l.v.logger.Debug("demo visitor report stream open", zap.Uint64("stream", a.gen))
	l.beat(l.v.beats(true))
}

// failed schedules a reconnect after a stream ended or could not open.
// Unacked heartbeats are not replayed: the next ones supersede them.
func (l *visitorLoop) failed(ctx context.Context, gen uint64, err error, healthy bool) {
	if ctx.Err() != nil {
		return
	}
	code, msg := codes.OK, "the server closed the stream"
	if !errors.Is(err, io.EOF) {
		st := status.Convert(err)
		code, msg = st.Code(), st.Message()
	}
	if healthy {
		l.bo.reset()
	}
	d := l.bo.next()
	fields := []zap.Field{zap.Uint64("stream", gen), zap.String("code", code.String()), zap.String("message", msg), zap.Duration("backoff", d)}
	if code == codes.Unauthenticated || code == codes.PermissionDenied {
		l.v.logger.Error("demo visitor report stream refused: check the visitors connection's ingest token; reconnecting", fields...)
	} else {
		l.v.logger.Warn("demo visitor report stream ended; reconnecting", fields...)
	}
	l.reconnectT = time.NewTimer(d)
	l.reconnectC = l.reconnectT.C
}

// beat sends one heartbeat per site in bs on the open stream; with none
// open they are dropped (connect heartbeats every site anyway).
func (l *visitorLoop) beat(bs []visitorBeat) {
	for _, b := range bs {
		a := l.cur
		if a == nil || a.broken {
			return
		}
		l.refs++
		r := &ingestv1.Report{
			ClientRef:     visitorName(b.guestID) + "/" + strconv.FormatUint(l.refs, 10),
			EmplacementId: b.empID,
			Body: &ingestv1.Report_EmplacementState{EmplacementState: &sentinelv1.EmplacementState{
				EmplacementId:        b.empID,
				Time:                 timestamppb.New(l.v.opts.Now()),
				HeartbeatIntervalSec: l.v.opts.HeartbeatIntervalSec,
			}},
		}
		if err := a.stream.Send(&ingestv1.StreamReportsRequest{Report: r}); err != nil {
			// The stream's status comes from its ack reader (Send returns io.EOF).
			a.broken = true
			l.v.logger.Debug("demo visitor heartbeat not sent; waiting for the stream's status", zap.Uint64("stream", a.gen), zap.Error(err))
		}
	}
}

func (l *visitorLoop) armRetry() {
	if l.retryT != nil {
		l.retryT.Stop()
	}
	l.retryC = nil
	at, ok := l.v.nextRetry()
	if !ok {
		return
	}
	l.retryT = time.NewTimer(max(time.Until(at), 0))
	l.retryC = l.retryT.C
}

// shutdown ends the open stream and waits for its ack reader.
func (l *visitorLoop) shutdown() {
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
}

type visitorBeat struct{ guestID, empID string }

// beats returns the sites to heartbeat now: every online placed one, or with
// all false only those kicked since the last beat. Kicks are cleared.
func (v *Visitors) beats(all bool) []visitorBeat {
	v.mu.Lock()
	defer v.mu.Unlock()
	var out []visitorBeat
	for id, e := range v.visitors {
		if e.site == sitePlaced && e.online && (all || e.kick) {
			out = append(out, visitorBeat{id, e.empID})
		}
		e.kick = false
	}
	return out
}

// dueRetries returns the sites whose unknown_emplacement retry is due.
func (v *Visitors) dueRetries(now time.Time) []visitorBeat {
	v.mu.Lock()
	defer v.mu.Unlock()
	var out []visitorBeat
	for id, e := range v.visitors {
		if e.retryAt.IsZero() || now.Before(e.retryAt) {
			continue
		}
		e.retryAt = time.Time{}
		if e.site == sitePlaced && e.online {
			out = append(out, visitorBeat{id, e.empID})
		}
	}
	return out
}

func (v *Visitors) nextRetry() (time.Time, bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	var next time.Time
	for _, e := range v.visitors {
		if !e.retryAt.IsZero() && (next.IsZero() || e.retryAt.Before(next)) {
			next = e.retryAt
		}
	}
	return next, !next.IsZero()
}

// dispatchRemovals hands every removal that fell due to the remover, once
// per schedule. A full queue leaves the rest for the next tick.
func (v *Visitors) dispatchRemovals() {
	now := v.opts.Now()
	v.mu.Lock()
	defer v.mu.Unlock()
	for id, e := range v.visitors {
		if e.removeAt.IsZero() || now.Before(e.removeAt) || e.dispatched == e.removeGen {
			continue
		}
		select {
		case v.removals <- removal{guestID: id, gen: e.removeGen}:
			e.dispatched = e.removeGen
		default:
			return
		}
	}
}

// ---------------------------------------------------------------- acks

// readAcks is a stream's only reader. It ends when the stream does, and its
// last act is to hand the stream's error to the loop.
func (v *Visitors) readAcks(a *visitorStream) {
	for {
		resp, err := a.stream.Recv()
		if err != nil {
			a.done <- err
			return
		}
		v.handleAck(a, resp.GetAck())
	}
}

// handleAck notes an OK and logs a refusal: at warn the first time a reason
// is seen in visitorWarnEvery, else at debug. An unknown_emplacement right
// after an upsert (the stream's replica has not caught up) is retried once,
// shortly.
func (v *Visitors) handleAck(a *visitorStream, ack *ingestv1.Ack) {
	if ack.GetStatus() == ingestv1.AckStatus_ACK_STATUS_OK {
		a.gotOK.Store(true)
		return
	}
	ref, reason := ack.GetClientRef(), ack.GetReason()
	name, _, _ := strings.Cut(ref, "/")
	guestID := strings.TrimPrefix(name, visitorNamePrefix)
	now := time.Now()
	retry := false
	v.mu.Lock()
	if e, ok := v.visitors[guestID]; ok && reason == ackUnknownEmplacement &&
		e.site == sitePlaced && e.online && !e.retried && v.opts.Now().Sub(e.placedAt) < visitorUpsertGrace {
		e.retried, e.retryAt, retry = true, now.Add(visitorAckRetryDelay), true
	}
	key := ack.GetStatus().String() + "/" + reason
	warn := now.Sub(v.warned[key]) >= visitorWarnEvery
	if warn {
		v.warned[key] = now
	}
	v.mu.Unlock()
	if retry {
		v.signal()
	}
	fields := []zap.Field{zap.String("clientRef", ref), zap.String("status", ack.GetStatus().String()),
		zap.String("reason", reason), zap.String("emplacementId", ack.GetEmplacementId()), zap.Bool("retry", retry)}
	if warn && !retry {
		v.logger.Warn("demo visitor heartbeat refused", fields...)
		return
	}
	v.logger.Debug("demo visitor heartbeat refused", fields...)
}

// ---------------------------------------------------------------- backoff

// visitorBackoff is jittered exponential backoff: min·2^n capped at max,
// times a jitter factor in [0.8,1.2), clamped to [min,max].
type visitorBackoff struct {
	min, max time.Duration
	n        int
}

func (b *visitorBackoff) next() time.Duration {
	d := b.min
	for i := 0; i < b.n && d < b.max; i++ {
		d *= 2
	}
	if b.n < 64 {
		b.n++
	}
	d = time.Duration(float64(min(d, b.max)) * (0.8 + 0.4*rand.Float64()))
	return min(max(d, b.min), b.max)
}

func (b *visitorBackoff) reset() { b.n = 0 }
