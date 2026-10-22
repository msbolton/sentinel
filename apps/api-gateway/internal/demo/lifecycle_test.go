package demo

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// A guest holds its slot only while someone is exploring with it: these
// options are the production ones (see cmd/server/demo.go).
func liveSlots(o *GuestOptions) {
	o.OfflineAfter = 5 * time.Minute
	o.ConnectWithin = 2 * time.Minute
	o.PerAddress = 2
}

func (r *guestRig) startFrom(t *testing.T, addr string) (Session, string) {
	t.Helper()
	s, err := r.guests.Start(context.Background(), addr)
	if err != nil {
		t.Fatalf("Start(%q): %v", addr, err)
	}
	created := r.kc.createdGuests()
	return s, created[len(created)-1].ID
}

func (r *guestRig) assertEnded(t *testing.T, userID, guestID string) {
	t.Helper()
	if got := r.kc.deletedIDs(); !contains(got, userID) {
		t.Errorf("DeleteUser calls = %v, want %s among them", got, userID)
	}
	if got := r.sockets.closedIDs(); !contains(got, userID) {
		t.Errorf("CloseUser calls = %v, want %s among them", got, userID)
	}
	if got := r.sites.removedIDs(); !contains(got, guestID) {
		t.Errorf("sites.Remove calls = %v, want %s among them", got, guestID)
	}
	if _, ok := r.guests.Lookup(userID); ok {
		t.Errorf("ended guest %s is still live", userID)
	}
}

func (r *guestRig) assertLive(t *testing.T, userID string) {
	t.Helper()
	if _, ok := r.guests.Lookup(userID); !ok {
		t.Fatalf("guest %s was ended early", userID)
	}
	if got := r.kc.deletedIDs(); contains(got, userID) {
		t.Fatalf("guest %s was deleted from Keycloak early", userID)
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func TestSweepEndsGuestOfflineForRemoveAfter(t *testing.T) {
	rig := newGuestRig(t, 25, liveSlots)
	s, userID := rig.startFrom(t, "203.0.113.7")
	if _, ok := rig.guests.SetOnline(userID, true); !ok {
		t.Fatal("SetOnline did not find the new guest")
	}
	rig.clock.Advance(20 * time.Minute) // exploring: no reap however long
	rig.guests.Sweep(context.Background())
	rig.assertLive(t, userID)

	if _, ok := rig.guests.SetOnline(userID, false); !ok {
		t.Fatal("SetOnline(false) did not find the guest")
	}
	rig.clock.Advance(5*time.Minute - time.Second)
	rig.guests.Sweep(context.Background())
	rig.assertLive(t, userID)

	rig.clock.Advance(time.Second)
	rig.guests.Sweep(context.Background())
	rig.assertEnded(t, userID, s.GuestID)
	if rig.guests.Count() != 0 {
		t.Fatalf("Count = %d, want 0: the slot is free once nobody explores with it", rig.guests.Count())
	}
}

func TestSweepKeepsGuestThatCameBack(t *testing.T) {
	rig := newGuestRig(t, 25, liveSlots)
	_, userID := rig.startFrom(t, "203.0.113.7")
	rig.guests.SetOnline(userID, true)
	rig.guests.SetOnline(userID, false)
	rig.clock.Advance(4 * time.Minute)
	rig.guests.SetOnline(userID, true)
	rig.clock.Advance(10 * time.Minute)
	rig.guests.Sweep(context.Background())
	rig.assertLive(t, userID)
	// Offline again: the clock restarts from the new disconnect.
	rig.guests.SetOnline(userID, false)
	rig.clock.Advance(4 * time.Minute)
	rig.guests.Sweep(context.Background())
	rig.assertLive(t, userID)
}

func TestSweepReapsGuestThatNeverConnects(t *testing.T) {
	rig := newGuestRig(t, 25, liveSlots)
	s, userID := rig.startFrom(t, "203.0.113.7")
	rig.clock.Advance(2*time.Minute - time.Second)
	rig.guests.Sweep(context.Background())
	rig.assertLive(t, userID)

	rig.clock.Advance(time.Second)
	rig.guests.Sweep(context.Background())
	rig.assertEnded(t, userID, s.GuestID)
	if rig.guests.Count() != 0 {
		t.Fatalf("Count = %d, want 0", rig.guests.Count())
	}
}

func TestSetOnlineIgnoresUnknownAndEndingGuests(t *testing.T) {
	rig := newGuestRig(t, 25, liveSlots)
	if _, ok := rig.guests.SetOnline("analyst-7", true); ok {
		t.Fatal("SetOnline found a guest for a user the registry never made")
	}
	_, userID := rig.startFrom(t, "203.0.113.7")
	rig.clock.Advance(61 * time.Minute)
	if _, ok := rig.guests.SetOnline(userID, true); ok {
		t.Fatal("SetOnline returned an expired guest as live")
	}
}

func TestStartCapsLiveGuestsPerAddress(t *testing.T) {
	rig := newGuestRig(t, 25, liveSlots)
	_, first := rig.startFrom(t, "203.0.113.7")
	rig.startFrom(t, "203.0.113.7")
	if _, err := rig.guests.Start(context.Background(), "203.0.113.7"); !errors.Is(err, ErrAddressBusy) {
		t.Fatalf("third Start from one address: err = %v, want ErrAddressBusy", err)
	}
	if n := len(rig.kc.createdGuests()); n != 2 {
		t.Errorf("Keycloak saw %d creates, want 2 (the refused one never reaches it)", n)
	}
	// Another address is unaffected.
	rig.startFrom(t, "198.51.100.1")

	// Once one of the first two ends, that address may start again.
	rig.clock.Advance(2 * time.Minute) // `first` never connected
	rig.guests.SetOnline(rig.kc.createdGuests()[1].ID, true)
	rig.guests.SetOnline(rig.kc.createdGuests()[2].ID, true)
	rig.guests.Sweep(context.Background())
	if _, ok := rig.guests.Lookup(first); ok {
		t.Fatal("the guest that never connected was not reaped")
	}
	rig.startFrom(t, "203.0.113.7")
}

func TestStartPerAddressCapIsRaceFree(t *testing.T) {
	rig := newGuestRig(t, 25, liveSlots)
	rig.kc.createDelay = 5 * time.Millisecond
	var ok, busy atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := rig.guests.Start(context.Background(), "2001:db8:1:2::1")
			switch {
			case err == nil:
				ok.Add(1)
			case errors.Is(err, ErrAddressBusy):
				busy.Add(1)
			default:
				t.Errorf("Start: %v", err)
			}
		}()
	}
	wg.Wait()
	if ok.Load() != 2 || busy.Load() != 8 {
		t.Fatalf("ok=%d busy=%d, want 2 and 8", ok.Load(), busy.Load())
	}
}

