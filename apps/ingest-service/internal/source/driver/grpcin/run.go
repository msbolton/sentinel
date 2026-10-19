package grpcin

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"sync/atomic"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/keepalive"

	"github.com/sentinel/ingest-service/internal/ingesttoken"
	"github.com/sentinel/ingest-service/internal/source/driver"
	"github.com/sentinel/ingest-service/internal/source/driver/stream"
	"github.com/sentinel/ingest-service/internal/source/transport"
	ingestv1 "github.com/sentinel/proto-gen/sentinel/ingest/v1"
)

const (
	// stopGrace is how long open calls get to finish when the feed stops
	// before they are cut: the supervisor waits for Run, and every other
	// feed's lease renewal waits with it.
	stopGrace        = 3 * time.Second
	handshakeTimeout = 10 * time.Second
	sendersWindow    = 10 * time.Minute
	// idleTimeout closes a connection with no RPCs for that long, so an idle
	// client cannot hold a place.
	idleTimeout = 5 * time.Minute
)

func (d *Driver) serverOptions(svc *service) []grpc.ServerOption {
	opts := []grpc.ServerOption{
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{MinTime: 10 * time.Second, PermitWithoutStream: true}),
		grpc.MaxRecvMsgSize(d.maxMessageBytes),
		grpc.MaxConcurrentStreams(4),
		grpc.ConnectionTimeout(handshakeTimeout),
		grpc.KeepaliveParams(keepalive.ServerParameters{MaxConnectionIdle: d.idleTimeout}),
		grpc.ChainUnaryInterceptor(svc.unary),
		grpc.ChainStreamInterceptor(svc.stream),
	}
	if d.creds != nil {
		opts = append(opts, grpc.Creds(d.creds))
	}
	return opts
}

// Run binds the port and serves until ctx ends, then stops within
// stopGrace and returns nil. An error before that (the bind, the
// listener) fails the feed, and the supervisor retries.
func (d *Driver) Run(ctx context.Context, sink driver.Sink) error {
	if d.tokens == nil || d.emps == nil {
		return errNoTokens
	}
	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", d.addr)
	if err != nil {
		return transport.BindError("tcp", d.addr, err)
	}
	gl := transport.NewGuardListener(ln, d.allow, d.maxConns, &d.counts, d.senders)
	d.gl.Store(gl)
	d.stopping.Store(false)

	tlsCtx, stopTLS := context.WithCancel(ctx)
	reloadDone := make(chan struct{})
	go func() { defer close(reloadDone); d.tlsrc.Run(tlsCtx) }()
	defer func() { stopTLS(); <-reloadDone }()

	svc := newService(d, sink, ctx)
	unsubscribe := d.tokens.OnRevoke(func(t ingesttoken.Token) {
		if t.FeedID == d.feedID {
			svc.calls.revoke(t.ID)
		}
	})
	defer unsubscribe()
	srv := grpc.NewServer(d.serverOptions(svc)...)
	ingestv1.RegisterIngestServiceServer(srv, svc)
	served := make(chan error, 1)
	go func() { served <- srv.Serve(gl) }()
	d.connected.Store(true)
	defer d.connected.Store(false)

	select {
	case err := <-served:
		d.stopping.Store(true)
		gl.CloseAll()
		d.stop(srv, gl)
		return fmt.Errorf("grpc: serving stopped: %w", err)
	case <-ctx.Done():
	}
	d.stopping.Store(true)
	d.stop(srv, gl)
	<-served
	return nil
}

// stop lets open calls end (they are told the connection is moving) for up
// to stopGrace, then cuts what is left. grpc's Stop does not close a
// connection still in its TLS handshake or HTTP/2 preface, and waits for it
// until the connection timeout, so the guard listener closes them all first.
func (d *Driver) stop(srv *grpc.Server, gl *transport.GuardListener) {
	done := make(chan struct{})
	go func() { srv.GracefulStop(); close(done) }()
	select {
	case <-done:
	case <-time.After(stopGrace):
		gl.CloseAll()
		srv.Stop()
		<-done
	}
}

// prepareTLS loads the certificate (and client CA) from the secrets, with
// reloads, and wraps them in grpc credentials that count failed handshakes.
func (d *Driver) prepareTLS(spec driver.Spec, deps driver.Deps, mutual bool) error {
	tcfg := transport.Config{TLS: &transport.TLS{Enabled: true, RequireClientCert: mutual}}
	stream.ApplySecrets(&tcfg, spec.Secrets)
	tcfg.ReloadTLS, tcfg.ReloadEvery = stream.ReloadTLS(spec.Secrets, deps.ReloadSecrets), deps.SecretReloadEvery
	tcfg.Logger = deps.Logger
	tcfg.FileDirs = stream.FileDirs
	finish := func(t *tls.Config) error {
		if mutual && (t == nil || t.ClientCAs == nil) {
			return errors.New("requiring client certificates needs a client CA")
		}
		if t == nil {
			return nil
		}
		if len(t.Certificates) == 0 {
			return errors.New("TLS needs a certificate and key")
		}
		t.SessionTicketsDisabled = true
		if mutual {
			t.ClientAuth = tls.RequireAndVerifyClientCert
		}
		// grpc-go refuses a connection that did not negotiate h2, and this
		// config replaces the one credentials.NewTLS fills in.
		t.NextProtos = []string{"h2"}
		return nil
	}
	src, err := transport.NewTLSSource(tcfg, finish)
	if err != nil {
		return err
	}
	d.tlsrc = src
	base := credentials.NewTLS(&tls.Config{
		GetConfigForClient: func(*tls.ClientHelloInfo) (*tls.Config, error) { return src.Current(), nil },
	})
	d.creds = &countingCreds{TransportCredentials: base, failed: &d.counts.TLSFailed, stopping: &d.stopping, log: deps.Logger}
	return nil
}

// countingCreds counts the handshakes that fail while the server runs.
type countingCreds struct {
	credentials.TransportCredentials
	failed   *atomic.Int64
	stopping *atomic.Bool
	log      *zap.Logger
}

func (c *countingCreds) ServerHandshake(raw net.Conn) (net.Conn, credentials.AuthInfo, error) {
	conn, info, err := c.TransportCredentials.ServerHandshake(raw)
	if err != nil && !c.stopping.Load() {
		// The log comes first, so that whoever sees the counter move can
		// find the sender in it.
		if c.log != nil {
			c.log.Debug("grpc TLS handshake failed", zap.String("remote", raw.RemoteAddr().String()), zap.Error(err))
		}
		c.failed.Add(1)
	}
	return conn, info, err
}

func (c *countingCreds) Clone() credentials.TransportCredentials {
	return &countingCreds{TransportCredentials: c.TransportCredentials.Clone(), failed: c.failed, stopping: c.stopping, log: c.log}
}
