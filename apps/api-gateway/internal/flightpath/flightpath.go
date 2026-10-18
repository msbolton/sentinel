// Package flightpath serves where an aircraft has flown and where it is
// going. Paths come from IntelSky's track endpoints: today's path
// (stats/map.php?ajax_history=1) or a range of days
// (stats/stats.php?ajax_track=1). Routes come from adsbdb, which maps a
// callsign to its scheduled departure and arrival airports; a military
// aircraft with no published route gets the first point IntelSky saw it at
// today. Browsers ask the gateway, never the providers, and the gateway
// caches answers and keeps to a request budget for each.
package flightpath

import (
	"container/list"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
	"golang.org/x/time/rate"

	"github.com/sentinel/go-common/httputil"
)

// DefaultBaseURL is IntelSky.
const DefaultBaseURL = "https://intelsky.org"

const (
	todayTTL        = time.Minute
	rangeTTL        = 10 * time.Minute
	defaultEntries  = 500
	defaultPerMin   = 100 // half of IntelSky's 200 a minute for stats/*.php
	upstreamTimeout = 15 * time.Second
	userAgent       = "Sentinel gateway (flight paths)"
	maxBody         = 8 << 20
)

var hexRe = regexp.MustCompile(`^[0-9A-F]{6}$`)

// Point is one position on the path, oldest first.
type Point struct {
	Lat   float64 `json:"lat"`
	Lon   float64 `json:"lon"`
	AltFt float64 `json:"altFt"`
	TS    int64   `json:"ts"`
}

// Meta is what IntelSky knows about the aircraft over a range of days.
type Meta struct {
	Callsign     string `json:"callsign,omitempty"`
	Registration string `json:"registration,omitempty"`
	Type         string `json:"type,omitempty"`
	Operator     string `json:"operator,omitempty"`
	Description  string `json:"description,omitempty"`
	FirstSeen    string `json:"firstSeen,omitempty"`
	LastSeen     string `json:"lastSeen,omitempty"`
}

// Path is the endpoint's answer.
type Path struct {
	Hex    string  `json:"hex"`
	Source string  `json:"source"`
	Days   int     `json:"days"`
	Points []Point `json:"points"`
	Meta   *Meta   `json:"meta,omitempty"`
}

// Options configure a Handler; zero values take the defaults.
type Options struct {
	BaseURL string
	// RoutesBaseURL is where scheduled routes come from (adsbdb).
	RoutesBaseURL string
	Client        *http.Client
	PerMinute     int
	MaxEntries    int
	Now           func() time.Time
}

type key struct {
	hex  string
	days int
}

type entry struct {
	key     key
	path    *Path
	expires time.Time
}

type call struct {
	done chan struct{}
	path *Path
	err  error
}

// limitedError means IntelSky, or our own budget for it, says wait.
type limitedError struct{ retryAfter time.Duration }

func (e *limitedError) Error() string { return fmt.Sprintf("rate limited; retry in %v", e.retryAfter) }

var errUpstream = errors.New("intelsky unavailable")

// Handler serves GET /api/v1/aircraft/{hex}/path.
type Handler struct {
	base    string
	client  *http.Client
	limiter *rate.Limiter
	perMin  int
	max     int
	now     func() time.Time
	logger  *zap.Logger
	routes  *routes

	mu       sync.Mutex
	lru      *list.List // of *entry, most recent at the front
	entries  map[key]*list.Element
	inflight map[key]*call
}

