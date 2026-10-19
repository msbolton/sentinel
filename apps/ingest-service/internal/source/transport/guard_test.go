package transport

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"net"
	"net/netip"
	"slices"
	"sort"
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

// allowedBothWays checks a sender against the list as text (Allows) and as the
// parsed address (AllowsAddr), which must answer alike: a stream listener asks
// the first and a datagram listener the second.
func allowedBothWays(t *testing.T, a Allowlist, remote string, want bool) {
	t.Helper()
	if got := a.Allows(remote); got != want {
		t.Errorf("Allows(%q) = %v, want %v", remote, got, want)
	}
	var addr netip.Addr
	if ap, err := netip.ParseAddrPort(remote); err == nil {
		addr = ap.Addr()
	} else if ip, err := netip.ParseAddr(remote); err == nil {
		addr = ip
	} else {
		t.Fatalf("%q is not an address", remote)
	}
	if got := a.AllowsAddr(addr); got != want {
		t.Errorf("AllowsAddr(%v) = %v, want %v: it must answer as Allows(%q) does", addr, got, want, remote)
	}
}

// A link-local sender reaches the listener with a zone, the interface it came
// in on ("fe80::1%en0"). The zone is not part of who the sender is, and a CIDR
// cannot carry one (a prefix with a zone does not parse, and a prefix never
// contains an address that has one), so the list matches on the address alone.
func TestAllowlistZonedIPv6(t *testing.T) {
	a, err := ParseAllowlist([]string{"fe80::/10", "2001:db8::/32", "fd00::7"})
	if err != nil {
		t.Fatal(err)
	}
	for remote, want := range map[string]bool{
		"[fe80::1%en0]:9":    true,  // in fe80::/10
		"[fe80::1%eth0]:9":   true,  // whatever the zone
		"[febf::1%en0]:9":    true,  // the last address of the /10
		"[fec0::1%en0]:9":    false, // the first one past it
		"[2001:db8::1%lo]:9": true,
		"[2001:db9::1%lo]:9": false,
		"[fd00::7%en0]:9":    true, // a single address
		"[fd00::8%en0]:9":    false,
		"fe80::1%en0":        true, // a bare address
		"[fe80::1]:9":        true, // no zone, as before
	} {
		allowedBothWays(t, a, remote, want)
	}

	// A zone written in an entry is dropped as well: the entry names an address.
	zoned, err := ParseAllowlist([]string{"fe80::5%en0"})
	if err != nil {
		t.Fatal(err)
	}
	for remote, want := range map[string]bool{
		"[fe80::5%en0]:9": true,
		"[fe80::5%en1]:9": true,
		"[fe80::5]:9":     true,
		"[fe80::6%en0]:9": false,
	} {
		allowedBothWays(t, zoned, remote, want)
	}

	sender := netip.MustParseAddr("fe80::1%en0")
	if n := testing.AllocsPerRun(100, func() { a.AllowsAddr(sender) }); n != 0 {
		t.Errorf("AllowsAddr allocated %v times per call for a zoned sender, want none: it runs for every datagram", n)
	}
}

// An entry written as an IPv4-mapped IPv6 CIDR ("::ffff:10.0.0.0/104") means
// the IPv4 range inside it. A sender is matched as its IPv4 address, however its
// address reaches a dual-stack socket, so an entry left as IPv6 could never
// match one.
func TestAllowlist4in6CIDR(t *testing.T) {
	cases := []struct {
		entry   string
		allowed []string
		refused []string
	}{
		{"::ffff:10.0.0.0/104",
			[]string{"10.0.0.1:1", "10.255.255.255:1", "[::ffff:10.1.2.3]:1"},
			[]string{"11.0.0.1:1", "9.255.255.255:1", "[::ffff:11.0.0.1]:1", "[2001:db8::1]:1"}},
		{"::ffff:192.168.1.20/128",
			[]string{"192.168.1.20:1", "[::ffff:192.168.1.20]:1"},
			[]string{"192.168.1.21:1", "[::ffff:192.168.1.21]:1"}},
		// The whole mapped block is every IPv4 address, and no IPv6 one.
		{"::ffff:0.0.0.0/96",
			[]string{"8.8.8.8:1", "10.1.2.3:1", "[::ffff:8.8.8.8]:1"},
			[]string{"[2001:db8::1]:1", "[::1]:1"}},
		// A prefix that is not wholly inside the mapped block is no IPv4 range
		// and stays the IPv6 range it says: here ::/64, which holds ::1 and
		// none of IPv4.
		{"::ffff:10.0.0.0/64",
			[]string{"[::1]:1"},
			[]string{"10.1.2.3:1", "[::ffff:10.1.2.3]:1"}},
	}
	for _, tc := range cases {
		a, err := ParseAllowlist([]string{tc.entry})
		if err != nil {
			t.Fatalf("%s: %v", tc.entry, err)
		}
		for _, remote := range tc.allowed {
			t.Run(tc.entry+" allows "+remote, func(t *testing.T) { allowedBothWays(t, a, remote, true) })
		}
		for _, remote := range tc.refused {
			t.Run(tc.entry+" refuses "+remote, func(t *testing.T) { allowedBothWays(t, a, remote, false) })
		}
	}
}

