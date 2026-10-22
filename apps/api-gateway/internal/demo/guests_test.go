package demo

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/sentinel/api-gateway/internal/auth"
	"github.com/sentinel/api-gateway/internal/registration"
)

// fakeKeycloak records what the registry asks of Keycloak.
type fakeKeycloak struct {
	mu        sync.Mutex
	nextID    int
	created   []createdGuest
	deleted   []string
	grants    []string // usernames
	clientIDs []string
	withRole  map[string][]registration.GuestUser

	createDelay time.Duration
	createErr   error
	// onCreate runs at the start of CreateGuestUser, outside the lock.
	onCreate func()
	// createCtxLive and grantCtxLive record whether each call's context was
	// still live when the call was made.
	createCtxLive []bool
	grantCtxLive  []bool
	grantErr    error
	// deleteErrs counts down: while positive, DeleteUser fails and decrements it.
	deleteErrs int
	// deleteCtxLive records, per DeleteUser call, whether its context was
	// still live and had a deadline when the call was made.
	deleteCtxLive []bool
}

type createdGuest struct {
	ID, Username, Password string
	Roles                  []string
}

func newFakeKeycloak() *fakeKeycloak {
	return &fakeKeycloak{withRole: map[string][]registration.GuestUser{}}
}

func (f *fakeKeycloak) CreateGuestUser(ctx context.Context, username, password string, roles []string) (string, error) {
	if f.onCreate != nil {
		f.onCreate()
	}
	if f.createDelay > 0 {
		time.Sleep(f.createDelay)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.createCtxLive = append(f.createCtxLive, ctx.Err() == nil)
	if f.createErr != nil {
		return "", f.createErr
	}
	f.nextID++
	id := fmt.Sprintf("kc-%d", f.nextID)
	f.created = append(f.created, createdGuest{ID: id, Username: username, Password: password, Roles: append([]string(nil), roles...)})
	return id, nil
}

func (f *fakeKeycloak) DeleteUser(ctx context.Context, userID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, hasDeadline := ctx.Deadline()
	f.deleteCtxLive = append(f.deleteCtxLive, ctx.Err() == nil && hasDeadline)
	if f.deleteErrs > 0 {
		f.deleteErrs--
		return errors.New("keycloak down")
	}
	f.deleted = append(f.deleted, userID)
	return nil
}

func (f *fakeKeycloak) UsersWithRole(_ context.Context, role string) ([]registration.GuestUser, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]registration.GuestUser(nil), f.withRole[role]...), nil
}

func (f *fakeKeycloak) PasswordGrant(ctx context.Context, clientID, username, password string) (registration.TokenResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.grantCtxLive = append(f.grantCtxLive, ctx.Err() == nil)
	if f.grantErr != nil {
		return registration.TokenResponse{}, f.grantErr
	}
	f.grants = append(f.grants, username)
	f.clientIDs = append(f.clientIDs, clientID)
	return registration.TokenResponse{AccessToken: "access-" + username, RefreshToken: "refresh-" + username, ExpiresIn: 300}, nil
}

func (f *fakeKeycloak) deletedIDs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := append([]string(nil), f.deleted...)
	sort.Strings(out)
	return out
}

func (f *fakeKeycloak) createdGuests() []createdGuest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]createdGuest(nil), f.created...)
}

// fakeSites stands in for the visitor emplacements.
type fakeSites struct {
	mu       sync.Mutex
	placed   []placement
	removed  []string
	ops      []string // "place:<id>" / "remove:<id>", in the order they took effect
	placeErr error
	// beforePlace runs at the start of Place, outside the lock, standing in
	// for whatever else happens while a Place is in flight.
	beforePlace func()
}

type placement struct {
	GuestID  string
	Lat, Lon float64
}

func (s *fakeSites) Place(_ context.Context, guestID string, lat, lon float64) (string, error) {
	if s.beforePlace != nil {
		s.beforePlace()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.placeErr != nil {
		return "", s.placeErr
	}
	s.placed = append(s.placed, placement{guestID, lat, lon})
	s.ops = append(s.ops, "place:"+guestID)
	return "emp-" + guestID, nil
}

func (s *fakeSites) Remove(_ context.Context, guestID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.removed = append(s.removed, guestID)
	s.ops = append(s.ops, "remove:"+guestID)
}

func (s *fakeSites) opLog() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.ops...)
}

func (s *fakeSites) placements() []placement {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]placement(nil), s.placed...)
}

func (s *fakeSites) removedIDs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.removed...)
}

