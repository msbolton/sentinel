// Package udp receives datagrams; each datagram is one frame. It binds a
// port, so a feed using it runs on a listener replica.
package udp

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"sync/atomic"
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
	// to multiply, and the count is the signal). A sender is an IP address,
	// whatever port its datagrams come from.
	trackedSenders = 1000
	// sendersWindow is how long an idle sender stays in the report.
	sendersWindow = 10 * time.Minute
)

// Listener is the udp transport.
type Listener struct {
	cfg       transport.Config
	allow     transport.Allowlist
	limit     *transport.Limiter       // shared by every sender
	perSender *transport.SenderLimiter // a bucket for each sender IP; nil when off
	senders   *transport.Senders       // a row for each sender IP
	counts    transport.ListenCounters

	// receiveBuffer is the size of the socket's receive buffer as the OS reports
	// it: 0 until the socket is open, and where the OS cannot say.
	receiveBuffer atomic.Int64
	// kernelDropped is how many datagrams the OS dropped from the socket's full
	// receive buffer, as of the latest datagram it delivered. Only Linux reports
	// it; elsewhere it stays 0.
	kernelDropped atomic.Int64

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
	// The schema holds a config to the same range; this is for one that skipped it.
	if n := cfg.ReceiveBufferBytes; n < 0 || n > transport.MaxReceiveBufferBytes {
		return nil, fmt.Errorf("udp: receiveBufferBytes must be from 0 to %d, not %d", transport.MaxReceiveBufferBytes, n)
	}
	allow, err := transport.ParseAllowlist(cfg.Allow)
	if err != nil {
		return nil, err
	}
	return &Listener{
		cfg: cfg, allow: allow,
		limit:     transport.NewLimiter(cfg.MaxMessagesPerSec),
		perSender: transport.NewSenderLimiter(cfg.MaxMessagesPerSecPerSender, transport.SenderLimiterKeys),
		senders:   transport.NewSenders(trackedSenders),
	}, nil
}

// Run reads datagrams until ctx ends. A datagram holding several newline
// separated sentences (AIS-catcher batches them) is split into one frame
// per line when framing is "line"; otherwise the datagram is the frame.
// Datagrams from outside the allowlist and datagrams over MaxFrameBytes
// are dropped whole and counted; messages over a sender's rate or the
// listener's are dropped and counted one by one. A sender is told apart by
// its IP address, in the senders list and for its rate limit; the frame's
// Remote is the exact ip:port. Datagrams that arrive faster than the loop
// reads them wait in the socket's receive buffer, which is sized from the
// config; on Linux the datagrams the OS had to drop when it was full are
// counted too.
func (l *Listener) Run(ctx context.Context, emit transport.Emit) error {
	lc := net.ListenConfig{}
	pc, err := lc.ListenPacket(ctx, "udp", l.cfg.Address)
	if err != nil {
		return transport.BindError("udp", l.cfg.Address, err)
	}
	defer pc.Close()
	go func() { <-ctx.Done(); pc.Close() }()
	conn := pc.(*net.UDPConn) // ListenPacket("udp") always returns one
	l.sizeReceiveBuffer(conn)
	reader := newDatagramReader(conn, &l.kernelDropped)
	l.Set(func(s *transport.State) { s.Connected = true; s.LastError = "" })

	// The buffer holds the largest possible datagram, so an oversize one is
	// measured whole rather than truncated by the OS to look small enough.
	buf := make([]byte, 65535)
	for {
		n, ap, err := reader.read(buf)
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
		// A socket bound to every interface reports an IPv4 sender as an
		// IPv4-mapped IPv6 address; below it is the IPv4 address.
		ap = netip.AddrPortFrom(ap.Addr().Unmap(), ap.Port())
		if !l.allow.AllowsAddr(ap.Addr()) {
			l.counts.Rejected.Add(1)
			continue
		}
		if maxBytes := l.cfg.MaxFrameBytes; maxBytes > 0 && n > maxBytes {
			l.counts.Oversize.Add(1)
			continue
		}
		// The sender's bucket is asked on received, whose monotonic reading
		// survives a step of the wall clock; frames and rows are stamped with
		// its UTC form, which has none (see SenderLimiter.Allow).
		received := time.Now()
		now := received.UTC()
		// The two forms of the address are built only for a datagram that is
		// taken, so one that is turned away costs no text.
		remote, sender := ap.String(), ap.Addr().String()
		parts := split(buf[:n], l.cfg.Framing)
		for i, part := range parts {
			// The sender's own bucket is asked before the listener's, so a
			// flooder spends none of the shared budget. The datagram's
			// messages are all asked at the one instant, so once one is
			// refused the rest are too, and are counted together.
			if !l.perSender.Allow(sender, received) {
				dropped := int64(len(parts) - i)
				l.counts.SenderRateLimited.Add(dropped)
				l.senders.RateLimited(sender, dropped)
				break
			}
			if !l.limit.Allow() {
				l.counts.RateLimited.Add(1)
				continue
			}
			l.counts.Accepted.Add(1)
			l.senders.Seen(sender, now)
			payload := make([]byte, len(part))
			copy(payload, part)
			if err := emit(ctx, transport.Frame{Payload: payload, ReceivedAt: now, Remote: remote}); err != nil && !errors.Is(err, transport.ErrBackpressure) {
				return nil
			}
		}
	}
}

// sizeReceiveBuffer asks the OS for the socket's receive buffer and records the
// size the OS says the socket has, which is not always the ask: Linux caps it
// at net.core.rmem_max, and other systems have a cap of their own. The listener
// runs with whatever it got, so the ask failing is not an error; the size
// recorded is how an operator learns the buffer is smaller than configured.
func (l *Listener) sizeReceiveBuffer(conn *net.UDPConn) {
	_ = conn.SetReadBuffer(l.cfg.ReceiveBuffer())
	if size, err := receiveBufferSize(conn); err == nil {
		l.receiveBuffer.Store(int64(size))
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

var _ transport.MalformedNoter = (*Listener)(nil)

// NoteMalformed counts a message from remote that did not decode against that
// sender, which State lists. remote is a frame's Remote, ip:port, and senders
// are listed by IP, so the port is dropped to reach the sender's row.
func (l *Listener) NoteMalformed(remote, reason, errText string) {
	l.senders.Malformed(transport.SenderIP(remote), reason, errText, time.Now().UTC())
}

// State reports connectivity, counters and the sender IPs seen in the last 10
// minutes: at most 50 are listed, distinctSenders counts them all. It also
// reports the receive buffer in bytes (receiveBufferEffectiveBytes) and the
// datagrams the OS dropped (kernelDropped), which only a datagram listener has.
func (l *Listener) State() transport.State {
	st := l.Get()
	st.Detail = l.counts.Detail()
	st.Detail["receiveBufferEffectiveBytes"] = l.receiveBuffer.Load()
	st.Detail["kernelDropped"] = l.kernelDropped.Load()
	list, n := l.senders.Snapshot(time.Now().UTC(), sendersWindow)
	if len(list) > maxSenders {
		list = list[:maxSenders]
	}
	st.Detail["senders"], st.Detail["distinctSenders"] = list, n
	return st
}
