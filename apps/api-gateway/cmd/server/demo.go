package main

import (
	"context"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"

	"github.com/sentinel/api-gateway/internal/auth"
	"github.com/sentinel/api-gateway/internal/config"
	"github.com/sentinel/api-gateway/internal/demo"
	"github.com/sentinel/api-gateway/internal/registration"
	"github.com/sentinel/go-common/httputil"
)

const (
	// demoSweepEvery is how often expired guests are ended.
	demoSweepEvery = 30 * time.Second
	// demoReconcileTimeout bounds one startup reconcile attempt.
	demoReconcileTimeout = 2 * time.Minute
	// demoRetryMin and demoRetryMax bound the startup reconcile backoff.
	demoRetryMin = time.Second
	demoRetryMax = 30 * time.Second
	// demoConnectWithin ends a guest that never opens the entity stream
	// this long after it was granted (a bot, or a tab closed at once).
	demoConnectWithin = 2 * time.Minute
	// demoGuestsPerAddress caps the live guests one visitor address (an
	// IPv6 /64) holds, so no single visitor can fill the demo.
	demoGuestsPerAddress = 2
)

// demoHub is the part of the entity-stream hub the demo uses.
type demoHub interface {
	SetPresenceListener(func(userID string, online bool))
	UsersWithRole(role string) int
	LastEventAt() time.Time
	CloseUser(userID string)
}

// publicDemo is the running public demo: guest sessions, visitor sites and
// the status the portfolio site polls. Guests and sites live in this
// process's memory, so it assumes a single gateway replica.
type publicDemo struct {
	logger   *zap.Logger
	guests   *demo.Guests
	visitors *demo.Visitors
	handler  *demo.Handler
	status   http.Handler
	geo      io.Closer

	// ready is set once both startup reconciles have succeeded; until then
	// no session is started, so nothing they delete can be a live guest.
	ready  atomic.Bool
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// startPublicDemo builds the demo and starts its background work: the
// visitor-site client, the startup reconciles (retried until each succeeds
// once) and the expiry sweep. It registers the presence listener on hub, so
// call it before the HTTP server starts. Stop ends it all.
func startPublicDemo(cfg config.DemoConfig, kc demo.Keycloak, hub demoHub, logger *zap.Logger) (*publicDemo, error) {
	logger = logger.Named("demo")
	n, window, err := cfg.SessionLimit()
	if err != nil { // Validate has already refused this
		return nil, err
	}
	d := &publicDemo{logger: logger}
	ctx, cancel := context.WithCancel(context.Background())
	d.cancel = cancel

	var loc demo.Locator = demo.NopLocator{}
	if cfg.GeoIPDB != "" {
		mm, err := demo.OpenMaxMind(cfg.GeoIPDB)
		if err != nil {
			logger.Error("cannot open the GeoIP database; visitors who deny location get no site", zap.String("path", cfg.GeoIPDB), zap.Error(err))
		} else {
			loc, d.geo = mm, mm
		}
	}

	d.visitors = demo.NewVisitors(demo.VisitorOptions{
		Addr:        cfg.IngestAddr,
		Token:       cfg.IngestToken,
		Insecure:    cfg.IngestInsecure,
		RemoveAfter: cfg.OfflineRemoveAfter,
	}, logger)
	if err := d.visitors.Start(ctx); err != nil {
		cancel()
		d.closeGeo()
		return nil, err
	}

	opts := demoGuestOptions(cfg)
	opts.Sites, opts.Sockets = d.visitors, hub
	d.guests = demo.NewGuests(kc, opts, logger)
	hub.SetPresenceListener(presenceListener(d.guests.SetOnline, d.visitors))

	d.handler = demo.NewHandler(d.guests, d.visitors, loc, cfg.VisitorsFeedID, registration.NewIPLimiter(n, window), logger)
	d.status = demo.NewStatusHandler(demoStatus{hub: hub, guests: d.guests.Count, ready: &d.ready}, cfg.MaxGuests, cfg.StatusOrigin, time.Now)

	logger.Info("public demo enabled; guests and visitor sites are tracked in memory, so this assumes a single gateway replica",
		zap.Int("maxGuests", cfg.MaxGuests), zap.Duration("guestTTL", cfg.GuestTTL), zap.String("visitorsFeedId", cfg.VisitorsFeedID))

	d.wg.Add(2)
	go func() {
		defer d.wg.Done()
		d.reconcile(ctx)
	}()
	go func() {
		defer d.wg.Done()
		d.sweep(ctx)
	}()
	return d, nil
}

// demoGuestOptions are the registry's limits: a guest holds its slot for its
// TTL only while someone explores with it. One that has had no socket for
// DEMO_OFFLINE_REMOVE_AFTER (when its site goes too) or never opened one is
// ended early, and one address holds at most demoGuestsPerAddress.
func demoGuestOptions(cfg config.DemoConfig) demo.GuestOptions {
	return demo.GuestOptions{
		Max:           cfg.MaxGuests,
		TTL:           cfg.GuestTTL,
		OfflineAfter:  cfg.OfflineRemoveAfter,
		ConnectWithin: demoConnectWithin,
		PerAddress:    demoGuestsPerAddress,
	}
}

// reconcile clears what a previous process left (guest accounts in
// Keycloak, guest sites in ingest), retrying each until it succeeds once,
// then opens the session route.
func (d *publicDemo) reconcile(ctx context.Context) {
	var wg sync.WaitGroup
	var guestsOK, sitesOK bool
	wg.Add(2)
	go func() {
		defer wg.Done()
		guestsOK = retryUntilSuccess(ctx, d.logger, "leftover demo guests", demoRetryMin, demoRetryMax, sleepCtx, d.guests.Reconcile)
	}()
	go func() {
		defer wg.Done()
		sitesOK = retryUntilSuccess(ctx, d.logger, "leftover demo visitor sites", demoRetryMin, demoRetryMax, sleepCtx, d.visitors.Reconcile)
	}()
	wg.Wait()
	if guestsOK && sitesOK {
		d.ready.Store(true)
		d.logger.Info("public demo ready: leftovers cleared, sessions open")
	}
}

func (d *publicDemo) sweep(ctx context.Context) {
	t := time.NewTicker(demoSweepEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			d.guests.Sweep(ctx)
		}
	}
}

