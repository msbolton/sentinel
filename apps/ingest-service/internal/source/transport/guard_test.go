package transport

import (
	"errors"
	"net"
	"syscall"
	"testing"
	"time"
)

func TestAllowlist(t *testing.T) {
	a, err := ParseAllowlist([]string{"10.0.0.0/8", "192.168.1.20", "2001:db8::/32"})
	if err != nil {
		t.Fatal(err)
	}
	for remote, want := range map[string]bool{
		"10.1.2.3:5000": true, "192.168.1.20:1": true, "192.168.1.21:1": false,
		"[2001:db8::1]:9": true, "[2001:db9::1]:9": false, "garbage": false,
	} {
		if got := a.Allows(remote); got != want {
			t.Errorf("Allows(%q) = %v, want %v", remote, got, want)
		}
	}
	if a.Open() {
		t.Error("a non-empty list is not open")
	}
	open, _ := ParseAllowlist(nil)
	if !open.Open() || !open.Allows("8.8.8.8:53") {
		t.Error("an empty list allows anyone")
	}
	if _, err := ParseAllowlist([]string{"10.0.0.0/33"}); err == nil {
		t.Error("a bad CIDR must be refused")
	}
}

func TestAllowlistMatchesMappedIPv4(t *testing.T) {
	a, _ := ParseAllowlist([]string{"10.0.0.0/8"})
	if !a.Allows("[::ffff:10.0.0.5]:4000") {
		t.Error("an IPv4-mapped IPv6 sender must match an IPv4 CIDR")
	}
}

func TestLimiter(t *testing.T) {
	var unlimited *Limiter
	if !unlimited.Allow() || NewLimiter(0) != nil {
		t.Error("0 means unlimited")
	}
	l := NewLimiter(2)
	allowed := 0
	for i := 0; i < 10; i++ {
		if l.Allow() {
			allowed++
		}
	}
	if allowed < 2 || allowed > 3 {
		t.Errorf("allowed %d of 10 at 2/s", allowed)
	}
}

func TestSenders(t *testing.T) {
	s := NewSenders(2)
	t0 := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	s.Connect("a:1", t0)
	s.Seen("a:1", t0.Add(time.Second))
	s.Seen("b:1", t0.Add(2*time.Second))
	s.Seen("c:1", t0.Add(3*time.Second)) // over the cap: the oldest unconnected one goes
	list, distinct := s.Snapshot(t0.Add(4*time.Second), 10*time.Minute)
	if distinct != 2 || len(list) != 2 || list[0].Remote != "c:1" || !list[1].Connected || list[1].Messages != 1 {
		t.Errorf("snapshot = %+v (%d)", list, distinct)
	}
	s.Disconnect("a:1")
	if list, _ := s.Snapshot(t0.Add(20*time.Minute), 10*time.Minute); len(list) != 0 {
		t.Errorf("stale senders must age out: %+v", list)
	}
}

func TestBindError(t *testing.T) {
	err := BindError("tcp", ":6000", &net.OpError{Op: "listen", Err: syscall.EADDRINUSE})
	if err == nil || err.Error() != "port 6000/tcp is already in use on the listener host" {
		t.Errorf("err = %v", err)
	}
	other := errors.New("boom")
	if BindError("udp", ":1", other) != other {
		t.Error("other errors pass through")
	}
}
