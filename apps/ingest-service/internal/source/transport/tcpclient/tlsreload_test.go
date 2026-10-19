package tcpclient

import (
	"crypto/tls"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sentinel/ingest-service/internal/source/transport"
	"github.com/sentinel/ingest-service/internal/source/transport/transporttest"
)

// The client builds its TLS config again before each dial, so a server whose
// certificate moved to a new CA is trusted after the next reconnect, with no
// restart.
func TestClientReloadsMaterialOnReconnect(t *testing.T) {
	oldCA, newCA := transporttest.NewCA(t), transporttest.NewCA(t)
	oldPair, newPair := oldCA.Issue(t, "localhost"), newCA.Issue(t, "localhost")
	var serving atomic.Pointer[tls.Certificate]
	load := func(p transporttest.Pair) *tls.Certificate {
		c, err := tls.X509KeyPair(p.CertPEM, p.KeyPEM)
		if err != nil {
			t.Fatal(err)
		}
		return &c
	}
	serving.Store(load(oldPair))
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{MinVersion: tls.VersionTLS12,
		GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) { return serving.Load(), nil }})
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	accepted := make(chan net.Conn, 4)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			accepted <- c
		}
	}()

	var trusted atomic.Pointer[[]byte]
	trusted.Store(&oldCA.CertPEM)
	tr, err := New(transport.Config{Kind: "tcpclient", Address: ln.Addr().String(), Framing: "line",
		TLS: &transport.TLS{Enabled: true, ServerName: "localhost"}, CAPEM: oldCA.CertPEM,
		ReloadTLS:   func() (transport.TLSMaterial, error) { return transport.TLSMaterial{CAPEM: *trusted.Load()}, nil },
		ReloadEvery: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	col := &transporttest.Collector{}
	stop := transporttest.Run(t, tr, col.Emit)
	defer stop()

	next := func(what string) net.Conn {
		t.Helper()
		select {
		case c := <-accepted:
			return c
		case <-time.After(5 * time.Second):
			t.Fatalf("no connection %s", what)
			return nil
		}
	}
	c := next("under the old CA")
	_, _ = c.Write([]byte("before\n"))
	if !transporttest.WaitFor(func() bool { return col.Count() == 1 }, 2*time.Second) {
		t.Fatalf("frames = %v", col.Payloads())
	}

	// The server moves to a certificate from the new CA, and the material the
	// client reloads names that CA. The old connection drops; the dial that
	// follows must read the new material.
	serving.Store(load(newPair))
	trusted.Store(&newCA.CertPEM)
	c.Close()
	c = next("under the new CA")
	_, _ = c.Write([]byte("after\n"))
	if !transporttest.WaitFor(func() bool { return col.Count() == 2 }, 2*time.Second) {
		t.Fatalf("frames after the reconnect = %v (LastError %q)", col.Payloads(), tr.State().LastError)
	}
	c.Close()
	if got := tr.State().Detail["tlsReloads"]; got != int64(1) {
		t.Errorf("tlsReloads = %v, want 1", got)
	}
}

// A reload that fails does not stop the client: it dials with the config it
// has, and counts the failure.
func TestClientKeepsOldMaterialWhenTheReloadFails(t *testing.T) {
	ca := transporttest.NewCA(t)
	pair := ca.Issue(t, "localhost")
	addr, handshakes := transporttest.TLSListener(t, pair, nil)
	tr, err := New(transport.Config{Kind: "tcpclient", Address: addr, Framing: "line",
		TLS: &transport.TLS{Enabled: true, ServerName: "localhost"}, CAPEM: ca.CertPEM,
		ReloadTLS: func() (transport.TLSMaterial, error) {
			return transport.TLSMaterial{CAPEM: []byte("not a certificate")}, nil
		},
		ReloadEvery: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	stop := transporttest.Run(t, tr, (&transporttest.Collector{}).Emit)
	defer stop()
	if !transporttest.WaitFor(func() bool { return handshakes.Load() >= 1 }, 3*time.Second) {
		t.Fatalf("no handshake with the old material (LastError %q)", tr.State().LastError)
	}
	if got, _ := tr.State().Detail["tlsReloadFailed"].(int64); got < 1 {
		t.Errorf("tlsReloadFailed = %v, want at least 1", tr.State().Detail["tlsReloadFailed"])
	}
}