// RegisterPublicRoutes mounts the session route (refused until the startup
// reconcile is done) and the status route, both unauthenticated.
func (d *publicDemo) RegisterPublicRoutes(mux *http.ServeMux) {
	sessions := http.NewServeMux()
	d.handler.RegisterPublicRoutes(sessions)
	mux.Handle("POST /api/v1/demo/session", gateUntilReady(&d.ready, sessions))
	mux.Handle("GET /api/v1/demo/status", d.status)
}

// AdmitStream is the entity stream's admission check: a guest the registry
// does not know as live (ended, or left by a previous process with a token
// still valid) is refused with 1008 so its browser shows "session ended".
func (d *publicDemo) AdmitStream(u *auth.User) bool { return admitStream(d.guests.Lookup)(u) }

func admitStream(lookup func(userID string) (demo.Guest, bool)) func(*auth.User) bool {
	return func(u *auth.User) bool {
		if !u.HasAnyRole(auth.RoleGuest) {
			return true
		}
		_, live := lookup(u.UserID)
		return live
	}
}

// RegisterProtectedRoutes mounts the position route.
func (d *publicDemo) RegisterProtectedRoutes(mux *http.ServeMux) {
	d.handler.RegisterProtectedRoutes(mux)
}

// Stop ends the background work, closes the visitor-site client (sites stay
// for the next start's reconcile) and the GeoIP database.
func (d *publicDemo) Stop() {
	d.cancel()
	d.wg.Wait()
	d.visitors.Stop()
	d.closeGeo()
}

func (d *publicDemo) closeGeo() {
	if d.geo != nil {
		if err := d.geo.Close(); err != nil {
			d.logger.Warn("closing the GeoIP database", zap.Error(err))
		}
	}
}

// presenceSites is what the presence listener drives.
type presenceSites interface {
	Online(guestID string)
	Offline(guestID string)
}

// presenceListener turns entity-stream presence into guest and visitor-site
// presence: setOnline tells the registry (which ends a guest that stays away)
// and returns the guest if it is live; anyone else is ignored. The hub calls
// it with every connect and disconnect serialised, so it does no I/O: a map
// update and a non-blocking signal.
func presenceListener(setOnline func(userID string, online bool) (demo.Guest, bool), sites presenceSites) func(userID string, online bool) {
	return func(userID string, online bool) {
		g, ok := setOnline(userID, online)
		if !ok {
			return
		}
		if online {
			sites.Online(g.GuestID)
		} else {
			sites.Offline(g.GuestID)
		}
	}
}

// retryUntilSuccess runs fn until it returns nil, waiting min, then twice as
// long each time up to max, between attempts. It reports false if ctx ended
// first. Each attempt gets its own timeout.
func retryUntilSuccess(ctx context.Context, logger *zap.Logger, what string, min, max time.Duration,
	sleep func(context.Context, time.Duration) bool, fn func(context.Context) error) bool {
	delay := min
	for attempt := 1; ; attempt++ {
		actx, cancel := context.WithTimeout(ctx, demoReconcileTimeout)
		err := fn(actx)
		cancel()
		if err == nil {
			if attempt > 1 {
				logger.Info("startup reconcile succeeded", zap.String("what", what), zap.Int("attempts", attempt))
			}
			return true
		}
		if ctx.Err() != nil {
			return false
		}
		logger.Warn("startup reconcile failed, will retry; demo sessions stay closed until it succeeds",
			zap.String("what", what), zap.Int("attempt", attempt), zap.Duration("retryIn", delay), zap.Error(err))
		if !sleep(ctx, delay) {
			return false
		}
		if delay *= 2; delay > max {
			delay = max
		}
	}
}

// sleepCtx waits for d, reporting false if ctx ends first.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// gateUntilReady answers 503 until ready is set.
func gateUntilReady(ready *atomic.Bool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !ready.Load() {
			w.Header().Set("Retry-After", "5")
			httputil.Error(w, http.StatusServiceUnavailable, "The demo is starting; try again in a moment")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// statusHub is the part of the hub the status reads.
type statusHub interface {
	UsersWithRole(role string) int
	LastEventAt() time.Time
}

// demoStatus is the demo.StatusSource: guests connected to the stream, the
// latest entity event, and guest slots in use. Until the startup reconcile is
// done the demo reports itself down, since it starts no sessions yet.
type demoStatus struct {
	hub    statusHub
	guests func() int
	ready  *atomic.Bool
}

func (s demoStatus) Exploring() int { return s.hub.UsersWithRole(auth.RoleGuest) }

func (s demoStatus) LastEventAt() time.Time {
	if !s.ready.Load() {
		return time.Time{}
	}
	return s.hub.LastEventAt()
}

func (s demoStatus) Guests() int { return s.guests() }
