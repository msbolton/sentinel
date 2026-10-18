package aisstream

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"net/http"
	"time"

	gws "github.com/gorilla/websocket"
	"go.uber.org/zap"

	"github.com/sentinel/ingest-service/internal/aoi"
	"github.com/sentinel/ingest-service/internal/source/transport"
)

// Connection timing. aisstream.io closes a connection that subscribes
// later than 3 s after connecting or more often than once a second.
var (
	writeTimeout       = 2 * time.Second
	confirmWait        = 5 * time.Second
	minUpdateInterval  = 2 * time.Second
	pingInterval       = 30 * time.Second
	maxBackoff         = time.Minute
	stableConnection   = time.Minute
	handshakeTimeout   = 10 * time.Second
	maxMessageBytes    = int64(1 << 20)
	errNotConfirmed    = errors.New("subscription not confirmed")
	confirmationMarker = []byte(`"SubscriptionConfirmation"`)
)

// subscription is the message aisstream.io expects. Boxes are corner
// pairs of [latitude, longitude].
type subscription struct {
	APIKey             string          `json:"APIKey"`
	BoundingBoxes      [][2][2]float64 `json:"BoundingBoxes"`
	FiltersShipMMSI    []string        `json:"FiltersShipMMSI,omitempty"`
	FilterMessageTypes []string        `json:"FilterMessageTypes,omitempty"`
}

// key identifies what a subscription asks for, without the API key.
func (s subscription) key() string {
	s.APIKey = ""
	b, _ := json.Marshal(s)
	return string(b)
}

func (d *Driver) subscription(boxes [][2][2]float64, mmsi []string) subscription {
	return subscription{APIKey: d.apiKey, BoundingBoxes: boxes, FiltersShipMMSI: mmsi, FilterMessageTypes: d.types}
}

func globalBoxes() [][2][2]float64 {
	return [][2][2]float64{{{-90, -180}, {90, 180}}}
}

// roundBox turns a box into aisstream.io's corner pair, rounded outward to
// a tenth of a degree.
func roundBox(b aoi.BBox) [2][2]float64 {
	down := func(v, lo float64) float64 { return math.Max(lo, math.Floor(v*10)/10) }
	up := func(v, hi float64) float64 { return math.Min(hi, math.Ceil(v*10)/10) }
	return [2][2]float64{{down(b.South, -90), down(b.West, -180)}, {up(b.North, 90), up(b.East, 180)}}
}

// connState is what Health reports about one connection; guarded by
// Driver.mu.
type connState struct {
	connected     bool
	confirmed     bool
	compression   bool
	lastError     string
	connects      int
	subscriptions int
	connectedAt   time.Time
}

func (c *connState) detail() map[string]any {
	m := map[string]any{
		"connected":     c.connected,
		"confirmed":     c.confirmed,
		"compression":   c.compression,
		"connects":      c.connects,
		"subscriptions": c.subscriptions,
	}
	if c.lastError != "" {
		m["lastError"] = c.lastError
	}
	if !c.connectedAt.IsZero() {
		m["connectedAt"] = c.connectedAt.UTC().Format(time.RFC3339)
	}
	return m
}

// wsConn is the part of a WebSocket connection the driver uses.
type wsConn interface {
	WriteMessage(messageType int, data []byte) error
	WriteControl(messageType int, data []byte, deadline time.Time) error
	ReadMessage() (int, []byte, error)
	SetWriteDeadline(t time.Time) error
	SetReadLimit(limit int64)
	Close() error
}

type dialFunc func(ctx context.Context, url string) (wsConn, error)

// gorillaDial connects with permessage-deflate offered: aisstream.io
// limits the bandwidth of uncompressed connections.
func gorillaDial(ctx context.Context, url string) (wsConn, error) {
	dialer := gws.Dialer{HandshakeTimeout: handshakeTimeout, EnableCompression: true, Proxy: http.ProxyFromEnvironment}
	c, resp, err := dialer.DialContext(ctx, url, nil)
	if resp != nil && resp.Body != nil {
		resp.Body.Close()
	}
	if err != nil {
		if resp != nil {
			return nil, fmt.Errorf("dial: %w (HTTP %d)", err, resp.StatusCode)
		}
		return nil, fmt.Errorf("dial: %w", err)
	}
	return c, nil
}

// conn is one subscribed connection that reconnects until its context
// ends. The latest subscription handed to update is sent on connect and
// whenever it changes, never more often than minUpdateInterval.
type conn struct {
	d     *Driver
	name  string
	subCh chan subscription
}

func (d *Driver) newConn(name string) *conn {
	d.mu.Lock()
	d.conns[name] = &connState{}
	d.mu.Unlock()
	return &conn{d: d, name: name, subCh: make(chan subscription, 1)}
}

// update replaces the subscription waiting to be sent.
func (c *conn) update(s subscription) {
	for {
		select {
		case c.subCh <- s:
			return
		default:
			select {
			case <-c.subCh:
			default:
			}
		}
	}
}

func (c *conn) state(fn func(*connState)) {
	c.d.mu.Lock()
	if s := c.d.conns[c.name]; s != nil {
		fn(s)
	}
	c.d.mu.Unlock()
}

