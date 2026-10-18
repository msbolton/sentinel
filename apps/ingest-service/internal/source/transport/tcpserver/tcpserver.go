// Package tcpserver accepts TCP connections from remote senders and reads a
// framed byte stream from each. It binds a port, so a feed using it runs on
// a listener replica.
package tcpserver

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sentinel/ingest-service/internal/source/transport"
	"github.com/sentinel/ingest-service/internal/source/transport/framing"
)

func init() {
	transport.Register(transport.Definition{
		Kind: "tcpserver", Type: transport.KindStream, Listens: true,
		Description: "Listen on a TCP port and read a framed byte stream from every connection (optional TLS).",
		New:         New,
	})
}

const (
	// handshakeTimeout bounds a TLS handshake, so a silent peer cannot hold
	// a connection slot.
	handshakeTimeout = 10 * time.Second
	// maxSenders is how many senders State renders (and Senders tracks).
	// Connected senders are never evicted, so with MaxConnections above this
	// the tracker can hold more; State truncates, distinctSenders does not.
	maxSenders = 50
	// sendersWindow is how long an idle sender stays in the report.
	sendersWindow = 10 * time.Minute
)

// Server is the tcpserver transport.
type Server struct {
	cfg       transport.Config
	tls       *tls.Config
	allow     transport.Allowlist
	limit     *transport.Limiter       // shared by every sender
	perSender *transport.SenderLimiter // a bucket for each sender IP; nil when off
	senders   *transport.Senders       // a row for each connection, named ip:port
	counts    transport.ListenCounters
	transport.StateTracker
}

// New validates the config and builds the transport.
func New(cfg transport.Config) (transport.Transport, error) {
	if cfg.Address == "" {
		return nil, errors.New("tcpserver: address is required")
	}
	_, port, err := net.SplitHostPort(cfg.Address)
	if err != nil {
		return nil, errors.New("tcpserver: address must be host:port")
	}
	// A service name such as "smtp" would resolve inside Listen to a port the
	// listener policy never sees, so only a number in range is accepted.
	if p, err := strconv.Atoi(port); err != nil || p < 0 || p > 65535 {
		return nil, fmt.Errorf("tcpserver: address port must be a number from 0 to 65535, not %q", port)
	}
	// A client CA verifies only the certificates a listener asks for: set
	// without requireClientCert it would leave every sender unchecked.
	mutualTLS := cfg.TLS != nil && cfg.TLS.Enabled && cfg.TLS.RequireClientCert
	if hasClientCA(cfg) && !mutualTLS {
		return nil, errors.New("tcpserver: a client CA is set (the clientCa secret or tls.clientCaFile) but tls.requireClientCert is off; turn it on (with TLS) to verify senders, or remove the client CA")
	}
	t, err := cfg.TLSConfig()
	if err != nil {
		return nil, err
	}
	// Asking senders for certificates is this listener's decision, not the
	// shared TLS builder's, which dialers use too. It can check them only
	// against a client CA.
	if mutualTLS {
		if t.ClientCAs == nil {
			return nil, errors.New("tcpserver: requiring client certificates needs a client CA")
		}
		t.ClientAuth = tls.RequireAndVerifyClientCert
	}
	if t != nil && len(t.Certificates) == 0 {
		return nil, errors.New("tcpserver: TLS needs a certificate and key")
	}
	allow, err := transport.ParseAllowlist(cfg.Allow)
	if err != nil {
		return nil, err
	}
	return &Server{
		cfg: cfg, tls: t, allow: allow,
		limit:     transport.NewLimiter(cfg.MaxMessagesPerSec),
		perSender: transport.NewSenderLimiter(cfg.MaxMessagesPerSecPerSender, transport.SenderLimiterKeys),
		senders:   transport.NewSenders(maxSenders),
	}, nil
}

// hasClientCA reports whether the config names a CA for client certificates,
// as PEM (the clientCa secret) or as a file.
func hasClientCA(cfg transport.Config) bool {
	return len(cfg.ClientCAPEM) > 0 || (cfg.TLS != nil && cfg.TLS.ClientCAFile != "")
}

