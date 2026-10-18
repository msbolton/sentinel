package tcpserver

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sentinel/ingest-service/internal/source/transport"
	"github.com/sentinel/ingest-service/internal/source/transport/transporttest"
)

func TestAcceptsConnectionsAndFrames(t *testing.T) {
	addr := transporttest.FreePort(t)
	tr, err := New(transport.Config{Kind: "tcpserver", Address: addr, Framing: "line"})
	if err != nil {
		t.Fatal(err)
	}
	col := &transporttest.Collector{}
	stop := transporttest.Run(t, tr, col.Emit)
	defer stop()

	var c1, c2 net.Conn
	if !transporttest.WaitFor(func() bool {
		c, err := net.Dial("tcp", addr)
		if err == nil {
			c1 = c
		}
		return err == nil
	}, 2*time.Second) {
		t.Fatal("listener not up")
	}
	defer c1.Close()
	c2, err = net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()
	_, _ = c1.Write([]byte("one\r\ntwo\n"))
	_, _ = c2.Write([]byte("three\n"))
	if !transporttest.WaitFor(func() bool { return col.Count() == 3 }, 2*time.Second) {
		t.Fatalf("frames = %v", col.Payloads())
	}
	if st := tr.State(); !st.Connected || st.Connections != 2 {
		t.Errorf("state = %+v", st)
	}
	for _, f := range col.Frames() {
		if f.Remote == "" || f.ReceivedAt.IsZero() {
			t.Errorf("frame lacks provenance: %+v", f)
		}
	}
	c1.Close()
	if !transporttest.WaitFor(func() bool { return tr.State().Connections == 1 }, 2*time.Second) {
		t.Errorf("connection close not tracked: %+v", tr.State())
	}
}

func TestBindFailureIsFatal(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	tr, err := New(transport.Config{Kind: "tcpserver", Address: l.Addr().String()})
	if err != nil {
		t.Fatal(err)
	}
	if err := tr.Run(t.Context(), (&transporttest.Collector{}).Emit); err == nil {
		t.Error("expected a bind error")
	}
	if _, err := New(transport.Config{Kind: "tcpserver", Address: "nope"}); err == nil {
		t.Error("expected a validation error")
	}
}

func TestRefusesNonNumericPorts(t *testing.T) {
	// A service name resolves inside net.Listen, so it would bind a port the
	// listener policy never sees; out-of-range and signed ports are refused too.
	for _, addr := range []string{":smtp", "127.0.0.1:postgresql", ":70000", ":-1"} {
		if _, err := New(transport.Config{Kind: "tcpserver", Address: addr}); err == nil {
			t.Errorf("%s: expected a validation error", addr)
		}
	}
	if _, err := New(transport.Config{Kind: "tcpserver", Address: "127.0.0.1:0"}); err != nil {
		t.Errorf("port 0 should stay allowed: %v", err)
	}
}

func detailInt(st transport.State, key string) int64 {
	v, _ := st.Detail[key].(int64)
	return v
}

// dial connects once the listener is up and fails the test otherwise.
func dial(t *testing.T, addr string) net.Conn {
	t.Helper()
	var c net.Conn
	if !transporttest.WaitFor(func() bool {
		conn, err := net.Dial("tcp", addr)
		if err == nil {
			c = conn
		}
		return err == nil
	}, 2*time.Second) {
		t.Fatal("listener not up")
	}
	return c
}

