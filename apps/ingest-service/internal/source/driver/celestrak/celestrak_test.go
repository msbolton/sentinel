package celestrak

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/sentinel/ingest-service/internal/obs"
	"github.com/sentinel/ingest-service/internal/source/driver"
)

const tleBlock = "ISS (ZARYA)\n1 25544U 98067A   26063.86671769  .00009014  00000+0  17477-3 0  9999\n2 25544  51.6315  96.2009 0008177 157.5272 202.6076 15.48447334555585\n" +
	"GOES 16\n1 41866U 16071A   26063.50000000  .00000100  00000+0  00000+0 0  9995\n2 41866   0.0569 271.4567 0000345 123.4567 236.7890  1.00271400 34567\n"

type memSink struct {
	mu      sync.Mutex
	obs     []*obs.Observation
	success int
}

func (s *memSink) Emit(_ context.Context, o *obs.Observation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.obs = append(s.obs, o)
	return nil
}
func (s *memSink) DeadLetter([]byte, string, error) {}
func (s *memSink) Skipped(string)                   {}
func (s *memSink) Succeeded(int)                    { s.mu.Lock(); s.success++; s.mu.Unlock() }
func (s *memSink) Blocked(time.Duration)            {}
func (s *memSink) count() int                       { s.mu.Lock(); defer s.mu.Unlock(); return len(s.obs) }

func waitFor(cond func() bool, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return cond()
}

func TestFetchesThenPropagates(t *testing.T) {
	var hits atomic.Int32
	var groups []string
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		groups = append(groups, r.URL.Query().Get("GROUP"))
		mu.Unlock()
		if hits.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable) // first fetch fails: retried with backoff
			return
		}
		_, _ = w.Write([]byte(tleBlock))
	}))
	defer srv.Close()
	d, err := New(driver.Spec{FeedID: "celestrak", Config: json.RawMessage(`{"groups":["stations"],"propagationIntervalSec":1,"baseUrl":"` + srv.URL + `"}`)}, driver.Deps{Logger: zap.NewNop()})
	if err != nil {
		t.Fatal(err)
	}
	drv := d.(*Driver)
	drv.step = 30 * time.Millisecond
	sink := &memSink{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = drv.Run(ctx, sink) }()
	defer func() { cancel(); <-done }()

	if !waitFor(func() bool { return sink.count() >= 4 }, 5*time.Second) {
		t.Fatalf("expected two propagation rounds of two satellites, got %d (fetch hits %d)", sink.count(), hits.Load())
	}
	if hits.Load() < 2 {
		t.Errorf("fetch should have been retried after the 503: %d", hits.Load())
	}
	mu.Lock()
	g := groups[0]
	mu.Unlock()
	if g != "stations" {
		t.Errorf("group = %q", g)
	}
	o := sink.obs[0]
	if o.Source != "CELESTRAK" || o.Decoder != "tle-sgp4" || o.EntityType != obs.EntitySatellite || !o.HasPosition || o.TLE == nil {
		t.Errorf("observation = %+v", o)
	}
	h := drv.Health()
	if !h.Connected || h.Detail["satellites"] != 2 || h.LastError != "" {
		t.Errorf("health = %+v", h)
	}
}
