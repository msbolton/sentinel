package flightpath

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
	"golang.org/x/time/rate"

	"github.com/sentinel/go-common/httputil"
)

// DefaultRoutesBaseURL is adsbdb, which maps a callsign to its scheduled
// route (departure and arrival airports with coordinates). No key.
const DefaultRoutesBaseURL = "https://api.adsbdb.com"

const (
	routeTTL        = 6 * time.Hour
	unknownRouteTTL = time.Hour
	routesPerMinute = 30
	maxRoutes       = 2000
)

var callsignRe = regexp.MustCompile(`^[A-Z0-9]{2,8}$`)

// Place is a departure or arrival: an airport, or for a military aircraft
// with no published route the first point IntelSky saw today.
type Place struct {
	ICAO         string  `json:"icao,omitempty"`
	IATA         string  `json:"iata,omitempty"`
	Name         string  `json:"name"`
	Municipality string  `json:"municipality,omitempty"`
	Country      string  `json:"country,omitempty"`
	Lat          float64 `json:"lat"`
	Lon          float64 `json:"lon"`
	Time         string  `json:"time,omitempty"` // RFC 3339, for a first-seen point
}

// Airline operates a scheduled route.
type Airline struct {
	Name    string `json:"name"`
	ICAO    string `json:"icao,omitempty"`
	IATA    string `json:"iata,omitempty"`
	Country string `json:"country,omitempty"`
}

// Route is the route endpoint's answer. Provider is "adsbdb" for a
// scheduled route by callsign, "intelsky" for a first-seen point, or empty
// when nothing is known.
type Route struct {
	Hex       string   `json:"hex"`
	Callsign  string   `json:"callsign,omitempty"`
	Provider  string   `json:"provider,omitempty"`
	Airline   *Airline `json:"airline,omitempty"`
	Departure *Place   `json:"departure,omitempty"`
	Arrival   *Place   `json:"arrival,omitempty"`
}

type routeEntry struct {
	route   *Route // nil: adsbdb does not know the callsign
	expires time.Time
}

// routes looks up and caches scheduled routes by callsign.
type routes struct {
	base    string
	client  *http.Client
	limiter *rate.Limiter
	now     func() time.Time
	logger  *zap.Logger

	mu    sync.Mutex
	cache map[string]routeEntry
	order []string // insertion order, for eviction
}

func newRoutes(base string, client *http.Client, now func() time.Time, logger *zap.Logger) *routes {
	if base == "" {
		base = DefaultRoutesBaseURL
	}
	return &routes{
		base: strings.TrimRight(base, "/"), client: client, now: now, logger: logger,
		limiter: rate.NewLimiter(rate.Every(time.Minute/routesPerMinute), 5),
		cache:   map[string]routeEntry{},
	}
}

