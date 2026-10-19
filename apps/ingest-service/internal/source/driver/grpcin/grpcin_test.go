package grpcin

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	"github.com/sentinel/ingest-service/internal/source/driver"
	"github.com/sentinel/ingest-service/internal/source/transport/transporttest"
	ingestv1 "github.com/sentinel/proto-gen/sentinel/ingest/v1"
)

func TestListen(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		port int
		ok   bool
	}{
		{`{"port":6100}`, 6100, true},
		{`{"port":"6100"}`, 0, true}, // listens, so the port rules refuse it
		{`{}`, 0, true},
		{`{"port":6100,"allow":"oops"}`, 6100, true},
		{`{nope`, 0, false},
		{``, 0, false},
	} {
		network, port, ok := listen(json.RawMessage(tc.raw))
		if ok != tc.ok || port != tc.port || (ok && network != "tcp") {
			t.Errorf("listen(%s) = %q, %d, %v; want tcp, %d, %v", tc.raw, network, port, ok, tc.port, tc.ok)
		}
	}
}

func TestNewRefusals(t *testing.T) {
	for _, tc := range []struct {
		name    string
		cfg     map[string]any
		secrets map[string]string
		bp      string
		want    string
	}{
		{"low port", map[string]any{"port": 80}, nil, "", "port"},
		{"high port", map[string]any{"port": 70000}, nil, "", "port"},
		{"huge messages", map[string]any{"port": 6100, "maxMessageBytes": 32 << 20}, nil, "", "maxMessageBytes"},
		{"zero batch", map[string]any{"port": 6100, "maxPublishBatch": 0}, nil, "", ""}, // 0 means the default
		{"batch too big", map[string]any{"port": 6100, "maxPublishBatch": 5000}, nil, "", "maxPublishBatch"},
		{"bad allowlist", map[string]any{"port": 6100, "allow": []string{"not an ip"}}, nil, "", "allow"},
		{"client CA without requiring certs", map[string]any{"port": 6100, "tls": map[string]any{"enabled": true}}, map[string]string{"clientCa": "x"}, "", "requireClientCert"},
		{"client certs without TLS", map[string]any{"port": 6100, "tls": map[string]any{"requireClientCert": true}}, nil, "", "tls.enabled"},
		{"tiny messages", map[string]any{"port": 6100, "maxMessageBytes": 100}, nil, "", "maxMessageBytes"},
		{"negative messages", map[string]any{"port": 6100, "maxMessageBytes": -1}, nil, "", "maxMessageBytes"},
		{"negative connections", map[string]any{"port": 6100, "maxConnections": -1}, nil, "", "maxConnections"},
		{"negative rate", map[string]any{"port": 6100, "maxMessagesPerSec": -1}, nil, "", "maxMessagesPerSec"},
		{"negative sender rate", map[string]any{"port": 6100, "maxMessagesPerSecPerSender": -1}, nil, "", "maxMessagesPerSecPerSender"},
		{"drop backpressure", map[string]any{"port": 6100}, nil, "drop", "backpressure"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := spec(tc.cfg, tc.secrets)
			s.Backpressure = tc.bp
			_, err := New(s, driver.Deps{Logger: zap.NewNop(), Tokens: newFakeTokens(), Emplacements: &fakeEmplacements{}})
			if tc.want == "" {
				if err != nil {
					t.Fatalf("New: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("New error = %v, want one naming %q", err, tc.want)
			}
		})
	}
}

// The server answers calls, and stops promptly, freeing its port.
func TestServesAndStopsPromptly(t *testing.T) {
	r := start(t, nil, nil, nil, nil, nil)
	ctx, cancel := context.WithTimeout(context.Background(), waitTimeout)
	defer cancel()
	_, err := r.client(t).ListEmplacements(ctx, &ingestv1.ListEmplacementsRequest{})
	if c := status.Code(err); c == codes.Unavailable || c == codes.DeadlineExceeded {
		t.Fatalf("the server did not answer: %v", err)
	}
	began := time.Now()
	if err := r.stop(t); err != nil {
		t.Fatalf("Run after cancel = %v, want nil", err)
	}
	if took := time.Since(began); took > time.Second {
		t.Errorf("an idle server took %v to stop", took)
	}
	l, err := net.Listen("tcp", r.addr())
	if err != nil {
		t.Fatalf("the port is still held: %v", err)
	}
	l.Close()
}

func TestBindConflictIsExplained(t *testing.T) {
	held, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	port := held.Addr().(*net.TCPAddr).Port
	dv, err := New(spec(map[string]any{"port": port, "address": "127.0.0.1"}, nil), driver.Deps{Logger: zap.NewNop(), Tokens: newFakeTokens(), Emplacements: &fakeEmplacements{}})
	if err != nil {
		t.Fatal(err)
	}
	err = dv.Run(context.Background(), &memSink{})
	if err == nil || !strings.Contains(err.Error(), "already in use") {
		t.Fatalf("Run = %v, want the port named as in use", err)
	}
}

func TestRunNeedsTokensAndEmplacements(t *testing.T) {
	dv, err := New(spec(map[string]any{"port": freePort(t), "address": "127.0.0.1"}, nil), driver.Deps{Logger: zap.NewNop()})
	if err != nil {
		t.Fatal(err)
	}
	if err := dv.Run(context.Background(), &memSink{}); err == nil || !strings.Contains(err.Error(), "token") {
		t.Fatalf("Run without a token cache = %v, want an error naming it", err)
	}
}

// answered fails the test unless a good client gets a gRPC answer.
func answered(t *testing.T, r *rig, what string) {
	t.Helper()
	// The client's connection is closed at once: with a cap of one it would
	// hold the only place.
	cc, err := grpc.NewClient("passthrough:///"+r.addr(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer cc.Close()
	ctx, cancel := context.WithTimeout(context.Background(), waitTimeout)
	defer cancel()
	_, err = ingestv1.NewIngestServiceClient(cc).ListEmplacements(ctx, &ingestv1.ListEmplacementsRequest{})
	if c := status.Code(err); c == codes.Unavailable || c == codes.DeadlineExceeded {
		t.Fatalf("the server did not answer %s: %v", what, err)
	}
}

func stillRunning(t *testing.T, r *rig) {
	t.Helper()
	select {
	case err := <-r.done:
		t.Fatalf("Run returned while serving: %v", err)
	default:
	}
}

// Refused senders, failed handshakes and garbage never stop the server.
func TestBadConnectionsKeepTheServerUp(t *testing.T) {
	r := start(t, map[string]any{"maxConnections": 1}, nil, nil, nil, nil)
	gl := r.d.gl.Load()

	// Garbage: not an HTTP/2 preface.
	c, err := net.Dial("tcp", r.addr())
	if err != nil {
		t.Fatal(err)
	}
	c.Write([]byte("hello there\n"))
	c.Close()
	waitFor(t, func() bool { return gl.Live() == 0 })
	answered(t, r, "after garbage")

	// The cap: one admitted connection is held, so the next is refused.
	waitFor(t, func() bool { return gl.Live() == 0 })
	held, err := net.Dial("tcp", r.addr())
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	waitFor(t, func() bool { return gl.Live() == 1 })
	over, err := net.Dial("tcp", r.addr())
	if err != nil {
		t.Fatal(err)
	}
	defer over.Close()
	waitFor(t, func() bool { return r.d.counts.Refused.Load() == 1 })
	stillRunning(t, r)
	held.Close()
	waitFor(t, func() bool { return gl.Live() == 0 })
	answered(t, r, "after the cap was reached")
	stillRunning(t, r)
}

func TestAllowlistRefusalIsCounted(t *testing.T) {
	r := start(t, map[string]any{"allow": []string{"10.0.0.0/8"}}, nil, nil, nil, nil)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := r.client(t).ListEmplacements(ctx, &ingestv1.ListEmplacementsRequest{})
	if err == nil {
		t.Fatal("a sender outside the allowlist got an answer")
	}
	waitFor(t, func() bool { return r.d.counts.Rejected.Load() >= 1 })
	select {
	case err := <-r.done:
		t.Fatalf("Run returned after a refusal: %v", err)
	default:
	}
}

func TestTLSFromSecrets(t *testing.T) {
	ca := transporttest.NewCA(t)
	pair := ca.Issue(t, "localhost")
	r := start(t, map[string]any{"tls": map[string]any{"enabled": true}}, map[string]string{"tlsCert": string(pair.CertPEM), "tlsKey": string(pair.KeyPEM)}, nil, nil, nil)
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(ca.CertPEM)
	cc, err := grpc.NewClient("passthrough:///"+r.addr(), grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{RootCAs: roots, ServerName: "localhost"})))
	if err != nil {
		t.Fatal(err)
	}
	defer cc.Close()
	ctx, cancel := context.WithTimeout(context.Background(), waitTimeout)
	defer cancel()
	_, err = ingestv1.NewIngestServiceClient(cc).ListEmplacements(ctx, &ingestv1.ListEmplacementsRequest{})
	if c := status.Code(err); c == codes.Unavailable || c == codes.DeadlineExceeded {
		t.Fatalf("a TLS client got no answer: %v", err)
	}
	// A plain-text client fails its handshake, which is counted.
	ctx2, cancel2 := context.WithTimeout(context.Background(), time.Second)
	defer cancel2()
	_, _ = r.client(t).ListEmplacements(ctx2, &ingestv1.ListEmplacementsRequest{})
	waitFor(t, func() bool { return r.d.counts.TLSFailed.Load() >= 1 })
	ctx3, cancel3 := context.WithTimeout(context.Background(), waitTimeout)
	defer cancel3()
	if _, err := ingestv1.NewIngestServiceClient(cc).ListEmplacements(ctx3, &ingestv1.ListEmplacementsRequest{}); status.Code(err) == codes.Unavailable || status.Code(err) == codes.DeadlineExceeded {
		t.Fatalf("a TLS client got no answer after a failed handshake: %v", err)
	}
	stillRunning(t, r)
	if h := r.d.Health(); h.Detail["tlsCertNotAfter"] == nil || h.Detail["tls"] != true {
		t.Errorf("TLS health detail missing: %v", h.Detail)
	}
}

func TestHealthDetail(t *testing.T) {
	r := start(t, nil, nil, nil, nil, nil)
	d := r.d.Health().Detail
	for _, k := range []string{"accepted", "rejected", "refused", "rateLimited", "senderRateLimited", "oversize", "tlsFailed", "connections", "distinctSenders", "reportsOk", "deadLettered"} {
		if _, ok := d[k]; !ok {
			t.Errorf("health detail lacks %q", k)
		}
	}
	listen, _ := d["listen"].(map[string]any)
	if listen["protocol"] != "grpc" || listen["host"] != "sentinel.example" || listen["port"] != r.port {
		t.Errorf("listen = %v", listen)
	}
	if _, ok := d["reportsRejected"].(map[string]int64); !ok {
		t.Errorf("reportsRejected = %T, want map[string]int64", d["reportsRejected"])
	}
	if d["allowlist"] != "open" {
		t.Errorf("allowlist = %v, want open", d["allowlist"])
	}
}

func tlsClient(t *testing.T, addr string, cfg *tls.Config) ingestv1.IngestServiceClient {
	t.Helper()
	cc, err := grpc.NewClient("passthrough:///"+addr, grpc.WithTransportCredentials(credentials.NewTLS(cfg)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cc.Close() })
	return ingestv1.NewIngestServiceClient(cc)
}

// With client certificates required, a certificate from the right CA is
// answered; none, or one from another CA, is refused and counted.
func TestMutualTLS(t *testing.T) {
	ca := transporttest.NewCA(t)
	server := ca.Issue(t, "localhost")
	good := ca.Issue(t, "sender")
	other := transporttest.NewCA(t).Issue(t, "intruder")
	r := start(t, map[string]any{"tls": map[string]any{"enabled": true, "requireClientCert": true}},
		map[string]string{"tlsCert": string(server.CertPEM), "tlsKey": string(server.KeyPEM), "clientCa": string(ca.CertPEM)}, nil, nil, nil)
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(ca.CertPEM)
	call := func(pair *transporttest.Pair) error {
		cfg := &tls.Config{RootCAs: roots, ServerName: "localhost"}
		if pair != nil {
			cert, err := tls.X509KeyPair(pair.CertPEM, pair.KeyPEM)
			if err != nil {
				t.Fatal(err)
			}
			cfg.Certificates = []tls.Certificate{cert}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_, err := tlsClient(t, r.addr(), cfg).ListEmplacements(ctx, &ingestv1.ListEmplacementsRequest{})
		return err
	}
	if err := call(&good); status.Code(err) == codes.Unavailable || status.Code(err) == codes.DeadlineExceeded {
		t.Fatalf("a client with a certificate from the right CA got no answer: %v", err)
	}
	if r.d.counts.TLSFailed.Load() != 0 {
		t.Fatalf("tlsFailed = %d after a good client", r.d.counts.TLSFailed.Load())
	}
	if err := call(nil); err == nil {
		t.Fatal("a client without a certificate was answered")
	}
	waitFor(t, func() bool { return r.d.counts.TLSFailed.Load() >= 1 })
	before := r.d.counts.TLSFailed.Load()
	if err := call(&other); err == nil {
		t.Fatal("a client with a certificate from another CA was answered")
	}
	waitFor(t, func() bool { return r.d.counts.TLSFailed.Load() > before })
	if err := call(&good); status.Code(err) == codes.Unavailable || status.Code(err) == codes.DeadlineExceeded {
		t.Fatalf("the good client got no answer after refusals: %v", err)
	}
	stillRunning(t, r)
}

// Before Run, Health already reports every listener counter, as numbers.
func TestHealthBeforeRun(t *testing.T) {
	dv, err := New(spec(map[string]any{"port": 6100}, nil), driver.Deps{Logger: zap.NewNop()})
	if err != nil {
		t.Fatal(err)
	}
	h := dv.Health()
	if h.Connected {
		t.Error("connected before Run")
	}
	for _, k := range []string{"accepted", "rejected", "refused", "rateLimited", "senderRateLimited", "oversize", "tlsFailed", "connections", "distinctSenders", "reportsOk", "deadLettered"} {
		switch h.Detail[k].(type) {
		case int, int64, uint64, int32, float64:
		default:
			t.Errorf("health detail %q = %T(%v), want a number", k, h.Detail[k], h.Detail[k])
		}
	}
}

// A driver whose Run returned serves again when the supervisor runs it again.
func TestRunTwice(t *testing.T) {
	r := start(t, nil, nil, nil, nil, nil)
	answered(t, r, "the first time")
	if err := r.stop(t); err != nil {
		t.Fatalf("first Run = %v, want nil", err)
	}
	if r.d.connected.Load() {
		t.Fatal("still connected after Run returned")
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.d.Run(ctx, &memSink{}) }()
	waitFor(t, func() bool { return r.d.connected.Load() })
	answered(t, r, "the second time")
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("second Run = %v, want nil", err)
		}
	case <-time.After(stopGrace + 2*time.Second):
		t.Fatal("second Run did not return")
	}
}

// One connection that sends nothing, in the TLS handshake or the HTTP/2
// preface, must not hold Run past its bound: grpc waits for such a
// connection until its connection timeout (10 s) unless it is closed.
func TestStopIsBoundedWithAStalledConnection(t *testing.T) {
	for _, tc := range []struct {
		name string
		tls  bool
	}{{"plain", false}, {"tls", true}} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := map[string]any{}
			var secrets map[string]string
			if tc.tls {
				pair := transporttest.NewCA(t).Issue(t, "localhost")
				cfg["tls"] = map[string]any{"enabled": true}
				secrets = map[string]string{"tlsCert": string(pair.CertPEM), "tlsKey": string(pair.KeyPEM)}
			}
			r := start(t, cfg, secrets, nil, nil, nil)
			c, err := net.Dial("tcp", r.addr())
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			waitFor(t, func() bool { return r.d.gl.Load().Live() == 1 })
			began := time.Now()
			r.cancel()
			select {
			case err := <-r.done:
				r.done <- err
				if err != nil {
					t.Fatalf("Run = %v, want nil", err)
				}
			case <-time.After(stopGrace + 500*time.Millisecond):
				t.Fatalf("Run still running %v after cancel", time.Since(began))
			}
		})
	}
}
