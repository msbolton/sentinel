package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap/zaptest"

	"github.com/sentinel/api-gateway/internal/auth"
	"github.com/sentinel/api-gateway/internal/config"
	"github.com/sentinel/api-gateway/internal/demo"
)

type fakePresenceSites struct {
	mu     sync.Mutex
	events []string
}

func (f *fakePresenceSites) Online(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, "on:"+id)
}

func (f *fakePresenceSites) Offline(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, "off:"+id)
}

func TestPresenceListenerTranslatesGuestsAndIgnoresOthers(t *testing.T) {
	guests := map[string]demo.Guest{"kc-user-1": {UserID: "kc-user-1", GuestID: "ab12cd34"}}
	var seen []string
	setOnline := func(userID string, online bool) (demo.Guest, bool) {
		seen = append(seen, fmt.Sprintf("%s:%v", userID, online))
		g, ok := guests[userID]
		return g, ok
	}
	sites := &fakePresenceSites{}
	listen := presenceListener(setOnline, sites)

	listen("kc-user-1", true)
	listen("analyst-7", true)
	listen("analyst-7", false)
	listen("kc-user-1", false)

	want := []string{"on:ab12cd34", "off:ab12cd34"}
	if len(sites.events) != len(want) || sites.events[0] != want[0] || sites.events[1] != want[1] {
		t.Fatalf("events = %v, want %v", sites.events, want)
	}
	// The registry hears every transition, so it can end a guest that left.
	if got := strings.Join(seen, ","); got != "kc-user-1:true,analyst-7:true,analyst-7:false,kc-user-1:false" {
		t.Fatalf("registry saw %s", got)
	}
}

func TestDemoGuestOptionsFreeSlotsOfVisitorsWhoLeft(t *testing.T) {
	cfg := config.DemoConfig{MaxGuests: 25, GuestTTL: time.Hour, OfflineRemoveAfter: 5 * time.Minute}
	o := demoGuestOptions(cfg)
	if o.Max != 25 || o.TTL != time.Hour {
		t.Errorf("Max/TTL = %d/%v, want 25/1h", o.Max, o.TTL)
	}
	if o.OfflineAfter != 5*time.Minute {
		t.Errorf("OfflineAfter = %v, want DEMO_OFFLINE_REMOVE_AFTER (5m)", o.OfflineAfter)
	}
	if o.ConnectWithin != 2*time.Minute {
		t.Errorf("ConnectWithin = %v, want 2m", o.ConnectWithin)
	}
	if o.PerAddress != 2 {
		t.Errorf("PerAddress = %d, want 2", o.PerAddress)
	}
}

func TestRetryUntilSuccessBacksOff(t *testing.T) {
	var calls atomic.Int32
	var delays []time.Duration
	sleep := func(ctx context.Context, d time.Duration) bool {
		delays = append(delays, d)
		return true
	}
	ok := retryUntilSuccess(context.Background(), zaptest.NewLogger(t), "thing", time.Second, 4*time.Second, sleep,
		func(context.Context) error {
			if calls.Add(1) < 5 {
				return errors.New("not yet")
			}
			return nil
		})
	if !ok || calls.Load() != 5 {
		t.Fatalf("ok = %v after %d calls, want true after 5", ok, calls.Load())
	}
	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 4 * time.Second}
	if len(delays) != len(want) {
		t.Fatalf("delays = %v, want %v", delays, want)
	}
	for i := range want {
		if delays[i] != want[i] {
			t.Fatalf("delays = %v, want %v", delays, want)
		}
	}
}

func TestRetryUntilSuccessStopsWhenCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	done := make(chan bool)
	go func() {
		done <- retryUntilSuccess(ctx, zaptest.NewLogger(t), "thing", time.Hour, time.Hour, sleepCtx,
			func(context.Context) error { calls++; return errors.New("down") })
	}()
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case ok := <-done:
		if ok || calls != 1 {
			t.Fatalf("ok = %v after %d calls, want false after 1", ok, calls)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("retry did not stop on cancel")
	}
}

func TestSessionGateRefusesUntilReady(t *testing.T) {
	var ready atomic.Bool
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	h := gateUntilReady(&ready, inner)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/demo/session", nil))
	if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("before ready: %d, Retry-After %q", rec.Code, rec.Header().Get("Retry-After"))
	}

	ready.Store(true)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/demo/session", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("after ready: %d", rec.Code)
	}
}

type fakeStatusHub struct{ last time.Time }

func (fakeStatusHub) UsersWithRole(role string) int {
	if role == "sentinel-guest" {
		return 3
	}
	return 99
}
func (h fakeStatusHub) LastEventAt() time.Time { return h.last }

func TestDemoStatusReadsHubAndGuests(t *testing.T) {
	var ready atomic.Bool
	last := time.Unix(1_800_000_000, 0)
	s := demoStatus{hub: fakeStatusHub{last: last}, guests: func() int { return 7 }, ready: &ready}
	if s.Exploring() != 3 || s.Guests() != 7 {
		t.Fatalf("exploring %d, guests %d", s.Exploring(), s.Guests())
	}
	if !s.LastEventAt().IsZero() {
		t.Fatal("the demo must not report up before its startup reconcile is done")
	}
	ready.Store(true)
	if !s.LastEventAt().Equal(last) {
		t.Fatalf("LastEventAt = %v, want %v", s.LastEventAt(), last)
	}
}

func TestAdmitStreamRefusesGuestsTheRegistryDoesNotKnow(t *testing.T) {
	known := map[string]bool{"kc-live": true}
	admit := admitStream(func(userID string) (demo.Guest, bool) {
		return demo.Guest{UserID: userID}, known[userID]
	})
	cases := []struct {
		user *auth.User
		want bool
	}{
		{&auth.User{UserID: "kc-live", Roles: []string{auth.RoleViewer, auth.RoleGuest}}, true},
		// A guest from before a restart, or one already ended: valid token, no session.
		{&auth.User{UserID: "kc-zombie", Roles: []string{auth.RoleViewer, auth.RoleGuest}}, false},
		// Everyone else is not the demo's business.
		{&auth.User{UserID: "analyst-7", Roles: []string{auth.RoleAnalyst}}, true},
		{&auth.User{UserID: "viewer-1", Roles: []string{auth.RoleViewer}}, true},
	}
	for _, c := range cases {
		if got := admit(c.user); got != c.want {
			t.Errorf("admit(%s %v) = %v, want %v", c.user.UserID, c.user.Roles, got, c.want)
		}
	}
}
