package transport

import (
	"encoding/json"
	"errors"
	"net"
	"strings"
	"syscall"
	"testing"
	"time"
	"unicode/utf8"
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

func TestSendersMalformed(t *testing.T) {
	t0 := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	snapshot := func(s *Senders, now time.Time) map[string]SenderStat {
		list, _ := s.Snapshot(now, 10*time.Minute)
		out := map[string]SenderStat{}
		for _, e := range list {
			out[e.Remote] = e
		}
		return out
	}

	t.Run("counts, and the last reason and error win", func(t *testing.T) {
		s := NewSenders(10)
		s.Seen("a:1", t0)
		s.Malformed("a:1", "parse_error", "first", t0.Add(time.Second))
		s.Malformed("a:1", "missing_identity", "second", t0.Add(2*time.Second))
		a, ok := snapshot(s, t0.Add(3*time.Second))["a:1"]
		if !ok || a.Malformed != 2 || a.LastReason != "missing_identity" || a.LastError != "second" ||
			a.LastMalformedAt == nil || !a.LastMalformedAt.Equal(t0.Add(2*time.Second)) {
			t.Fatalf("a:1 = %+v", a)
		}
		// The message was already counted when it arrived; noting that it did
		// not decode neither counts it again nor makes the sender look newer.
		if a.Messages != 1 || !a.LastAt.Equal(t0) {
			t.Errorf("messages = %d, lastAt = %v, want 1 and %v", a.Messages, a.LastAt, t0)
		}
	})

	t.Run("a snapshot taken earlier is not rewritten by a later note", func(t *testing.T) {
		s := NewSenders(10)
		s.Malformed("a:1", "parse_error", "first", t0)
		before := snapshot(s, t0)["a:1"]
		s.Malformed("a:1", "parse_error", "second", t0.Add(time.Minute))
		if before.LastMalformedAt == nil || !before.LastMalformedAt.Equal(t0) {
			t.Errorf("an earlier snapshot now says lastMalformedAt = %v, want %v", before.LastMalformedAt, t0)
		}
	})

	t.Run("a sender not seen before is listed", func(t *testing.T) {
		s := NewSenders(10)
		s.Malformed("x:1", "parse_error", "bad", t0)
		x, ok := snapshot(s, t0.Add(time.Minute))["x:1"]
		if !ok {
			t.Fatal("a sender with only an undecodable message was dropped by Snapshot")
		}
		if x.Malformed != 1 || x.Messages != 0 || !x.Since.Equal(t0) || !x.LastAt.Equal(t0) {
			t.Errorf("x:1 = %+v, want one malformed, no messages, since and lastAt at %v", x, t0)
		}
	})

	t.Run("the error is cut at 200 bytes on a rune boundary", func(t *testing.T) {
		for name, tc := range map[string]struct{ in, want string }{
			"empty":               {"", ""},
			"short":               {"bad", "bad"},
			"exactly 200":         {strings.Repeat("x", 200), strings.Repeat("x", 200)},
			"ascii over 200":      {strings.Repeat("x", 500), strings.Repeat("x", 200)},
			"a cut inside a rune": {strings.Repeat("€", 100), strings.Repeat("€", 66)}, // 66 runes are 198 bytes; the 67th spans 198-200
			"a cut between runes": {strings.Repeat("é", 150), strings.Repeat("é", 100)},
			"invalid bytes":       {strings.Repeat("\xff", 300), strings.Repeat("\xff", 200)},
		} {
			s := NewSenders(10)
			s.Malformed("a:1", "parse_error", tc.in, t0)
			got := snapshot(s, t0)["a:1"].LastError
			if got != tc.want || len(got) > 200 || (utf8.ValidString(tc.in) && !utf8.ValidString(got)) {
				t.Errorf("%s: lastError = %d bytes %q, want %d bytes", name, len(got), got, len(tc.want))
			}
		}
	})

	t.Run("an evicted sender is recreated", func(t *testing.T) {
		s := NewSenders(2)
		s.Seen("a:1", t0)
		s.Seen("b:1", t0.Add(time.Second))
		s.Seen("c:1", t0.Add(2*time.Second)) // over the cap: a:1, the oldest, goes
		if _, ok := snapshot(s, t0.Add(3*time.Second))["a:1"]; ok {
			t.Fatal("setup: a:1 should have been evicted")
		}
		s.Malformed("a:1", "parse_error", "late", t0.Add(3*time.Second))
		got := snapshot(s, t0.Add(4*time.Second))
		a, ok := got["a:1"]
		if !ok || a.Malformed != 1 || a.LastReason != "parse_error" || a.LastError != "late" || a.Messages != 0 {
			t.Fatalf("a:1 = %+v (%v), want it recreated with the one undecodable message", a, ok)
		}
		if _, ok := got["c:1"]; !ok || len(got) != 2 {
			t.Errorf("senders = %v, want a:1 and c:1: the cap holds, and the oldest idle one (b:1) made room", got)
		}
	})
}

// The field names are what the client reads. A sender that has sent nothing
// yet carries no lastAt, and one with no undecodable message carries no
// detail beyond its count.
func TestSenderStatJSON(t *testing.T) {
	t0 := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	fields := func(v SenderStat) map[string]any {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		m := map[string]any{}
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatal(err)
		}
		return m
	}

	quiet := fields(SenderStat{Remote: "a:1", Since: t0, Connected: true})
	for _, k := range []string{"remote", "since", "messages", "connected", "malformed"} {
		if _, ok := quiet[k]; !ok {
			t.Errorf("a sender that has sent nothing lacks %q: %v", k, quiet)
		}
	}
	for _, k := range []string{"lastAt", "lastReason", "lastError", "lastMalformedAt"} {
		if _, ok := quiet[k]; ok {
			t.Errorf("a sender that has sent nothing has %q: %v", k, quiet)
		}
	}

	at := t0.Add(time.Second)
	bad := fields(SenderStat{Remote: "a:1", Since: t0, LastAt: t0, Messages: 3, Malformed: 2, LastReason: "parse_error", LastError: "bad", LastMalformedAt: &at})
	for k, want := range map[string]any{
		"malformed": float64(2), "lastReason": "parse_error", "lastError": "bad",
		"lastMalformedAt": at.Format(time.RFC3339Nano), "lastAt": t0.Format(time.RFC3339Nano), "messages": float64(3),
	} {
		if bad[k] != want {
			t.Errorf("%s = %v, want %v (%v)", k, bad[k], want, bad)
		}
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
