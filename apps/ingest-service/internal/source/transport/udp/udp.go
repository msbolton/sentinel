// Package udp receives datagrams; each datagram is one frame. It binds a
// port, so a feed using it runs on a listener replica.
package udp

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
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

const (
	// maxSenders is how many senders State renders.
	maxSenders = 50
	// trackedSenders is how many senders are tracked, so distinctSenders can
	// count past what State renders (a datagram sender is cheap to spoof or
	// to multiply, and the count is the signal).
	trackedSenders = 1000
	// sendersWindow is how long an idle sender stays in the report.
	sendersWindow = 10 * time.Minute
)

// Listener is the udp transport.
type Listener struct {
	cfg     transport.Config
	allow   transport.Allowlist
	limit   *transport.Limiter
	senders *transport.Senders
	counts  transport.ListenCounters
	transport.StateTracker
}

// New validates the config and builds the transport.
func New(cfg transport.Config) (transport.Transport, error) {
	if cfg.Address == "" {
		return nil, errors.New("udp: address is required")
	}
	_, port, err := net.SplitHostPort(cfg.Address)
	if err != nil {
		return nil, errors.New("udp: address must be host:port")
	}
	// A service name such as "domain" would resolve inside ListenPacket to a
	// port the listener policy never sees, so only a number in range is accepted.
	if p, err := strconv.Atoi(port); err != nil || p < 0 || p > 65535 {
		return nil, fmt.Errorf("udp: address port must be a number from 0 to 65535, not %q", port)
	}
	allow, err := transport.ParseAllowlist(cfg.Allow)
	if err != nil {
		return nil, err
	}
	return &Listener{cfg: cfg, allow: allow, limit: transport.NewLimiter(cfg.MaxMessagesPerSec), senders: transport.NewSenders(trackedSenders)}, nil
}

// Run reads datagrams until ctx ends. A datagram holding several newline
// separated sentences (AIS-catcher batches them) is split into one frame
// per line when framing is "line"; otherwise the datagram is the frame.
// Datagrams from outside the allowlist and datagrams over MaxFrameBytes
// are dropped whole and counted; messages over the rate are dropped and
// counted one by one.
func (l *Listener) Run(ctx context.Context, emit transport.Emit) error {
	lc := net.ListenConfig{}
	pc, err := lc.ListenPacket(ctx, "udp", l.cfg.Address)
	if err != nil {
		return transport.BindError("udp", l.cfg.Address, err)
	}
	defer pc.Close()
	go func() { <-ctx.Done(); pc.Close() }()
	l.Set(func(s *transport.State) { s.Connected = true; s.LastError = "" })

	// The buffer holds the largest possible datagram, so an oversize one is
	// measured whole rather than truncated by the OS to look small enough.
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
		remote := addr.String()
		if !l.allow.Allows(remote) {
			l.counts.Rejected.Add(1)
			continue
		}
		if maxBytes := l.cfg.MaxFrameBytes; maxBytes > 0 && n > maxBytes {
			l.counts.Oversize.Add(1)
			continue
		}
		now := time.Now().UTC()
		for _, part := range split(buf[:n], l.cfg.Framing) {
			if !l.limit.Allow() {
				l.counts.RateLimited.Add(1)
				continue
			}
			l.counts.Accepted.Add(1)
			l.senders.Seen(remote, now)
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

// State reports connectivity, counters and the senders seen in the last 10
// minutes: at most 50 are listed, distinctSenders counts them all.
func (l *Listener) State() transport.State {
	st := l.Get()
	st.Detail = l.counts.Detail()
	list, n := l.senders.Snapshot(time.Now().UTC(), sendersWindow)
	if len(list) > maxSenders {
		list = list[:maxSenders]
	}
	st.Detail["senders"], st.Detail["distinctSenders"] = list, n
	return st
}
