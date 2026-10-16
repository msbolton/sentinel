// Package tcpserver accepts TCP connections from remote senders and reads a
// framed byte stream from each. It binds a port, so a feed using it must be
// pinned to one replica.
package tcpserver

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"sync"
	"time"

	"github.com/sentinel/ingest-service/internal/source/transport"
)

func init() {
	transport.Register(transport.Definition{
		Kind: "tcpserver", Type: transport.KindStream, Listens: true,
		Description: "Listen on a TCP port and read a framed byte stream from every connection (optional TLS).",
		New:         New,
	})
}

// Server is the tcpserver transport.
type Server struct {
	cfg transport.Config
	tls *tls.Config
	transport.StateTracker
}

// New validates the config and builds the transport.
func New(cfg transport.Config) (transport.Transport, error) {
	if cfg.Address == "" {
		return nil, errors.New("tcpserver: address is required")
	}
	if _, _, err := net.SplitHostPort(cfg.Address); err != nil {
		return nil, errors.New("tcpserver: address must be host:port")
	}
	t, err := cfg.TLSConfig()
	if err != nil {
		return nil, err
	}
	if t != nil && len(t.Certificates) == 0 {
		return nil, errors.New("tcpserver: TLS needs certFile and keyFile")
	}
	return &Server{cfg: cfg, tls: t}, nil
}

// Run listens until ctx ends. A bind failure is fatal; accept errors are
// retried with a pause so a transient error cannot spin the loop.
func (s *Server) Run(ctx context.Context, emit transport.Emit) error {
	lc := net.ListenConfig{}
	ln, err := lc.Listen(ctx, "tcp", s.cfg.Address)
	if err != nil {
		return err
	}
	if s.tls != nil {
		ln = tls.NewListener(ln, s.tls)
	}
	defer ln.Close()
	s.Set(func(st *transport.State) { st.Connected = true; st.LastError = "" })

	var wg sync.WaitGroup
	var conns sync.Map
	go func() {
		<-ctx.Done()
		ln.Close()
		conns.Range(func(_, v any) bool { v.(net.Conn).Close(); return true })
	}()

	for {
		conn, err := ln.Accept()
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
		conns.Store(conn, conn)
		s.Set(func(st *transport.State) { st.Connections++ })
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() {
				conn.Close()
				conns.Delete(conn)
				s.Set(func(st *transport.State) { st.Connections-- })
			}()
			if err := transport.ScanConn(ctx, conn, s.cfg, emit); err != nil && ctx.Err() == nil {
				s.Set(func(st *transport.State) { st.LastError = err.Error() })
			}
		}()
	}
	wg.Wait()
	s.Set(func(st *transport.State) { st.Connected = false })
	return nil
}

// State reports connectivity.
func (s *Server) State() transport.State { return s.Get() }