// AllowsAddr is the check for a sender whose address is already parsed, as a
// datagram listener's is: it must answer as Allows does for the same address,
// and cost no allocation, since it runs for every datagram.
func TestAllowsAddr(t *testing.T) {
	a, err := ParseAllowlist([]string{"10.0.0.0/8", "192.168.1.20", "2001:db8::/32"})
	if err != nil {
		t.Fatal(err)
	}
	for ip, want := range map[string]bool{
		"10.1.2.3": true, "192.168.1.20": true, "192.168.1.21": false, "11.0.0.1": false,
		"2001:db8::1": true, "2001:db9::1": false,
		// An IPv4-mapped IPv6 sender is the IPv4 address it wraps, so it matches
		// an IPv4 entry and only that.
		"::ffff:10.0.0.5": true, "::ffff:192.168.1.20": true, "::ffff:11.0.0.1": false,
	} {
		addr := netip.MustParseAddr(ip)
		if got := a.AllowsAddr(addr); got != want {
			t.Errorf("AllowsAddr(%s) = %v, want %v", ip, got, want)
		}
		// The string form of the same sender, with a port, gets the same answer.
		if got := a.Allows(netip.AddrPortFrom(addr, 4000).String()); got != want {
			t.Errorf("Allows(%q) = %v, want %v: it must answer as AllowsAddr does", netip.AddrPortFrom(addr, 4000), got, want)
		}
	}
	if a.AllowsAddr(netip.Addr{}) {
		t.Error("the zero address is nobody, so it is not on a list")
	}
	open, _ := ParseAllowlist(nil)
	for _, addr := range []netip.Addr{netip.MustParseAddr("8.8.8.8"), netip.MustParseAddr("2001:4860::1"), {}} {
		if !open.AllowsAddr(addr) {
			t.Errorf("an empty list refused %v: it allows anyone", addr)
		}
	}

	sender := netip.MustParseAddr("::ffff:10.1.2.3")
	if n := testing.AllocsPerRun(100, func() { a.AllowsAddr(sender) }); n != 0 {
		t.Errorf("AllowsAddr allocated %v times per call, want none: it runs for every datagram", n)
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

// rowsOf lists the senders a table holds, sorted, without disturbing it.
func rowsOf(s *Senders) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	keys := make([]string, 0, len(s.m))
	for k := range s.m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// The sender that makes room in a full table is the idle one that sent least
// recently, by the stamps it was given: which is not the order the calls came
// in when a stamp is older than one before it (two goroutines that stamped in
// one order and recorded in another, a clock that stepped back). A connected
// sender is never the one, however long ago it sent; and a table whose senders
// are all connected grows past its cap rather than drop one.
func TestSendersEvictTheIdleSenderThatSentLeastRecently(t *testing.T) {
	t0 := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	at := func(sec int) time.Time { return t0.Add(time.Duration(sec) * time.Second) }
	check := func(t *testing.T, s *Senders, want ...string) {
		t.Helper()
		if got := rowsOf(s); !slices.Equal(got, want) {
			t.Errorf("table holds %v, want %v", got, want)
		}
	}

	t.Run("by stamp, not by the order of the calls", func(t *testing.T) {
		s := NewSenders(3)
		s.Seen("a", at(5))
		s.Seen("b", at(1)) // recorded after a, but sent before it
		s.Seen("c", at(3))
		s.Seen("d", at(9))
		check(t, s, "a", "c", "d")
	})

	t.Run("a message makes a sender recent again", func(t *testing.T) {
		s := NewSenders(2)
		s.Seen("a", at(0))
		s.Seen("b", at(1))
		s.Seen("a", at(2))
		s.Seen("c", at(3))
		check(t, s, "a", "c")
	})

	t.Run("a connected sender stays, and an idle one goes before it", func(t *testing.T) {
		s := NewSenders(2)
		s.Connect("a", at(0)) // connected and silent: the oldest of all
		s.Seen("b", at(1))
		s.Seen("c", at(2))
		check(t, s, "a", "c")
		// Once it hangs up, a is idle and never sent anything: older than c.
		s.Disconnect("a")
		s.Seen("d", at(3))
		check(t, s, "c", "d")
	})

	t.Run("a sender that hangs up is placed by when it last sent", func(t *testing.T) {
		s := NewSenders(3)
		s.Connect("a", at(0))
		s.Seen("a", at(5))
		s.Seen("b", at(1))
		s.Seen("c", at(9))
		s.Disconnect("a") // idle now, newer than b and older than c
		s.Seen("d", at(10))
		check(t, s, "a", "c", "d")
		s.Seen("e", at(11))
		check(t, s, "c", "d", "e")
	})

	t.Run("an undecodable message counts as a stamp for a sender with none", func(t *testing.T) {
		s := NewSenders(2)
		s.Malformed("a", "parse_error", "bad", at(4))
		s.Seen("b", at(1))
		s.Seen("c", at(5))
		check(t, s, "a", "c")
		// It does not make a sender that has a stamp look newer.
		s.Malformed("a", "parse_error", "bad", at(8))
		s.Seen("d", at(6))
		check(t, s, "c", "d")
	})

	t.Run("a table of connected senders grows past its cap", func(t *testing.T) {
		s := NewSenders(2)
		for i, k := range []string{"a", "b", "c", "d"} {
			s.Connect(k, at(i))
		}
		check(t, s, "a", "b", "c", "d")
		s.Disconnect("b")
		s.Connect("e", at(5)) // b is the only idle sender, and the cap is long passed
		check(t, s, "a", "c", "d", "e")
	})

	t.Run("a sender the snapshot forgot is gone from the order too", func(t *testing.T) {
		s := NewSenders(3)
		s.Seen("a", at(0))
		s.Seen("b", at(700))
		s.Seen("c", at(710))
		if list, _ := s.Snapshot(at(720), 10*time.Minute); len(list) != 2 {
			t.Fatalf("snapshot = %+v, want b and c: a is past the window", list)
		}
		s.Seen("d", at(730))
		s.Seen("e", at(740)) // the table is full again, so b goes
		check(t, s, "c", "d", "e")
	})
}

// referenceSenders is what the senders table did before it kept its idle
// senders in order: a map, and a scan of all of it to find the one to evict.
type referenceSenders struct {
	max  int
	rows map[string]*SenderStat
}

func (r *referenceSenders) entry(key string, now time.Time, evicted func(candidates []string)) *SenderStat {
	e, ok := r.rows[key]
	if ok {
		return e
	}
	if len(r.rows) >= r.max {
		var oldest []string
		var at time.Time
		for k, row := range r.rows {
			if row.Connected {
				continue
			}
			switch {
			case len(oldest) == 0 || row.LastAt.Before(at):
				oldest, at = []string{k}, row.LastAt
			case row.LastAt.Equal(at):
				oldest = append(oldest, k)
			}
		}
		evicted(oldest)
	}
	e = &SenderStat{Remote: key, Since: now}
	r.rows[key] = e
	return e
}

// The table must evict as the scan did. A random run of operations goes to
// both, with stamps that arrive out of order and a few senders that tie, and
// after every step they must hold the same senders: whichever of several tied
// senders the table gives up, the scan could have given up too, and the
// reference then gives up the same one.
func TestSendersEvictionMatchesAScanOfTheTable(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	t0 := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	const (
		tableCap = 8
		keys     = 30
		steps    = 20000
	)
	table := NewSenders(tableCap)
	ref := &referenceSenders{max: tableCap, rows: map[string]*SenderStat{}}
	stamp := func() time.Time {
		// Mostly forward in time, sometimes well behind, sometimes the same
		// instant as the step before.
		return t0.Add(time.Duration(rng.IntN(60)) * time.Second)
	}
	for step := range steps {
		key := fmt.Sprintf("10.0.0.%d", rng.IntN(keys))
		now := stamp()
		before := rowsOf(table)
		var candidates []string
		evicted := func(c []string) { candidates = c }
		op := rng.IntN(7)
		var name string
		switch op {
		case 0:
			name = "Connect"
			table.Connect(key, now)
			e := ref.entry(key, now, evicted)
			e.Connected, e.Since = true, now
		case 1, 2:
			name = "Disconnect"
			table.Disconnect(key)
			if e, ok := ref.rows[key]; ok {
				e.Connected = false
			}
		case 3, 4:
			name = "Seen"
			table.Seen(key, now)
			e := ref.entry(key, now, evicted)
			e.Messages++
			e.LastAt = now
		case 5:
			name = "Malformed"
			table.Malformed(key, "parse_error", "bad", now)
			e := ref.entry(key, now, evicted)
			if e.LastAt.IsZero() {
				e.LastAt = now
			}
		case 6:
			name = "Snapshot"
			table.Snapshot(now.Add(5*time.Minute), 5*time.Minute+30*time.Second)
			for k, e := range ref.rows {
				if !e.Connected && now.Add(5*time.Minute).Sub(e.LastAt) > 5*time.Minute+30*time.Second {
					delete(ref.rows, k)
				}
			}
		}
		if candidates != nil {
			// The reference had to give one up, and could have given up any of
			// the candidates; the table must have given up one of them.
			after := rowsOf(table)
			var gone []string
			for _, k := range before {
				if !slices.Contains(after, k) {
					gone = append(gone, k)
				}
			}
			if len(gone) != 1 || !slices.Contains(candidates, gone[0]) {
				t.Fatalf("step %d, %s %s: the table gave up %v, the scan could have given up only %v", step, name, key, gone, candidates)
			}
			delete(ref.rows, gone[0])
		}
		refKeys := make([]string, 0, len(ref.rows))
		for k := range ref.rows {
			refKeys = append(refKeys, k)
		}
		sort.Strings(refKeys)
		if got := rowsOf(table); !slices.Equal(got, refKeys) {
			t.Fatalf("step %d, %s %s: table holds %v, the scan holds %v", step, name, key, got, refKeys)
		}
		checkSendersOrder(t, table)
	}
}

// A message from a sender the table already holds is the common case on a busy
// datagram listener, so it allocates nothing: whether the sender is the newest
// (front), older than some (the middle) or older than all (the back), and whether
// it is idle or has just hung up.
func TestSendersSeenOfAHeldSenderDoesNotAllocate(t *testing.T) {
	t0 := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	s := NewSenders(100)
	for i := range 20 {
		s.Seen(fmt.Sprintf("10.0.0.%d", i), t0.Add(time.Duration(i)*time.Second))
	}
	step := 0
	for name, stamp := range map[string]func() time.Time{
		"the newest yet":  func() time.Time { step++; return t0.Add(time.Hour + time.Duration(step)*time.Second) },
		"older than some": func() time.Time { return t0.Add(10*time.Second + 500*time.Millisecond) },
		"older than all":  func() time.Time { return t0.Add(-time.Hour) },
	} {
		for _, key := range []string{"10.0.0.0", "10.0.0.7", "10.0.0.19"} {
			if n := testing.AllocsPerRun(200, func() { s.Seen(key, stamp()) }); n != 0 {
				t.Errorf("Seen of %s with a stamp %s allocated %v times per call, want none", key, name, n)
			}
		}
	}
	checkSendersOrder(t, s)
}

// checkSendersOrder checks the table's idle list against its rows: it holds
// exactly the rows that are not connected, each once, newest stamp first.
func checkSendersOrder(t *testing.T, s *Senders) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	idle := 0
	for _, row := range s.m {
		if !row.Connected {
			idle++
		}
	}
	if s.idle.Len() != idle {
		t.Fatalf("the order holds %d senders, and %d are idle", s.idle.Len(), idle)
	}
	var prev time.Time
	for el := s.idle.Front(); el != nil; el = el.Next() {
		row := el.Value.(*senderRow)
		if row.Connected || s.m[row.Remote] != row || row.at != el {
			t.Fatalf("the order lists %s, which is connected (%v), unknown or points elsewhere", row.Remote, row.Connected)
		}
		if el != s.idle.Front() && row.LastAt.After(prev) {
			t.Fatalf("the order puts %s (%v) behind an older stamp (%v)", row.Remote, row.LastAt, prev)
		}
		prev = row.LastAt
	}
}

// The cut never splits a rune and never gives up more than the three bytes a
// rune can have before the cut, whatever the bytes are: text of one-, two-,
// three- and four-byte runes, and bytes that are no text.
func TestTruncateUTF8(t *testing.T) {
	texts := map[string]string{
		"ascii":           strings.Repeat("abcdefghij", 8),
		"two-byte runes":  strings.Repeat("é", 60),
		"three-byte":      strings.Repeat("€", 40),
		"four-byte":       strings.Repeat("\U0001D11E", 30),
		"mixed":           strings.Repeat("a€é\U0001D11Eb", 12),
		"continuations":   strings.Repeat("\x80", 120),
		"text, then them": "abc" + strings.Repeat("\x80", 120),
	}
	for name, s := range texts {
		for n := 0; n <= len(s)+2; n++ {
			got := truncateUTF8(s, n)
			switch {
			case n >= len(s):
				if got != s {
					t.Fatalf("%s, n=%d: cut a string that fits: %d bytes", name, n, len(got))
				}
			case !strings.HasPrefix(s, got) || len(got) > n:
				t.Fatalf("%s, n=%d: got %d bytes %q, want a prefix of at most %d", name, n, len(got), got, n)
			case len(got) < n-(utf8.UTFMax-1):
				t.Fatalf("%s, n=%d: kept %d bytes, gave up more than %d to find a rune boundary", name, n, len(got), utf8.UTFMax-1)
			case utf8.ValidString(s) && !utf8.ValidString(got):
				t.Fatalf("%s, n=%d: split a rune: %q", name, n, got)
			}
		}
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
			// A rune is at most four bytes, so a cut walks back at most three
			// to find where one starts. Bytes that start none are not a rune
			// to keep whole, and are not worth losing the text before them.
			"a run of 0x80 bytes":            {strings.Repeat("\x80", 300), strings.Repeat("\x80", 197)},
			"a run of 0x80 bytes after text": {"ab" + strings.Repeat("\x80", 300), "ab" + strings.Repeat("\x80", 195)},
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
// a new row evicts another, and sources rotating through more addresses than
// the list holds would push out the senders that are being heard with nothing
// but dropped messages. The listener's own counter counts every drop anyway.
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

// An address that names no port cannot be shortened to one: the message gives
// the address, where it would otherwise read "port /tcp is already in use".
func TestBindErrorWithoutPort(t *testing.T) {
	inUse := &net.OpError{Op: "listen", Err: syscall.EADDRINUSE}
	for address, want := range map[string]string{
		"somesocket": "address somesocket/tcp is already in use on the listener host",
		"127.0.0.1":  "address 127.0.0.1/tcp is already in use on the listener host",
		"host:":      "address host:/tcp is already in use on the listener host",
		":6000":      "port 6000/tcp is already in use on the listener host", // with a port, as before
	} {
		if err := BindError("tcp", address, inUse); err == nil || err.Error() != want {
			t.Errorf("BindError(tcp, %q) = %v, want %q", address, err, want)
		}
	}
}

// BenchmarkSendersNewSenderAtTheCap is what a flood of spoofed addresses costs
// a datagram listener per datagram once its senders list is full: a row in, a
// row out.
func BenchmarkSendersNewSenderAtTheCap(b *testing.B) {
	const tableCap = 1000
	t0 := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	table := NewSenders(tableCap)
	keys := make([]string, 4*tableCap)
	for i := range keys {
		keys[i] = netip.AddrFrom4([4]byte{10, byte(i >> 16), byte(i >> 8), byte(i)}).String()
	}
	for i := range tableCap {
		table.Seen(keys[i], t0.Add(time.Duration(i)*time.Millisecond))
	}
	b.ResetTimer()
	for i := range b.N {
		// Keys cycle through four times the cap, so each one has been evicted by
		// the time it comes round again and is new to the table.
		table.Seen(keys[tableCap+i%(3*tableCap)], t0.Add(time.Duration(tableCap+i)*time.Millisecond))
	}
}
