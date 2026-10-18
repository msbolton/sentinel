package flightpath

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fake struct {
	srv      *httptest.Server
	hits     atomic.Int32
	mu       sync.Mutex
	status   int
	body     []byte
	header   http.Header
	lastPath string
	lastQ    map[string]string
	gate     chan struct{} // when set, requests wait for it
}

func newFake(t *testing.T) *fake {
	f := &fake{status: http.StatusOK}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.hits.Add(1)
		f.mu.Lock()
		gate, status, body := f.gate, f.status, f.body
		f.lastPath = r.URL.Path
		f.lastQ = map[string]string{}
		for k := range r.URL.Query() {
			f.lastQ[k] = r.URL.Query().Get(k)
		}
		for k, v := range f.header {
			w.Header()[k] = v
		}
		f.mu.Unlock()
		if gate != nil {
			<-gate
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		w.Write(body)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fake) set(status int, body []byte, header http.Header) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.status, f.body, f.header = status, body, header
}

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

var t0 = time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)

func serve(h *Handler, path string) *httptest.ResponseRecorder {
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func decode(t *testing.T, rec *httptest.ResponseRecorder) Path {
	t.Helper()
	var p Path
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatalf("body %s: %v", rec.Body, err)
	}
	return p
}

func TestValidation(t *testing.T) {
	f := newFake(t)
	h := NewHandler(Options{BaseURL: f.srv.URL}, nil)
	for _, path := range []string{
		"/api/v1/aircraft/zzz/path", "/api/v1/aircraft/AE745/path", "/api/v1/aircraft/AE74570/path",
		"/api/v1/aircraft/AE7457/path?days=0", "/api/v1/aircraft/AE7457/path?days=8", "/api/v1/aircraft/AE7457/path?days=x",
	} {
		if rec := serve(h, path); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d", path, rec.Code)
		}
	}
	if f.hits.Load() != 0 {
		t.Error("invalid requests must not reach IntelSky")
	}
}

func TestTodaysPath(t *testing.T) {
	f := newFake(t)
	f.set(http.StatusOK, fixture(t, "today.json"), nil)
	h := NewHandler(Options{BaseURL: f.srv.URL, Now: func() time.Time { return t0 }}, nil)
	rec := serve(h, "/api/v1/aircraft/ae7457/path")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if f.lastPath != "/stats/map.php" || f.lastQ["ajax_history"] != "1" || f.lastQ["hex"] != "AE7457" {
		t.Errorf("upstream request %s %v", f.lastPath, f.lastQ)
	}
	p := decode(t, rec)
	if p.Hex != "AE7457" || p.Source != "INTELSKY" || p.Days != 1 || p.Meta != nil || len(p.Points) != 7 {
		t.Fatalf("path = %+v", p)
	}
	first := p.Points[0]
	if first.Lat != 26.3615 || first.Lon != 127.7742 || first.AltFt != 0 || first.TS != 1792281604 {
		t.Errorf("first point = %+v", first)
	}
	for i := 1; i < len(p.Points); i++ {
		if p.Points[i].TS < p.Points[i-1].TS {
			t.Fatal("points must be oldest first")
		}
	}
}

func TestRangeOfDays(t *testing.T) {
	f := newFake(t)
	f.set(http.StatusOK, fixture(t, "range.json"), nil)
	h := NewHandler(Options{BaseURL: f.srv.URL, Now: func() time.Time { return t0 }}, nil)
	rec := serve(h, "/api/v1/aircraft/AE7457/path?days=3")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if f.lastPath != "/stats/stats.php" || f.lastQ["ajax_track"] != "1" || f.lastQ["date_from"] != "2026-10-16" || f.lastQ["date_to"] != "2026-10-18" {
		t.Errorf("upstream request %s %v", f.lastPath, f.lastQ)
	}
	p := decode(t, rec)
	if p.Days != 3 || len(p.Points) != 6 || p.Meta == nil || p.Meta.Operator != "US Air Force" || p.Meta.Callsign != "CNV4062" || p.Meta.FirstSeen != "2026-10-15 23:40:04" {
		t.Errorf("path = %+v meta %+v", p, p.Meta)
	}
}

