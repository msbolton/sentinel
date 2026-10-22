package demo

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/sentinel/api-gateway/internal/auth"
	"github.com/sentinel/api-gateway/internal/registration"
)

// ErrFull is returned by Start when the guest cap is reached.
var ErrFull = errors.New("demo: guest limit reached")

// ErrAddressBusy is returned by Start when the visitor's address already has
// PerAddress live guests.
var ErrAddressBusy = errors.New("demo: this address has as many guests as it may")

// guestClientID is the public client a guest's tokens are issued for, the same
// one the web app logs in with.
const guestClientID = "sentinel-web"

// guestRoles are the realm roles every guest gets: read-only, unclassified.
var guestRoles = []string{auth.RoleViewer, "classification-u", auth.RoleGuest}

// startTimeout bounds the create-then-login sequence. It runs detached from
// the request: a visitor who hangs up mid-create must not leave an account
// behind that the registry never learned about.
const startTimeout = 30 * time.Second

// cleanupTimeout bounds the detached Keycloak call that removes a guest whose
// login failed after its account was made.
const cleanupTimeout = 10 * time.Second

// Keycloak is the part of registration.KeycloakClient the guest registry uses.
type Keycloak interface {
	CreateGuestUser(ctx context.Context, username, password string, roles []string) (string, error)
	DeleteUser(ctx context.Context, userID string) error
	UsersWithRole(ctx context.Context, role string) ([]registration.GuestUser, error)
	PasswordGrant(ctx context.Context, clientID, username, password string) (registration.TokenResponse, error)
}

// VisitorSites places and removes a guest's emplacement. Positions passed to
// Place are already blurred.
type VisitorSites interface {
	Place(ctx context.Context, guestID string, lat, lon float64) (emplacementID string, err error)
	Remove(ctx context.Context, guestID string)
}

// SocketCloser closes every entity-stream socket a user has open.
type SocketCloser interface {
	CloseUser(userID string)
}

// GuestOptions configures a Guests registry. Sites and Sockets may be nil.
// A zero OfflineAfter, ConnectWithin or PerAddress turns that rule off.
type GuestOptions struct {
	Max int
	TTL time.Duration
	// OfflineAfter ends a guest that has had no socket open this long
	// (DEMO_OFFLINE_REMOVE_AFTER), so its slot goes back to the pool while
	// its time is not yet up.
	OfflineAfter time.Duration
	// ConnectWithin ends a guest that never opens a socket this long after
	// it was granted.
	ConnectWithin time.Duration
	// PerAddress caps the live guests one visitor address (see AddressKey)
	// may hold at once.
	PerAddress int
	Sites      VisitorSites
	Sockets    SocketCloser
	Now        func() time.Time
}

// Guest is a live throwaway account.
type Guest struct {
	UserID    string // Keycloak user id, the token's subject
	GuestID   string // 8 lowercase hex characters
	Username  string // guest-<GuestID>
	ExpiresAt time.Time
}

// Session is what the browser receives for a new guest. Start leaves
// VisitorsFeedID empty; the handler fills it in.
type Session struct {
	AccessToken    string    `json:"accessToken"`
	RefreshToken   string    `json:"refreshToken"`
	ExpiresIn      int       `json:"expiresIn"`
	GuestID        string    `json:"guestId"`
	ExpiresAt      time.Time `json:"expiresAt"`
	VisitorsFeedID string    `json:"visitorsFeedId"`
}

// Guests is the in-memory registry of live demo guests, keyed by Keycloak
// user id. It is safe for concurrent use.
type Guests struct {
	kc            Keycloak
	max           int
	ttl           time.Duration
	offlineAfter  time.Duration
	connectWithin time.Duration
	perAddress    int
	sites         VisitorSites
	sockets       SocketCloser
	now           func() time.Time
	logger        *zap.Logger

	mu         sync.Mutex
	byUser     map[string]*guestEntry
	starting   int            // slots reserved by Start calls still talking to Keycloak
	startingAt map[string]int // the same, per address key
}

// NewGuests creates an empty registry.
func NewGuests(kc Keycloak, opts GuestOptions, logger *zap.Logger) *Guests {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Guests{
		kc:            kc,
		max:           opts.Max,
		ttl:           opts.TTL,
		offlineAfter:  opts.OfflineAfter,
		connectWithin: opts.ConnectWithin,
		perAddress:    opts.PerAddress,
		sites:         opts.Sites,
		sockets:       opts.Sockets,
		now:           opts.Now,
		logger:        logger,
		byUser:        map[string]*guestEntry{},
		startingAt:    map[string]int{},
	}
}

