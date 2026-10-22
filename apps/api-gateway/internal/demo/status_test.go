package demo

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/sentinel/go-common/middleware"
)

const portfolioOrigin = "https://msbolton.dev"

type fakeStatusSource struct {
	mu        sync.Mutex
	exploring int
	lastEvent time.Time
	guests    int
	reads     int
}

func (f *fakeStatusSource) Exploring() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads++
	return f.exploring
}

func (f *fakeStatusSource) LastEventAt() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastEvent
}

func (f *fakeStatusSource) Guests() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.guests
}

func (f *fakeStatusSource) readCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.reads
}

type statusBody struct {
	Up        bool `json:"up"`
	Exploring int  `json:"exploring"`
	Full      bool `json:"full"`
}

func getStatus(t *testing.T, h http.Handler) (*httptest.ResponseRecorder, statusBody) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/demo/status", nil))
	var body statusBody
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("body %q: %v", rec.Body.String(), err)
		}
	}
	return rec, body
}

func TestStatusUpWhenRecentEvents(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	src := &fakeStatusSource{exploring: 7, lastEvent: now.Add(-time.Minute), guests: 3}
	h := NewStatusHandler(src, 25, portfolioOrigin, func() time.Time { return now })

	rec, body := getStatus(t, h)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if !body.Up || body.Exploring != 7 || body.Full {
		t.Fatalf("body = %+v", body)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("content type = %q", ct)
	}
	var raw map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil || len(raw) != 3 {
		t.Fatalf("body must be exactly three fields: %v %v", raw, err)
	}
}

func TestStatusDownWhenNoEventsForFiveMinutes(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		last time.Time
		up   bool
	}{
		{"six minutes", now.Add(-6 * time.Minute), false},
		{"exactly five minutes", now.Add(-5 * time.Minute), true},
		{"never", time.Time{}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			src := &fakeStatusSource{lastEvent: tc.last}
			h := NewStatusHandler(src, 25, portfolioOrigin, func() time.Time { return now })
			_, body := getStatus(t, h)
			if body.Up != tc.up {
				t.Fatalf("up = %v, want %v", body.Up, tc.up)
			}
		})
	}
}

func TestStatusFullAtCap(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		guests int
		full   bool
	}{{24, false}, {25, true}, {26, true}} {
		src := &fakeStatusSource{lastEvent: now, guests: tc.guests}
		h := NewStatusHandler(src, 25, portfolioOrigin, func() time.Time { return now })
		_, body := getStatus(t, h)
		if body.Full != tc.full {
			t.Fatalf("guests %d: full = %v, want %v", tc.guests, body.Full, tc.full)
		}
	}
}

func TestStatusCORSOnlyForPortfolio(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	src := &fakeStatusSource{lastEvent: now}
	h := middleware.CORS("https://sentinel.msbolton.dev")(
		NewStatusHandler(src, 25, portfolioOrigin, func() time.Time { return now }))

	rec, _ := getStatus(t, h)
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != portfolioOrigin {
		t.Fatalf("allow origin = %q", got)
	}
	if got := rec.Header().Get("Vary"); got != "Origin" {
		t.Fatalf("vary = %q", got)
	}
}

func TestStatusCachesFiveSeconds(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	src := &fakeStatusSource{exploring: 1, lastEvent: now}
	h := NewStatusHandler(src, 25, portfolioOrigin, func() time.Time { return now })

	rec, _ := getStatus(t, h)
	if cc := rec.Header().Get("Cache-Control"); cc != "public, max-age=5" {
		t.Fatalf("cache-control = %q", cc)
	}
	now = now.Add(4 * time.Second)
	src.mu.Lock()
	src.exploring = 9
	src.mu.Unlock()
	_, body := getStatus(t, h)
	if src.readCount() != 1 || body.Exploring != 1 {
		t.Fatalf("reads = %d exploring = %d, want cached", src.readCount(), body.Exploring)
	}

	now = now.Add(2 * time.Second)
	_, body = getStatus(t, h)
	if src.readCount() != 2 || body.Exploring != 9 {
		t.Fatalf("reads = %d exploring = %d, want refreshed", src.readCount(), body.Exploring)
	}
}

func TestStatusConcurrentRequestsReadOnce(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	src := &fakeStatusSource{lastEvent: now}
	h := NewStatusHandler(src, 25, portfolioOrigin, func() time.Time { return now })

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			getStatus(t, h)
		}()
	}
	wg.Wait()
	if n := src.readCount(); n != 1 {
		t.Fatalf("source read %d times", n)
	}
}

func TestStatusMethods(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	h := NewStatusHandler(&fakeStatusSource{lastEvent: now}, 25, portfolioOrigin, func() time.Time { return now })

	for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(m, "/api/v1/demo/status", nil))
		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("%s = %d", m, rec.Code)
		}
		if allow := rec.Header().Get("Allow"); allow != "GET, HEAD" {
			t.Fatalf("%s allow = %q", m, allow)
		}
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodHead, "/api/v1/demo/status", nil))
	if rec.Code != http.StatusOK || rec.Body.Len() != 0 {
		t.Fatalf("HEAD = %d body %q", rec.Code, rec.Body.String())
	}
}
