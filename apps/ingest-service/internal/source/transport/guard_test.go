package transport

import (
	"encoding/json"
	"errors"
	"net"
	"net/netip"
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

// A sender's limit is its own: one sender spending its bucket leaves another's
// whole, a bucket refills with time up to one second's worth, and past its cap
// the limiter forgets the sender used least recently.
func TestSenderLimiterPerKeyAndEviction(t *testing.T) {
	t0 := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	// size is how many senders the limiter remembers, counted by each of its
	// two indexes, which must agree.
	size := func(l *SenderLimiter) (byKey, byAge int) {
		l.mu.Lock()
		defer l.mu.Unlock()
		return len(l.m), l.order.Len()
	}

	t.Run("a rate of 0 or less is no limiter, and no limiter limits nobody", func(t *testing.T) {
		for _, perSec := range []int{0, -1} {
			if l := NewSenderLimiter(perSec, 10); l != nil {
				t.Errorf("NewSenderLimiter(%d, 10) = %v, want nil: no per-sender limit", perSec, l)
			}
		}
		var off *SenderLimiter
		for range 100 {
			if !off.Allow("a", t0) {
				t.Fatal("a nil limiter limited a sender")
			}
		}
	})

	t.Run("each sender has a bucket of one second's worth that refills", func(t *testing.T) {
		l := NewSenderLimiter(3, 10)
		sent := func(key string, at time.Time, n int) (allowed int) {
			for range n {
				if l.Allow(key, at) {
					allowed++
				}
			}
			return allowed
		}
		if got := sent("a", t0, 10); got != 3 {
			t.Errorf("a sent 10 at once and %d passed, want its burst of 3", got)
		}
		if got := sent("b", t0, 10); got != 3 {
			t.Errorf("b sent 10 at once and %d passed, want its own burst of 3: a's flood is not b's", got)
		}
		if got := sent("a", t0.Add(400*time.Millisecond), 10); got != 1 {
			t.Errorf("a sent 10 after 0.4 s and %d passed, want the one token that refilled (3 a second)", got)
		}
		if got := sent("a", t0.Add(time.Hour), 10); got != 3 {
			t.Errorf("a sent 10 after an hour and %d passed, want the burst of 3: idle time does not store more", got)
		}
	})

	t.Run("at the cap the sender that sent least recently is forgotten", func(t *testing.T) {
		// A burst of one: a sender is spent after its first message, and a
		// sender the limiter forgot is told by getting a full bucket again.
		l := NewSenderLimiter(1, 2)
		l.Allow("a", t0)
		l.Allow("b", t0)
		// Asking about a makes b the least recently used.
		if l.Allow("a", t0) {
			t.Fatal("a sent twice in a second and was not limited")
		}
		// c is new, and takes b's place.
		if !l.Allow("c", t0) {
			t.Fatal("a new sender was refused")
		}
		if l.Allow("a", t0) {
			t.Error("a was forgotten, but it was used more recently than b")
		}
		if !l.Allow("b", t0) {
			t.Error("b was kept, but it was the least recently used: a forgotten sender starts again with a full bucket")
		}
		if byKey, byAge := size(l); byKey != 2 || byAge != 2 {
			t.Errorf("limiter holds %d senders by key and %d by age, want 2 and 2", byKey, byAge)
		}
	})
}

// A flood of spoofed senders must not grow what a listener remembers: the
// senders table and the per-sender limiter stay at their caps however many
// distinct senders come. Each spoofed sender is new to the limiter, so its
// bucket is full and it is never limited on its own; the listener's bucket
// is what still holds the total down.
func TestSenderChurnStaysBounded(t *testing.T) {
	const (
		churn      = 20000 // distinct senders
		tableCap   = 1000
		limiterCap = 4096
		sharedRate = 100
	)
	t0 := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	table := NewSenders(tableCap)
	perSender := NewSenderLimiter(5, limiterCap)
	shared := NewLimiter(sharedRate)
	sizes := func() (inTable, inLimiter, inLimiterByAge int) {
		table.mu.Lock()
		inTable = len(table.m)
		table.mu.Unlock()
		perSender.mu.Lock()
		inLimiter, inLimiterByAge = len(perSender.m), perSender.order.Len()
		perSender.mu.Unlock()
		return inTable, inLimiter, inLimiterByAge
	}

	start := time.Now()
	passed := 0
	for i := range churn {
		sender := netip.AddrFrom4([4]byte{10, byte(i >> 16), byte(i >> 8), byte(i)}).String()
		now := t0.Add(time.Duration(i) * time.Millisecond)
		// Every sender is tracked, as for a listener with no limit of its own;
		// the listeners ask the shared bucket only of what the sender's own
		// lets through.
		table.Seen(sender, now)
		if perSender.Allow(sender, now) && shared.Allow() {
			passed++
		}
		if inTable, inLimiter, byAge := sizes(); inTable > tableCap || inLimiter > limiterCap || byAge > limiterCap {
			t.Fatalf("after %d distinct senders: the table holds %d (cap %d), the limiter %d by key and %d by age (cap %d)",
				i+1, inTable, tableCap, inLimiter, byAge, limiterCap)
		}
	}

	// Both filled to their caps, so staying within them was not for want of senders.
	if inTable, inLimiter, _ := sizes(); inTable != tableCap || inLimiter != limiterCap {
		t.Errorf("after %d distinct senders: the table holds %d and the limiter %d, want them full at %d and %d", churn, inTable, inLimiter, tableCap, limiterCap)
	}
	// None was limited on its own, so the listener's bucket is all that held
	// the total to its burst plus what it refilled while this ran.
	if most := sharedRate + int(time.Since(start).Seconds()*sharedRate) + 1; passed > most {
		t.Errorf("%d of %d spoofed senders' messages passed, want at most %d: the listener's bucket (%d a second) stopped limiting", passed, churn, most, sharedRate)
	}
	if passed < sharedRate {
		t.Errorf("%d messages passed, want at least the listener's burst of %d", passed, sharedRate)
	}
}

func TestSenderIP(t *testing.T) {
	for remote, want := range map[string]string{
		"10.0.0.5:4000":          "10.0.0.5",
		"[2001:db8::1]:9":        "2001:db8::1",
		"[::1]:9":                "::1",
		"[::ffff:10.0.0.5]:4000": "10.0.0.5", // an IPv4 sender on a dual-stack socket is the IPv4 address
		"[fe80::1%en0]:9":        "fe80::1%en0",
		"10.0.0.5":               "10.0.0.5", // a bare address
		"::ffff:10.0.0.5":        "10.0.0.5",
		"2001:db8::1":            "2001:db8::1",
		"sensors/temp":           "sensors/temp", // not an address: as it is
		"":                       "",
	} {
		if got := SenderIP(remote); got != want {
			t.Errorf("SenderIP(%q) = %q, want %q", remote, got, want)
		}
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

// A message dropped by a sender's own limit is counted against that sender's
// row. It was not accepted, so it is not one of the sender's messages and does
// not make the sender look newer. A drop never adds a row: with the list full,
// a new row evicts another by a scan of the whole list, which sources rotating
// through more addresses than the list holds would make the listener pay for
// every dropped message. The listener's own counter counts every drop anyway.
func TestSendersRateLimited(t *testing.T) {
	t0 := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	snapshot := func(s *Senders, now time.Time) map[string]SenderStat {
		list, _ := s.Snapshot(now, 10*time.Minute)
		out := map[string]SenderStat{}
		for _, e := range list {
			out[e.Remote] = e
		}
		return out
	}

	s := NewSenders(2)
	s.Seen("a:1", t0)
	s.RateLimited("a:1", 1)
	s.RateLimited("a:1", 3) // the rest of a datagram of lines, counted in one step
	a, ok := snapshot(s, t0.Add(3*time.Second))["a:1"]
	if !ok || a.RateLimited != 4 || a.Messages != 1 || !a.LastAt.Equal(t0) {
		t.Errorf("a:1 = %+v (%v), want 4 rate limited, 1 message and lastAt %v", a, ok, t0)
	}
	if a.Malformed != 0 {
		t.Errorf("a:1 malformed = %d, want none: a message dropped for its rate never reached a decoder", a.Malformed)
	}

	// The list is full once b:1 is in. A drop for x:1, which it does not hold,
	// adds no row and so evicts none: a:1 and b:1 stay as they were.
	s.Seen("b:1", t0.Add(time.Minute))
	s.RateLimited("x:1", 5)
	got := snapshot(s, t0.Add(time.Minute+2*time.Second))
	if _, ok := got["x:1"]; ok {
		t.Errorf("senders = %+v, want no row for x:1: a dropped message is no reason to track a sender", got)
	}
	if len(got) != 2 || got["a:1"].RateLimited != 4 || got["a:1"].Messages != 1 || got["b:1"].Messages != 1 {
		t.Errorf("senders = %+v, want a:1 (1 message, 4 rate limited) and b:1 (1 message) still listed", got)
	}
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
	for _, k := range []string{"remote", "since", "messages", "connected", "malformed", "rateLimited"} {
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
	bad := fields(SenderStat{Remote: "a:1", Since: t0, LastAt: t0, Messages: 3, Malformed: 2, RateLimited: 7, LastReason: "parse_error", LastError: "bad", LastMalformedAt: &at})
	for k, want := range map[string]any{
		"malformed": float64(2), "rateLimited": float64(7), "lastReason": "parse_error", "lastError": "bad",
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
