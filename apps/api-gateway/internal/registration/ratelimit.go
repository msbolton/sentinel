package registration

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"github.com/sentinel/go-common/httputil"
)

// IPLimiter allows `requests` per `window` per client IP (token bucket with
// burst equal to the window allowance), mirroring the NestJS throttler.
type IPLimiter struct {
	limit  rate.Limit
	burst  int
	window time.Duration
	now    func() time.Time

	mu      sync.Mutex
	clients map[string]*ipEntry
}

type ipEntry struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// NewIPLimiter creates a limiter allowing `requests` per `window` per IP.
func NewIPLimiter(requests int, window time.Duration) *IPLimiter {
	return &IPLimiter{
		limit:   rate.Every(window / time.Duration(requests)),
		burst:   requests,
		window:  window,
		now:     time.Now,
		clients: map[string]*ipEntry{},
	}
}

// Allow reports whether a request from ip may proceed.
func (l *IPLimiter) Allow(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	for k, e := range l.clients {
		if now.Sub(e.lastSeen) > l.window {
			delete(l.clients, k)
		}
	}
	e, found := l.clients[ip]
	if !found {
		e = &ipEntry{limiter: rate.NewLimiter(l.limit, l.burst)}
		l.clients[ip] = e
	}
	e.lastSeen = now
	return e.limiter.AllowN(now, 1)
}

// Middleware rejects over-limit requests with 429.
func (l *IPLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !l.Allow(clientIP(r)) {
			httputil.Error(w, http.StatusTooManyRequests, "ThrottlerException: Too Many Requests")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		return strings.TrimSpace(strings.Split(xff, ",")[0])
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