func TestAllowlistAndConnectionCap(t *testing.T) {
	addr := transporttest.FreePort(t)
	tr, err := New(transport.Config{Kind: "tcpserver", Address: addr, Framing: "line", Allow: []string{"192.0.2.0/24"}})
	if err != nil {
		t.Fatal(err)
	}
	col := &transporttest.Collector{}
	stop := transporttest.Run(t, tr, col.Emit)
	defer stop()
	dial(t, addr).Close() // the readiness probe is itself rejected: one count
	c := dial(t, addr)
	_, _ = c.Write([]byte("hello\n"))
	// The probe plus this connection: the second count proves the sender
	// was processed, so the frame count below cannot be early.
	if !transporttest.WaitFor(func() bool { return detailInt(tr.State(), "rejected") >= 2 }, 2*time.Second) {
		t.Errorf("a sender outside the allowlist was not rejected: %+v", tr.State())
	}
	c.Close()
	if col.Count() != 0 {
		t.Errorf("a sender outside the allowlist was let in: frames %d, state %+v", col.Count(), tr.State())
	}

	addr2 := transporttest.FreePort(t)
	tr2, err := New(transport.Config{Kind: "tcpserver", Address: addr2, Framing: "line", MaxConnections: 1})
	if err != nil {
		t.Fatal(err)
	}
	stop2 := transporttest.Run(t, tr2, (&transporttest.Collector{}).Emit)
	defer stop2()
	first := dial(t, addr2)
	defer first.Close()
	if !transporttest.WaitFor(func() bool { return tr2.State().Connections == 1 }, 2*time.Second) {
		t.Fatalf("first connection not tracked: %+v", tr2.State())
	}
	second, err := net.Dial("tcp", addr2)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if !transporttest.WaitFor(func() bool { return detailInt(tr2.State(), "refused") == 1 }, 2*time.Second) {
		t.Errorf("the connection over the cap was not refused: %+v", tr2.State())
	}
	if tr2.State().Connections != 1 {
		t.Errorf("connections = %d, want the cap of 1", tr2.State().Connections)
	}
}

func TestRateLimitAndSenders(t *testing.T) {
	addr := transporttest.FreePort(t)
	tr, err := New(transport.Config{Kind: "tcpserver", Address: addr, Framing: "line", MaxMessagesPerSec: 5})
	if err != nil {
		t.Fatal(err)
	}
	col := &transporttest.Collector{}
	stop := transporttest.Run(t, tr, col.Emit)
	defer stop()
	c := dial(t, addr)
	defer c.Close()
	for i := 0; i < 50; i++ {
		_, _ = c.Write([]byte("m\n"))
	}
	transporttest.WaitFor(func() bool { st := tr.State(); return detailInt(st, "accepted")+detailInt(st, "rateLimited") == 50 }, 3*time.Second)
	st := tr.State()
	if a := detailInt(st, "accepted"); a < 5 || a > 6 {
		t.Errorf("accepted %d of 50 at 5/s", a)
	}
	if r := detailInt(st, "rateLimited"); r < 44 {
		t.Errorf("rateLimited = %d, want the rest of the 50", r)
	}
	senders, _ := st.Detail["senders"].([]transport.SenderStat)
	if len(senders) != 1 || !senders[0].Connected || st.Detail["distinctSenders"] != 1 {
		t.Errorf("senders = %+v", st.Detail["senders"])
	}
	if len(senders) == 1 && senders[0].Messages != detailInt(st, "accepted") {
		t.Errorf("sender counted %d messages, listener accepted %d", senders[0].Messages, detailInt(st, "accepted"))
	}
}

// handled is how many frames the listener has taken in or dropped for being
// over a sender's own limit.
func handled(tr transport.Transport) int64 {
	st := tr.State()
	return detailInt(st, "accepted") + detailInt(st, "senderRateLimited")
}

