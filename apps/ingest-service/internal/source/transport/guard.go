package transport

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sort"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"golang.org/x/time/rate"
)

// Allowlist holds the IPs and CIDRs a listener accepts senders from.
type Allowlist struct{ prefixes []netip.Prefix }

// ParseAllowlist parses IPs ("10.0.0.5", "2001:db8::1") and CIDRs.
func ParseAllowlist(entries []string) (Allowlist, error) {
	var a Allowlist
	for _, e := range entries {
		if p, err := netip.ParsePrefix(e); err == nil {
			a.prefixes = append(a.prefixes, p.Masked())
			continue
		}
		addr, err := netip.ParseAddr(e)
		if err != nil {
			return Allowlist{}, fmt.Errorf("allow: %q is not an IP address or CIDR", e)
		}
		a.prefixes = append(a.prefixes, netip.PrefixFrom(addr.Unmap(), addr.Unmap().BitLen()))
	}
	return a, nil
}

// Open reports whether the list is empty: anyone who can reach the port.
func (a Allowlist) Open() bool { return len(a.prefixes) == 0 }

// Allows reports whether the sender at remote ("host:port" or a bare IP)
// may send. IPv4-mapped IPv6 addresses match IPv4 entries.
func (a Allowlist) Allows(remote string) bool {
	if a.Open() {
		return true
	}
	host := remote
	if h, _, err := net.SplitHostPort(remote); err == nil {
		host = h
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	addr = addr.Unmap()
	for _, p := range a.prefixes {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

// Limiter caps a listener's frames per second; a nil limiter allows all.
type Limiter struct{ l *rate.Limiter }

// NewLimiter returns nil (unlimited) for perSec <= 0. The burst is one
// second's worth.
func NewLimiter(perSec int) *Limiter {
	if perSec <= 0 {
		return nil
	}
	return &Limiter{l: rate.NewLimiter(rate.Limit(perSec), perSec)}
}

// Allow takes one token.
func (l *Limiter) Allow() bool { return l == nil || l.l.Allow() }

// SenderStat is one sender as health reports it.
type SenderStat struct {
	Remote    string    `json:"remote"`
	Since     time.Time `json:"since"`
	LastAt    time.Time `json:"lastAt"`
	Messages  int64     `json:"messages"`
	Connected bool      `json:"connected"`
}

// Senders tracks who is sending, capped at max entries (connected senders
// are kept over idle ones).
type Senders struct {
	mu  sync.Mutex
	max int
	m   map[string]*SenderStat
}

// NewSenders tracks up to max senders.
func NewSenders(max int) *Senders { return &Senders{max: max, m: map[string]*SenderStat{}} }

func (s *Senders) entry(remote string, now time.Time) *SenderStat {
	e, ok := s.m[remote]
	if !ok {
		if len(s.m) >= s.max {
			s.evict()
		}
		e = &SenderStat{Remote: remote, Since: now}
		s.m[remote] = e
	}
	return e
}

func (s *Senders) evict() {
	var victim *SenderStat
	for _, e := range s.m {
		if !e.Connected && (victim == nil || e.LastAt.Before(victim.LastAt)) {
			victim = e
		}
	}
	if victim != nil {
		delete(s.m, victim.Remote)
	}
}

// Connect records a TCP connection opening.
func (s *Senders) Connect(remote string, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e := s.entry(remote, now)
	e.Connected, e.Since = true, now
}

// Disconnect records a TCP connection closing.
func (s *Senders) Disconnect(remote string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e, ok := s.m[remote]; ok {
		e.Connected = false
	}
}

// Seen counts one message from remote.
func (s *Senders) Seen(remote string, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e := s.entry(remote, now)
	e.Messages++
	e.LastAt = now
}

// Snapshot returns connected senders and those seen within window, newest
// first, and their count; older idle senders are forgotten.
func (s *Senders) Snapshot(now time.Time, window time.Duration) ([]SenderStat, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]SenderStat, 0, len(s.m))
	for k, e := range s.m {
		if !e.Connected && now.Sub(e.LastAt) > window {
			delete(s.m, k)
			continue
		}
		out = append(out, *e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastAt.After(out[j].LastAt) })
	return out, len(out)
}

// ListenCounters are a listener's running totals.
type ListenCounters struct {
	Accepted, Rejected, Refused, RateLimited, Oversize, TLSFailed atomic.Int64
}

// Detail renders the counters for health.
func (c *ListenCounters) Detail() map[string]any {
	return map[string]any{
		"accepted": c.Accepted.Load(), "rejected": c.Rejected.Load(), "refused": c.Refused.Load(),
		"rateLimited": c.RateLimited.Load(), "oversize": c.Oversize.Load(), "tlsFailed": c.TLSFailed.Load(),
	}
}

// BindError explains an address already in use; other errors pass through.
func BindError(network, address string, err error) error {
	if !errors.Is(err, syscall.EADDRINUSE) {
		return err
	}
	_, port, _ := net.SplitHostPort(address)
	return fmt.Errorf("port %s/%s is already in use on the listener host", port, network)
}
