package stream

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"go.uber.org/zap"

	"github.com/sentinel/api-gateway/internal/auth"

	sentinelv1 "github.com/sentinel/proto-gen/sentinel/v1"

	"github.com/sentinel/go-common/kafka"
)

type presenceEvent struct {
	user   string
	online bool
}

type presenceRecorder struct {
	mu     sync.Mutex
	events []presenceEvent
}

func (r *presenceRecorder) record(user string, online bool) {
	r.mu.Lock()
	r.events = append(r.events, presenceEvent{user, online})
	r.mu.Unlock()
}

func (r *presenceRecorder) snapshot() []presenceEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]presenceEvent(nil), r.events...)
}

func identified(hub *Hub, id, user string, roles ...string) *Client {
	c := newClient(id, hub, nil, zap.NewNop())
	c.UserID = user
	c.Roles = roles
	return c
}

func isClosed(c *Client) bool {
	select {
	case <-c.done:
		return true
	default:
		return false
	}
}

func TestPresenceOnlineOnFirstSocketOfflineOnLast(t *testing.T) {
	hub := NewHub(zap.NewNop())
	rec := &presenceRecorder{}
	hub.SetPresenceListener(rec.record)

	a1 := identified(hub, "a1", "A")
	a2 := identified(hub, "a2", "A")
	hub.register(a1)
	hub.register(a2)
	if got := rec.snapshot(); len(got) != 1 || got[0] != (presenceEvent{"A", true}) {
		t.Fatalf("after two registers events = %+v, want one online", got)
	}

	hub.unregister(a1)
	if got := rec.snapshot(); len(got) != 1 {
		t.Fatalf("closing first socket fired %+v", got)
	}
	hub.unregister(a2)
	if got := rec.snapshot(); len(got) != 2 || got[1] != (presenceEvent{"A", false}) {
		t.Fatalf("after last close events = %+v, want online then offline", got)
	}

	// A repeated unregister of the same client must not fire again.
	hub.unregister(a2)
	if got := rec.snapshot(); len(got) != 2 {
		t.Fatalf("duplicate unregister fired %+v", got)
	}
}

func TestPresenceReconnectFiresAgain(t *testing.T) {
	hub := NewHub(zap.NewNop())
	rec := &presenceRecorder{}
	hub.SetPresenceListener(rec.record)
	c1 := identified(hub, "c1", "A")
	hub.register(c1)
	hub.unregister(c1)
	c2 := identified(hub, "c2", "A")
	hub.register(c2)
	want := []presenceEvent{{"A", true}, {"A", false}, {"A", true}}
	got := rec.snapshot()
	if len(got) != len(want) {
		t.Fatalf("events = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("events = %+v, want %+v", got, want)
		}
	}
}

func TestPresenceIgnoresEmptyUserID(t *testing.T) {
	hub := NewHub(zap.NewNop())
	rec := &presenceRecorder{}
	hub.SetPresenceListener(rec.record)
	c := identified(hub, "anon", "", "sentinel-guest")
	hub.register(c)
	if n := hub.UsersWithRole("sentinel-guest"); n != 0 {
		t.Errorf("empty user id counted as user: %d", n)
	}
	hub.unregister(c)
	if got := rec.snapshot(); len(got) != 0 {
		t.Errorf("empty user id fired presence: %+v", got)
	}
	if hub.ClientCount() != 0 {
		t.Errorf("client not unregistered")
	}
}

func TestPresenceListenerRunsOutsideHubLock(t *testing.T) {
	hub := NewHub(zap.NewNop())
	done := make(chan struct{})
	hub.SetPresenceListener(func(string, bool) {
		// Would deadlock if the hub lock were held (write lock vs RLock).
		_ = hub.ClientCount()
		_ = hub.UsersWithRole("x")
		close(done)
	})
	hub.register(identified(hub, "a", "A"))
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("listener deadlocked")
	}
}

func TestUsersWithRoleCountsDistinctUsers(t *testing.T) {
	hub := NewHub(zap.NewNop())
	hub.register(identified(hub, "a1", "A", "sentinel-guest"))
	hub.register(identified(hub, "a2", "A", "sentinel-guest"))
	hub.register(identified(hub, "b1", "B", "sentinel-guest"))
	hub.register(identified(hub, "c1", "C", "sentinel-admin"))
	if n := hub.UsersWithRole("sentinel-guest"); n != 2 {
		t.Errorf("guests = %d, want 2", n)
	}
	if n := hub.UsersWithRole("sentinel-admin"); n != 1 {
		t.Errorf("admins = %d, want 1", n)
	}
	if n := hub.UsersWithRole("nobody"); n != 0 {
		t.Errorf("unknown role = %d", n)
	}
}

func TestCloseUserClosesAllItsSockets(t *testing.T) {
	hub := NewHub(zap.NewNop())
	a1 := identified(hub, "a1", "A")
	a2 := identified(hub, "a2", "A")
	b := identified(hub, "b", "B")
	for _, c := range []*Client{a1, a2, b} {
		hub.register(c)
	}
	hub.CloseUser("A")
	if !isClosed(a1) || !isClosed(a2) {
		t.Error("A's sockets should be closed")
	}
	if isClosed(b) {
		t.Error("B's socket must stay open")
	}
}