// NewHandler builds the handler.
func NewHandler(opts Options, logger *zap.Logger) *Handler {
	h := &Handler{
		base: strings.TrimRight(opts.BaseURL, "/"), client: opts.Client, perMin: opts.PerMinute, max: opts.MaxEntries, now: opts.Now, logger: logger,
		lru: list.New(), entries: map[key]*list.Element{}, inflight: map[key]*call{},
	}
	if h.base == "" {
		h.base = DefaultBaseURL
	}
	if h.client == nil {
		h.client = &http.Client{Timeout: upstreamTimeout}
	}
	if h.perMin <= 0 {
		h.perMin = defaultPerMin
	}
	if h.max <= 0 {
		h.max = defaultEntries
	}
	if h.now == nil {
		h.now = time.Now
	}
	if h.logger == nil {
		h.logger = zap.NewNop()
	}
	h.limiter = rate.NewLimiter(rate.Every(time.Minute/time.Duration(h.perMin)), 10)
	h.routes = newRoutes(opts.RoutesBaseURL, h.client, h.now, h.logger)
	return h
}

// RegisterRoutes mounts the endpoint on the protected API mux.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/aircraft/{hex}/path", h.handlePath)
	mux.HandleFunc("GET /api/v1/aircraft/{hex}/route", h.handleRoute)
}

func (h *Handler) handlePath(w http.ResponseWriter, r *http.Request) {
	hex := strings.ToUpper(r.PathValue("hex"))
	if !hexRe.MatchString(hex) {
		httputil.Error(w, http.StatusBadRequest, "hex must be a six-digit ICAO 24-bit address")
		return
	}
	days := 1
	if v := r.URL.Query().Get("days"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 7 {
			httputil.Error(w, http.StatusBadRequest, "days must be 1 to 7")
			return
		}
		days = n
	}
	path, err := h.Get(hex, days)
	var limited *limitedError
	switch {
	case errors.As(err, &limited):
		w.Header().Set("Retry-After", strconv.Itoa(int((limited.retryAfter+time.Second-1)/time.Second)))
		httputil.Error(w, http.StatusServiceUnavailable, "IntelSky is rate limiting; try again shortly")
	case err != nil:
		httputil.Error(w, http.StatusBadGateway, "IntelSky did not answer")
	default:
		httputil.JSON(w, http.StatusOK, path)
	}
}

// Get returns the path, from the cache when fresh. Concurrent misses for
// the same aircraft and range share one request to IntelSky.
func (h *Handler) Get(hex string, days int) (*Path, error) {
	k := key{hex, days}
	h.mu.Lock()
	if el, ok := h.entries[k]; ok {
		e := el.Value.(*entry)
		if h.now().Before(e.expires) {
			h.lru.MoveToFront(el)
			h.mu.Unlock()
			return e.path, nil
		}
	}
	if c, ok := h.inflight[k]; ok {
		h.mu.Unlock()
		<-c.done
		return c.path, c.err
	}
	c := &call{done: make(chan struct{})}
	h.inflight[k] = c
	h.mu.Unlock()

	// Detached from any one request so a caller that gives up does not
	// fail the others waiting on the same answer.
	ctx, cancel := context.WithTimeout(context.Background(), upstreamTimeout)
	c.path, c.err = h.fetch(ctx, k)
	cancel()

	h.mu.Lock()
	delete(h.inflight, k)
	if c.err == nil {
		h.store(k, c.path)
	}
	h.mu.Unlock()
	close(c.done)
	return c.path, c.err
}

// store caches a path; h.mu is held.
func (h *Handler) store(k key, p *Path) {
	ttl := rangeTTL
	if k.days == 1 {
		ttl = todayTTL
	}
	if el, ok := h.entries[k]; ok {
		el.Value = &entry{key: k, path: p, expires: h.now().Add(ttl)}
		h.lru.MoveToFront(el)
		return
	}
	h.entries[k] = h.lru.PushFront(&entry{key: k, path: p, expires: h.now().Add(ttl)})
	for h.lru.Len() > h.max {
		oldest := h.lru.Back()
		h.lru.Remove(oldest)
		delete(h.entries, oldest.Value.(*entry).key)
	}
}