// fakeSockets records which users had their sockets closed.
type fakeSockets struct {
	mu     sync.Mutex
	closed []string
}

func (s *fakeSockets) CloseUser(userID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = append(s.closed, userID)
}

func (s *fakeSockets) closedIDs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.closed...)
}

// clock is a settable Now.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

type guestRig struct {
	kc      *fakeKeycloak
	sites   *fakeSites
	sockets *fakeSockets
	clock   *clock
	logs    *observer.ObservedLogs
	guests  *Guests
}

func newGuestRig(t *testing.T, max int) *guestRig {
	t.Helper()
	core, logs := observer.New(zapcore.DebugLevel)
	r := &guestRig{
		kc:      newFakeKeycloak(),
		sites:   &fakeSites{},
		sockets: &fakeSockets{},
		clock:   &clock{t: time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)},
		logs:    logs,
	}
	r.guests = NewGuests(r.kc, GuestOptions{
		Max:     max,
		TTL:     60 * time.Minute,
		Sites:   r.sites,
		Sockets: r.sockets,
		Now:     r.clock.Now,
	}, zap.New(core))
	return r
}

var usernamePattern = regexp.MustCompile(`^guest-[0-9a-f]{8}$`)

func TestStartCreatesGuestAndReturnsTokens(t *testing.T) {
	rig := newGuestRig(t, 25)
	s, err := rig.guests.Start(context.Background())
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	created := rig.kc.createdGuests()
	if len(created) != 1 {
		t.Fatalf("created %d users, want 1", len(created))
	}
	c := created[0]
	if !usernamePattern.MatchString(c.Username) {
		t.Errorf("username %q does not match %s", c.Username, usernamePattern)
	}
	wantRoles := []string{auth.RoleViewer, "classification-u", auth.RoleGuest}
	gotRoles := append([]string(nil), c.Roles...)
	sort.Strings(gotRoles)
	sort.Strings(wantRoles)
	if strings.Join(gotRoles, ",") != strings.Join(wantRoles, ",") {
		t.Errorf("roles = %v, want %v", c.Roles, wantRoles)
	}
	// 32 random bytes, base64url without padding, is 43 characters.
	if len(c.Password) != 43 || strings.ContainsAny(c.Password, "+/=") {
		t.Errorf("password %q is not 32 bytes of base64url", c.Password)
	}

	if s.GuestID != strings.TrimPrefix(c.Username, "guest-") {
		t.Errorf("guestId = %q, want the username's suffix of %q", s.GuestID, c.Username)
	}
	if s.AccessToken != "access-"+c.Username || s.RefreshToken != "refresh-"+c.Username || s.ExpiresIn != 300 {
		t.Errorf("tokens not passed through: %+v", s)
	}
	if want := rig.clock.Now().Add(60 * time.Minute); !s.ExpiresAt.Equal(want) {
		t.Errorf("expiresAt = %v, want %v", s.ExpiresAt, want)
	}
	if s.VisitorsFeedID != "" {
		t.Errorf("Start must leave visitorsFeedId to the handler, got %q", s.VisitorsFeedID)
	}
	if got := rig.kc.clientIDs; len(got) != 1 || got[0] != "sentinel-web" {
		t.Errorf("password grant client ids = %v, want [sentinel-web]", got)
	}

	g, ok := rig.guests.Lookup(c.ID)
	if !ok {
		t.Fatalf("Lookup(%q) found nothing", c.ID)
	}
	if g.UserID != c.ID || g.GuestID != s.GuestID || g.Username != c.Username || !g.ExpiresAt.Equal(s.ExpiresAt) {
		t.Errorf("Lookup = %+v, session %+v", g, s)
	}
	if rig.guests.Count() != 1 {
		t.Errorf("Count = %d, want 1", rig.guests.Count())
	}
	for _, e := range rig.logs.All() {
		if strings.Contains(fmt.Sprint(e.Message, e.ContextMap()), c.Password) {
			t.Errorf("log entry %q leaks the guest password", e.Message)
		}
	}
}

func TestStartRefusesAtCap(t *testing.T) {
	rig := newGuestRig(t, 2)
	for i := 0; i < 2; i++ {
		if _, err := rig.guests.Start(context.Background()); err != nil {
			t.Fatalf("Start %d: %v", i, err)
		}
	}
	if _, err := rig.guests.Start(context.Background()); !errors.Is(err, ErrFull) {
		t.Fatalf("third Start err = %v, want ErrFull", err)
	}
	if n := len(rig.kc.createdGuests()); n != 2 {
		t.Errorf("Keycloak saw %d creates, want 2 (the refused one never reaches it)", n)
	}
	// The handler half of this test lives in handler_test.go.
}