// A sender over its own limit is counted as senderRateLimited, and against its
// row in the senders list. rateLimited stays what the limit all senders share
// dropped: the sender's own bucket is asked first, so a flooder cannot spend
// the shared budget (here 10 a second against a sender's 3), and only the
// messages that pass its bucket reach the shared one.
func TestPerSenderRateLimit(t *testing.T) {
	addr := transporttest.FreePort(t)
	tr, err := New(transport.Config{Kind: "tcpserver", Address: addr, Framing: "line", MaxMessagesPerSec: 10, MaxMessagesPerSecPerSender: 3})
	if err != nil {
		t.Fatal(err)
	}
	col := &transporttest.Collector{}
	stop := transporttest.Run(t, tr, col.Emit)
	defer stop()
	c := dial(t, addr)
	defer c.Close()
	for i := 0; i < 50; i++ {
		_, _ = c.Write([]byte("m\n"))
	}
	if !transporttest.WaitFor(func() bool { return handled(tr) == 50 }, 3*time.Second) {
		t.Fatalf("accepted + senderRateLimited never reached 50: %+v", tr.State().Detail)
	}
	st := tr.State()
	if a := detailInt(st, "accepted"); a < 3 || a > 4 {
		t.Errorf("accepted %d of 50 at 3/s for the sender, want its burst of 3 (4 if a token refilled)", a)
	}
	if n := detailInt(st, "senderRateLimited"); n < 46 {
		t.Errorf("senderRateLimited = %d, want the rest of the 50", n)
	}
	if n := detailInt(st, "rateLimited"); n != 0 {
		t.Errorf("rateLimited = %d, want 0: the shared limit only sees what the sender's own passes", n)
	}
	if int64(col.Count()) != detailInt(st, "accepted") {
		t.Errorf("emitted %d frames, accepted %d", col.Count(), detailInt(st, "accepted"))
	}
	senders, _ := st.Detail["senders"].([]transport.SenderStat)
	if len(senders) != 1 {
		t.Fatalf("senders = %+v", st.Detail["senders"])
	}
	if senders[0].RateLimited != detailInt(st, "senderRateLimited") || senders[0].Messages != detailInt(st, "accepted") {
		t.Errorf("the sender's row = %+v, want %d messages and %d rate limited, as the listener counted", senders[0], detailInt(st, "accepted"), detailInt(st, "senderRateLimited"))
	}
}

// A sender is told apart by its IP for the limit, however many connections it
// opens (or it could multiply its budget), while the senders list keeps one
// row per connection, named ip:port.
func TestPerSenderLimitSpansConnections(t *testing.T) {
	addr := transporttest.FreePort(t)
	tr, err := New(transport.Config{Kind: "tcpserver", Address: addr, Framing: "line", MaxMessagesPerSecPerSender: 4})
	if err != nil {
		t.Fatal(err)
	}
	stop := transporttest.Run(t, tr, (&transporttest.Collector{}).Emit)
	defer stop()
	first, second := dial(t, addr), dial(t, addr)
	defer first.Close()
	defer second.Close()

	sendAndSettle := func(c net.Conn, n int, total int64) {
		t.Helper()
		for i := 0; i < n; i++ {
			_, _ = c.Write([]byte("m\n"))
		}
		if !transporttest.WaitFor(func() bool { return handled(tr) == total }, 3*time.Second) {
			t.Fatalf("accepted + senderRateLimited never reached %d: %+v", total, tr.State().Detail)
		}
	}
	sendAndSettle(first, 3, 3)
	sendAndSettle(second, 3, 6)

	st := tr.State()
	if a := detailInt(st, "accepted"); a < 4 || a > 5 {
		t.Errorf("accepted %d of 6 over two connections at 4/s for the sender's IP, want its one burst of 4 (5 if a token refilled)", a)
	}
	senders, _ := st.Detail["senders"].([]transport.SenderStat)
	byRemote := map[string]transport.SenderStat{}
	for _, s := range senders {
		byRemote[s.Remote] = s
	}
	if len(byRemote) != 2 || st.Detail["distinctSenders"] != 2 {
		t.Fatalf("senders = %+v, want one row for each connection", senders)
	}
	var limited int64
	for _, c := range []net.Conn{first, second} {
		s, ok := byRemote[c.LocalAddr().String()]
		if !ok {
			t.Errorf("no row named %s (the connection's ip:port) in %+v", c.LocalAddr(), senders)
		}
		limited += s.RateLimited
	}
	if limited != detailInt(st, "senderRateLimited") {
		t.Errorf("the rows count %d rate limited, the listener %d", limited, detailInt(st, "senderRateLimited"))
	}
}

