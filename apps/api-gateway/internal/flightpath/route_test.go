package flightpath

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// providers stubs adsbdb and IntelSky on one server.
func providers(t *testing.T, routeStatus int, routeBody []byte, todayBody []byte) (*httptest.Server, *atomic.Int32, *atomic.Int32) {
	t.Helper()
	var routeHits, pathHits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v0/callsign/BAW123", "/v0/callsign/ZZZ9999", "/v0/callsign/RCH678":
			routeHits.Add(1)
			w.WriteHeader(routeStatus)
			w.Write(routeBody)
		case "/stats/map.php":
			pathHits.Add(1)
			w.Write(todayBody)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &routeHits, &pathHits
}

func routeOf(t *testing.T, h *Handler, path string) (*httptest.ResponseRecorder, Route) {
	t.Helper()
	rec := serve(h, path)
	var r Route
	if rec.Code == http.StatusOK {
		if err := jsonUnmarshal(rec.Body.Bytes(), &r); err != nil {
			t.Fatal(err)
		}
	}
	return rec, r
}

func TestScheduledRouteByCallsign(t *testing.T) {
	srv, hits, _ := providers(t, http.StatusOK, fixture(t, "adsbdb_BAW123.json"), nil)
	h := NewHandler(Options{BaseURL: srv.URL, RoutesBaseURL: srv.URL}, nil)
	rec, r := routeOf(t, h, "/api/v1/aircraft/400abc/route?callsign=baw123")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if r.Hex != "400ABC" || r.Callsign != "BAW123" || r.Provider != "adsbdb" || r.Airline == nil || r.Airline.Name != "British Airways" {
		t.Errorf("route = %+v", r)
	}
	if d := r.Departure; d == nil || d.ICAO != "EGLL" || d.IATA != "LHR" || d.Lat != 51.4706 || d.Name != "London Heathrow Airport" {
		t.Errorf("departure = %+v", d)
	}
	if a := r.Arrival; a == nil || a.ICAO != "OTHH" || a.Municipality != "Doha" {
		t.Errorf("arrival = %+v", a)
	}
	routeOf(t, h, "/api/v1/aircraft/400abc/route?callsign=BAW123")
	if hits.Load() != 1 {
		t.Errorf("adsbdb hits = %d, want the second answer cached", hits.Load())
	}
}

func TestUnknownCallsignAndIntelSkyFallback(t *testing.T) {
	srv, routeHits, pathHits := providers(t, http.StatusNotFound, []byte(`{"response":"unknown callsign"}`), fixture(t, "today.json"))
	h := NewHandler(Options{BaseURL: srv.URL, RoutesBaseURL: srv.URL}, nil)

	// An airliner adsbdb does not know: no route, and no IntelSky lookup.
	rec, r := routeOf(t, h, "/api/v1/aircraft/400abc/route?callsign=ZZZ9999")
	if rec.Code != http.StatusOK || r.Departure != nil || r.Provider != "" || pathHits.Load() != 0 {
		t.Errorf("unknown: %d %+v (path hits %d)", rec.Code, r, pathHits.Load())
	}
	routeOf(t, h, "/api/v1/aircraft/400abc/route?callsign=ZZZ9999")
	if routeHits.Load() != 1 {
		t.Errorf("unknown answers must be cached too: %d hits", routeHits.Load())
	}

	// A military aircraft: the first point IntelSky saw today.
	rec, r = routeOf(t, h, "/api/v1/aircraft/AE2FA8/route?callsign=RCH678&source=INTELSKY")
	if rec.Code != http.StatusOK || r.Provider != "intelsky" || r.Departure == nil || r.Departure.Name != "First seen today" || r.Departure.Lat != 26.3615 || r.Arrival != nil {
		t.Fatalf("fallback: %d %+v", rec.Code, r)
	}
	if want := time.Unix(1792281604, 0).UTC().Format(time.RFC3339); r.Departure.Time != want {
		t.Errorf("time = %q, want %q", r.Departure.Time, want)
	}
	// No callsign at all still gets the fallback.
	if _, r := routeOf(t, h, "/api/v1/aircraft/AE2FA8/route?source=INTELSKY"); r.Provider != "intelsky" {
		t.Errorf("no callsign: %+v", r)
	}
}

func TestRouteValidationAndFailures(t *testing.T) {
	srv, _, _ := providers(t, http.StatusInternalServerError, []byte("oops"), nil)
	h := NewHandler(Options{BaseURL: srv.URL, RoutesBaseURL: srv.URL}, nil)
	for _, path := range []string{"/api/v1/aircraft/zzz/route", "/api/v1/aircraft/400ABC/route?callsign=BA-123", "/api/v1/aircraft/400ABC/route?callsign=X"} {
		if rec := serve(h, path); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d", path, rec.Code)
		}
	}
	if rec := serve(h, "/api/v1/aircraft/400ABC/route?callsign=BAW123"); rec.Code != http.StatusBadGateway {
		t.Errorf("adsbdb failure: %d", rec.Code)
	}
}

func TestParseAdsbdb(t *testing.T) {
	if r, err := parseAdsbdb(stringReader(`{"response":"unknown callsign"}`)); r != nil || err != nil {
		t.Errorf("unknown: %+v %v", r, err)
	}
	if r, err := parseAdsbdb(stringReader(`{"response":{"flightroute":{"callsign":"X1","origin":null,"destination":null}}}`)); r != nil || err != nil {
		t.Errorf("no airports: %+v %v", r, err)
	}
	if _, err := parseAdsbdb(stringReader(`not json`)); err == nil {
		t.Error("garbage must be an error")
	}
}
