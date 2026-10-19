// Package stomp consumes a STOMP queue; every message is a frame. Messages
// are acknowledged after the feed takes them and negatively acknowledged
// when the feed refuses (backpressure policy nack).
package stomp

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"time"

	gostomp "github.com/go-stomp/stomp/v3"

	"github.com/sentinel/ingest-service/internal/source/transport"
)

const (
	dialTimeout = 10 * time.Second
	maxBackoff  = 30 * time.Second
)

func init() {
	transport.Register(transport.Definition{
		Kind: "stomp", Type: transport.KindMessage,
		Description: "Consume a STOMP queue (ActiveMQ); every message is one frame, acknowledged after the feed takes it.",
		New:         New,
	})
}

// Consumer is the stomp transport.
type Consumer struct {
	cfg   transport.Config
	tlsrc *transport.TLSSource // nil without TLS; builds the config again before each connect when the config reloads
	transport.StateTracker
}

// New validates the config and builds the transport.
func New(cfg transport.Config) (transport.Transport, error) {
	if cfg.BrokerURL == "" {
		return nil, errors.New("stomp: brokerUrl is required (host:port)")
	}
	if _, _, err := net.SplitHostPort(cfg.BrokerURL); err != nil {
		return nil, errors.New("stomp: brokerUrl must be host:port")
	}
	if cfg.Queue == "" {
		return nil, errors.New("stomp: queue is required")
	}
	if cfg.Framing != "" && cfg.Framing != "message" {
		return nil, errors.New("stomp: framing must be \"message\"")
	}
	src, err := transport.NewTLSSource(cfg, nil)
	if err != nil {
		return nil, err
	}
	return &Consumer{cfg: cfg, tlsrc: src}, nil
}

// Run connects (retrying), subscribes and forwards until ctx ends.
func (c *Consumer) Run(ctx context.Context, emit transport.Emit) error {
	backoff := time.Duration(0)
	for ctx.Err() == nil {
		err := c.session(ctx, emit)
		if ctx.Err() != nil {
			return nil
		}
		if err != nil {
			c.Set(func(s *transport.State) { s.Connected = false; s.Connections = 0; s.LastError = err.Error() })
		}
		backoff = transport.Backoff(backoff, maxBackoff)
		if err := transport.Sleep(ctx, backoff); err != nil {
			return nil
		}
	}
	return nil
}

func (c *Consumer) session(ctx context.Context, emit transport.Emit) error {
	d := net.Dialer{Timeout: dialTimeout}
	var netConn net.Conn
	var err error
	if t := c.tlsrc.Fresh(); t != nil {
		netConn, err = (&tls.Dialer{NetDialer: &d, Config: t}).DialContext(ctx, "tcp", c.cfg.BrokerURL)
	} else {
		netConn, err = d.DialContext(ctx, "tcp", c.cfg.BrokerURL)
	}
	if err != nil {
		return err
	}
	opts := []func(*gostomp.Conn) error{gostomp.ConnOpt.HeartBeat(10*time.Second, 10*time.Second)}
	if c.cfg.Username != "" {
		opts = append(opts, gostomp.ConnOpt.Login(c.cfg.Username, c.cfg.Password))
	}
	conn, err := gostomp.Connect(netConn, opts...)
	if err != nil {
		netConn.Close()
		return err
	}
	sub, err := conn.Subscribe(c.cfg.Queue, gostomp.AckClientIndividual)
	if err != nil {
		_ = conn.Disconnect()
		return err
	}
	c.Set(func(s *transport.State) { s.Connected = true; s.Connections = 1; s.LastError = "" })

	defer func() {
		_ = sub.Unsubscribe()
		_ = conn.Disconnect()
		c.Set(func(s *transport.State) { s.Connected = false; s.Connections = 0 })
	}()

	for {
		select {
		case <-ctx.Done():
			return nil
		case msg, ok := <-sub.C:
			if !ok {
				return errors.New("subscription closed")
			}
			if msg.Err != nil {
				return msg.Err
			}
			attrs := map[string]string{}
			for i := 0; i < msg.Header.Len(); i++ {
				k, v := msg.Header.GetAt(i)
				attrs[k] = v
			}
			f := transport.Frame{Payload: msg.Body, ReceivedAt: time.Now().UTC(), Remote: c.cfg.Queue, Attrs: attrs}
			if err := emit(ctx, f); err != nil {
				if errors.Is(err, transport.ErrBackpressure) {
					_ = conn.Nack(msg)
					continue
				}
				return nil
			}
			if err := conn.Ack(msg); err != nil {
				return err
			}
		}
	}
}

// State reports connectivity, and the TLS reloads when there is TLS.
func (c *Consumer) State() transport.State {
	st := c.Get()
	c.tlsrc.Apply(&st)
	return st
}
