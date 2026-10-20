package client

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"

	"github.com/sentinel/ew-sensor/internal/config"
)

// tokenCredentials sends the ingest token on every call as
// `authorization: Bearer <token>`.
type tokenCredentials struct {
	token      string
	requireTLS bool
}

// NewTokenCredentials returns per-RPC credentials carrying token. With
// requireTLS false they may be sent over plaintext h2c (--insecure).
func NewTokenCredentials(token string, requireTLS bool) credentials.PerRPCCredentials {
	return tokenCredentials{token: token, requireTLS: requireTLS}
}

func (t tokenCredentials) GetRequestMetadata(context.Context, ...string) (map[string]string, error) {
	return map[string]string{"authorization": "Bearer " + t.token}, nil
}

func (t tokenCredentials) RequireTransportSecurity() bool { return t.requireTLS }

// KeepaliveParams are the client keepalive settings. Time must stay at or
// above the server's enforcement MinTime (10 s), or the server answers with
// GOAWAY "too_many_pings".
func KeepaliveParams() keepalive.ClientParameters {
	return keepalive.ClientParameters{Time: 20 * time.Second, Timeout: 10 * time.Second, PermitWithoutStream: true}
}

// DialOptions returns the transport and per-RPC credentials for conn:
// plaintext h2c for --insecure (logging that the token travels in clear),
// else TLS with the system roots or --ca, plus the --cert/--key client
// certificate when given. Errors never contain the token.
func DialOptions(conn config.Connection, log *slog.Logger) ([]grpc.DialOption, error) {
	switch {
	case conn.Insecure && conn.TLS:
		return nil, errors.New("--insecure and --tls are mutually exclusive")
	case conn.Insecure:
		log.Warn("insecure transport: the ingest token travels in clear (h2c); use --tls outside a trusted network",
			"token", conn.RedactedToken())
		return []grpc.DialOption{
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithPerRPCCredentials(NewTokenCredentials(conn.Token, false)),
		}, nil
	case conn.TLS:
		tc, err := tlsConfig(conn)
		if err != nil {
			return nil, err
		}
		return []grpc.DialOption{
			grpc.WithTransportCredentials(credentials.NewTLS(tc)),
			grpc.WithPerRPCCredentials(NewTokenCredentials(conn.Token, true)),
		}, nil
	}
	return nil, errors.New("one of --insecure and --tls is required")
}

func tlsConfig(conn config.Connection) (*tls.Config, error) {
	tc := &tls.Config{MinVersion: tls.VersionTLS12}
	if conn.CA != "" {
		pem, err := os.ReadFile(conn.CA)
		if err != nil {
			return nil, fmt.Errorf("--ca: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("--ca %s: no PEM certificates found", conn.CA)
		}
		tc.RootCAs = pool
	} else {
		pool, err := x509.SystemCertPool()
		if err != nil {
			return nil, fmt.Errorf("loading the system root certificates: %w", err)
		}
		tc.RootCAs = pool
	}
	if conn.Cert != "" || conn.Key != "" {
		cert, err := tls.LoadX509KeyPair(conn.Cert, conn.Key)
		if err != nil {
			return nil, fmt.Errorf("--cert/--key: %w", err)
		}
		tc.Certificates = []tls.Certificate{cert}
	}
	return tc, nil
}
