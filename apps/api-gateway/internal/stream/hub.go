package stream

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"go.uber.org/zap"
)

// PositionedEvent is an entity event plus the coordinates used for viewport filtering.
type PositionedEvent struct {
	Event EntityEvent
	Lat   float64
	Lng   float64
	// Rank is the entity's classification rank; a client sees the event only
	// when its clearance rank is at least this.
	Rank int
}

// Hub tracks connected clients and fans out messages.
type Hub struct {
	mu      sync.RWMutex
	clients map[*Client]struct{}
	logger  *zap.Logger

	viewportListener func() // called when the set of client viewports may have changed

	// userSockets counts the open sockets per authenticated user; it is
	// guarded by mu. Sockets without a UserID are not counted.
	userSockets      map[string]int
	presenceListener func(userID string, online bool)
	// presenceMu serialises identified register/unregister so presence
	// callbacks reach the listener in the order the transitions happened.
	// It is taken before mu and never held while mu is held by the caller.
	presenceMu sync.Mutex

	lastEventNanos atomic.Int64 // unix nanoseconds of the latest live (non-exercise) entity event; 0 = none yet
}

// NewHub creates an empty hub.
func NewHub(logger *zap.Logger) *Hub {
	return &Hub{clients: map[*Client]struct{}{}, userSockets: map[string]int{}, logger: logger}
}

func (h *Hub) register(c *Client) {
	if c.UserID != "" {
		h.presenceMu.Lock()
		defer h.presenceMu.Unlock()
	}
	h.mu.Lock()
	_, already := h.clients[c]
	h.clients[c] = struct{}{}
	n := len(h.clients)
	first := false
	if c.UserID != "" && !already {
		h.userSockets[c.UserID]++
		first = h.userSockets[c.UserID] == 1
	}
	fn := h.presenceListener
	h.mu.Unlock()
	h.logger.Info("ws client connected", zap.String("clientId", c.ID), zap.Int("clients", n))
	if first && fn != nil {
		fn(c.UserID, true)
	}
}

func (h *Hub) unregister(c *Client) {
	if c.UserID != "" {
		h.presenceMu.Lock()
		defer h.presenceMu.Unlock()
	}
	h.mu.Lock()
	_, present := h.clients[c]
	delete(h.clients, c)
	n := len(h.clients)
	last := false
	if present && c.UserID != "" {
		if h.userSockets[c.UserID] <= 1 {
			delete(h.userSockets, c.UserID)
			last = true
		} else {
			h.userSockets[c.UserID]--
		}
	}
	fn := h.presenceListener
	h.mu.Unlock()
	if present {
		h.logger.Info("ws client disconnected", zap.String("clientId", c.ID), zap.Int("clients", n))
		h.viewportsChanged()
	}
	if last && fn != nil {
		fn(c.UserID, false)
	}
}

// SetPresenceListener registers a callback fired with online=true when a
// user's first socket connects and online=false when their last one goes
// away, however it ends. Calls are serialised and ordered per hub, made
// outside the hub's state lock, but the callback must not block or call back
// into register/unregister. Sockets without a user id never fire it.
func (h *Hub) SetPresenceListener(fn func(userID string, online bool)) {
	h.mu.Lock()
	h.presenceListener = fn
	h.mu.Unlock()
}

// UsersWithRole returns how many distinct connected users hold role.
func (h *Hub) UsersWithRole(role string) int {
	users := map[string]struct{}{}
	for _, c := range h.snapshot() {
		if c.UserID == "" {
			continue
		}
		for _, r := range c.Roles {
			if r == role {
				users[c.UserID] = struct{}{}
				break
			}
		}
	}
	return len(users)
}

// CloseUser closes every socket of userID. It is idempotent and a no-op for
// an unknown or empty user; each socket's handler unregisters it as it ends,
// which is what fires the offline presence event.
func (h *Hub) CloseUser(userID string) {
	if userID == "" {
		return
	}
	var wg sync.WaitGroup
	for _, c := range h.snapshot() {
		if c.UserID != userID {
			continue
		}
		wg.Add(1)
		go func() { // the close handshake can block on a dead peer
			defer wg.Done()
			c.close(websocket.StatusPolicyViolation, "session ended")
		}()
	}
	wg.Wait()
}

// LastEventAt returns when the bridge last saw a live entity event, one that
// is not an exercise, or the zero time if it has seen none. The sensor
// simulators' exercise traffic never stops, so it does not count.
func (h *Hub) LastEventAt() time.Time {
	n := h.lastEventNanos.Load()
	if n == 0 {
		return time.Time{}
	}
	return time.Unix(0, n)
}

// noteEvent records that a live entity event just arrived.
func (h *Hub) noteEvent() { h.lastEventNanos.Store(time.Now().UnixNano()) }

// SetViewportListener registers a callback invoked whenever a client sends
// a viewport or disconnects. The callback must not block.
func (h *Hub) SetViewportListener(fn func()) {
	h.mu.Lock()
	h.viewportListener = fn
	h.mu.Unlock()
}

func (h *Hub) viewportsChanged() {
	h.mu.RLock()
	fn := h.viewportListener
	h.mu.RUnlock()
	if fn != nil {
		fn()
	}
}

// Viewports returns the bounds of every client that has sent one.
func (h *Hub) Viewports() []Bounds {
	var out []Bounds
	for _, c := range h.snapshot() {
		if c.HasViewport() {
			out = append(out, c.Viewport())
		}
	}
	return out
}

// ClientCount returns the number of connected clients.
func (h *Hub) ClientCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}

func (h *Hub) snapshot() []*Client {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]*Client, 0, len(h.clients))
	for c := range h.clients {
		out = append(out, c)
	}
	return out
}

// BroadcastBatch sends each client the subset of events inside its viewport
// or among its explicit subscriptions, as one entity:batch frame.
func (h *Hub) BroadcastBatch(events []PositionedEvent) {
	if len(events) == 0 {
		return
	}
	sent := 0
	for _, c := range h.snapshot() {
		matching := c.filter(events)
		if len(matching) == 0 {
			continue
		}
		frame, err := Encode(MsgEntityBatch, matching)
		if err != nil {
			h.logger.Error("encode batch failed", zap.Error(err))
			return
		}
		c.trySend(frame)
		sent++
	}
	h.logger.Debug("broadcast batch", zap.Int("events", len(events)), zap.Int("clients", sent))
}

// BroadcastAll sends one frame to every client regardless of viewport.
func (h *Hub) BroadcastAll(msgType string, data interface{}) {
	frame, err := Encode(msgType, data)
	if err != nil {
		h.logger.Error("encode broadcast failed", zap.String("type", msgType), zap.Error(err))
		return
	}
	for _, c := range h.snapshot() {
		c.trySend(frame)
	}
}

// BroadcastRanked sends a frame only to clients cleared to at least rank.
func (h *Hub) BroadcastRanked(msgType string, data interface{}, rank int) {
	frame, err := Encode(msgType, data)
	if err != nil {
		h.logger.Error("encode broadcast failed", zap.String("type", msgType), zap.Error(err))
		return
	}
	for _, c := range h.snapshot() {
		if c.clearance >= rank {
			c.trySend(frame)
		}
	}
}

// BroadcastRaw sends a frame whose data is already-encoded JSON.
func (h *Hub) BroadcastRaw(msgType string, data []byte) {
	h.BroadcastAll(msgType, rawJSON(data))
}

type rawJSON []byte

func (r rawJSON) MarshalJSON() ([]byte, error) {
	if len(r) == 0 {
		return []byte("null"), nil
	}
	return r, nil
}
