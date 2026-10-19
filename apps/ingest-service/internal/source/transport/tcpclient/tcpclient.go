// Package tcpclient dials a remote TCP endpoint (a readsb Beast or SBS port,
// an AIS-catcher TCP output, a TAK server) and reconnects with backoff.
package tcpclient

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"sync/atomic"
	"time"

	"github.com/sentinel/ingest-service/internal/source/transport"
)

const (
	dialTimeout = 10 * time.Second
	maxBackoff  = 30 * time.Second
)

func init() {
	transport.Register(transport.Definition{
		Kind: "tcpclient", Type: transport.KindStream,
		Description: "Connect to a TCP endpoint and read a framed byte stream (optional TLS).",
		New:         New,
	})
}

// Client is the tcpclient transport.
type Client struct {
	cfg   transport.Config
	tlsrc *transport.TLSSource // nil without TLS; builds the config again before each dial when the config reloads
	// connects counts successful dials, messages the frames read, and since
	// is when the live connection came up (unix nanoseconds, 0 while down).
	connects, messages, since atomic.Int64
	transport.StateTracker
}

// New validates the config and builds the transport.
func New(cfg transport.Config) (transport.Transport, error) {
	if cfg.Address == "" {
		return nil, errors.New("tcpclient: address is required")
	}
	if _, _, err := net.SplitHostPort(cfg.Address); err != nil {
		return nil, errors.New("tcpclient: address must be host:port")
	}
	src, err := transport.NewTLSSource(cfg, nil)
	if err != nil {
		return nil, err
	}
	return &Client{cfg: cfg, tlsrc: src}, nil
}

// Run dials, scans and reconnects until ctx ends.
func (c *Client) Run(ctx context.Context, emit transport.Emit) error {
	backoff := time.Duration(0)
	for ctx.Err() == nil {
		conn, err := c.dial(ctx)
		if err != nil {
			c.Set(func(s *transport.State) { s.Connected = false; s.LastError = err.Error() })
			backoff = transport.Backoff(backoff, maxBackoff)
			if err := transport.Sleep(ctx, backoff); err != nil {
				return nil
			}
			continue
		}
		backoff = 0
		c.connects.Add(1)
		c.since.Store(time.Now().UnixNano())
		c.Set(func(s *transport.State) { s.Connected = true; s.Connections = 1; s.LastError = "" })
		done := make(chan struct{})
		go func() {
			select {
			case <-ctx.Done():
				conn.Close()
			case <-done:
			}
		}()
		counted := func(ctx context.Context, f transport.Frame) error {
			c.messages.Add(1)
			return emit(ctx, f)
		}
		err = transport.ScanConn(ctx, conn, c.cfg, counted)
		close(done)
		conn.Close()
		c.since.Store(0)
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
		// Remote closed or read failed: reconnect after a short pause.
		if err := transport.Sleep(ctx, time.Second); err != nil {
			return nil
		}
	}
	return nil
}

func (c *Client) dial(ctx context.Context) (net.Conn, error) {
	d := net.Dialer{Timeout: dialTimeout}
	// Each dial reads the TLS material again (a rotated CA or client certificate
	// is followed at the next reconnect); a reload that fails dials with the
	// config in use.
	if t := c.tlsrc.Fresh(); t != nil {
		td := tls.Dialer{NetDialer: &d, Config: t}
		return td.DialContext(ctx, "tcp", c.cfg.Address)
	}
	return d.DialContext(ctx, "tcp", c.cfg.Address)
}

// State reports connectivity, the remote, messages and reconnects (dials
// after the first). since is when the live connection came up, and is absent
// while the connection is down.
func (c *Client) State() transport.State {
	st := c.Get()
	reconnects := c.connects.Load() - 1
	if reconnects < 0 {
		reconnects = 0
	}
	st.Detail = map[string]any{"remote": c.cfg.Address, "messages": c.messages.Load(), "reconnects": reconnects}
	if ns := c.since.Load(); ns > 0 {
		st.Detail["since"] = time.Unix(0, ns).UTC().Format(time.RFC3339)
	}
	c.tlsrc.Apply(&st)
	return st
}