// lookup returns the scheduled route for a callsign, nil when adsbdb does
// not know it.
func (r *routes) lookup(ctx context.Context, callsign string) (*Route, error) {
	r.mu.Lock()
	if e, ok := r.cache[callsign]; ok && r.now().Before(e.expires) {
		r.mu.Unlock()
		return e.route, nil
	}
	r.mu.Unlock()
	if !r.limiter.Allow() {
		return nil, &limitedError{retryAfter: time.Minute / routesPerMinute}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.base+"/v0/callsign/"+url.PathEscape(callsign), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := r.client.Do(req)
	if err != nil {
		r.logger.Warn("adsbdb route request failed", zap.String("callsign", callsign), zap.Error(err))
		return nil, errUpstream
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		r.store(callsign, nil, unknownRouteTTL)
		return nil, nil
	case http.StatusTooManyRequests:
		wait := time.Minute
		if s, err := strconv.Atoi(strings.TrimSpace(resp.Header.Get("Retry-After"))); err == nil && s > 0 {
			wait = time.Duration(s) * time.Second
		}
		return nil, &limitedError{retryAfter: wait}
	default:
		r.logger.Warn("adsbdb route request failed", zap.String("callsign", callsign), zap.Int("status", resp.StatusCode))
		return nil, errUpstream
	}
	route, err := parseAdsbdb(http.MaxBytesReader(nil, resp.Body, 1<<20))
	if err != nil {
		r.logger.Warn("adsbdb answer not understood", zap.String("callsign", callsign), zap.Error(err))
		return nil, errUpstream
	}
	ttl := routeTTL
	if route == nil {
		ttl = unknownRouteTTL
	}
	r.store(callsign, route, ttl)
	return route, nil
}

func (r *routes) store(callsign string, route *Route, ttl time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.cache[callsign]; !ok {
		r.order = append(r.order, callsign)
	}
	r.cache[callsign] = routeEntry{route: route, expires: r.now().Add(ttl)}
	for len(r.order) > maxRoutes {
		delete(r.cache, r.order[0])
		r.order = r.order[1:]
	}
}

type adsbdbAirport struct {
	ICAO         string  `json:"icao_code"`
	IATA         string  `json:"iata_code"`
	Name         string  `json:"name"`
	Municipality string  `json:"municipality"`
	Country      string  `json:"country_name"`
	Lat          float64 `json:"latitude"`
	Lon          float64 `json:"longitude"`
}

func (a *adsbdbAirport) place() *Place {
	if a == nil || (a.Lat == 0 && a.Lon == 0) {
		return nil
	}
	return &Place{ICAO: a.ICAO, IATA: a.IATA, Name: a.Name, Municipality: a.Municipality, Country: a.Country, Lat: a.Lat, Lon: a.Lon}
}

// parseAdsbdb reads {"response": {"flightroute": {...}}}; a string
// response ("unknown callsign") means no route.
func parseAdsbdb(body interface{ Read([]byte) (int, error) }) (*Route, error) {
	var doc struct {
		Response json.RawMessage `json:"response"`
	}
	if err := json.NewDecoder(body).Decode(&doc); err != nil {
		return nil, err
	}
	if len(doc.Response) == 0 || doc.Response[0] == '"' {
		return nil, nil
	}
	var resp struct {
		FlightRoute *struct {
			Callsign string `json:"callsign"`
			Airline  *struct {
				Name    string `json:"name"`
				ICAO    string `json:"icao"`
				IATA    string `json:"iata"`
				Country string `json:"country"`
			} `json:"airline"`
			Origin      *adsbdbAirport `json:"origin"`
			Destination *adsbdbAirport `json:"destination"`
		} `json:"flightroute"`
	}
	if err := json.Unmarshal(doc.Response, &resp); err != nil {
		return nil, err
	}
	fr := resp.FlightRoute
	if fr == nil {
		return nil, nil
	}
	route := &Route{Callsign: fr.Callsign, Provider: "adsbdb", Departure: fr.Origin.place(), Arrival: fr.Destination.place()}
	if a := fr.Airline; a != nil && a.Name != "" {
		route.Airline = &Airline{Name: a.Name, ICAO: a.ICAO, IATA: a.IATA, Country: a.Country}
	}
	if route.Departure == nil && route.Arrival == nil {
		return nil, nil
	}
	return route, nil
}

// handleRoute serves GET /api/v1/aircraft/{hex}/route?callsign=&source=:
// the scheduled route for the callsign, or for an IntelSky aircraft with
// none the first point IntelSky saw it at today.
func (h *Handler) handleRoute(w http.ResponseWriter, r *http.Request) {
	hex := strings.ToUpper(r.PathValue("hex"))
	if !hexRe.MatchString(hex) {
		httputil.Error(w, http.StatusBadRequest, "hex must be a six-digit ICAO 24-bit address")
		return
	}
	callsign := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("callsign")))
	if callsign != "" && !callsignRe.MatchString(callsign) {
		httputil.Error(w, http.StatusBadRequest, "callsign must be 2 to 8 letters and digits")
		return
	}
	out := &Route{Hex: hex, Callsign: callsign}
	if callsign != "" {
		route, err := h.routes.lookup(r.Context(), callsign)
		var limited *limitedError
		switch {
		case errors.As(err, &limited):
			w.Header().Set("Retry-After", strconv.Itoa(int((limited.retryAfter+time.Second-1)/time.Second)))
			httputil.Error(w, http.StatusServiceUnavailable, "route lookups are rate limited; try again shortly")
			return
		case err != nil:
			httputil.Error(w, http.StatusBadGateway, "route lookup failed")
			return
		case route != nil:
			out.Provider, out.Airline, out.Departure, out.Arrival = route.Provider, route.Airline, route.Departure, route.Arrival
		}
	}
	if out.Departure == nil && strings.EqualFold(r.URL.Query().Get("source"), "INTELSKY") {
		// Military flights publish no route: where IntelSky first saw the
		// aircraft today stands in for the departure.
		if path, err := h.Get(hex, 1); err == nil && len(path.Points) > 0 {
			p := path.Points[0]
			out.Provider = "intelsky"
			out.Departure = &Place{Name: "First seen today", Lat: p.Lat, Lon: p.Lon, Time: time.Unix(p.TS, 0).UTC().Format(time.RFC3339)}
		}
	}
	httputil.JSON(w, http.StatusOK, out)
}