func TestStartCapIsRaceFree(t *testing.T) {
	rig := newGuestRig(t, 5)
	rig.kc.createDelay = 5 * time.Millisecond

	var ok, full atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := rig.guests.Start(context.Background())
			switch {
			case err == nil:
				ok.Add(1)
			case errors.Is(err, ErrFull):
				full.Add(1)
			default:
				t.Errorf("Start: %v", err)
			}
		}()
	}
	wg.Wait()
	if ok.Load() != 5 || full.Load() != 15 {
		t.Fatalf("ok=%d full=%d, want 5 and 15", ok.Load(), full.Load())
	}
	if n := len(rig.kc.createdGuests()); n != 5 {
		t.Errorf("Keycloak saw %d creates, want 5", n)
	}
	if rig.guests.Count() != 5 {
		t.Errorf("Count = %d, want 5", rig.guests.Count())
	}
}

func TestStartReleasesSlotWhenCreateFails(t *testing.T) {
	rig := newGuestRig(t, 1)
	rig.kc.createErr = errors.New("keycloak down")
	if _, err := rig.guests.Start(context.Background()); err == nil || errors.Is(err, ErrFull) {
		t.Fatalf("Start err = %v, want the create error", err)
	}
	if rig.guests.Count() != 0 {
		t.Fatalf("Count = %d after a failed create, want 0", rig.guests.Count())
	}
	rig.kc.mu.Lock()
	rig.kc.createErr = nil
	rig.kc.mu.Unlock()
	if _, err := rig.guests.Start(context.Background()); err != nil {
		t.Fatalf("Start after the failure: %v", err)
	}
}

func TestStartDeletesUserWhenGrantFails(t *testing.T) {
	rig := newGuestRig(t, 1)
	rig.kc.grantErr = errors.New("login failed")

	// The visitor hangs up while the account is being made; the grant then
	// fails, and cleanup must still run on a live context.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rig.kc.onCreate = cancel
	if _, err := rig.guests.Start(ctx); err == nil {
		t.Fatal("Start succeeded although the password grant failed")
	}
	created := rig.kc.createdGuests()
	if len(created) != 1 {
		t.Fatalf("created %d users, want 1", len(created))
	}
	if got := rig.kc.deletedIDs(); len(got) != 1 || got[0] != created[0].ID {
		t.Fatalf("deleted %v, want [%s]", got, created[0].ID)
	}
	rig.kc.mu.Lock()
	live := rig.kc.deleteCtxLive
	rig.kc.mu.Unlock()
	if len(live) != 1 || !live[0] {
		t.Error("cleanup ran on the cancelled request context or without a deadline")
	}
	if rig.guests.Count() != 0 {
		t.Fatalf("Count = %d after a failed grant, want 0", rig.guests.Count())
	}
	if _, ok := rig.guests.Lookup(created[0].ID); ok {
		t.Error("a guest whose grant failed is still registered")
	}
}

