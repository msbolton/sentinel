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
	if len(c.Certificates) != 1 || c.ClientCAs == nil || c.ClientAuth != tls.RequireAndVerifyClientCert {
		t.Errorf("tls config = %+v", c)
	}
	cfg.ClientCAPEM = nil
	if _, err := cfg.TLSConfig(); err == nil {
		t.Error("requiring client certificates without a client CA must fail")
	}
	cfg = transport.Config{TLS: &transport.TLS{Enabled: true}, CAPEM: ca.CertPEM}
	if c, err := cfg.TLSConfig(); err != nil || c.RootCAs == nil {
		t.Errorf("client side: %v %+v", err, c)
	}
}
