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

// guestClientID is the public client a guest's tokens are issued for, the same
// one the web app logs in with.
const guestClientID = "sentinel-web"

// guestRoles are the realm roles every guest gets: read-only, unclassified.
var guestRoles = []string{auth.RoleViewer, "classification-u", auth.RoleGuest}

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
type GuestOptions struct {
	Max     int
	TTL     time.Duration
	Sites   VisitorSites
	Sockets SocketCloser
	Now     func() time.Time
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
	kc      Keycloak
	max     int
	ttl     time.Duration
	sites   VisitorSites
	sockets SocketCloser
	now     func() time.Time
	logger  *zap.Logger

	mu       sync.Mutex
	byUser   map[string]Guest
	starting int // slots reserved by Start calls still talking to Keycloak
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
		kc:      kc,
		max:     opts.Max,
		ttl:     opts.TTL,
		sites:   opts.Sites,
		sockets: opts.Sockets,
		now:     opts.Now,
		logger:  logger,
		byUser:  map[string]Guest{},
	}
}

// Count is the number of guest slots in use, including ones whose Start is
// still in progress, so it never reads below the cap while the cap is full.
func (g *Guests) Count() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.byUser) + g.starting
}

// Lookup returns the guest whose Keycloak user id is userID.
func (g *Guests) Lookup(userID string) (Guest, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	guest, ok := g.byUser[userID]
	return guest, ok
}

// Start creates a guest account in Keycloak, logs it in and registers it. It
// returns ErrFull at the cap without calling Keycloak. The slot is reserved
// before Keycloak is called, so concurrent calls cannot overshoot the cap, and
// released again on any failure.
func (g *Guests) Start(ctx context.Context) (Session, error) {
	if !g.reserve() {
		return Session{}, ErrFull
	}
	registered := false
	defer func() {
		if !registered {
			g.release()
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

	guest := Guest{UserID: userID, GuestID: guestID, Username: username, ExpiresAt: g.now().Add(g.ttl)}
	g.mu.Lock()
	g.starting--
	g.byUser[userID] = guest
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

func (g *Guests) reserve() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.byUser)+g.starting >= g.max {
		return false
	}
	g.starting++
	return true
}

func (g *Guests) release() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.starting--
}

// Sweep ends every guest past its TTL: it deletes the Keycloak account, closes
// the guest's sockets and removes its site. A guest whose Keycloak delete fails
// stays registered, holding its slot, and is retried on the next sweep; dropping
// it would orphan an account that can still refresh its tokens. Its sockets and
// site go at once either way, since its time is up. One failure never stops the
// sweep of the others.
func (g *Guests) Sweep(ctx context.Context) {
	now := g.now()
	var expired []Guest
	g.mu.Lock()
	for _, guest := range g.byUser {
		if !now.Before(guest.ExpiresAt) {
			expired = append(expired, guest)
		}
	}
	g.mu.Unlock()

	// No lock is held below: CloseUser fires the presence listener, which
	// calls Lookup.
	for _, guest := range expired {
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
		g.logger.Info("demo guest expired", zap.String("guestId", guest.GuestID), zap.String("userId", guest.UserID))
	}
}

// Reconcile deletes every Keycloak account holding the guest role that this
// registry does not know, such as those left by a previous gateway process.
// Call it at startup, before the session route is served.
func (g *Guests) Reconcile(ctx context.Context) {
	users, err := g.kc.UsersWithRole(ctx, auth.RoleGuest)
	if err != nil {
		g.logger.Error("failed to list leftover demo guests", zap.Error(err))
		return
	}
	deleted := 0
	for _, u := range users {
		if _, live := g.Lookup(u.ID); live {
			continue
		}
		if err := g.kc.DeleteUser(ctx, u.ID); err != nil {
			g.logger.Error("failed to delete leftover demo guest", zap.String("userId", u.ID), zap.Error(err))
			continue
		}
		deleted++
	}
	if deleted > 0 {
		g.logger.Info("deleted leftover demo guests", zap.Int("count", deleted))
	}
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