// Run listens until ctx ends. A bind failure is fatal; accept errors are
// retried with a pause so a transient error cannot spin the loop. Senders
// outside the allowlist, connections over the cap, failed TLS handshakes
// and TLS clients on a plain listener are closed and counted; frames over
// a sender's rate or the listener's are dropped and counted.
func (s *Server) Run(ctx context.Context, emit transport.Emit) error {
	lc := net.ListenConfig{}
	ln, err := lc.Listen(ctx, "tcp", s.cfg.Address)
	if err != nil {
		return transport.BindError("tcp", s.cfg.Address, err)
	}
	defer ln.Close()
	s.Set(func(st *transport.State) { st.Connected = true; st.LastError = "" })

	// guarded is the emit for the connection of the sender at host, its IP,
	// which is the key of its rate limit: every connection of one IP draws on
	// one bucket, or a sender could multiply its budget by connecting again.
	// The sender's own bucket is asked before the listener's, so a flooder
	// spends none of the shared budget. The senders list names the
	// connection, ip:port.
	guarded := func(host string) transport.Emit {
		return func(ctx context.Context, f transport.Frame) error {
			if !s.perSender.Allow(host, f.ReceivedAt) {
				s.counts.SenderRateLimited.Add(1)
				s.senders.RateLimited(f.Remote, f.ReceivedAt)
				return nil
			}
			if !s.limit.Allow() {
				s.counts.RateLimited.Add(1)
				return nil
			}
			s.counts.Accepted.Add(1)
			s.senders.Seen(f.Remote, f.ReceivedAt)
			return emit(ctx, f)
		}
	}

	var wg sync.WaitGroup
	var conns sync.Map
	var live atomic.Int64
	go func() {
		<-ctx.Done()
		ln.Close()
		conns.Range(func(_, v any) bool { v.(net.Conn).Close(); return true })
	}()

	for {
		raw, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				break
			}
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				continue
			}
			s.Set(func(st *transport.State) { st.LastError = err.Error() })
			if err := transport.Sleep(ctx, 100*time.Millisecond); err != nil {
				break
			}
			continue
		}
		remote := raw.RemoteAddr().String()
		if !s.allow.Allows(remote) {
			s.counts.Rejected.Add(1)
			raw.Close()
			continue
		}
		if s.cfg.MaxConnections > 0 && live.Load() >= int64(s.cfg.MaxConnections) {
			s.counts.Refused.Add(1)
			raw.Close()
			continue
		}
		live.Add(1)
		conns.Store(raw, raw)
		if ctx.Err() != nil {
			raw.Close() // the shutdown sweep may have run before the Store
		}
		connEmit := guarded(transport.SenderIP(remote))
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() {
				raw.Close()
				conns.Delete(raw)
				live.Add(-1)
				s.senders.Disconnect(remote)
				s.Set(func(st *transport.State) { st.Connections = int(live.Load()) })
			}()
			conn := raw
			if s.tls != nil {
				tc := tls.Server(raw, s.tls)
				hctx, cancel := context.WithTimeout(ctx, handshakeTimeout)
				err := tc.HandshakeContext(hctx)
				cancel()
				if err != nil {
					s.counts.TLSFailed.Add(1)
					return
				}
				conn = tc
			} else {
				conn = &sniffConn{Conn: raw}
			}
			s.senders.Connect(remote, time.Now().UTC())
			s.Set(func(st *transport.State) { st.Connections = int(live.Load()) })
			if err := transport.ScanConn(ctx, conn, s.cfg, connEmit); err != nil && ctx.Err() == nil {
				if errors.Is(err, errTLSClient) {
					s.counts.TLSFailed.Add(1) // closed and counted; not a read error worth LastError
					return
				}
				var ne net.Error
				if errors.As(err, &ne) && ne.Timeout() {
					return // the read timeout closed an idle sender, as configured
				}
				if errors.Is(err, bufio.ErrTooLong) || errors.Is(err, framing.ErrFrameTooLong) {
					s.counts.Oversize.Add(1)
				}
				s.Set(func(st *transport.State) { st.LastError = err.Error() })
			}
		}()
	}
	wg.Wait()
	s.Set(func(st *transport.State) { st.Connected = false })
	return nil
}

var _ transport.MalformedNoter = (*Server)(nil)

// NoteMalformed counts a message from remote that did not decode against that
// sender, which State lists.
func (s *Server) NoteMalformed(remote, reason, errText string) {
	s.senders.Malformed(remote, reason, errText, time.Now().UTC())
}

// State reports connectivity, counters and senders.
func (s *Server) State() transport.State {
	st := s.Get()
	st.Detail = s.counts.Detail()
	list, n := s.senders.Snapshot(time.Now().UTC(), sendersWindow)
	if len(list) > maxSenders {
		list = list[:maxSenders]
	}
	st.Detail["senders"], st.Detail["distinctSenders"] = list, n
	return st
}
