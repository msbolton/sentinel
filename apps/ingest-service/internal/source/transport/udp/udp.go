// Package udp receives datagrams; each datagram is one frame. It binds a
// port, so a feed using it must be pinned to one replica.
package udp

import (
	"context"
	"errors"
	"net"
	"time"

	"github.com/sentinel/ingest-service/internal/source/transport"
)

func init() {
	transport.Register(transport.Definition{
		Kind: "udp", Type: transport.KindMessage, Listens: true,
		Description: "Listen on a UDP port; every datagram is one frame (AIS-catcher, GPS NMEA broadcasters).",
		New:         New,
	})
}

// Listener is the udp transport.
type Listener struct {
	cfg transport.Config
	transport.StateTracker
}

// New validates the config and builds the transport.
func New(cfg transport.Config) (transport.Transport, error) {
	if cfg.Address == "" {
		return nil, errors.New("udp: address is required")
	}
	if _, _, err := net.SplitHostPort(cfg.Address); err != nil {
		return nil, errors.New("udp: address must be host:port")
	}
	return &Listener{cfg: cfg}, nil
}

// Run reads datagrams until ctx ends. A datagram holding several newline
// separated sentences (AIS-catcher batches them) is split into one frame
// per line when framing is "line"; otherwise the datagram is the frame.
func (l *Listener) Run(ctx context.Context, emit transport.Emit) error {
	lc := net.ListenConfig{}
	pc, err := lc.ListenPacket(ctx, "udp", l.cfg.Address)
	if err != nil {
		return err
	}
	defer pc.Close()
	go func() { <-ctx.Done(); pc.Close() }()
	l.Set(func(s *transport.State) { s.Connected = true; s.LastError = "" })

	buf := make([]byte, 65535)
	for {
		n, addr, err := pc.ReadFrom(buf)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			l.Set(func(s *transport.State) { s.LastError = err.Error() })
			if err := transport.Sleep(ctx, 100*time.Millisecond); err != nil {
				return nil
			}
			continue
		}
		now := time.Now().UTC()
		remote := addr.String()
		for _, part := range split(buf[:n], l.cfg.Framing) {
			payload := make([]byte, len(part))
			copy(payload, part)
			if err := emit(ctx, transport.Frame{Payload: payload, ReceivedAt: now, Remote: remote}); err != nil && !errors.Is(err, transport.ErrBackpressure) {
				return nil
			}
		}
	}
}

func split(b []byte, framing string) [][]byte {
	if framing != "" && framing != "line" {
		return [][]byte{b}
	}
	var out [][]byte
	start := 0
	for i, c := range b {
		if c == '\n' {
			line := trimCR(b[start:i])
			if len(line) > 0 {
				out = append(out, line)
			}
			start = i + 1
		}
	}
	if tail := trimCR(b[start:]); len(tail) > 0 {
		out = append(out, tail)
	}
	return out
}

func trimCR(b []byte) []byte {
	for len(b) > 0 && (b[len(b)-1] == '\r' || b[len(b)-1] == '\n') {
		b = b[:len(b)-1]
	}
	return b
}

// State reports connectivity.
func (l *Listener) State() transport.State { return l.Get() }