// A sender's bucket runs on the clock the listener reads itself, not on the
// stamps of its frames. The stamps are wall-clock readings, and a step of the
// wall clock back (an NTP correction, a virtual machine resumed) would put
// every later stamp behind the bucket's last reading and hold a spent bucket
// shut until the clock caught up. Here the stamps step back an hour once the
// sender has spent its bucket, and 50 ms later (5 tokens at 100 a second) its
// frames pass again.
func TestPerSenderLimitSurvivesAStepBackOfTheWallClock(t *testing.T) {
	tr, err := New(transport.Config{Kind: "tcpserver", Address: "127.0.0.1:0", Framing: "line", MaxMessagesPerSecPerSender: 100})
	if err != nil {
		t.Fatal(err)
	}
	col := &transporttest.Collector{}
	emit := tr.(*Server).guard("10.0.0.1", col.Emit)
	send := func(stamp time.Time) {
		t.Helper()
		if err := emit(context.Background(), transport.Frame{Payload: []byte("m"), ReceivedAt: stamp, Remote: "10.0.0.1:5000"}); err != nil {
			t.Fatal(err)
		}
	}

	stamp := time.Now().UTC()
	for i := 0; i < 1000; i++ {
		send(stamp)
	}
	spent := col.Count()
	if spent < 100 || spent > 150 {
		t.Fatalf("%d of 1000 frames passed, want the burst of 100 (a few more if the loop was slow): the bucket is not spent", spent)
	}

	time.Sleep(50 * time.Millisecond)
	for i := 0; i < 20; i++ {
		send(stamp.Add(-time.Hour))
	}
	if col.Count() == spent {
		t.Error("no frame passed 50 ms after the bucket was spent, with the stamps an hour behind: the bucket follows the frames' wall-clock stamps and stays shut until they catch up")
	}
}

// State renders at most 50 senders however many are connected; the
// distinct count stays the full number.
func TestSenderReportIsTruncated(t *testing.T) {
	addr := transporttest.FreePort(t)
	tr, err := New(transport.Config{Kind: "tcpserver", Address: addr, Framing: "line"})
	if err != nil {
		t.Fatal(err)
	}
	stop := transporttest.Run(t, tr, (&transporttest.Collector{}).Emit)
	defer stop()
	const n = 55
	for i := 0; i < n; i++ {
		c := dial(t, addr)
		defer c.Close()
	}
	if !transporttest.WaitFor(func() bool { return tr.State().Detail["distinctSenders"] == n }, 3*time.Second) {
		t.Fatalf("distinctSenders = %v, want %d", tr.State().Detail["distinctSenders"], n)
	}
	senders, _ := tr.State().Detail["senders"].([]transport.SenderStat)
	if len(senders) != 50 {
		t.Errorf("rendered %d senders, want the 50 cap", len(senders))
	}
}

// Both ways a frame can be too long count as oversize: a framer that
// refuses a declared length and the scanner running out of buffer.
func TestOversizeFrameIsCounted(t *testing.T) {
	cases := []struct {
		name, framing string
		maxFrame      int
		send          []byte
	}{
		{"declared length", "lengthprefix", 16, append([]byte{0, 100}, make([]byte, 100)...)},
		{"unterminated line", "line", 1 << 16, []byte(strings.Repeat("x", 2<<16))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			addr := transporttest.FreePort(t)
			tr, err := New(transport.Config{Kind: "tcpserver", Address: addr, Framing: tc.framing, MaxFrameBytes: tc.maxFrame})
			if err != nil {
				t.Fatal(err)
			}
			col := &transporttest.Collector{}
			stop := transporttest.Run(t, tr, col.Emit)
			defer stop()
			c := dial(t, addr)
			defer c.Close()
			_, _ = c.Write(tc.send)
			if !transporttest.WaitFor(func() bool { return detailInt(tr.State(), "oversize") == 1 }, 2*time.Second) {
				t.Errorf("oversize frame not counted: %+v", tr.State())
			}
			if col.Count() != 0 {
				t.Errorf("an oversize frame was emitted: %d frames", col.Count())
			}
		})
	}
}

