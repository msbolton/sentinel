package intelsky

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/sentinel/ingest-service/internal/obs"
	"github.com/sentinel/ingest-service/internal/source/driver"
)

type memSink struct {
	mu   sync.Mutex
	obs  []*obs.Observation
	dead []string
}

func (s *memSink) Emit(_ context.Context, o *obs.Observation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.obs = append(s.obs, o)
	return nil
}
func (s *memSink) DeadLetter(_ []byte, reason string, _ error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dead = append(s.dead, reason)
}
func (s *memSink) Skipped(string)        {}
func (s *memSink) Succeeded(int)         {}
func (s *memSink) Blocked(time.Duration) {}

func newDriver(t *testing.T, url string, cfg map[string]any) *Driver {
	t.Helper()
	if cfg == nil {
		cfg = map[string]any{}
	}
	cfg["url"] = url
	raw, _ := json.Marshal(cfg)
	d, err := New(driver.Spec{FeedID: "intelsky", Driver: ID, Config: raw}, driver.Deps{})
	if err != nil {
		t.Fatal(err)
	}
	return d.(*Driver)
}

func fixture(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/live.json")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestPollEmitsEveryAircraft(t *testing.T) {
	body := fixture(t)
	var ua string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ua = r.Header.Get("User-Agent")
		w.Header().Set("Content-Type", "text/html; charset=UTF-8") // as IntelSky sends it
		w.Write(body)
	}))
	defer srv.Close()
	d := newDriver(t, srv.URL, nil)
	sink := &memSink{}
	if wait := d.poll(context.Background(), sink); wait != defaultInterval {
		t.Errorf("wait = %v", wait)
	}
	if len(sink.obs) != 5 || ua != userAgent {
		t.Fatalf("emitted %d, user agent %q", len(sink.obs), ua)
	}
	o := sink.obs[0]
	if o.Source != "INTELSKY" || o.Decoder != ID || o.SourceEntityID != "ICAO-AE7457" || o.CountryOfOrigin != "US" || o.DisplayName != "CNV4062" || o.ADSB.AircraftTypeName != "Boeing C-40A" {
		t.Errorf("observation = %+v %+v", o, o.ADSB)
	}
	if sink.obs[1].CountryOfOrigin != "FR" || sink.obs[2].CountryOfOrigin != "BE" {
		t.Errorf("countries = %s %s", sink.obs[1].CountryOfOrigin, sink.obs[2].CountryOfOrigin)
	}
	h := d.Health()
	if !h.Connected || h.Idle || h.LastError != "" || h.Detail["aircraft"] != 5 || h.Detail["lastStatus"] != 200 {
		t.Errorf("health = %+v", h)
	}
}

func TestIntervalHasAFloor(t *testing.T) {
	if d := newDriver(t, "http://x", map[string]any{"intervalSec": 2}); d.interval != minInterval {
		t.Errorf("interval = %v", d.interval)
	}
	if d := newDriver(t, "http://x", map[string]any{"intervalSec": 30}); d.interval != 30*time.Second {
		t.Errorf("interval = %v", d.interval)
	}
}

func TestFailuresBackOffAndRecover(t *testing.T) {
	var mu sync.Mutex
	status, retryAfter := http.StatusTooManyRequests, "45"
	body := fixture(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if retryAfter != "" {
			w.Header().Set("Retry-After", retryAfter)
		}
		w.WriteHeader(status)
		if status == http.StatusOK {
			w.Write(body)
		} else {
			w.Write([]byte("<html>slow down</html>"))
		}
	}))
	defer srv.Close()
	set := func(s int, ra string) { mu.Lock(); status, retryAfter = s, ra; mu.Unlock() }
	d := newDriver(t, srv.URL, nil)
	sink := &memSink{}

	// Retry-After wins.
	if wait := d.poll(context.Background(), sink); wait != 45*time.Second {
		t.Errorf("429 with Retry-After: wait %v", wait)
	}
	// Without it: backoff doubling from 1 s (the 429 was failure 1, so this
	// starts at 2 s), never under the interval.
	set(http.StatusInternalServerError, "")
	var waits []time.Duration
	for i := 0; i < 6; i++ {
		waits = append(waits, d.poll(context.Background(), sink))
	}
	want := []time.Duration{10 * time.Second, 10 * time.Second, 10 * time.Second, 16 * time.Second, 32 * time.Second, time.Minute}
	for i := range want {
		if waits[i] != want[i] {
			t.Errorf("waits = %v, want %v", waits, want)
			break
		}
	}
	h := d.Health()
	if h.Connected || h.LastError == "" || h.Detail["lastStatus"] != 500 || h.Detail["consecutiveFailures"] != 7 {
		t.Errorf("health while failing = %+v", h)
	}

	set(http.StatusOK, "")
	if wait := d.poll(context.Background(), sink); wait != defaultInterval || len(sink.obs) != 5 {
		t.Errorf("recovery: wait %v, emitted %d", wait, len(sink.obs))
	}
	if h := d.Health(); !h.Connected || h.LastError != "" || h.Detail["consecutiveFailures"] != nil || h.Detail["errors"] != int64(7) {
		t.Errorf("health after recovery = %+v", h)
	}
}

func TestUndecodableBodyIsDeadLettered(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("<!DOCTYPE html><html>maintenance</html>"))
	}))
	defer srv.Close()
	d := newDriver(t, srv.URL, nil)
	sink := &memSink{}
	d.poll(context.Background(), sink)
	if len(sink.dead) != 1 || sink.dead[0] != "bad_response" || len(sink.obs) != 0 {
		t.Errorf("dead %v, emitted %d", sink.dead, len(sink.obs))
	}
}

func TestRunPollsOnTheInterval(t *testing.T) {
	var mu sync.Mutex
	hits := 0
	body := fixture(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits++
		mu.Unlock()
		w.Write(body)
	}))
	defer srv.Close()
	d := newDriver(t, srv.URL, nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); d.Run(ctx, &memSink{}) }()
	time.Sleep(200 * time.Millisecond)
	cancel()
	<-done
	mu.Lock()
	defer mu.Unlock()
	if hits != 1 {
		t.Errorf("polled %d times in 200 ms, want 1 (immediately, then every 10 s)", hits)
	}
	if d.Health().Detail["nextFetchAt"] == nil {
		t.Error("next fetch not reported")
	}
}