func TestCacheAndExpiry(t *testing.T) {
	f := newFake(t)
	f.set(http.StatusOK, fixture(t, "today.json"), nil)
	now := t0
	h := NewHandler(Options{BaseURL: f.srv.URL, Now: func() time.Time { return now }}, nil)
	for i := 0; i < 3; i++ {
		if _, err := h.Get("AE7457", 1); err != nil {
			t.Fatal(err)
		}
	}
	if f.hits.Load() != 1 {
		t.Fatalf("hits = %d, want 1", f.hits.Load())
	}
	now = now.Add(61 * time.Second) // today's path lives a minute
	h.Get("AE7457", 1)
	h.Get("AE7457", 3) // a different range is a different entry
	if f.hits.Load() != 3 {
		t.Errorf("hits = %d, want 3", f.hits.Load())
	}
}

func TestConcurrentMissesShareOneRequest(t *testing.T) {
	f := newFake(t)
	f.set(http.StatusOK, fixture(t, "today.json"), nil)
	f.gate = make(chan struct{})
	h := NewHandler(Options{BaseURL: f.srv.URL}, nil)
	var wg sync.WaitGroup
	errs := make(chan error, 5)
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p, err := h.Get("AE7457", 1)
			if err == nil && len(p.Points) != 7 {
				err = errUpstream
			}
			errs <- err
		}()
	}
	for f.hits.Load() == 0 {
		time.Sleep(time.Millisecond)
	}
	time.Sleep(20 * time.Millisecond)
	close(f.gate)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Error(err)
		}
	}
	if f.hits.Load() != 1 {
		t.Errorf("hits = %d, want 1", f.hits.Load())
	}
}

func TestUpstreamFailures(t *testing.T) {
	f := newFake(t)
	h := NewHandler(Options{BaseURL: f.srv.URL}, nil)

	f.set(http.StatusTooManyRequests, []byte("slow down"), http.Header{"Retry-After": {"30"}})
	rec := serve(h, "/api/v1/aircraft/AE0001/path")
	if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") != "30" {
		t.Errorf("429: status %d retry %q", rec.Code, rec.Header().Get("Retry-After"))
	}
	f.set(http.StatusInternalServerError, []byte("oops"), nil)
	if rec := serve(h, "/api/v1/aircraft/AE0002/path"); rec.Code != http.StatusBadGateway {
		t.Errorf("500: status %d", rec.Code)
	}
	f.set(http.StatusOK, []byte("<!DOCTYPE html><html></html>"), nil)
	if rec := serve(h, "/api/v1/aircraft/AE0003/path"); rec.Code != http.StatusBadGateway {
		t.Errorf("html: status %d", rec.Code)
	}
	f.set(http.StatusOK, []byte("[]"), nil)
	rec = serve(h, "/api/v1/aircraft/AE0004/path")
	if p := decode(t, rec); rec.Code != http.StatusOK || p.Points == nil || len(p.Points) != 0 {
		t.Errorf("empty: status %d body %s", rec.Code, rec.Body)
	}
	// Failures are not cached.
	before := f.hits.Load()
	serve(h, "/api/v1/aircraft/AE0002/path")
	if f.hits.Load() != before+1 {
		t.Error("a failed lookup must be retried, not cached")
	}
}

func TestOwnBudgetStopsBeforeIntelSkyDoes(t *testing.T) {
	f := newFake(t)
	f.set(http.StatusOK, []byte("[]"), nil)
	h := NewHandler(Options{BaseURL: f.srv.URL, PerMinute: 6}, nil)
	codes := map[int]int{}
	hexes := []string{"A00001", "A00002", "A00003", "A00004", "A00005", "A00006", "A00007", "A00008", "A00009", "A0000A", "A0000B"}
	for _, hex := range hexes {
		codes[serve(h, "/api/v1/aircraft/"+hex+"/path").Code]++
	}
	if codes[http.StatusOK] != 10 || codes[http.StatusServiceUnavailable] != 1 || f.hits.Load() != 10 {
		t.Errorf("codes %v, hits %d (burst 10, then the budget)", codes, f.hits.Load())
	}
}

func jsonUnmarshal(b []byte, v any) error { return json.Unmarshal(b, v) }

func stringReader(s string) *strings.Reader { return strings.NewReader(s) }