// A malformed delimited prefix is a protocol error, not an oversize frame:
// the connection closes and LastError says why.
func TestMalformedPrefixClosesWithoutOversize(t *testing.T) {
	addr := transporttest.FreePort(t)
	tr, err := New(transport.Config{Kind: "tcpserver", Address: addr, Framing: "delimited"})
	if err != nil {
		t.Fatal(err)
	}
	stop := transporttest.Run(t, tr, (&transporttest.Collector{}).Emit)
	defer stop()
	c := dial(t, addr)
	defer c.Close()
	_, _ = c.Write([]byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff})
	if !transporttest.WaitFor(func() bool { return tr.State().LastError != "" && tr.State().Connections == 0 }, 2*time.Second) {
		t.Errorf("a malformed prefix did not close the connection: %+v", tr.State())
	}
	if n := detailInt(tr.State(), "oversize"); n != 0 {
		t.Errorf("oversize = %d, want 0 for a malformed prefix", n)
	}
}

func TestMutualTLS(t *testing.T) {
	ca := transporttest.NewCA(t)
	server := ca.Issue(t, "localhost")
	addr := transporttest.FreePort(t)
	tr, err := New(transport.Config{Kind: "tcpserver", Address: addr, Framing: "line",
		TLS: &transport.TLS{Enabled: true, RequireClientCert: true}, CertPEM: server.CertPEM, KeyPEM: server.KeyPEM, ClientCAPEM: ca.CertPEM})
	if err != nil {
		t.Fatal(err)
	}
	// The listener asks for the certificates: the shared TLS builder leaves ClientAuth alone.
	if got := tr.(*Server).tls.ClientAuth; got != tls.RequireAndVerifyClientCert {
		t.Errorf("ClientAuth = %v, want RequireAndVerifyClientCert", got)
	}
	col := &transporttest.Collector{}
	stop := transporttest.Run(t, tr, col.Emit)
	defer stop()
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(ca.CertPEM)
	client := ca.Issue(t, "sender")
	pair, err := tls.X509KeyPair(client.CertPEM, client.KeyPEM)
	if err != nil {
		t.Fatal(err)
	}

	var good *tls.Conn
	if !transporttest.WaitFor(func() bool {
		c, err := tls.Dial("tcp", addr, &tls.Config{RootCAs: roots, ServerName: "localhost", Certificates: []tls.Certificate{pair}})
		if err == nil {
			good = c
		}
		return err == nil
	}, 2*time.Second) {
		t.Fatal("the signed sender could not connect")
	}
	_, _ = good.Write([]byte("signed\n"))
	if !transporttest.WaitFor(func() bool { return col.Count() == 1 }, 2*time.Second) {
		t.Fatalf("the signed sender's frame did not arrive: %v", col.Payloads())
	}
	good.Close()

	// No client certificate: the handshake fails and is counted.
	bad, err := tls.Dial("tcp", addr, &tls.Config{RootCAs: roots, ServerName: "localhost"})
	if err == nil {
		_ = bad.SetDeadline(time.Now().Add(2 * time.Second))
		_, _ = bad.Write([]byte("anonymous\n"))
		_, _ = bufio.NewReader(bad).ReadByte() // surfaces the server's alert
		bad.Close()
	}
	if !transporttest.WaitFor(func() bool { return detailInt(tr.State(), "tlsFailed") >= 1 }, 2*time.Second) {
		t.Errorf("the anonymous handshake was not counted: %+v", tr.State())
	}
	if col.Count() != 1 {
		t.Errorf("frames = %d, want only the signed sender's", col.Count())
	}
}

