package udp

import (
	"fmt"
	"net"
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
func TestUDPStateTruncatesSendersButCountsThemAll(t *testing.T) {
	tr, err := New(transport.Config{Kind: "udp", Address: "127.0.0.1:9"})
	if err != nil {
		t.Fatal(err)
	}
	l := tr.(*Listener)
	now := time.Now().UTC()
	for i := 0; i < 60; i++ {
		l.senders.Seen(fmt.Sprintf("10.0.0.%d:5000", i+1), now)
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