func TestStartPerAddressCapReleasedOnFailure(t *testing.T) {
	rig := newGuestRig(t, 25, liveSlots)
	rig.kc.createErr = errors.New("keycloak down")
	for i := 0; i < 3; i++ {
		if _, err := rig.guests.Start(context.Background(), "203.0.113.7"); err == nil || errors.Is(err, ErrAddressBusy) {
			t.Fatalf("Start %d: err = %v, want the Keycloak failure", i, err)
		}
	}
	rig.kc.createErr = nil
	rig.startFrom(t, "203.0.113.7")
}

func TestSessionAnswers429WhenAddressHasTwoLiveGuests(t *testing.T) {
	rig := newHandlerRig(t, 25, nil, nil, liveSlots)
	for i := 0; i < 2; i++ {
		if rec := rig.postSession(t, "198.51.100.9, 203.0.113.7"); rec.Code != http.StatusOK {
			t.Fatalf("session %d: status %d", i, rec.Code)
		}
	}
	rec := rig.postSession(t, "192.0.2.1, 203.0.113.7")
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("third session from one address: status %d, want 429", rec.Code)
	}
	if got := decodeResponse(t, rec); got["error"] != "busy" || !strings.Contains(got["message"].(string), "try again in a moment") {
		t.Fatalf("body = %s, want {\"error\":\"busy\",\"message\":\"…try again in a moment…\"}", rec.Body)
	}
	// One IPv6 /64 is one household: other addresses in it share the cap.
	for _, ip := range []string{"2001:db8:1:2::1", "2001:db8:1:2:aaaa::2"} {
		if rec := rig.postSession(t, ip); rec.Code != http.StatusOK {
			t.Fatalf("session from %s: status %d", ip, rec.Code)
		}
	}
	if rec := rig.postSession(t, "2001:db8:1:2:ffff:ffff:ffff:ffff"); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("third session from one /64: status %d, want 429", rec.Code)
	}
	if rec := rig.postSession(t, "2001:db8:1:3::1"); rec.Code != http.StatusOK {
		t.Fatalf("session from another /64: status %d, want 200", rec.Code)
	}
}

// The whole of a guest's life: granted, a socket opens, a position is
// placed, the socket closes, and RemoveAfter later the slot is free again.
func TestGuestLifecycleFreesSlotAfterLeaving(t *testing.T) {
	rig := newHandlerRig(t, 1, nil, nil, liveSlots)
	rec := rig.postSession(t, "203.0.113.7")
	if rec.Code != http.StatusOK {
		t.Fatalf("session: status %d", rec.Code)
	}
	c := rig.kc.createdGuests()[0]
	guestID := strings.TrimPrefix(c.Username, "guest-")
	u := guestUser(c)

	// The demo is full with this one guest.
	if rec := rig.postSession(t, "198.51.100.1"); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("second session: status %d, want 503 full", rec.Code)
	}

	rig.clock.Advance(30 * time.Second)
	if _, ok := rig.guests.SetOnline(c.ID, true); !ok {
		t.Fatal("socket online: guest not found")
	}
	if rec := rig.postPosition(t, u, `{"lat":51.55,"lon":-0.15}`, ""); rec.Code != http.StatusOK {
		t.Fatalf("position: status %d, body %s", rec.Code, rec.Body)
	}
	if got := rig.sites.placements(); len(got) != 1 || got[0].GuestID != guestID {
		t.Fatalf("placements = %+v", got)
	}

	rig.clock.Advance(10 * time.Minute)
	rig.guests.SetOnline(c.ID, false)
	rig.clock.Advance(4 * time.Minute)
	rig.guests.Sweep(context.Background())
	rig.assertLive(t, c.ID)

	rig.clock.Advance(time.Minute)
	rig.guests.Sweep(context.Background())
	rig.assertEnded(t, c.ID, guestID)
	if ops := rig.sites.opLog(); ops[len(ops)-1] != "remove:"+guestID {
		t.Fatalf("site ops = %v, want the removal last", ops)
	}
	if rec := rig.postSession(t, "198.51.100.1"); rec.Code != http.StatusOK {
		t.Fatalf("session after the slot was freed: status %d, want 200", rec.Code)
	}
}