// requireClientCert is a listener setting, and a listener that demands client
// certificates can check them only against a client CA: asked for with none, it
// is refused at build time instead of failing every sender's handshake.
func TestMutualTLSNeedsAClientCA(t *testing.T) {
	server := transporttest.NewCA(t).Issue(t, "localhost")
	_, err := New(transport.Config{Kind: "tcpserver", Address: "127.0.0.1:0", Framing: "line",
		TLS: &transport.TLS{Enabled: true, RequireClientCert: true}, CertPEM: server.CertPEM, KeyPEM: server.KeyPEM})
	const want = "tcpserver: requiring client certificates needs a client CA"
	if err == nil || err.Error() != want {
		t.Errorf("err = %v, want %q", err, want)
	}
}

// A client CA verifies only the certificates a listener asks for, so one set
// without requireClientCert is refused, whether it came as PEM (the clientCa
// secret) or as the clientCaFile path: the listener would let every sender in
// unchecked. Mutual TLS, and TLS with no client CA, still build.
func TestClientCANeedsRequireClientCert(t *testing.T) {
	ca := transporttest.NewCA(t)
	server := ca.Issue(t, "localhost")
	caFile := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(caFile, ca.CertPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name        string
		tls         *transport.TLS
		clientCAPEM []byte
		refused     bool
	}{
		{"secret, no tls section", nil, ca.CertPEM, true},
		{"secret, TLS off", &transport.TLS{RequireClientCert: true}, ca.CertPEM, true},
		{"secret, requireClientCert off", &transport.TLS{Enabled: true}, ca.CertPEM, true},
		{"file, TLS off", &transport.TLS{RequireClientCert: true, ClientCAFile: caFile}, nil, true},
		{"file, requireClientCert off", &transport.TLS{Enabled: true, ClientCAFile: caFile}, nil, true},
		{"secret, mutual TLS", &transport.TLS{Enabled: true, RequireClientCert: true}, ca.CertPEM, false},
		{"file, mutual TLS", &transport.TLS{Enabled: true, RequireClientCert: true, ClientCAFile: caFile}, nil, false},
		{"no client CA, TLS without client certificates", &transport.TLS{Enabled: true}, nil, false},
	}
	for _, tc := range cases {
		_, err := New(transport.Config{Kind: "tcpserver", Address: "127.0.0.1:0", Framing: "line", TLS: tc.tls,
			CertPEM: server.CertPEM, KeyPEM: server.KeyPEM, ClientCAPEM: tc.clientCAPEM})
		if !tc.refused {
			if err != nil {
				t.Errorf("%s: %v", tc.name, err)
			}
			continue
		}
		if err == nil {
			t.Errorf("%s: a client CA without requireClientCert was accepted", tc.name)
			continue
		}
		// The message names both ways to set a client CA, and the setting to turn on.
		for _, want := range []string{"clientCa secret", "tls.clientCaFile", "requireClientCert"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%s: err = %q, want it to name %q", tc.name, err, want)
			}
		}
	}
}

func TestPlainClientAgainstTLSListener(t *testing.T) {
	ca := transporttest.NewCA(t)
	server := ca.Issue(t, "localhost")
	addr := transporttest.FreePort(t)
	tr, err := New(transport.Config{Kind: "tcpserver", Address: addr, Framing: "line", TLS: &transport.TLS{Enabled: true}, CertPEM: server.CertPEM, KeyPEM: server.KeyPEM})
	if err != nil {
		t.Fatal(err)
	}
	col := &transporttest.Collector{}
	stop := transporttest.Run(t, tr, col.Emit)
	defer stop()
	c := dial(t, addr)
	_, _ = c.Write([]byte("plain text\n"))
	c.Close()
	if !transporttest.WaitFor(func() bool { return detailInt(tr.State(), "tlsFailed") >= 1 }, 2*time.Second) {
		t.Errorf("the plain handshake was not counted: %+v", tr.State())
	}
	if !tr.State().Connected {
		t.Error("the listener must keep running after a bad handshake")
	}
	if col.Count() != 0 {
		t.Errorf("plain text got through a TLS listener: %v", col.Payloads())
	}
}