func TestStartKeycloakCallsSurviveCancelledRequest(t *testing.T) {
	rig := newGuestRig(t, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rig.kc.onCreate = cancel // the visitor hangs up mid-create

	if _, err := rig.guests.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	rig.kc.mu.Lock()
	create, grant := rig.kc.createCtxLive, rig.kc.grantCtxLive
	rig.kc.mu.Unlock()
	if len(create) != 1 || !create[0] || len(grant) != 1 || !grant[0] {
		t.Fatalf("Keycloak calls saw the cancelled request context: create=%v grant=%v", create, grant)
	}
	userID := rig.kc.createdGuests()[0].ID
	if _, ok := rig.guests.Lookup(userID); !ok {
		t.Fatal("the account Keycloak made is not registered, so nothing would ever delete it")
	}
}

func TestStartRefusesAlreadyCancelledRequest(t *testing.T) {
	rig := newGuestRig(t, 1)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := rig.guests.Start(ctx); err == nil {
		t.Fatal("Start succeeded for a request that was already gone")
	}
	if n := len(rig.kc.createdGuests()); n != 0 {
		t.Fatalf("Keycloak saw %d creates, want 0", n)
	}
	if rig.guests.Count() != 0 {
		t.Fatalf("Count = %d, want 0", rig.guests.Count())
	}
}

func TestLookupRefusesExpiredGuest(t *testing.T) {
	rig := newGuestRig(t, 1)
	if _, err := rig.guests.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	userID := rig.kc.createdGuests()[0].ID
	rig.clock.Advance(60 * time.Minute)
	if _, ok := rig.guests.Lookup(userID); ok {
		t.Fatal("Lookup returned a guest at its expiry, before any Sweep")
	}
	if rig.guests.Count() != 1 {
		t.Fatalf("Count = %d, want 1: the slot is held until Sweep deletes the account", rig.guests.Count())
	}
}

func TestSweepFreesTheSlot(t *testing.T) {
	rig := newGuestRig(t, 1)
	s, err := rig.guests.Start(context.Background())
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	userID := rig.kc.createdGuests()[0].ID

	// Not yet expired: nothing happens.
	rig.clock.Advance(59 * time.Minute)
	rig.guests.Sweep(context.Background())
	if rig.guests.Count() != 1 || len(rig.kc.deletedIDs()) != 0 {
		t.Fatalf("Sweep before the TTL removed the guest")
	}

	rig.clock.Advance(2 * time.Minute)
	rig.guests.Sweep(context.Background())

	if got := rig.kc.deletedIDs(); len(got) != 1 || got[0] != userID {
		t.Errorf("DeleteUser calls = %v, want [%s]", got, userID)
	}
	if got := rig.sites.removedIDs(); len(got) != 1 || got[0] != s.GuestID {
		t.Errorf("sites.Remove calls = %v, want [%s]", got, s.GuestID)
	}
	if got := rig.sockets.closedIDs(); len(got) != 1 || got[0] != userID {
		t.Errorf("CloseUser calls = %v, want [%s]", got, userID)
	}
	if rig.guests.Count() != 0 {
		t.Errorf("Count = %d after Sweep, want 0", rig.guests.Count())
	}
	if _, ok := rig.guests.Lookup(userID); ok {
		t.Error("swept guest is still registered")
	}
	if _, err := rig.guests.Start(context.Background()); err != nil {
		t.Fatalf("Start after Sweep: %v", err)
	}
}

func TestSweepRetriesFailedDelete(t *testing.T) {
	rig := newGuestRig(t, 2)
	for i := 0; i < 2; i++ {
		if _, err := rig.guests.Start(context.Background()); err != nil {
			t.Fatalf("Start: %v", err)
		}
	}
	rig.clock.Advance(61 * time.Minute)
	rig.kc.deleteErrs = 1 // the first delete fails, the second succeeds

	rig.guests.Sweep(context.Background())
	if got := len(rig.kc.deletedIDs()); got != 1 {
		t.Fatalf("deleted %d users, want 1 (the sweep must go on past a failure)", got)
	}
	if rig.guests.Count() != 1 {
		t.Fatalf("Count = %d, want 1: a guest still in Keycloak keeps its slot", rig.guests.Count())
	}
	// Its time is up either way: the site and sockets go now.
	if got := len(rig.sockets.closedIDs()); got != 2 {
		t.Errorf("closed sockets for %d users, want 2", got)
	}
	if got := len(rig.sites.removedIDs()); got != 2 {
		t.Errorf("removed %d sites, want 2", got)
	}

	rig.guests.Sweep(context.Background())
	if got := len(rig.kc.deletedIDs()); got != 2 {
		t.Fatalf("deleted %d users after the retry, want 2", got)
	}
	if rig.guests.Count() != 0 {
		t.Fatalf("Count = %d after the retry, want 0", rig.guests.Count())
	}
}

func TestReconcileDeletesLeftoverGuests(t *testing.T) {
	rig := newGuestRig(t, 25)
	rig.kc.withRole[auth.RoleGuest] = []registration.GuestUser{
		{ID: "old-1", Username: "guest-00000001"},
		{ID: "old-2", Username: "guest-00000002"},
	}
	rig.guests.Reconcile(context.Background())
	if got := rig.kc.deletedIDs(); strings.Join(got, ",") != "old-1,old-2" {
		t.Fatalf("deleted %v, want [old-1 old-2]", got)
	}
}

func TestReconcileSparesRegisteredGuests(t *testing.T) {
	rig := newGuestRig(t, 25)
	if _, err := rig.guests.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	live := rig.kc.createdGuests()[0]
	rig.kc.withRole[auth.RoleGuest] = []registration.GuestUser{
		{ID: live.ID, Username: live.Username},
		{ID: "old-1", Username: "guest-00000001"},
	}
	rig.guests.Reconcile(context.Background())
	if got := rig.kc.deletedIDs(); len(got) != 1 || got[0] != "old-1" {
		t.Fatalf("deleted %v, want [old-1]", got)
	}
}
