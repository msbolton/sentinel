package tcpserver

import (
	"bufio"
	"crypto/tls"
	"crypto/x509"
	"net"
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
