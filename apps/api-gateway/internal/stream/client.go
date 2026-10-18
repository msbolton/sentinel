package stream

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/coder/websocket"
	"go.uber.org/zap"
)

const (
	sendBufferSize = 256
	writeTimeout   = 10 * time.Second
	maxMessageSize = 64 * 1024
)

// Client is one websocket connection with its viewport and subscriptions.
type Client struct {
	ID   string
	hub  *Hub
	conn *websocket.Conn
	send chan []byte
	log  *zap.Logger

	mu          sync.RWMutex
	viewport    Bounds
	hasViewport bool // false until the client sends its first viewport:update
	subs        map[string]struct{}

	// clearance is the user's classification rank, fixed at connect time;
	// events ranked above it are never sent, subscribed or not.
	clearance int

	closeOnce sync.Once
	done      chan struct{}
}

func newClient(id string, hub *Hub, conn *websocket.Conn, logger *zap.Logger) *Client {
	return &Client{
		ID:       id,
		hub:      hub,
		conn:     conn,
		send:     make(chan []byte, sendBufferSize),
		log:      logger.With(zap.String("clientId", id)),
		viewport: GlobalBounds,
		subs:     map[string]struct{}{},
		done:     make(chan struct{}),
	}
}

// Viewport returns the client's current bounds.
func (c *Client) Viewport() Bounds {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.viewport
}

// HasViewport reports whether the client has told us what it is looking at.
// Until then its viewport is the whole globe for filtering purposes only and
// must not widen the ingest area.
func (c *Client) HasViewport() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.hasViewport
}

func (c *Client) setViewport(b Bounds) {
	c.mu.Lock()
	c.viewport = b
	c.hasViewport = true
	c.mu.Unlock()
	if c.hub != nil {
		c.hub.viewportsChanged()
	}
}

func (c *Client) subscribe(entityID string) {
	c.mu.Lock()
	c.subs[entityID] = struct{}{}
	c.mu.Unlock()
}

func (c *Client) unsubscribe(entityID string) {
	c.mu.Lock()
	delete(c.subs, entityID)
	c.mu.Unlock()
}

// filter returns the events this client should receive: those in its
// viewport, unless the viewport is wider than detailMax (then the map draws
// counts), and those it subscribed to, whatever its viewport.
func (c *Client) filter(events []PositionedEvent, detailMax float64) []EntityEvent {
	c.mu.RLock()
	defer c.mu.RUnlock()
	inDetail := isDetail(c.viewport, detailMax)
	var out []EntityEvent
	for _, e := range events {
		if e.Rank > c.clearance {
			continue
		}
		if _, subscribed := c.subs[e.Event.Entity.ID]; subscribed || (inDetail && c.viewport.Contains(e.Lat, e.Lng)) {
			out = append(out, e.Event)
		}
	}
	return out
}

// trySend queues a frame without blocking. A client that cannot keep up is
// disconnected rather than stalling the broadcast.
func (c *Client) trySend(frame []byte) {
	select {
	case c.send <- frame:
	case <-c.done:
	default:
		c.log.Warn("ws send buffer full, dropping client")
		c.close(websocket.StatusPolicyViolation, "client too slow")
	}
}

func (c *Client) close(code websocket.StatusCode, reason string) {
	c.closeOnce.Do(func() {
		close(c.done)
		if c.conn != nil {
			_ = c.conn.Close(code, reason)
		}
	})
}

// sendTyped encodes and queues a frame.
func (c *Client) sendTyped(msgType string, data interface{}) {
	frame, err := Encode(msgType, data)
	if err != nil {
		c.log.Error("encode failed", zap.String("type", msgType), zap.Error(err))
		return
	}
	c.trySend(frame)
}

func (c *Client) sendError(code, message string) {
	c.sendTyped(MsgError, ErrorData{Code: code, Message: message})
}

// writeLoop drains the send queue to the socket.
func (c *Client) writeLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-c.done:
			return
		case frame := <-c.send:
			wctx, cancel := context.WithTimeout(ctx, writeTimeout)
			err := c.conn.Write(wctx, websocket.MessageText, frame)
			cancel()
			if err != nil {
				c.log.Debug("ws write failed", zap.Error(err))
				c.close(websocket.StatusAbnormalClosure, "write failed")
				return
			}
		}
	}
}

// readLoop processes inbound frames until the socket closes.
func (c *Client) readLoop(ctx context.Context) {
	c.conn.SetReadLimit(maxMessageSize)
	for {
		_, data, err := c.conn.Read(ctx)
		if err != nil {
			return
		}
		c.handleInbound(data)
	}
}

type viewportMessage = Bounds

type entityIDMessage struct {
	EntityID string `json:"entityId"`
}

// handleInbound dispatches one client frame.
func (c *Client) handleInbound(data []byte) {
	var env Envelope
	if err := json.Unmarshal(data, &env); err != nil || env.Type == "" {
		c.sendError("BAD_MESSAGE", "Expected {type, data} envelope")
		return
	}
	switch env.Type {
	case MsgViewportUpdate:
		var b viewportMessage
		if err := json.Unmarshal(env.Data, &b); err != nil || b.Validate() != nil {
			c.sendError("INVALID_VIEWPORT", ErrInvalidBounds.Error())
			return
		}
		c.setViewport(b)
		c.sendTyped(MsgViewportUpdated, map[string]interface{}{"success": true, "viewport": b})
	case MsgEntitySubscribe, MsgEntityUnsubscribe:
		var m entityIDMessage
		if err := json.Unmarshal(env.Data, &m); err != nil || m.EntityID == "" {
			c.sendError("INVALID_SUBSCRIPTION", "entityId is required")
			return
		}
		if env.Type == MsgEntitySubscribe {
			c.subscribe(m.EntityID)
			c.sendTyped(MsgEntitySubscribed, map[string]interface{}{"entityId": m.EntityID, "subscribed": true})
		} else {
			c.unsubscribe(m.EntityID)
			c.sendTyped(MsgEntityUnsubscribed, map[string]interface{}{"entityId": m.EntityID, "subscribed": false})
		}
	case MsgPing:
		c.sendTyped(MsgPong, nil)
	default:
		c.sendError("UNKNOWN_TYPE", "Unknown message type: "+env.Type)
	}
}
