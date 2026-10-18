package tcp

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"
	"google.golang.org/protobuf/proto"

	"github.com/sentinel/ingest-service/internal/obs"
	"github.com/sentinel/ingest-service/internal/source/driver"
	"github.com/sentinel/ingest-service/internal/source/transport/transporttest"
	sentinelv1 "github.com/sentinel/proto-gen/sentinel/v1"

	// The pipeline pieces the driver assembles. The all package would pull
	// in this package and so cannot be imported from its own test.
	_ "github.com/sentinel/ingest-service/internal/source/decoder/sentinelobs"
	_ "github.com/sentinel/ingest-service/internal/source/transport/tcpclient"
	_ "github.com/sentinel/ingest-service/internal/source/transport/tcpserver"
)

const waitTimeout = 5 * time.Second

type memSink struct {
	mu  sync.Mutex
	obs []*obs.Observation
}

func (s *memSink) Emit(_ context.Context, o *obs.Observation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.obs = append(s.obs, o)
	return nil
}
func (s *memSink) DeadLetter([]byte, string, error) {}
func (s *memSink) Skipped(string)                   {}
func (s *memSink) Succeeded(int)                    {}
func (s *memSink) Blocked(time.Duration)            {}
func (s *memSink) count() int                       { s.mu.Lock(); defer s.mu.Unlock(); return len(s.obs) }

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func spec(t *testing.T, cfg map[string]any, secrets map[string]string) driver.Spec {
	t.Helper()
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return driver.Spec{FeedID: "f", Driver: ID, Config: raw, Secrets: secrets}
}

func newDriver(t *testing.T, cfg map[string]any, secrets map[string]string) (*Driver, error) {
	t.Helper()
	d, err := New(spec(t, cfg, secrets), driver.Deps{Logger: zap.NewNop(), ListenerHost: "sentinel.example"})
	if err != nil {
		return nil, err
	}
	return d.(*Driver), nil
}

// start builds the driver and runs it until the test ends.
func start(t *testing.T, cfg map[string]any, secrets map[string]string) (*Driver, *memSink) {
	t.Helper()
	d, err := newDriver(t, cfg, secrets)
	if err != nil {
		t.Fatal(err)
	}
	sink := &memSink{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = d.Run(ctx, sink) }()
	t.Cleanup(func() { cancel(); <-done })
	return d, sink
}

