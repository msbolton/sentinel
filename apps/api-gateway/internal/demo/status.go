package demo

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"
)

const (
	// statusTTL is how long a computed status is served before the source is
	// read again, however many visitors are polling.
	statusTTL = 5 * time.Second

	// upWindow is how recent the last entity event must be for the demo to
	// count as up.
	upWindow = 5 * time.Minute
)

// StatusSource is what the status endpoint reads: the number of guests
// connected to the entity stream, the time of the last entity event, and the
// number of live guest accounts.
type StatusSource interface {
	Exploring() int
	LastEventAt() time.Time
	Guests() int
}

type statusResponse struct {
	Up        bool `json:"up"`
	Exploring int  `json:"exploring"`
	Full      bool `json:"full"`
}

// NewStatusHandler serves the public demo status the portfolio site polls:
// {"up","exploring","full"}. It is unauthenticated, so the source is read at
// most once per statusTTL and the response is cacheable for the same time.
// CORS is set here, after the gateway's global middleware, so only origin may
// read it from a browser.
func NewStatusHandler(src StatusSource, maxGuests int, origin string, now func() time.Time) http.Handler {
	var (
		mu       sync.Mutex
		body     []byte
		cachedAt time.Time
	)
	current := func() []byte {
		mu.Lock()
		defer mu.Unlock()
		t := now()
		if body != nil && t.Sub(cachedAt) < statusTTL {
			return body
		}
		last := src.LastEventAt()
		b, _ := json.Marshal(statusResponse{
			Up:        !last.IsZero() && t.Sub(last) <= upWindow,
			Exploring: src.Exploring(),
			Full:      src.Guests() >= maxGuests,
		})
		body, cachedAt = b, t
		return body
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		h := w.Header()
		h.Set("Access-Control-Allow-Origin", origin)
		h.Set("Vary", "Origin")
		h.Set("Cache-Control", "public, max-age=5")
		h.Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if r.Method == http.MethodGet {
			_, _ = w.Write(current())
		}
	})
}
