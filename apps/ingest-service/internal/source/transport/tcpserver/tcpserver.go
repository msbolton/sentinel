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
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"

	"github.com/sentinel/ingest-service/internal/source/transport"
	"github.com/sentinel/ingest-service/internal/source/transport/framing"
)

func init() {
	transport.Register(transport.Definition{
		Kind: "tcpserver", Type: transport.KindStream,
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
	log       *zap.Logger
	tlsrc     *transport.TLSSource // the TLS config in use, and its reloads; nil for a plain listener
	serverTLS *tls.Config          // what tls.Server gets: it asks tlsrc for the config in use at each handshake
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
	// listener policy never sees, so only a number in range is accepted: decimal
	// digits alone, as ParseUint reads them (Atoi would take "+25" for 25).
	if _, err := strconv.ParseUint(port, 10, 16); err != nil {
		return nil, fmt.Errorf("tcpserver: address port must be a number from 0 to 65535, not %q", port)
	}
	// A client CA verifies only the certificates a listener asks for: set
	// without requireClientCert it would leave every sender unchecked.
	mutualTLS := cfg.TLS != nil && cfg.TLS.Enabled && cfg.TLS.RequireClientCert
	if hasClientCA(cfg) && !mutualTLS {
		return nil, errors.New("tcpserver: a client CA is set (the clientCa secret or tls.clientCaFile) but tls.requireClientCert is off; turn it on (with TLS) to verify senders, or remove the client CA")
	}
	// requireClientCert with TLS off asks for a check nothing can make: the
	// listener would run plain and open. Fail closed, as with the client CA.
	if cfg.TLS != nil && cfg.TLS.RequireClientCert && !cfg.TLS.Enabled {
		return nil, errors.New("tcpserver: tls.requireClientCert is set but TLS is off; turn TLS on or remove requireClientCert")
	}
	// What is the listener's own decision, not the shared builder's (which
	// dialers use too), is applied to every config it serves, the first and each
	// reload (see TLSSource), so a rotation keeps it:
	//   - Resumption is of no use to senders that send and go, and a TLS 1.3
	//     server sends a session ticket just after the handshake. A sender that
	//     writes and closes at once never reads it, so its close finds unread
	//     data and becomes a RST, which can take the message with it.
	//   - Asking senders for certificates can be checked only against a client
	//     CA, and a listener without a TLS config at all has nothing to check
	//     them with.
	//   - A listener serves a certificate or none.
	finish := func(t *tls.Config) error {
		if mutualTLS && (t == nil || t.ClientCAs == nil) {
			return errors.New("tcpserver: requiring client certificates needs a client CA")
		}
		if t == nil {
			return nil
		}
		t.SessionTicketsDisabled = true
		if mutualTLS {
			t.ClientAuth = tls.RequireAndVerifyClientCert
		}
		if len(t.Certificates) == 0 {
			return errors.New("tcpserver: TLS needs a certificate and key")
		}
		return nil
	}
	src, err := transport.NewTLSSource(cfg, finish)
	if err != nil {
		return nil, err
	}
	allow, err := transport.ParseAllowlist(cfg.Allow)
	if err != nil {
		return nil, err
	}
	log := cfg.Logger
	if log == nil {
		log = zap.NewNop()
	}
	srv := &Server{
		cfg: cfg, log: log, tlsrc: src, allow: allow,
		limit:     transport.NewLimiter(cfg.MaxMessagesPerSec),
		perSender: transport.NewSenderLimiter(cfg.MaxMessagesPerSecPerSender, transport.SenderLimiterKeys),
		senders:   transport.NewSenders(maxSenders),
	}
	if src != nil {
		// The config in use is looked up per handshake, so the certificate and the
		// client CA pool swap together; a connection already made keeps its own.
		srv.serverTLS = &tls.Config{GetConfigForClient: func(*tls.ClientHelloInfo) (*tls.Config, error) { return src.Current(), nil }}
	}
	return srv, nil
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

	var wg sync.WaitGroup
	reloadDone := make(chan struct{})
	go func() {
		defer close(reloadDone)
		s.tlsrc.Run(ctx) // returns at once when there is nothing to reload
	}()
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
		connEmit := s.guard(transport.SenderIP(remote), emit)
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
			if s.serverTLS != nil {
				tc := tls.Server(raw, s.serverTLS)
				hctx, cancel := context.WithTimeout(ctx, handshakeTimeout)
				err := tc.HandshakeContext(hctx)
				cancel()
				if err != nil {
					// A handshake that shutdown cut short is not the sender's
					// failure. The log comes first, so that whoever sees the
					// counter move can find the sender in it.
					if ctx.Err() == nil {
						s.log.Debug("tcpserver TLS handshake failed", zap.String("remote", remote), zap.Error(err))
						s.counts.TLSFailed.Add(1)
					}
					return
				}
				conn = tc
			} else {
				conn = &sniffConn{Conn: raw}
			}
			s.senders.Connect(remote, time.Now().UTC())
			s.Set(func(st *transport.State) { st.Connections = int(live.Load()) })
			if err := transport.ScanConn(ctx, conn, s.cfg, connEmit); err != nil && ctx.Err() == nil {
				s.readEnded(err)
			}
		}()
	}
	wg.Wait()
	<-reloadDone
	s.Set(func(st *transport.State) { st.Connected = false })
	return nil
}

// readEnded records why a connection's read loop ended with err, which was not
// the listener shutting down.
func (s *Server) readEnded(err error) {
	if errors.Is(err, errTLSClient) {
		s.counts.TLSFailed.Add(1) // closed and counted; not a read error worth LastError
		return
	}
	// The read deadline that ReadTimeoutSec sets expiring is the idle close it
	// asked for. Any other timeout is the network's: a dead peer, which keepalive
	// gives up on with ETIMEDOUT, and LastError should show it.
	if s.cfg.ReadTimeoutSec > 0 && errors.Is(err, os.ErrDeadlineExceeded) {
		return
	}
	if errors.Is(err, bufio.ErrTooLong) || errors.Is(err, framing.ErrFrameTooLong) {
		s.counts.Oversize.Add(1)
	}
	s.Set(func(st *transport.State) { st.LastError = err.Error() })
}

// guard is the emit for the connection of the sender at host, its IP, which is
// the key of its rate limit: every connection of one IP draws on one bucket, or
// a sender could multiply its budget by connecting again. The sender's own
// bucket is asked before the listener's, so a flooder spends none of the
// shared budget; it is asked on time.Now(), not on the frame's UTC stamp (see
// SenderLimiter.Allow), and the clock is read only when there is a bucket to
// ask. The senders list names the connection, ip:port. A sender's row is
// updated before the listener's counter, so that whoever waits for the counter
// to move finds the row already moved.
func (s *Server) guard(host string, emit transport.Emit) transport.Emit {
	return func(ctx context.Context, f transport.Frame) error {
		if s.perSender != nil && !s.perSender.Allow(host, time.Now()) {
			s.senders.RateLimited(f.Remote, 1)
			s.counts.SenderRateLimited.Add(1)
			return nil
		}
		if !s.limit.Allow() {
			s.counts.RateLimited.Add(1)
			return nil
		}
		s.senders.Seen(f.Remote, f.ReceivedAt)
		s.counts.Accepted.Add(1)
		return emit(ctx, f)
	}
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
	s.tlsrc.Apply(&st)
	return st
}
