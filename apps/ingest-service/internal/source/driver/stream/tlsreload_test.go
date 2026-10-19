package stream

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/sentinel/ingest-service/internal/source/driver"
	"github.com/sentinel/ingest-service/internal/source/transport/transporttest"
)

func TestMaterialFromReadsTheTLSSecrets(t *testing.T) {
	m := MaterialFrom(map[string]string{"password": "p", "tlsCert": "C", "tlsKey": "K", "clientCa": "CC", "serverCa": "SC"})
	if string(m.CertPEM) != "C" || string(m.KeyPEM) != "K" || string(m.ClientCAPEM) != "CC" || string(m.CAPEM) != "SC" {
		t.Errorf("material = %+v", m)
	}
	if m := MaterialFrom(nil); m.CertPEM != nil || m.KeyPEM != nil || m.ClientCAPEM != nil || m.CAPEM != nil {
		t.Errorf("no secrets gave material %+v, want none (so the files are read)", m)
	}
}

func tlsListenerSpec(addr string, first transporttest.Pair) driver.Spec {
	return driver.Spec{FeedID: "tls-1", Driver: ID,
		Config:  json.RawMessage(`{"transport":{"kind":"tcpserver","address":"` + addr + `","tls":{"enabled":true}},"framing":"line","decoder":{"name":"sbs"}}`),
		Secrets: map[string]string{"tlsCert": string(first.CertPEM), "tlsKey": string(first.KeyPEM)}}
}

func leafSerial(t *testing.T, p transporttest.Pair) string {
	t.Helper()
	pair, err := tls.X509KeyPair(p.CertPEM, p.KeyPEM)
	if err != nil {
		t.Fatal(err)
	}
	return pair.Leaf.SerialNumber.String()
}

func servedSerial(t *testing.T, addr string) string {
	t.Helper()
	c, err := tls.Dial("tcp", addr, &tls.Config{InsecureSkipVerify: true}) //nolint:gosec // a test asking which certificate is served
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()
	return c.ConnectionState().PeerCertificates[0].SerialNumber.String()
}

// The supervisor hands the driver a function that resolves the secret
// references again; the stream driver turns what it returns into the material
// the transport reloads, and the listener serves the new certificate.
func TestReloadSecretsReachTransport(t *testing.T) {
	ca := transporttest.NewCA(t)
	first, second := ca.Issue(t, "localhost"), ca.Issue(t, "localhost")
	var calls atomic.Int64
	addr := freePort(t)
	d, err := New(tlsListenerSpec(addr, first), driver.Deps{Logger: zap.NewNop(), SecretReloadEvery: 10 * time.Millisecond,
		ReloadSecrets: func() (map[string]string, error) {
			calls.Add(1)
			return map[string]string{"tlsCert": string(second.CertPEM), "tlsKey": string(second.KeyPEM)}, nil
		}})
	if err != nil {
		t.Fatal(err)
	}
	if d.(*Driver).tcfg.ReloadTLS == nil || d.(*Driver).tcfg.ReloadEvery != 10*time.Millisecond {
		t.Fatalf("the transport got ReloadTLS %v and ReloadEvery %v", d.(*Driver).tcfg.ReloadTLS != nil, d.(*Driver).tcfg.ReloadEvery)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = d.Run(ctx, newSink()) }()
	defer func() { cancel(); <-done }()

	want := leafSerial(t, second)
	if !waitFor(func() bool {
		c, err := tls.Dial("tcp", addr, &tls.Config{InsecureSkipVerify: true}) //nolint:gosec // see servedSerial
		if err != nil {
			return false
		}
		defer c.Close()
		return c.ConnectionState().PeerCertificates[0].SerialNumber.String() == want
	}, 3*time.Second) {
		t.Fatalf("the listener serves %s, want the reloaded secret's certificate %s (reloader called %d times)", servedSerial(t, addr), want, calls.Load())
	}
	h := d.Health()
	if h.Detail["tlsReloads"] != int64(1) {
		t.Errorf("tlsReloads = %v, want 1", h.Detail["tlsReloads"])
	}
}

// A reloader that fails keeps the certificate being served and counts a failed
// reload.
func TestReloadSecretsErrorKeepsTheCertificate(t *testing.T) {
	first := transporttest.NewCA(t).Issue(t, "localhost")
	addr := freePort(t)
	d, err := New(tlsListenerSpec(addr, first), driver.Deps{Logger: zap.NewNop(), SecretReloadEvery: 10 * time.Millisecond,
		ReloadSecrets: func() (map[string]string, error) { return nil, errors.New("environment variable GONE is not set") }})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = d.Run(ctx, newSink()) }()
	defer func() { cancel(); <-done }()
	if !waitFor(func() bool { n, _ := d.Health().Detail["tlsReloadFailed"].(int64); return n >= 1 }, 3*time.Second) {
		t.Fatalf("no failed reload counted: %+v", d.Health().Detail)
	}
	if got, want := servedSerial(t, addr), leafSerial(t, first); got != want {
		t.Errorf("serving %s after a failed reload, want %s", got, want)
	}
}

// Without TLS there is nothing to reload, and a listener that is not told how
// often to look does not look.
func TestNoReloadWithoutTLS(t *testing.T) {
	plain := driver.Spec{FeedID: "p", Driver: ID, Config: json.RawMessage(`{"transport":{"kind":"tcpserver","address":"127.0.0.1:0"},"framing":"line","decoder":{"name":"sbs"}}`)}
	d, err := New(plain, driver.Deps{Logger: zap.NewNop(), SecretReloadEvery: time.Second, ReloadSecrets: func() (map[string]string, error) { return nil, nil }})
	if err != nil {
		t.Fatal(err)
	}
	if d.(*Driver).tcfg.ReloadTLS != nil {
		t.Error("a plain listener was given a TLS reloader")
	}
	for _, key := range []string{"tlsReloads", "tlsReloadFailed", "tlsCertNotAfter"} {
		if _, has := d.Health().Detail[key]; has {
			t.Errorf("a plain listener reports %s", key)
		}
	}
}