func TestCloseUserIsIdempotentAndSafeForUnknownUsers(t *testing.T) {
	hub := NewHub(zap.NewNop())
	rec := &presenceRecorder{}
	hub.SetPresenceListener(rec.record)
	hub.CloseUser("ghost")
	hub.CloseUser("")
	a := identified(hub, "a", "A")
	hub.register(a)
	hub.CloseUser("A")
	hub.CloseUser("A")
	// The handler's deferred unregister runs when the socket ends.
	hub.unregister(a)
	hub.CloseUser("A")
	got := rec.snapshot()
	if len(got) != 2 || got[0] != (presenceEvent{"A", true}) || got[1] != (presenceEvent{"A", false}) {
		t.Fatalf("events = %+v, want exactly online then offline", got)
	}
}

func TestPresenceConcurrentChurnEndsConsistent(t *testing.T) {
	hub := NewHub(zap.NewNop())
	var mu sync.Mutex
	online := map[string]bool{}
	hub.SetPresenceListener(func(u string, on bool) {
		mu.Lock()
		online[u] = on
		mu.Unlock()
	})
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				c := identified(hub, "c", "A", "sentinel-guest")
				hub.register(c)
				_ = hub.UsersWithRole("sentinel-guest")
				hub.CloseUser("A")
				hub.unregister(c)
			}
		}()
	}
	wg.Wait()
	mu.Lock()
	defer mu.Unlock()
	if online["A"] {
		t.Error("A should be offline after all sockets closed")
	}
	if hub.UsersWithRole("sentinel-guest") != 0 {
		t.Error("no guests should remain")
	}
}

func TestLastEventAtZeroBeforeAnyEvent(t *testing.T) {
	hub := NewHub(zap.NewNop())
	if !hub.LastEventAt().IsZero() {
		t.Errorf("LastEventAt = %v, want zero", hub.LastEventAt())
	}
}

func TestLastEventAtAdvancesOnBroadcast(t *testing.T) {
	hub := NewHub(zap.NewNop())
	before := time.Now()
	hub.BroadcastBatch([]PositionedEvent{posEvent("e", 0, 0)})
	got := hub.LastEventAt()
	if got.IsZero() || got.Before(before) {
		t.Fatalf("LastEventAt = %v, want >= %v", got, before)
	}
	hub.BroadcastBatch(nil)
	if !hub.LastEventAt().Equal(got) {
		t.Error("an empty batch must not advance LastEventAt")
	}
	time.Sleep(2 * time.Millisecond)
	hub.BroadcastBatch([]PositionedEvent{posEvent("e", 0, 0)})
	if !hub.LastEventAt().After(got) {
		t.Error("second batch should advance LastEventAt")
	}
}

func TestBridgeEntityEventAdvancesLastEventAt(t *testing.T) {
	b, _ := newTestBridge(t)
	if !b.hub.LastEventAt().IsZero() {
		t.Fatal("LastEventAt should start zero")
	}
	// A bad payload is not an event.
	b.Handle(kafka.TopicEntityEvents, []byte("not a proto"))
	if !b.hub.LastEventAt().IsZero() {
		t.Fatal("undecodable payload advanced LastEventAt")
	}
	// Even an event that is later dropped (no position) proves the pipeline is alive.
	b.Handle(kafka.TopicEntityEvents, protoBytes(t, &sentinelv1.EntityEvent{EntityId: "e3",
		Event: &sentinelv1.EntityEvent_Updated{Updated: &sentinelv1.EntityUpdated{Entity: &sentinelv1.Entity{Id: "e3"}}}}))
	if b.hub.LastEventAt().IsZero() {
		t.Error("entity event did not advance LastEventAt")
	}
}

func TestHandlerRecordsUserAndCloseUserEndsRealSocket(t *testing.T) {
	hub := NewHub(zap.NewNop())
	rec := &presenceRecorder{}
	hub.SetPresenceListener(rec.record)
	h := NewHandler(hub, "*", zap.NewNop())
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := &auth.User{UserID: "guest-1", Roles: []string{"sentinel-guest"}}
		mux.ServeHTTP(w, r.WithContext(auth.WithUser(r.Context(), u)))
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http")+"/ws/entities", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	waitFor := func(what string, cond func() bool) {
		t.Helper()
		deadline := time.Now().Add(2 * time.Second)
		for !cond() {
			if time.Now().After(deadline) {
				t.Fatalf("timed out waiting for %s", what)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	waitFor("guest online", func() bool { return hub.UsersWithRole("sentinel-guest") == 1 })

	// The client must be reading to answer the close handshake.
	readErr := make(chan error, 1)
	go func() {
		for { // skip the connection ack and any other frames until the close
			if _, _, err := conn.Read(ctx); err != nil {
				readErr <- err
				return
			}
		}
	}()
	hub.CloseUser("guest-1")
	if err := <-readErr; err == nil {
		t.Fatal("expected the socket to be closed")
	}
	waitFor("guest offline", func() bool { return hub.ClientCount() == 0 })
	waitFor("offline event", func() bool { return len(rec.snapshot()) == 2 })
	got := rec.snapshot()
	if got[0] != (presenceEvent{"guest-1", true}) || got[1] != (presenceEvent{"guest-1", false}) {
		t.Fatalf("events = %+v", got)
	}
}
