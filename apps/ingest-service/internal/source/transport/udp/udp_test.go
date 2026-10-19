package udp

import (
	"fmt"
	"net"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sentinel/ingest-service/internal/source/transport"
	"github.com/sentinel/ingest-service/internal/source/transport/transporttest"
)

func TestDatagramsSplitIntoLines(t *testing.T) {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := pc.LocalAddr().String()
	pc.Close()
	tr, err := New(transport.Config{Kind: "udp", Address: addr})
	if err != nil {
		t.Fatal(err)
	}
	col := &transporttest.Collector{}
	stop := transporttest.Run(t, tr, col.Emit)
	defer stop()
	if !transporttest.WaitFor(func() bool { return tr.State().Connected }, 2*time.Second) {
		t.Fatal("listener not up")
	}
	conn, err := net.Dial("udp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_, _ = conn.Write([]byte("!AIVDM,1\r\n!AIVDM,2\n"))
	_, _ = conn.Write([]byte("!AIVDM,3"))
	if !transporttest.WaitFor(func() bool { return col.Count() == 3 }, 2*time.Second) {
		t.Fatalf("frames = %v", col.Payloads())
	}
	if p := col.Payloads(); p[0] != "!AIVDM,1" || p[2] != "!AIVDM,3" {
		t.Errorf("payloads = %v", p)
	}
}

// freeUDPAddr returns a loopback host:port that is free for UDP (a port free
// for TCP may still be taken by a UDP socket).
func freeUDPAddr(t *testing.T) string {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	return pc.LocalAddr().String()
}

func TestUDPAllowlistOversizeAndRate(t *testing.T) {
	addr := freeUDPAddr(t)
	tr, err := New(transport.Config{Kind: "udp", Address: addr, MaxFrameBytes: 8, MaxMessagesPerSec: 3, Allow: []string{"127.0.0.1"}})
	if err != nil {
		t.Fatal(err)
	}
	col := &transporttest.Collector{}
	stop := transporttest.Run(t, tr, col.Emit)
	defer stop()
	c, err := net.Dial("udp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if !transporttest.WaitFor(func() bool { _, _ = c.Write([]byte("ok")); return col.Count() > 0 }, 2*time.Second) {
		t.Fatal("no datagram from an allowed sender was accepted")
	}
	_, _ = c.Write([]byte("this datagram is too long"))
	for i := 0; i < 20; i++ {
		_, _ = c.Write([]byte("x"))
	}
	counter := func(key string) int64 { v, _ := tr.State().Detail[key].(int64); return v }
	if !transporttest.WaitFor(func() bool { return counter("oversize") == 1 }, 2*time.Second) {
		t.Errorf("oversize = %d, want 1: %+v", counter("oversize"), tr.State().Detail)
	}
	if !transporttest.WaitFor(func() bool { return counter("rateLimited") > 0 }, 2*time.Second) {
		t.Errorf("no datagram was rate limited: %+v", tr.State().Detail)
	}
	st := tr.State()
	if st.Detail["distinctSenders"] != 1 {
		t.Errorf("senders = %+v", st.Detail["senders"])
	}
	if !transporttest.WaitFor(func() bool { return counter("accepted") == int64(col.Count()) }, 2*time.Second) {
		t.Errorf("accepted = %d, emitted = %d", counter("accepted"), col.Count())
	}
	for _, p := range col.Payloads() {
		if strings.HasPrefix(p, "this") {
			t.Errorf("an oversize datagram was emitted: %q", p)
		}
	}

	addr2 := freeUDPAddr(t)
	tr2, err := New(transport.Config{Kind: "udp", Address: addr2, Allow: []string{"192.0.2.1"}})
	if err != nil {
		t.Fatal(err)
	}
	col2 := &transporttest.Collector{}
	stop2 := transporttest.Run(t, tr2, col2.Emit)
	defer stop2()
	c2, err := net.Dial("udp", addr2)
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()
	if !transporttest.WaitFor(func() bool {
		_, _ = c2.Write([]byte("hi"))
		v, _ := tr2.State().Detail["rejected"].(int64)
		return v > 0
	}, 2*time.Second) {
		t.Fatalf("a datagram from outside the allowlist was not counted as rejected: %+v", tr2.State().Detail)
	}
	if col2.Count() != 0 {
		t.Errorf("a datagram from outside the allowlist was accepted")
	}
}

// sendersOf lists the senders a listener reports, by the name they go under.
func sendersOf(t *testing.T, tr transport.Transport) map[string]transport.SenderStat {
	t.Helper()
	list, ok := tr.State().Detail["senders"].([]transport.SenderStat)
	if !ok {
		t.Fatalf("detail.senders = %T", tr.State().Detail["senders"])
	}
	out := map[string]transport.SenderStat{}
	for _, s := range list {
		out[s.Remote] = s
	}
	return out
}

// A datagram sender is told apart by its IP, not its port: a host that sends
// from a new socket each time is still one sender. Only the senders list and
// the limit go by the IP; the frame keeps ip:port, the exact socket, for the
// sensor id and the dead letter.
func TestUDPSendersKeyedByIP(t *testing.T) {
	addr := freeUDPAddr(t)
	tr, err := New(transport.Config{Kind: "udp", Address: addr})
	if err != nil {
		t.Fatal(err)
	}
	col := &transporttest.Collector{}
	stop := transporttest.Run(t, tr, col.Emit)
	defer stop()
	if !transporttest.WaitFor(func() bool { return tr.State().Connected }, 2*time.Second) {
		t.Fatal("listener not up")
	}
	var sockets []string
	for i := 0; i < 2; i++ {
		c, err := net.Dial("udp", addr)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		if _, err := c.Write([]byte("hi")); err != nil {
			t.Fatal(err)
		}
		sockets = append(sockets, c.LocalAddr().String())
	}
	if !transporttest.WaitFor(func() bool { return col.Count() == 2 }, 2*time.Second) {
		t.Fatalf("frames = %v", col.Payloads())
	}

	st := tr.State()
	senders := sendersOf(t, tr)
	if st.Detail["distinctSenders"] != 1 || len(senders) != 1 {
		t.Fatalf("distinctSenders = %v, senders = %+v, want the one address both sockets share", st.Detail["distinctSenders"], senders)
	}
	if s, ok := senders["127.0.0.1"]; !ok || s.Messages != 2 {
		t.Errorf("senders = %+v, want one named 127.0.0.1 with 2 messages", senders)
	}
	var remotes []string
	for _, f := range col.Frames() {
		remotes = append(remotes, f.Remote)
	}
	slices.Sort(remotes)
	slices.Sort(sockets)
	if !slices.Equal(remotes, sockets) {
		t.Errorf("frame remotes = %v, want the senders' sockets %v: Frame.Remote stays ip:port", remotes, sockets)
	}
}

// The stream driver notes an undecodable message under the frame's Remote,
// which is ip:port. The sender's row is kept by IP, so the note must reach it
// instead of starting a row of its own for the port.
func TestNoteMalformedCountsAgainstTheSenderIP(t *testing.T) {
	addr := freeUDPAddr(t)
	tr, err := New(transport.Config{Kind: "udp", Address: addr})
	if err != nil {
		t.Fatal(err)
	}
	col := &transporttest.Collector{}
	stop := transporttest.Run(t, tr, col.Emit)
	defer stop()
	if !transporttest.WaitFor(func() bool { return tr.State().Connected }, 2*time.Second) {
		t.Fatal("listener not up")
	}
	c, err := net.Dial("udp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_, _ = c.Write([]byte("garbage"))
	if !transporttest.WaitFor(func() bool { return col.Count() == 1 }, 2*time.Second) {
		t.Fatal("no frame")
	}
	noter, ok := tr.(transport.MalformedNoter)
	if !ok {
		t.Fatal("the udp transport does not note undecodable messages")
	}

	remote := col.Frames()[0].Remote // ip:port, as the driver passes it on
	noter.NoteMalformed(remote, "parse_error", "bad")
	senders := sendersOf(t, tr)
	if len(senders) != 1 {
		t.Fatalf("senders = %+v, want the one row of the sender's IP: the note made a row for the port", senders)
	}
	if s := senders["127.0.0.1"]; s.Messages != 1 || s.Malformed != 1 || s.LastReason != "parse_error" || s.LastError != "bad" || s.LastMalformedAt == nil {
		t.Errorf("127.0.0.1 = %+v, want its 1 message and the 1 undecodable one", s)
	}

	// The same address in the other forms a remote takes lands on the same row.
	noter.NoteMalformed("[::ffff:127.0.0.1]:9", "parse_error", "again")
	noter.NoteMalformed("127.0.0.1:65000", "parse_error", "again")
	senders = sendersOf(t, tr)
	if s := senders["127.0.0.1"]; len(senders) != 1 || s.Malformed != 3 {
		t.Errorf("senders = %+v, want one row with 3 undecodable messages", senders)
	}
}

// A limit is kept per sender IP: 127.0.0.1 and ::1 reach one dual-stack socket
// and have a bucket each. The flooder's bucket is asked before the shared one,
// so it spends none of the shared budget (10 a second, a sender's is 3), and
// the other sender's messages all get through.
func TestPerSenderLimitIsolatesSenders(t *testing.T) {
	probe, err := net.ListenPacket("udp", "[::1]:0")
	if err != nil {
		t.Skipf("no IPv6 loopback: %v", err)
	}
	port := probe.LocalAddr().(*net.UDPAddr).Port
	probe.Close()
	// An address with no host is the wildcard: one socket for both families.
	tr, err := New(transport.Config{Kind: "udp", Address: ":" + strconv.Itoa(port), MaxMessagesPerSec: 10, MaxMessagesPerSecPerSender: 3})
	if err != nil {
		t.Fatal(err)
	}
	col := &transporttest.Collector{}
	stop := transporttest.Run(t, tr, col.Emit)
	defer stop()
	if !transporttest.WaitFor(func() bool { return tr.State().Connected }, 2*time.Second) {
		t.Fatal("listener not up")
	}
	counter := func(key string) int64 { v, _ := tr.State().Detail[key].(int64); return v }
	handled := func() int64 { return counter("accepted") + counter("senderRateLimited") }
	flood := func(network, host string, n int, total int64) {
		t.Helper()
		c, err := net.Dial(network, net.JoinHostPort(host, strconv.Itoa(port)))
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		for i := 0; i < n; i++ {
			_, _ = c.Write([]byte("m"))
		}
		if !transporttest.WaitFor(func() bool { return handled() == total }, 3*time.Second) {
			t.Fatalf("%s: accepted + senderRateLimited = %d, want %d: %+v", host, handled(), total, tr.State().Detail)
		}
	}
	flood("udp4", "127.0.0.1", 20, 20)
	flood("udp6", "::1", 2, 22)

	if n := counter("rateLimited"); n != 0 {
		t.Errorf("rateLimited = %d, want 0: the flooder spent none of the shared budget, and the other sender's 2 are within it", n)
	}
	senders := sendersOf(t, tr)
	v4, v6 := senders["127.0.0.1"], senders["::1"]
	if len(senders) != 2 || tr.State().Detail["distinctSenders"] != 2 {
		t.Fatalf("senders = %+v, want 127.0.0.1 and ::1 (an IPv4 sender on a dual-stack socket is the IPv4 address)", senders)
	}
	if v4.Messages < 3 || v4.Messages > 4 || v4.RateLimited < 16 || v4.Messages+v4.RateLimited != 20 {
		t.Errorf("127.0.0.1 = %+v, want its burst of 3 (4 if a token refilled) accepted and the rest of its 20 limited", v4)
	}
	if v6.Messages != 2 || v6.RateLimited != 0 {
		t.Errorf("::1 = %+v, want both of its messages accepted and none limited", v6)
	}
	if got := counter("senderRateLimited"); got != v4.RateLimited {
		t.Errorf("senderRateLimited = %d, want the flooder's %d", got, v4.RateLimited)
	}
	var fromV6 int
	for _, f := range col.Frames() {
		if strings.HasPrefix(f.Remote, "[::1]:") {
			fromV6++
		}
	}
	if fromV6 != 2 {
		t.Errorf("%d frames came from ::1, want 2: %v", fromV6, col.Frames())
	}
}

// A datagram of lines is asked of the sender's bucket at one instant, so once
// a line is refused the rest are: they are dropped and counted together, and
// the lines that passed first are delivered.
func TestPerSenderLimitCountsTheRestOfADatagram(t *testing.T) {
	addr := freeUDPAddr(t)
	tr, err := New(transport.Config{Kind: "udp", Address: addr, MaxMessagesPerSecPerSender: 2})
	if err != nil {
		t.Fatal(err)
	}
	col := &transporttest.Collector{}
	stop := transporttest.Run(t, tr, col.Emit)
	defer stop()
	if !transporttest.WaitFor(func() bool { return tr.State().Connected }, 2*time.Second) {
		t.Fatal("listener not up")
	}
	c, err := net.Dial("udp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_, _ = c.Write([]byte("a\nb\nc\nd\ne\n"))
	counter := func(key string) int64 { v, _ := tr.State().Detail[key].(int64); return v }
	if !transporttest.WaitFor(func() bool { return counter("accepted")+counter("senderRateLimited") == 5 }, 2*time.Second) {
		t.Fatalf("accepted + senderRateLimited never reached 5: %+v", tr.State().Detail)
	}
	if counter("accepted") != 2 || counter("senderRateLimited") != 3 || counter("rateLimited") != 0 {
		t.Errorf("accepted %d, senderRateLimited %d, rateLimited %d; want 2, 3, 0", counter("accepted"), counter("senderRateLimited"), counter("rateLimited"))
	}
	if got := col.Payloads(); !slices.Equal(got, []string{"a", "b"}) {
		t.Errorf("delivered %q, want the lines that passed the sender's bucket: a and b", got)
	}
	if s := sendersOf(t, tr)["127.0.0.1"]; s.Messages != 2 || s.RateLimited != 3 {
		t.Errorf("127.0.0.1 = %+v, want 2 messages and 3 rate limited", s)
	}
}

// A datagram dropped by its sender's bucket adds no row to the senders list,
// whose full list would evict a sender that is being heard for it. Here the
// sender's bucket is spent by hand for the next hour, standing in for a sender
// the list has forgotten while the limiter still remembers it: the drop is
// counted by the listener and by no row.
func TestDroppedDatagramAddsNoSenderRow(t *testing.T) {
	addr := freeUDPAddr(t)
	tr, err := New(transport.Config{Kind: "udp", Address: addr, MaxMessagesPerSecPerSender: 1})
	if err != nil {
		t.Fatal(err)
	}
	if !tr.(*Listener).perSender.Allow("127.0.0.1", time.Now().Add(time.Hour)) {
		t.Fatal("a new sender's first message was refused")
	}
	col := &transporttest.Collector{}
	stop := transporttest.Run(t, tr, col.Emit)
	defer stop()
	if !transporttest.WaitFor(func() bool { return tr.State().Connected }, 2*time.Second) {
		t.Fatal("listener not up")
	}
	c, err := net.Dial("udp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_, _ = c.Write([]byte("a\nb\n"))
	if !transporttest.WaitFor(func() bool { v, _ := tr.State().Detail["senderRateLimited"].(int64); return v == 2 }, 2*time.Second) {
		t.Fatalf("the sender's lines were not dropped for its rate: %+v", tr.State().Detail)
	}
	if senders := sendersOf(t, tr); len(senders) != 0 || tr.State().Detail["distinctSenders"] != 0 {
		t.Errorf("senders = %+v, want none: a dropped datagram is no reason to list its sender", senders)
	}
	if col.Count() != 0 {
		t.Errorf("delivered %q, want nothing", col.Payloads())
	}
}

func TestUDPBindConflictIsExplained(t *testing.T) {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	tr, err := New(transport.Config{Kind: "udp", Address: pc.LocalAddr().String()})
	if err != nil {
		t.Fatal(err)
	}
	if err := tr.Run(t.Context(), (&transporttest.Collector{}).Emit); err == nil || !strings.Contains(err.Error(), "/udp is already in use") {
		t.Errorf("err = %v", err)
	}
}

func TestUDPRejectsABadAllowlist(t *testing.T) {
	if _, err := New(transport.Config{Kind: "udp", Address: "127.0.0.1:9", Allow: []string{"not-an-ip"}}); err == nil {
		t.Error("an unparsable allow entry was accepted")
	}
}

// State lists at most 50 senders but counts them all: the tracker holds more
// than it renders.
func TestUDPRefusesNonNumericPorts(t *testing.T) {
	// A service name resolves inside net.ListenPacket, so it would bind a port
	// the listener policy never sees; out-of-range and signed ports are refused too.
	for _, addr := range []string{":smtp", "127.0.0.1:domain", ":70000", ":-1"} {
		if _, err := New(transport.Config{Kind: "udp", Address: addr}); err == nil {
			t.Errorf("%s: expected a validation error", addr)
		}
	}
	if _, err := New(transport.Config{Kind: "udp", Address: "127.0.0.1:0"}); err != nil {
		t.Errorf("port 0 should stay allowed: %v", err)
	}
}

// A port is decimal digits. strconv.Atoi also takes a sign, so "+25" would pass
// for 25 and bind a port the operator did not write.
func TestRefusesSignedPorts(t *testing.T) {
	for _, addr := range []string{":+25", "127.0.0.1:+8080", "[::1]:+25", ":+0", ":-0", ":-25"} {
		if _, err := New(transport.Config{Kind: "udp", Address: addr}); err == nil {
			t.Errorf("%s: a signed port was accepted", addr)
		}
	}
	for _, addr := range []string{":25", "127.0.0.1:0", "[::1]:65535"} {
		if _, err := New(transport.Config{Kind: "udp", Address: addr}); err != nil {
			t.Errorf("%s: %v", addr, err)
		}
	}
}

func TestUDPStateTruncatesSendersButCountsThemAll(t *testing.T) {
	tr, err := New(transport.Config{Kind: "udp", Address: "127.0.0.1:9"})
	if err != nil {
		t.Fatal(err)
	}
	l := tr.(*Listener)
	now := time.Now().UTC()
	for i := 0; i < 60; i++ {
		l.senders.Seen(fmt.Sprintf("10.0.0.%d", i+1), now)
	}
	st := l.State()
	list, ok := st.Detail["senders"].([]transport.SenderStat)
	if !ok {
		t.Fatalf("senders = %T", st.Detail["senders"])
	}
	if len(list) != 50 {
		t.Errorf("listed %d senders, want 50", len(list))
	}
	if st.Detail["distinctSenders"] != 60 {
		t.Errorf("distinctSenders = %v, want 60", st.Detail["distinctSenders"])
	}
}

// A datagram listener reports both numbers from the start, as numbers, so the
// shape of its health does not depend on whether its socket is open yet, or on
// the OS: the size is 0 until the socket is open, and the kernel's drop count
// is 0 where the OS cannot report it.
func TestUDPReportsReceiveBufferAndKernelDropsFromTheStart(t *testing.T) {
	tr, err := New(transport.Config{Kind: "udp", Address: "127.0.0.1:9"})
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"receiveBufferEffectiveBytes", "kernelDropped"} {
		if v, ok := tr.State().Detail[key].(int64); !ok || v != 0 {
			t.Errorf("detail[%q] = %#v before the socket is open, want int64 0", key, tr.State().Detail[key])
		}
	}
}

// The listener asks the OS for the receive buffer its config names, 4 MiB when
// it names none, and reports the size the OS says the socket has. That is not
// the ask: Linux keeps twice the ask but caps the ask at net.core.rmem_max,
// and other systems cap it their own way. So what is expected is the size a
// socket of this machine reports after the same ask, not a fixed number.
func TestReceiveBufferApplied(t *testing.T) {
	grantedFor := func(t *testing.T, ask int) int64 {
		t.Helper()
		pc, err := net.ListenPacket("udp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer pc.Close()
		conn := pc.(*net.UDPConn)
		if err := conn.SetReadBuffer(ask); err != nil {
			t.Fatalf("a socket could not be given a receive buffer of %d bytes: %v", ask, err)
		}
		size, err := receiveBufferSize(conn)
		if err != nil {
			t.Skipf("this platform cannot say how big a socket's receive buffer is: %v", err)
		}
		return int64(size)
	}
	for _, tc := range []struct {
		name       string
		configured int // transport.Config.ReceiveBufferBytes
		asked      int // what the socket is expected to be asked for
	}{
		{"none configured", 0, transport.DefaultReceiveBufferBytes},
		{"a size configured", 64 << 10, 64 << 10},
	} {
		t.Run(tc.name, func(t *testing.T) {
			want := grantedFor(t, tc.asked)
			tr, err := New(transport.Config{Kind: "udp", Address: freeUDPAddr(t), ReceiveBufferBytes: tc.configured})
			if err != nil {
				t.Fatal(err)
			}
			stop := transporttest.Run(t, tr, (&transporttest.Collector{}).Emit)
			defer stop()
			if !transporttest.WaitFor(func() bool { return tr.State().Connected }, 2*time.Second) {
				t.Fatal("listener not up")
			}
			// The size is recorded before the listener reports itself connected.
			got, ok := tr.State().Detail["receiveBufferEffectiveBytes"].(int64)
			if !ok || got <= 0 {
				t.Fatalf("receiveBufferEffectiveBytes = %#v, want a positive int64", tr.State().Detail["receiveBufferEffectiveBytes"])
			}
			if got != want {
				t.Errorf("receiveBufferEffectiveBytes = %d, want %d: what this machine reports for a socket asked for %d bytes", got, want, tc.asked)
			}
			if n, ok := tr.State().Detail["kernelDropped"].(int64); !ok || n != 0 {
				t.Errorf("kernelDropped = %#v with nothing sent, want int64 0", tr.State().Detail["kernelDropped"])
			}
		})
	}
}

// A buffer the schema would refuse is refused here too, so a stored config
// that skipped the API cannot hand the OS a size that means nothing (a negative
// one, say, which different kernels read as different buffers).
func TestUDPRefusesAReceiveBufferOutOfRange(t *testing.T) {
	for _, size := range []int{-1, transport.MaxReceiveBufferBytes + 1} {
		if _, err := New(transport.Config{Kind: "udp", Address: "127.0.0.1:9", ReceiveBufferBytes: size}); err == nil {
			t.Errorf("receiveBufferBytes %d was accepted", size)
		}
	}
	for _, size := range []int{0, 4096, transport.DefaultReceiveBufferBytes, transport.MaxReceiveBufferBytes} {
		if _, err := New(transport.Config{Kind: "udp", Address: "127.0.0.1:9", ReceiveBufferBytes: size}); err != nil {
			t.Errorf("receiveBufferBytes %d: %v", size, err)
		}
	}
}