// guestEntry is a registered guest. ending is set by Sweep, under the lock and
// before any I/O, so that from then on Lookup refuses the guest; it stays
// registered (holding its slot) until its Keycloak account is gone.
type guestEntry struct {
	Guest
	ending bool
	addr   string // the address key it was started from
	// online is whether the guest has a socket open; connected whether it
	// ever had one; since is when it was granted or last went offline.
	online    bool
	connected bool
	since     time.Time
}

// endReason says why Sweep should end e now, or "" when it should not.
func (g *Guests) endReason(e *guestEntry, now time.Time) string {
	switch {
	case e.ending:
		return "retry"
	case !now.Before(e.ExpiresAt):
		return "expired"
	case e.online:
		return ""
	case e.connected && g.offlineAfter > 0 && now.Sub(e.since) >= g.offlineAfter:
		return "left"
	case !e.connected && g.connectWithin > 0 && now.Sub(e.since) >= g.connectWithin:
		return "never connected"
	}
	return ""
}

// Count is the number of guest slots in use, including ones whose Start is
// still in progress, so it never reads below the cap while the cap is full.
func (g *Guests) Count() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.byUser) + g.starting
}

// Lookup returns the live guest whose Keycloak user id is userID. A guest past
// its expiry, or one Sweep has begun ending, is not live, even while it is
// still registered.
func (g *Guests) Lookup(userID string) (Guest, bool) {
	now := g.now()
	g.mu.Lock()
	defer g.mu.Unlock()
	e, ok := g.byUser[userID]
	if !ok || e.ending || !now.Before(e.ExpiresAt) {
		return Guest{}, false
	}
	return e.Guest, true
}

// SetOnline records that the guest whose Keycloak user id is userID opened
// its first socket (online) or closed its last one, and returns the guest if
// it is live. The entity-stream presence listener calls it, so it does no
// I/O: a guest offline for OfflineAfter is ended by the next Sweep.
func (g *Guests) SetOnline(userID string, online bool) (Guest, bool) {
	now := g.now()
	g.mu.Lock()
	defer g.mu.Unlock()
	e, ok := g.byUser[userID]
	if !ok {
		return Guest{}, false
	}
	if online {
		e.online, e.connected = true, true
	} else if e.online {
		e.online, e.since = false, now
	}
	if e.ending || !now.Before(e.ExpiresAt) {
		return Guest{}, false
	}
	return e.Guest, true
}

// Start creates a guest account in Keycloak, logs it in and registers it,
// counting it against addr (an AddressKey). It returns ErrFull at the cap and
// ErrAddressBusy when addr already holds PerAddress guests, both without
// calling Keycloak. The slot is reserved
// before Keycloak is called, so concurrent calls cannot overshoot the cap, and
// released again on any failure. Once begun, the Keycloak calls run on a
// context detached from ctx, so a cancelled request cannot interrupt them
// half-way and orphan an account.
func (g *Guests) Start(ctx context.Context, addr string) (Session, error) {
	if err := ctx.Err(); err != nil {
		return Session{}, err
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), startTimeout)
	defer cancel()
	if err := g.reserve(addr); err != nil {
		return Session{}, err
	}
	registered := false
	defer func() {
		if !registered {
			g.release(addr)
		}
	}()

	guestID, password, err := newCredentials()
	if err != nil {
		return Session{}, err
	}
	username := "guest-" + guestID

	userID, err := g.kc.CreateGuestUser(ctx, username, password, guestRoles)
	if err != nil {
		g.logger.Error("demo guest creation failed", zap.String("guestId", guestID), zap.Error(err))
		return Session{}, fmt.Errorf("create guest: %w", err)
	}

	tok, err := g.kc.PasswordGrant(ctx, guestClientID, username, password)
	if err != nil {
		g.logger.Error("demo guest login failed, deleting it", zap.String("guestId", guestID), zap.String("userId", userID), zap.Error(err))
		// The request context may be what failed; clean up on a short one of our own.
		cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
		defer cancel()
		if derr := g.kc.DeleteUser(cctx, userID); derr != nil {
			// Reconcile at the next start removes it, as it holds the guest role.
			g.logger.Error("failed to delete demo guest after failed login", zap.String("userId", userID), zap.Error(derr))
		}
		return Session{}, fmt.Errorf("guest login: %w", err)
	}

	now := g.now()
	guest := Guest{UserID: userID, GuestID: guestID, Username: username, ExpiresAt: now.Add(g.ttl)}
	g.mu.Lock()
	g.unreserveLocked(addr)
	g.byUser[userID] = &guestEntry{Guest: guest, addr: addr, since: now}
	g.mu.Unlock()
	registered = true

	g.logger.Info("demo guest started", zap.String("guestId", guestID), zap.String("userId", userID))
	return Session{
		AccessToken:  tok.AccessToken,
		RefreshToken: tok.RefreshToken,
		ExpiresIn:    tok.ExpiresIn,
		GuestID:      guestID,
		ExpiresAt:    guest.ExpiresAt,
	}, nil
}