// dial connects once the listener is up.
func dial(t *testing.T, port int, tlsCfg *tls.Config) net.Conn {
	t.Helper()
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	var c net.Conn
	up := transporttest.WaitFor(func() bool {
		var err error
		if tlsCfg != nil {
			c, err = tls.Dial("tcp", addr, tlsCfg)
		} else {
			c, err = net.Dial("tcp", addr)
		}
		return err == nil
	}, waitTimeout)
	if !up {
		t.Fatalf("nothing listening on %s", addr)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func TestListenBinaryDelimited(t *testing.T) {
	port := freePort(t)
	d, sink := start(t, map[string]any{"mode": "listen", "port": port, "address": "127.0.0.1"}, nil)
	c := dial(t, port, nil)
	for _, id := range []string{"a", "b"} {
		data, _ := proto.Marshal(&sentinelv1.Observation{SourceEntityId: id, Position: &sentinelv1.Coordinate{Latitude: 1, Longitude: 2}})
		_, _ = c.Write(append(binary.AppendUvarint(nil, uint64(len(data))), data...))
	}
	if !transporttest.WaitFor(func() bool { return sink.count() == 2 }, waitTimeout) {
		t.Fatalf("observations = %d", sink.count())
	}
	h := d.Health().Detail
	l, _ := h["listen"].(map[string]any)
	if l["protocol"] != "tcp" || l["host"] != "sentinel.example" || l["port"] != port || h["allowlist"] != "open" || h["tls"] != false || h["format"] != "sentinel" {
		t.Errorf("listen detail = %+v", h)
	}
	if _, ok := h["connect"]; ok {
		t.Errorf("a listener reports no connect target: %+v", h)
	}
}

func TestListenJSONLines(t *testing.T) {
	port := freePort(t)
	_, sink := start(t, map[string]any{"mode": "listen", "port": port, "address": "127.0.0.1", "encoding": "json"}, nil)
	c := dial(t, port, nil)
	_, _ = c.Write([]byte(`{"sourceEntityId":"j1","position":{"latitude":1,"longitude":2}}` + "\n"))
	if !transporttest.WaitFor(func() bool { return sink.count() == 1 }, waitTimeout) {
		t.Fatal("no observation from a JSON line")
	}
}

func TestListenTLSFromSecrets(t *testing.T) {
	ca := transporttest.NewCA(t)
	pair := ca.Issue(t, "localhost")
	cfg := func() map[string]any {
		return map[string]any{"mode": "listen", "port": freePort(t), "address": "127.0.0.1", "encoding": "json", "tls": map[string]any{"enabled": true}}
	}
	if _, err := newDriver(t, cfg(), nil); err == nil {
		t.Fatal("TLS without a certificate secret was accepted")
	}

	c := cfg()
	d, sink := start(t, c, map[string]string{"tlsCert": string(pair.CertPEM), "tlsKey": string(pair.KeyPEM)})
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(ca.CertPEM)
	conn := dial(t, c["port"].(int), &tls.Config{RootCAs: roots, ServerName: "localhost"})
	_, _ = conn.Write([]byte(`{"sourceEntityId":"s1","position":{"latitude":1,"longitude":2}}` + "\n"))
	if !transporttest.WaitFor(func() bool { return sink.count() == 1 }, waitTimeout) {
		t.Fatal("no observation over TLS")
	}
	if d.Health().Detail["tls"] != true {
		t.Errorf("tls detail = %v", d.Health().Detail["tls"])
	}
}

// TLS material comes from the secrets only. A config that carries file
// paths (it never passes the schema, but New can be called without it) must
// not make the host read them.
func TestTLSFilePathsAreNotHonoured(t *testing.T) {
	pair := transporttest.NewCA(t).Issue(t, "localhost")
	dir := t.TempDir()
	certFile, keyFile := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certFile, pair.CertPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, pair.KeyPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	listen := func(files map[string]any) map[string]any {
		files["enabled"] = true
		return map[string]any{"mode": "listen", "port": 6000, "tls": files}
	}
	// A path that does not exist: the file would be read first and fail with
	// a file error, so the error here proves it was never read.
	_, err := newDriver(t, listen(map[string]any{"certFile": "/nonexistent/cert.pem", "keyFile": "/nonexistent/key.pem", "clientCaFile": "/nonexistent/ca.pem"}), nil)
	if err == nil || !strings.Contains(err.Error(), "needs a certificate") || strings.Contains(err.Error(), "reading") {
		t.Errorf("nonexistent paths: err = %v, want the needs-a-certificate error", err)
	}
	// Paths that hold a valid pair still give a listener no certificate.
	if _, err := newDriver(t, listen(map[string]any{"certFile": certFile, "keyFile": keyFile}), nil); err == nil || !strings.Contains(err.Error(), "needs a certificate") {
		t.Errorf("valid paths: err = %v, want the needs-a-certificate error", err)
	}
	// A client does not read a CA file either.
	if _, err := newDriver(t, map[string]any{"mode": "connect", "host": "h", "port": 7000, "tls": map[string]any{"enabled": true, "caFile": "/nonexistent/ca.pem"}}, nil); err != nil {
		t.Errorf("connect with a caFile: %v", err)
	}
}

func TestConnectMode(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	// The server holds its connection open until the test is over.
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		_, _ = c.Write([]byte(`{"sourceEntityId":"served","position":{"latitude":3,"longitude":4}}` + "\n"))
		<-release
	}()
	port := ln.Addr().(*net.TCPAddr).Port
	d, sink := start(t, map[string]any{"mode": "connect", "host": "127.0.0.1", "port": port, "encoding": "json"}, nil)
	if !transporttest.WaitFor(func() bool { return sink.count() == 1 }, waitTimeout) {
		t.Fatal("no observation in connect mode")
	}
	h := d.Health().Detail
	cd, _ := h["connect"].(map[string]any)
	if cd["protocol"] != "tcp" || cd["host"] != "127.0.0.1" || cd["port"] != port || h["tls"] != false || h["format"] != "sentinel" {
		t.Errorf("connect detail = %+v", h)
	}
	if _, ok := h["listen"]; ok {
		t.Errorf("a client reports no listen target: %+v", h)
	}
	if _, ok := h["allowlist"]; ok {
		t.Errorf("a client has no allowlist: %+v", h)
	}
}

func TestHealthAllowlist(t *testing.T) {
	d, err := newDriver(t, map[string]any{"mode": "listen", "port": 6000, "allow": []string{"10.0.0.0/8", "192.0.2.7"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := d.Health().Detail["allowlist"]; got != 2 {
		t.Errorf("allowlist = %v, want 2", got)
	}
}

func TestNewRefusals(t *testing.T) {
	for name, cfg := range map[string]map[string]any{
		"no mode":              {"port": 6000},
		"unknown mode":         {"mode": "both", "port": 6000},
		"listen without port":  {"mode": "listen"},
		"listen below 1024":    {"mode": "listen", "port": 80},
		"listen above 65535":   {"mode": "listen", "port": 70000},
		"connect without host": {"mode": "connect", "port": 7000},
		"connect without port": {"mode": "connect", "host": "h"},
		"unknown format":       {"mode": "listen", "port": 6000, "format": "xml"},
		"bad allowlist entry":  {"mode": "listen", "port": 6000, "allow": []string{"not-an-ip"}},
		"unknown framing":      {"mode": "listen", "port": 6000, "framing": "zigzag"},
		"bad encoding":         {"mode": "listen", "port": 6000, "encoding": "yaml"},
		"message framing":      {"mode": "listen", "port": 6000, "framing": "message"},
		"negative timeout":     {"mode": "listen", "port": 6000, "readTimeoutSec": -1},
	} {
		if d, err := newDriver(t, cfg, nil); err == nil {
			t.Errorf("%s: accepted, driver %v", name, d)
		}
	}
}

func TestListenHookAndFraming(t *testing.T) {
	for cfg, want := range map[string]struct {
		port int
		ok   bool
	}{
		`{"mode":"listen","port":6000}`: {6000, true},
		// Listening is decided by the mode alone, so the port rules refuse a
		// missing or mistyped port instead of skipping the feed.
		`{"mode":"listen"}`:                            {0, true},
		`{"mode":"listen","port":0}`:                   {0, true},
		`{"mode":"listen","port":"6000"}`:              {0, true},
		`{"mode":"listen","port":null}`:                {0, true},
		`{"mode":"listen","port":6000.5}`:              {0, true},
		`{"mode":"listen","port":6000,"allow":"oops"}`: {6000, true},
		`{"mode":"listen","port":6000,"tls":"yes"}`:    {6000, true},
		`{"mode":"connect","host":"h","port":6000}`:    {0, false},
		`{"port":6000}`:                                {0, false},
		`{"mode":5,"port":6000}`:                       {0, false},
		`{nope`:                                        {0, false},
		``:                                             {0, false},
	} {
		n, p, ok := driver.ListenOf(ID, json.RawMessage(cfg))
		if ok != want.ok || p != want.port || (ok && n != "tcp") || (!ok && n != "") {
			t.Errorf("%s: got %q %d %v", cfg, n, p, ok)
		}
	}
	for _, tc := range [][4]string{
		{"sentinel", "protobuf", "", "delimited"}, {"sentinel", "json", "", "line"}, {"cot", "", "", "xmlevent"},
		{"beast", "", "", "beast"}, {"sbs", "", "", "line"}, {"sentinel", "protobuf", "lengthprefix", "lengthprefix"},
		{"", "", "", "delimited"},
	} {
		if got := FramingFor(tc[0], tc[1], tc[2]); got != tc[3] {
			t.Errorf("FramingFor(%v) = %q, want %q", tc[:3], got, tc[3])
		}
	}
}

// A client CA verifies nothing unless client certificates are required, so
// a listener with the clientCa secret and requireClientCert off is refused
// (R20) instead of silently accepting any sender.
func TestClientCAWithoutRequireClientCert(t *testing.T) {
	ca := transporttest.NewCA(t)
	pair := ca.Issue(t, "localhost")
	secrets := map[string]string{"tlsCert": string(pair.CertPEM), "tlsKey": string(pair.KeyPEM), "clientCa": string(ca.CertPEM)}
	for name, tlsCfg := range map[string]any{
		"TLS off":               map[string]any{"enabled": false, "requireClientCert": true},
		"no tls section":        nil,
		"requireClientCert off": map[string]any{"enabled": true},
	} {
		cfg := map[string]any{"mode": "listen", "port": 6000}
		if tlsCfg != nil {
			cfg["tls"] = tlsCfg
		}
		_, err := newDriver(t, cfg, secrets)
		if err == nil || !strings.Contains(err.Error(), "clientCa") || !strings.Contains(err.Error(), "requireClientCert") {
			t.Errorf("%s: err = %v, want one naming clientCa and requireClientCert", name, err)
		}
	}
	cfg := map[string]any{"mode": "listen", "port": 6000, "tls": map[string]any{"enabled": true, "requireClientCert": true}}
	if _, err := newDriver(t, cfg, secrets); err != nil {
		t.Errorf("mutual TLS with a client CA: %v", err)
	}
}

// readTimeoutSec closes a connection that sends nothing for that long: one
// that never sends (it waits in the plain listener's TLS sniff) and one
// that goes quiet after a message (R22). An idle close is not an error.
func TestReadTimeoutClosesIdleConnections(t *testing.T) {
	port := freePort(t)
	d, sink := start(t, map[string]any{"mode": "listen", "port": port, "address": "127.0.0.1", "encoding": "json", "readTimeoutSec": 1}, nil)
	silent := dial(t, port, nil)
	quiet := dial(t, port, nil)
	_, _ = quiet.Write([]byte(`{"sourceEntityId":"q1","position":{"latitude":1,"longitude":2}}` + "\n"))
	if !transporttest.WaitFor(func() bool { return sink.count() == 1 }, waitTimeout) {
		t.Fatal("no observation before the idle period")
	}
	connections := func() any { return d.Health().Detail["connections"] }
	if !transporttest.WaitFor(func() bool { return connections() == 2 }, waitTimeout) {
		t.Fatalf("connections = %v, want 2", connections())
	}
	if !transporttest.WaitFor(func() bool { return connections() == 0 }, waitTimeout) {
		t.Fatalf("idle connections still open: %v", connections())
	}
	for name, c := range map[string]net.Conn{"silent": silent, "quiet": quiet} {
		_ = c.SetReadDeadline(time.Now().Add(time.Second))
		if _, err := c.Read(make([]byte, 1)); err == nil || isTimeout(err) {
			t.Errorf("%s connection not closed by the server: %v", name, err)
		}
	}
	if e := d.Health().LastError; e != "" {
		t.Errorf("an idle close is not an error: %q", e)
	}
}

func isTimeout(err error) bool {
	ne, ok := err.(net.Error)
	return ok && ne.Timeout()
}

// tlsServer serves line to every client that completes a TLS handshake
// with cfg and holds the connection open until the test ends. It returns
// the port.
func tlsServer(t *testing.T, cfg *tls.Config, line string) int {
	t.Helper()
	ln, err := tls.Listen("tcp", "127.0.0.1:0", cfg)
	if err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	t.Cleanup(func() { close(release); ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				if c.(*tls.Conn).Handshake() != nil {
					return
				}
				_, _ = c.Write([]byte(line + "\n"))
				<-release
			}()
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port
}

// In connect mode serverCa verifies the remote, serverName names the
// certificate to expect, and tlsCert/tlsKey present a client certificate.
func TestConnectTLS(t *testing.T) {
	ca := transporttest.NewCA(t)
	server := ca.Issue(t, "localhost")
	serverCert, err := tls.X509KeyPair(server.CertPEM, server.KeyPEM)
	if err != nil {
		t.Fatal(err)
	}
	const line = `{"sourceEntityId":"remote-1","position":{"latitude":3,"longitude":4}}`
	connect := func(port int, serverName string) map[string]any {
		return map[string]any{"mode": "connect", "host": "127.0.0.1", "port": port, "encoding": "json", "tls": map[string]any{"enabled": true, "serverName": serverName}}
	}

	t.Run("serverCa and serverName", func(t *testing.T) {
		port := tlsServer(t, &tls.Config{Certificates: []tls.Certificate{serverCert}}, line)
		d, sink := start(t, connect(port, "localhost"), map[string]string{"serverCa": string(ca.CertPEM)})
		if !transporttest.WaitFor(func() bool { return sink.count() == 1 }, waitTimeout) {
			t.Fatalf("no observation over TLS: %q", d.Health().LastError)
		}
		if h := d.Health().Detail; h["tls"] != true {
			t.Errorf("tls detail = %v", h["tls"])
		}
	})

	for name, tc := range map[string]struct {
		serverName string
		secrets    map[string]string
	}{
		"wrong CA":          {"localhost", map[string]string{"serverCa": string(transporttest.NewCA(t).CertPEM)}},
		"missing CA":        {"localhost", nil},
		"wrong server name": {"elsewhere", map[string]string{"serverCa": string(ca.CertPEM)}},
	} {
		t.Run(name, func(t *testing.T) {
			port := tlsServer(t, &tls.Config{Certificates: []tls.Certificate{serverCert}}, line)
			d, sink := start(t, connect(port, tc.serverName), tc.secrets)
			if !transporttest.WaitFor(func() bool { return d.Health().LastError != "" }, waitTimeout) {
				t.Fatal("an unverifiable server reported no error")
			}
			if e := d.Health().LastError; !strings.Contains(e, "certificate") {
				t.Errorf("last error = %q, want a certificate error", e)
			}
			if sink.count() != 0 {
				t.Errorf("observations from an unverified server: %d", sink.count())
			}
		})
	}

	t.Run("client certificate", func(t *testing.T) {
		roots := x509.NewCertPool()
		roots.AppendCertsFromPEM(ca.CertPEM)
		mutual := &tls.Config{Certificates: []tls.Certificate{serverCert}, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: roots}
		client := ca.Issue(t, "sentinel")

		port := tlsServer(t, mutual, line)
		d, sink := start(t, connect(port, "localhost"), map[string]string{"serverCa": string(ca.CertPEM)})
		if !transporttest.WaitFor(func() bool { return d.Health().LastError != "" }, waitTimeout) {
			t.Fatal("no error without a client certificate")
		}
		if sink.count() != 0 {
			t.Errorf("observations without a client certificate: %d", sink.count())
		}

		port = tlsServer(t, mutual, line)
		d, sink = start(t, connect(port, "localhost"), map[string]string{"serverCa": string(ca.CertPEM), "tlsCert": string(client.CertPEM), "tlsKey": string(client.KeyPEM)})
		if !transporttest.WaitFor(func() bool { return sink.count() == 1 }, waitTimeout) {
			t.Fatalf("no observation with a client certificate: %q", d.Health().LastError)
		}
	})
}