func TestTLSClientAgainstPlainListener(t *testing.T) {
	addr := transporttest.FreePort(t)
	tr, err := New(transport.Config{Kind: "tcpserver", Address: addr, Framing: "line"})
	if err != nil {
		t.Fatal(err)
	}
	col := &transporttest.Collector{}
	stop := transporttest.Run(t, tr, col.Emit)
	defer stop()
	dial(t, addr).Close() // wait for the listener

	// The ClientHello would otherwise reach the line framer, and any 0x0a in
	// it would emit a garbage frame; the server must close the connection.
	d := &net.Dialer{Deadline: time.Now().Add(3 * time.Second)}
	if c, err := tls.DialWithDialer(d, "tcp", addr, &tls.Config{InsecureSkipVerify: true}); err == nil { //nolint:gosec // the handshake is meant to fail
		c.Close()
		t.Error("a TLS handshake against a plain listener succeeded")
	}
	if !transporttest.WaitFor(func() bool { return detailInt(tr.State(), "tlsFailed") >= 1 }, 2*time.Second) {
		t.Errorf("the TLS client was not counted: %+v", tr.State())
	}
	if !transporttest.WaitFor(func() bool { return tr.State().Connections == 0 }, 2*time.Second) {
		t.Errorf("the TLS client's connection was left open: %+v", tr.State())
	}
	if st := tr.State(); col.Count() != 0 || detailInt(st, "accepted") != 0 || st.LastError != "" {
		t.Errorf("the ClientHello was treated as data: frames %d, state %+v", col.Count(), st)
	}

	// The listener keeps accepting.
	c := dial(t, addr)
	defer c.Close()
	_, _ = c.Write([]byte("after\n"))
	if !transporttest.WaitFor(func() bool { return col.Count() == 1 }, 2*time.Second) {
		t.Fatalf("the listener stopped delivering after a TLS client: %v", col.Payloads())
	}
	if got := col.Payloads()[0]; got != "after" {
		t.Errorf("payload = %q", got)
	}
}

// The TLS check must neither lose nor reorder a plain sender's bytes, nor
// hold back a short message to wait for a third byte it will never get.
func TestPlainSenderIsNotMistakenForTLS(t *testing.T) {
	cases := []struct {
		name   string
		writes []string
		want   string
	}{
		{"short message", []string{"a\n"}, "a"},
		{"split across reads", []string{"a", "b", "c\n"}, "abc"},
		{"starts like a record header", []string{"\x16\x03\x09near miss\n"}, "\x16\x03\x09near miss"},
		{"record header split across reads", []string{"\x16", "\x03", "\x05rest\n"}, "\x16\x03\x05rest"},
		{"one byte of a header then text", []string{"\x16", "x\n"}, "\x16x"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			addr := transporttest.FreePort(t)
			tr, err := New(transport.Config{Kind: "tcpserver", Address: addr, Framing: "line"})
			if err != nil {
				t.Fatal(err)
			}
			col := &transporttest.Collector{}
			stop := transporttest.Run(t, tr, col.Emit)
			defer stop()
			c := dial(t, addr)
			defer c.Close()
			for _, w := range tc.writes {
				_, _ = c.Write([]byte(w))
				time.Sleep(10 * time.Millisecond) // lets the server see separate reads; the assertion does not depend on it
			}
			if !transporttest.WaitFor(func() bool { return col.Count() == 1 }, 2*time.Second) {
				t.Fatalf("frames = %q, want %q", col.Payloads(), tc.want)
			}
			if got := col.Payloads()[0]; got != tc.want {
				t.Errorf("payload = %q, want %q", got, tc.want)
			}
			if n := detailInt(tr.State(), "tlsFailed"); n != 0 {
				t.Errorf("tlsFailed = %d for plain text", n)
			}
		})
	}
}

func TestBindConflictIsExplained(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	tr, err := New(transport.Config{Kind: "tcpserver", Address: l.Addr().String()})
	if err != nil {
		t.Fatal(err)
	}
	err = tr.Run(t.Context(), (&transporttest.Collector{}).Emit)
	if err == nil || !strings.Contains(err.Error(), "already in use on the listener host") {
		t.Errorf("err = %v", err)
	}
}