func (g *Guests) reserve(addr string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.byUser)+g.starting >= g.max {
		return ErrFull
	}
	if g.perAddress > 0 {
		n := g.startingAt[addr]
		for _, e := range g.byUser {
			if e.addr == addr {
				n++
			}
		}
		if n >= g.perAddress {
			return ErrAddressBusy
		}
	}
	g.starting++
	g.startingAt[addr]++
	return nil
}

func (g *Guests) release(addr string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.unreserveLocked(addr)
}

func (g *Guests) unreserveLocked(addr string) {
	g.starting--
	if g.startingAt[addr]--; g.startingAt[addr] <= 0 {
		delete(g.startingAt, addr)
	}
}

// Sweep ends every guest past its TTL, every guest that has had no socket for
// OfflineAfter and every guest that never opened one within ConnectWithin of
// being granted: it deletes the Keycloak account, closes
// the guest's sockets and removes its site. Each guest is first marked ending,
// under the lock, so no position report can be accepted for it once its site
// is removed. A guest whose Keycloak delete fails stays registered and ending,
// holding its slot, and is retried on the next sweep; dropping it would orphan
// an account that can still refresh its tokens. Its sockets and site go at
// once either way, since its time is up. One failure never stops the sweep of
// the others.
func (g *Guests) Sweep(ctx context.Context) {
	now := g.now()
	var expired []Guest
	var reasons []string
	g.mu.Lock()
	for _, e := range g.byUser {
		if why := g.endReason(e, now); why != "" {
			e.ending = true
			expired = append(expired, e.Guest)
			reasons = append(reasons, why)
		}
	}
	g.mu.Unlock()

	// No lock is held below: CloseUser fires the presence listener, which
	// calls Lookup.
	for i, guest := range expired {
		if ctx.Err() != nil {
			return
		}
		err := g.kc.DeleteUser(ctx, guest.UserID)
		if g.sockets != nil {
			g.sockets.CloseUser(guest.UserID)
		}
		if g.sites != nil {
			g.sites.Remove(ctx, guest.GuestID)
		}
		if err != nil {
			g.logger.Warn("demo guest delete failed, will retry", zap.String("guestId", guest.GuestID), zap.String("userId", guest.UserID), zap.Error(err))
			continue
		}
		g.mu.Lock()
		delete(g.byUser, guest.UserID)
		g.mu.Unlock()
		g.logger.Info("demo guest ended", zap.String("guestId", guest.GuestID), zap.String("userId", guest.UserID), zap.String("reason", reasons[i]))
	}
}

// Reconcile deletes every Keycloak account holding the guest role that this
// registry does not know, such as those left by a previous gateway process.
// Call it only at startup, before the session route is served: a guest whose
// Start is still in flight is not registered yet and would be deleted, and
// with more than one gateway replica each would delete the others' guests.
// It returns an error when the listing or any delete failed, so the caller
// can try again; a retry only deletes what is still left.
func (g *Guests) Reconcile(ctx context.Context) error {
	users, err := g.kc.UsersWithRole(ctx, auth.RoleGuest)
	if err != nil {
		g.logger.Error("failed to list leftover demo guests", zap.Error(err))
		return fmt.Errorf("list leftover demo guests: %w", err)
	}
	deleted, failed := 0, 0
	for _, u := range users {
		if g.registered(u.ID) {
			continue
		}
		if err := g.kc.DeleteUser(ctx, u.ID); err != nil {
			g.logger.Error("failed to delete leftover demo guest", zap.String("userId", u.ID), zap.Error(err))
			failed++
			continue
		}
		deleted++
	}
	if deleted > 0 {
		g.logger.Info("deleted leftover demo guests", zap.Int("count", deleted))
	}
	if failed > 0 {
		return fmt.Errorf("%d leftover demo guests could not be deleted", failed)
	}
	return nil
}

func (g *Guests) registered(userID string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	_, ok := g.byUser[userID]
	return ok
}

// newCredentials returns a guest id of 8 lowercase hex characters and a
// password of 32 random bytes, base64url-encoded.
func newCredentials() (guestID, password string, err error) {
	id := make([]byte, 4)
	if _, err := rand.Read(id); err != nil {
		return "", "", fmt.Errorf("guest id: %w", err)
	}
	pw := make([]byte, 32)
	if _, err := rand.Read(pw); err != nil {
		return "", "", fmt.Errorf("guest password: %w", err)
	}
	return hex.EncodeToString(id), base64.RawURLEncoding.EncodeToString(pw), nil
}
