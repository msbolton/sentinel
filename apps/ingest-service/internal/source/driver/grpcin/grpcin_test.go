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

// Refused senders, failed handshakes and garbage never stop the server.
func TestBadConnectionsKeepTheServerUp(t *testing.T) {
	r := start(t, map[string]any{"maxConnections": 1}, nil, nil, nil, nil)
	// Garbage: not an HTTP/2 preface.
	c, err := net.Dial("tcp", r.addr())
	if err != nil {
		t.Fatal(err)
	}
	c.Write([]byte("hello there\n"))
	c.Close()
	// A good client still gets an answer.
	ctx, cancel := context.WithTimeout(context.Background(), waitTimeout)
	defer cancel()
	_, err = r.client(t).ListEmplacements(ctx, &ingestv1.ListEmplacementsRequest{})
	if c := status.Code(err); c == codes.Unavailable || c == codes.DeadlineExceeded {
		t.Fatalf("the server stopped answering after garbage: %v", err)
	}
	select {
	case err := <-r.done:
		t.Fatalf("Run returned while serving: %v", err)
	default:
	}
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
