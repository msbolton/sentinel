package transport_test

import (
	"crypto/tls"
	"testing"

	"github.com/sentinel/ingest-service/internal/source/transport"
	"github.com/sentinel/ingest-service/internal/source/transport/transporttest"
)

// External test package: transporttest imports transport, so an internal
// test importing it would be an import cycle.
func TestTLSConfigFromPEM(t *testing.T) {
	ca := transporttest.NewCA(t)
	server := ca.Issue(t, "localhost")
	cfg := transport.Config{TLS: &transport.TLS{Enabled: true, RequireClientCert: true}, CertPEM: server.CertPEM, KeyPEM: server.KeyPEM, ClientCAPEM: ca.CertPEM}
	c, err := cfg.TLSConfig()
	if err != nil {
		t.Fatal(err)
	}
	// The client CA is built into the config; whether to ask for client
	// certificates is the listener's decision (tcpserver sets ClientAuth), since
	// this config serves dialers too.
	if len(c.Certificates) != 1 || c.ClientCAs == nil || c.ClientAuth != tls.NoClientCert {
		t.Errorf("tls config = %+v", c)
	}
	// requireClientCert is a listener setting: with no client CA the config
	// still builds, and asks for nothing.
	cfg.ClientCAPEM = nil
	c, err = cfg.TLSConfig()
	if err != nil || c.ClientAuth != tls.NoClientCert {
		t.Errorf("requireClientCert without a client CA: err = %v, config = %+v; want it to build and ask for no client certificate", err, c)
	}
	cfg = transport.Config{TLS: &transport.TLS{Enabled: true}, CAPEM: ca.CertPEM}
	if c, err := cfg.TLSConfig(); err != nil || c.RootCAs == nil {
		t.Errorf("client side: %v %+v", err, c)
	}
}
