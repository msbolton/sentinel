// Package websocket connects to a WebSocket endpoint; every text or binary
// message is one frame (or, with a framer, is split as a byte stream).
package websocket

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"time"

	gws "github.com/gorilla/websocket"

	"github.com/sentinel/ingest-service/internal/source/transport"
)

const maxBackoff = 30 * time.Second

func init() {
	transport.Register(transport.Definition{
		Kind: "websocket", Type: transport.KindMessage,
		Description: "Connect to a WebSocket endpoint; every message is one frame.",
		New:         New,
	})
}

// Client is the websocket transport.
type Client struct {
	cfg transport.Config
	transport.StateTracker
}

// New validates the config and builds the transport.
func New(cfg transport.Config) (transport.Transport, error) {
	if cfg.URL == "" {
		return nil, errors.New("websocket: url is required")
	}
	u, err := url.Parse(cfg.URL)
	if err != nil || (u.Scheme != "ws" && u.Scheme != "wss") {
		return nil, errors.New("websocket: url must start with ws:// or wss://")
	}
	if cfg.Framing != "" && cfg.Framing != "message" {
		return nil, errors.New("websocket: framing must be \"message\"")
	}
	return &Client{cfg: cfg}, nil
}

// Run dials, reads and reconnects until ctx ends.
func (c *Client) Run(ctx context.Context, emit transport.Emit) error {
	tlsCfg, err := c.cfg.TLSConfig()
	if err != nil {
		return err
	}
	dialer := gws.Dialer{HandshakeTimeout: 10 * time.Second, TLSClientConfig: tlsCfg}
	header := http.Header{}
	if c.cfg.Username != "" {
		req, _ := http.NewRequest(http.MethodGet, "/", nil)
		req.SetBasicAuth(c.cfg.Username, c.cfg.Password)
		header.Set("Authorization", req.Header.Get("Authorization"))
	}
	backoff := time.Duration(0)
	for ctx.Err() == nil {
		conn, resp, err := dialer.DialContext(ctx, c.cfg.URL, header)
		if resp != nil && resp.Body != nil {
			resp.Body.Close()
		}
		if err != nil {
			c.Set(func(s *transport.State) { s.Connected = false; s.LastError = err.Error() })
			backoff = transport.Backoff(backoff, maxBackoff)
			if err := transport.Sleep(ctx, backoff); err != nil {
				return nil
			}
			continue
		}
		backoff = 0
		c.Set(func(s *transport.State) { s.Connected = true; s.Connections = 1; s.LastError = "" })
		conn.SetReadLimit(int64(c.cfg.MaxFrame()))
		done := make(chan struct{})
		go func() {
			select {
			case <-ctx.Done():
				conn.Close()
			case <-done:
			}
		}()
		err = c.read(ctx, conn, emit)
		close(done)
		conn.Close()
		c.Set(func(s *transport.State) {
			s.Connected = false
			s.Connections = 0
			if err != nil && ctx.Err() == nil {
				s.LastError = err.Error()
			}
		})
		if ctx.Err() != nil {
			return nil
		}
		if err := transport.Sleep(ctx, time.Second); err != nil {
			return nil
		}
	}
	return nil
}

func (c *Client) read(ctx context.Context, conn *gws.Conn, emit transport.Emit) error {
	timeout := time.Duration(c.cfg.ReadTimeoutSec) * time.Second
	for {
		if timeout > 0 {
			_ = conn.SetReadDeadline(time.Now().Add(timeout))
		}
		_, data, err := conn.ReadMessage()
		if err != nil {
			return err
		}
		if len(data) == 0 {
			continue
		}
		if err := emit(ctx, transport.Frame{Payload: data, ReceivedAt: time.Now().UTC(), Remote: c.cfg.URL}); err != nil && !errors.Is(err, transport.ErrBackpressure) {
			return nil
		}
	}
}

// State reports connectivity.
func (c *Client) State() transport.State { return c.Get() }