// run waits for the first subscription, then connects, reconnecting with
// jittered exponential backoff, until ctx ends.
func (c *conn) run(ctx context.Context) {
	var sub subscription
	select {
	case <-ctx.Done():
		return
	case sub = <-c.subCh:
	}
	backoff := time.Duration(0)
	for ctx.Err() == nil {
		started := c.d.now()
		err := c.session(ctx, &sub)
		if ctx.Err() != nil {
			return
		}
		msg := "connection closed"
		if err != nil {
			msg = err.Error()
		}
		c.state(func(s *connState) { s.connected, s.confirmed, s.lastError = false, false, msg })
		c.d.logger.Warn("aisstream connection lost", zap.String("connection", c.name), zap.String("error", msg))
		if c.d.now().Sub(started) >= stableConnection {
			backoff = 0
		}
		backoff = transport.Backoff(backoff, maxBackoff)
		// ±20 % jitter so several deployments do not reconnect in step.
		wait := time.Duration(float64(backoff) * (0.8 + 0.4*rand.Float64()))
		if transport.Sleep(ctx, wait) != nil {
			return
		}
	}
}

// session runs one connection: subscribe, read, keep alive, resubscribe.
func (c *conn) session(ctx context.Context, sub *subscription) error {
	ws, err := c.d.dial(ctx, c.d.url)
	if err != nil {
		return err
	}
	ws.SetReadLimit(maxMessageBytes)
	c.state(func(s *connState) {
		s.connected, s.confirmed, s.compression = true, false, false
		s.connects++
		s.connectedAt = c.d.now()
	})

	confirmed := make(chan bool, 1)
	readErr := make(chan error, 1)
	go c.read(ws, confirmed, readErr)
	fail := func(err error) error {
		ws.Close()
		<-readErr
		return err
	}

	confirmTimer := time.NewTimer(confirmWait)
	defer confirmTimer.Stop()
	updateTimer := time.NewTimer(time.Hour)
	updateTimer.Stop()
	defer updateTimer.Stop()
	ping := time.NewTicker(pingInterval)
	defer ping.Stop()

	var lastSent time.Time
	pending := false
	send := func() error {
		b, _ := json.Marshal(sub)
		ws.SetWriteDeadline(time.Now().Add(writeTimeout))
		if err := ws.WriteMessage(gws.TextMessage, b); err != nil {
			return fmt.Errorf("subscribe: %w", err)
		}
		lastSent, pending = c.d.now(), false
		c.state(func(s *connState) { s.confirmed = false; s.subscriptions++ })
		confirmTimer.Reset(confirmWait)
		return nil
	}
	if err := send(); err != nil {
		return fail(err)
	}
	awaiting := true
	for {
		select {
		case <-ctx.Done():
			ws.WriteControl(gws.CloseMessage, gws.FormatCloseMessage(gws.CloseNormalClosure, ""), time.Now().Add(time.Second))
			return fail(nil)
		case err := <-readErr:
			ws.Close()
			return err
		case compression := <-confirmed:
			awaiting = false
			confirmTimer.Stop()
			// The last failure stays reported until a subscription is accepted again.
			c.state(func(s *connState) { s.confirmed, s.compression, s.lastError = true, compression, "" })
		case <-confirmTimer.C:
			if awaiting {
				return fail(errNotConfirmed)
			}
		case <-ping.C:
			if err := ws.WriteControl(gws.PingMessage, nil, time.Now().Add(writeTimeout)); err != nil {
				return fail(fmt.Errorf("ping: %w", err))
			}
		case s := <-c.subCh:
			*sub, pending = s, true
			if wait := minUpdateInterval - c.d.now().Sub(lastSent); wait > 0 {
				updateTimer.Reset(wait)
				continue
			}
			if err := send(); err != nil {
				return fail(err)
			}
			awaiting = true
		case <-updateTimer.C:
			if pending {
				if err := send(); err != nil {
					return fail(err)
				}
				awaiting = true
			}
		}
	}
}

// read hands every message to the driver. Confirmations and errors from
// aisstream.io are handled here; everything else is queued.
func (c *conn) read(ws wsConn, confirmed chan<- bool, done chan<- error) {
	for {
		_, data, err := ws.ReadMessage()
		if err != nil {
			done <- err
			return
		}
		if bytes.Contains(data, confirmationMarker) {
			var m struct {
				MessageType string `json:"MessageType"`
				Message     struct {
					CompressionEnabled bool `json:"CompressionEnabled"`
				} `json:"Message"`
			}
			if json.Unmarshal(data, &m) == nil && m.MessageType == "SubscriptionConfirmation" {
				select {
				case confirmed <- m.Message.CompressionEnabled:
				default:
				}
				continue
			}
		}
		if bytes.HasPrefix(bytes.TrimSpace(data), []byte(`{"error"`)) {
			var m struct {
				Error string `json:"error"`
			}
			json.Unmarshal(data, &m)
			done <- fmt.Errorf("aisstream.io: %s", m.Error)
			return
		}
		c.d.enqueue(frame{from: c.name, data: data, at: c.d.now()})
	}
}