func (h *Handler) fetch(ctx context.Context, k key) (*Path, error) {
	if !h.limiter.Allow() {
		return nil, &limitedError{retryAfter: time.Minute / time.Duration(h.perMin)}
	}
	q := url.Values{"hex": {k.hex}}
	var u string
	if k.days == 1 {
		q.Set("ajax_history", "1")
		u = h.base + "/stats/map.php?" + q.Encode()
	} else {
		today := h.now().UTC()
		q.Set("ajax_track", "1")
		q.Set("date_from", today.AddDate(0, 0, -(k.days-1)).Format("2006-01-02"))
		q.Set("date_to", today.Format("2006-01-02"))
		u = h.base + "/stats/stats.php?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json")
	resp, err := h.client.Do(req)
	if err != nil {
		h.logger.Warn("intelsky path request failed", zap.String("hex", k.hex), zap.Error(err))
		return nil, errUpstream
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusTooManyRequests {
		wait := time.Minute
		if s, err := strconv.Atoi(strings.TrimSpace(resp.Header.Get("Retry-After"))); err == nil && s > 0 {
			wait = time.Duration(s) * time.Second
		}
		h.logger.Warn("intelsky rate limited a path request", zap.String("hex", k.hex), zap.Duration("retry_after", wait))
		return nil, &limitedError{retryAfter: wait}
	}
	if resp.StatusCode != http.StatusOK {
		h.logger.Warn("intelsky path request failed", zap.String("hex", k.hex), zap.Int("status", resp.StatusCode))
		return nil, errUpstream
	}
	var raw json.RawMessage
	if err := json.NewDecoder(http.MaxBytesReader(nil, resp.Body, maxBody)).Decode(&raw); err != nil {
		h.logger.Warn("intelsky path response is not JSON", zap.String("hex", k.hex), zap.Error(err))
		return nil, errUpstream
	}
	p, err := parse(raw)
	if err != nil {
		h.logger.Warn("intelsky path response not understood", zap.String("hex", k.hex), zap.Error(err))
		return nil, errUpstream
	}
	p.Hex, p.Days, p.Source = k.hex, k.days, "INTELSKY"
	return p, nil
}

type rawPoint struct {
	Lat float64 `json:"lat"`
	Lng float64 `json:"lng"`
	Alt float64 `json:"alt"`
	TS  int64   `json:"ts"`
}

type rawMeta struct {
	Callsign     string `json:"callsign"`
	Registration string `json:"registration"`
	Type         string `json:"type"`
	Operator     string `json:"operator"`
	Description  string `json:"description"`
	FirstSeen    string `json:"first_seen"`
	LastSeen     string `json:"last_seen"`
}

// parse reads today's bare array or a range's {points, meta}.
func parse(raw json.RawMessage) (*Path, error) {
	var pts []rawPoint
	var meta *Meta
	trimmed := strings.TrimSpace(string(raw))
	switch {
	case strings.HasPrefix(trimmed, "["):
		if err := json.Unmarshal(raw, &pts); err != nil {
			return nil, err
		}
	case strings.HasPrefix(trimmed, "{"):
		var doc struct {
			Points []rawPoint `json:"points"`
			Meta   *rawMeta   `json:"meta"`
		}
		if err := json.Unmarshal(raw, &doc); err != nil {
			return nil, err
		}
		pts = doc.Points
		if m := doc.Meta; m != nil {
			meta = &Meta{Callsign: m.Callsign, Registration: m.Registration, Type: m.Type, Operator: m.Operator, Description: m.Description, FirstSeen: m.FirstSeen, LastSeen: m.LastSeen}
		}
	default:
		return nil, fmt.Errorf("unexpected body %.40q", trimmed)
	}
	out := make([]Point, 0, len(pts))
	for _, p := range pts {
		if p.TS <= 0 || p.Lat < -90 || p.Lat > 90 || p.Lng < -180 || p.Lng > 180 {
			continue
		}
		out = append(out, Point{Lat: p.Lat, Lon: p.Lng, AltFt: p.Alt, TS: p.TS})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TS < out[j].TS })
	return &Path{Points: out, Meta: meta}, nil
}
