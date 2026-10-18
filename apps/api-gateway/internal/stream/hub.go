package stream

import (
	"sync"

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
	// detailMax is the largest viewport (sq deg) that gets live positions
	// and steers ingestion; 0 means no limit.
	detailMax float64
}

// NewHub creates an empty hub.
func NewHub(logger *zap.Logger) *Hub {
	return &Hub{clients: map[*Client]struct{}{}, logger: logger}
}

func (h *Hub) register(c *Client) {
	h.mu.Lock()
	h.clients[c] = struct{}{}
	n := len(h.clients)
	h.mu.Unlock()
	h.logger.Info("ws client connected", zap.String("clientId", c.ID), zap.Int("clients", n))
}

func (h *Hub) unregister(c *Client) {
	h.mu.Lock()
	_, present := h.clients[c]
	delete(h.clients, c)
	n := len(h.clients)
	h.mu.Unlock()
	if present {
		h.logger.Info("ws client disconnected", zap.String("clientId", c.ID), zap.Int("clients", n))
		h.viewportsChanged()
	}
}

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

// SetDetailMaxArea sets the largest viewport, in square degrees, that gets
// live entity positions and steers ingestion; 0 removes the limit. A wider
// view (a continent or the globe) draws counts from the entity service.
func (h *Hub) SetDetailMaxArea(sqDeg float64) {
	h.mu.Lock()
	h.detailMax = sqDeg
	h.mu.Unlock()
}

// DetailMaxArea returns the limit set by SetDetailMaxArea.
func (h *Hub) DetailMaxArea() float64 {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.detailMax
}

// isDetail reports whether a viewport is small enough for live positions.
func isDetail(b Bounds, max float64) bool {
	return max <= 0 || BoundsAreaSqDeg(b) <= max
}

// DetailViewports returns the viewports small enough to steer ingestion:
// one client looking at the globe must not turn every feed into a global
// full-rate poll.
func (h *Hub) DetailViewports() []Bounds {
	max := h.DetailMaxArea()
	var out []Bounds
	for _, b := range h.Viewports() {
		if isDetail(b, max) {
			out = append(out, b)
		}
	}
	return out
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
	max := h.DetailMaxArea()
	for _, c := range h.snapshot() {
		matching := c.filter(events, max)
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
