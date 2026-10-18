package opensky

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/sentinel/ingest-service/internal/aoi"
	"github.com/sentinel/ingest-service/internal/obs"
	"github.com/sentinel/ingest-service/internal/source/decoder/openskystates"
	"github.com/sentinel/ingest-service/internal/source/driver"
	"github.com/sentinel/ingest-service/internal/source/httppoll"
)

type memSink struct {
	mu      sync.Mutex
	obs     []*obs.Observation
	success []int
}

func (s *memSink) Emit(_ context.Context, o *obs.Observation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.obs = append(s.obs, o)
	return nil
}
func (s *memSink) DeadLetter([]byte, string, error) {}
func (s *memSink) Skipped(string)                   {}
func (s *memSink) Succeeded(n int)                  { s.mu.Lock(); s.success = append(s.success, n); s.mu.Unlock() }
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

// fixtureServer serves the 3-aircraft fixture and records request URLs.
func fixtureServer(t *testing.T) (*httptest.Server, *atomic.Int32, func() []string, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	var remaining atomic.Int32
	remaining.Store(-1)
	var mu sync.Mutex
	var urls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		urls = append(urls, r.URL.String())
		mu.Unlock()
		hits.Add(1)
		if n := remaining.Load(); n >= 0 {
			w.Header().Set("X-Rate-Limit-Remaining", strconv.Itoa(int(n)))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(fixture))
	}))
	t.Cleanup(srv.Close)
	return srv, &hits, func() []string { mu.Lock(); defer mu.Unlock(); return append([]string(nil), urls...) }, &remaining
}

func build(t *testing.T, srv *httptest.Server, cfg string) *Driver {
	t.Helper()
	var c map[string]any
	_ = json.Unmarshal([]byte(cfg), &c)
	if c == nil {
		c = map[string]any{}
	}
	c["baseUrl"] = srv.URL
	if _, set := c["backgroundIntervalSec"]; !set {
		c["backgroundIntervalSec"] = 0 // the tests below count viewport requests
	}
	raw, _ := json.Marshal(c)
	d, err := New(driver.Spec{FeedID: "opensky", Driver: ID, Config: raw}, driver.Deps{Logger: zap.NewNop()})
	if err != nil {
		t.Fatal(err)
	}
	return d.(*Driver)
}

func start(t *testing.T, d *Driver) *memSink {
	t.Helper()
	sink := &memSink{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = d.Run(ctx, sink) }()
	t.Cleanup(func() { cancel(); <-done })
	return sink
}

func newDriver(t *testing.T, srv *httptest.Server, cfg string) (*Driver, *memSink, context.CancelFunc) {
	t.Helper()
	d := build(t, srv, cfg)
	return d, start(t, d), func() {}
}

var box = aoi.BBox{South: 40, West: -90, North: 45, East: -80}

const fixture = `{"time": 1700000000, "states": [
    ["ab1234", "UAL123  ", "United States", 1700000000, 1700000000, -87.9045, 41.9742, 10972.8, false, 230.5, 45.0, 0.0, null, 11277.6, "1234", false, 0],
    ["cd5678", "DAL456  ", "United States", 1700000000, 1700000000, -73.7781, 40.6413, null, false, 150.2, 270.3, 0.0, null, 5486.4, "5678", false, 0],
    ["ef9012", "        ", "Germany", 1699999990, 1699999990, 13.4050, 52.5200, 3048.0, false, null, 180.0, 0.0, null, null, "9012", false, 0]
]}`

func TestIdleInViewportMode(t *testing.T) {
	srv, hits, _, _ := fixtureServer(t)
	d, _, _ := newDriver(t, srv, `{"intervalSec":1}`)
	time.Sleep(150 * time.Millisecond)
	if hits.Load() != 0 || !d.AreaStatus().Idle || !d.Health().Idle {
		t.Fatalf("expected no polls without a viewport, got %d idle=%v", hits.Load(), d.AreaStatus().Idle)
	}
}

func TestAreaWakesPollsAndDedupesAcrossBoxes(t *testing.T) {
	srv, hits, urls, _ := fixtureServer(t)
	d, sink, _ := newDriver(t, srv, `{"intervalSec":3600,"minPollSpacingSec":0}`)
	st := d.SetAreaOfInterest(aoi.Area{Active: true, Clients: 1, Boxes: []aoi.BBox{
		{South: 10, West: 170, North: 20, East: 180},
		{South: 10, West: -180, North: 20, East: -170},
	}, ExpiresAt: time.Now().Add(time.Minute)})
	if st.Idle || st.AreaSqDeg != 200 {
		t.Errorf("status = %+v", st)
	}
	if !waitFor(func() bool { return hits.Load() == 2 }, 2*time.Second) {
		t.Fatalf("expected one request per box, got %d", hits.Load())
	}
	want := []string{"/?lamin=10.0000&lomin=170.0000&lamax=20.0000&lomax=180.0000", "/?lamin=10.0000&lomin=-180.0000&lamax=20.0000&lomax=-170.0000"}
	got := urls()
	for i, u := range want {
		if got[i] != u {
			t.Errorf("request %d = %q, want %q", i, got[i], u)
		}
	}
	if !waitFor(func() bool { return sink.count() == 3 }, time.Second) {
		t.Fatalf("expected 3 deduplicated observations, got %d", sink.count())
	}
	if o := sink.obs[0]; o.Source != "OPENSKY" || o.Decoder != openskystates.Name || o.SourceEntityID != "ICAO-AB1234" {
		t.Errorf("observation = %+v", o)
	}
	time.Sleep(100 * time.Millisecond)
	if hits.Load() != 2 {
		t.Errorf("hour-long interval: no further polls expected, got %d", hits.Load())
	}
	// Heartbeat with the same boxes: no poll.
	d.SetAreaOfInterest(aoi.Area{Active: true, Clients: 2, Boxes: []aoi.BBox{{South: 10, West: 170, North: 20, East: 180}, {South: 10, West: -180, North: 20, East: -170}}, ExpiresAt: time.Now().Add(time.Minute)})
	time.Sleep(50 * time.Millisecond)
	if hits.Load() != 2 {
		t.Errorf("heartbeat must not poll, got %d", hits.Load())
	}
	if st := d.SetAreaOfInterest(aoi.Area{Active: false}); !st.Idle {
		t.Errorf("inactive area should idle: %+v", st)
	}
	if len(sink.success) == 0 || sink.success[0] != 3 {
		t.Errorf("success counts = %v", sink.success)
	}
}

func TestLeaseExpiryIdles(t *testing.T) {
	srv, hits, _, _ := fixtureServer(t)
	d := build(t, srv, `{"intervalSec":1,"minPollSpacingSec":0}`)
	d.sched = httppoll.NewScheduler(httppoll.Options{Interval: 10 * time.Millisecond, MinSpacing: 0, AreaRequired: true}, d.poll)
	start(t, d)
	d.SetAreaOfInterest(aoi.Area{Active: true, Boxes: []aoi.BBox{box}, ExpiresAt: time.Now().Add(80 * time.Millisecond)})
	if !waitFor(func() bool { return hits.Load() >= 2 }, 2*time.Second) {
		t.Fatalf("expected repeated polls, got %d", hits.Load())
	}
	time.Sleep(150 * time.Millisecond)
	settled := hits.Load()
	time.Sleep(100 * time.Millisecond)
	if hits.Load() != settled || !d.AreaStatus().Idle {
		t.Errorf("polling continued after expiry: %d -> %d idle=%v", settled, hits.Load(), d.AreaStatus().Idle)
	}
}

func TestLegacyFixedBBox(t *testing.T) {
	srv, hits, urls, _ := fixtureServer(t)
	d, _, _ := newDriver(t, srv, `{"viewportMode":false,"bbox":"45.8389, 5.9962, 47.8084, 10.5226","intervalSec":3600}`)
	if !waitFor(func() bool { return hits.Load() == 1 }, 2*time.Second) {
		t.Fatal("legacy mode must poll immediately")
	}
	if u := urls()[0]; u != "/?lamin=45.8389&lomin=5.9962&lamax=47.8084&lomax=10.5226" {
		t.Errorf("url = %q", u)
	}
	st := d.AreaStatus()
	if st.Idle || !st.Active || len(st.Boxes) != 1 {
		t.Errorf("status = %+v", st)
	}
	// A pushed area is ignored in legacy mode.
	if st := d.SetAreaOfInterest(aoi.Area{Active: false}); st.Idle {
		t.Error("legacy mode never idles")
	}
	if _, err := New(driver.Spec{Config: json.RawMessage(`{"viewportMode":false,"bbox":"1,2"}`)}, driver.Deps{Logger: zap.NewNop()}); err == nil {
		t.Error("malformed bbox must be rejected")
	}
}

func TestLowCreditsSlowPolling(t *testing.T) {
	srv, _, _, remaining := fixtureServer(t)
	remaining.Store(500)
	d := build(t, srv, `{"intervalSec":10,"maxIntervalSec":60,"creditFloor":200,"minPollSpacingSec":0}`)
	d.sink = &memSink{}
	d.SetAreaOfInterest(aoi.Area{Active: true, Boxes: []aoi.BBox{box}})
	if st := d.AreaStatus(); st.CreditsRemaining != -1 || st.LowCredits {
		t.Errorf("credits should be unknown before the first poll: %+v", st)
	}
	if err := d.poll(context.Background(), aoi.Area{Active: true, Boxes: []aoi.BBox{box}}); err != nil {
		t.Fatal(err)
	}
	if st := d.AreaStatus(); st.CreditsRemaining != 500 || st.LowCredits || st.EffectiveInterval != 10*time.Second {
		t.Errorf("plenty of credits: %+v", st)
	}
	remaining.Store(150)
	if err := d.poll(context.Background(), aoi.Area{Active: true, Boxes: []aoi.BBox{box}}); err != nil {
		t.Fatal(err)
	}
	if st := d.AreaStatus(); st.CreditsRemaining != 150 || !st.LowCredits || st.EffectiveInterval != 60*time.Second {
		t.Errorf("below the floor: %+v", st)
	}
	var detail = d.Health().Detail
	if detail["lowCredits"] != true {
		t.Errorf("health detail = %v", detail)
	}
}

func TestHTTPErrorsAreReported(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte("slow down"))
	}))
	defer srv.Close()
	d := build(t, srv, `{"intervalSec":3600}`)
	d.sink = &memSink{}
	err := d.poll(context.Background(), aoi.Area{Active: true, Boxes: []aoi.BBox{box}})
	if err == nil || d.Health().LastError == "" {
		t.Errorf("expected an error, got %v / %q", err, d.Health().LastError)
	}
}

func TestOAuthTokenUsedWhenSecretsPresent(t *testing.T) {
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.FormValue("client_id") != "id" || r.FormValue("client_secret") != "sec" {
			t.Errorf("bad credentials %v", r.Form)
		}
		_, _ = w.Write([]byte(`{"access_token":"tok","expires_in":3600}`))
	}))
	defer tokenServer.Close()
	var auth string
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"time":1,"states":[]}`))
	}))
	defer api.Close()
	d, err := New(driver.Spec{FeedID: "opensky", Config: json.RawMessage(`{"baseUrl":"` + api.URL + `","tokenUrl":"` + tokenServer.URL + `"}`), Secrets: map[string]string{"clientId": "id", "clientSecret": "sec"}}, driver.Deps{Logger: zap.NewNop()})
	if err != nil {
		t.Fatal(err)
	}
	drv := d.(*Driver)
	drv.sink = &memSink{}
	if err := drv.poll(context.Background(), aoi.Area{}); err != nil {
		t.Fatal(err)
	}
	if auth != "Bearer tok" {
		t.Errorf("Authorization = %q", auth)
	}
}

func TestBackgroundIntervalDefaults(t *testing.T) {
	mk := func(cfg string) *Driver {
		d, err := New(driver.Spec{FeedID: "opensky", Driver: ID, Config: json.RawMessage(cfg)}, driver.Deps{})
		if err != nil {
			t.Fatal(err)
		}
		return d.(*Driver)
	}
	for cfg, want := range map[string]time.Duration{
		`{}`:                             2 * time.Minute,
		`{"backgroundIntervalSec": 0}`:   0,
		`{"backgroundIntervalSec": 30}`:  time.Minute,
		`{"backgroundIntervalSec": 300}`: 5 * time.Minute,
		`{"viewportMode": false}`:        0,
		`{"viewportMode": false, "backgroundIntervalSec": 120}`: 0,
	} {
		if got := mk(cfg).background; got != want {
			t.Errorf("%s: background = %v, want %v", cfg, got, want)
		}
	}
}

func TestBackgroundPollCoversTheGlobeOutsideTheArea(t *testing.T) {
	srv, _, urls, _ := fixtureServer(t)
	d := build(t, srv, `{"intervalSec":3600, "backgroundIntervalSec": 120}`)
	sink := &memSink{}

	// Nobody looking: every aircraft is a background fix, from one global request.
	d.pollGlobe(context.Background(), sink)
	if sink.count() != 3 {
		t.Fatalf("emitted %d", sink.count())
	}
	for _, o := range sink.obs {
		if !o.Background {
			t.Errorf("%s should be a background fix", o.SourceEntityID)
		}
	}
	if u := urls(); len(u) != 1 || strings.Contains(u[0], "lamin") {
		t.Errorf("requests = %v, want one global", u)
	}

	// With a live area, aircraft inside it are left to the full-rate poll.
	d.sched.SetArea(aoi.Area{Active: true, Boxes: []aoi.BBox{box}, ExpiresAt: time.Now().Add(time.Minute)})
	sink2 := &memSink{}
	d.pollGlobe(context.Background(), sink2)
	if sink2.count() != 2 {
		t.Fatalf("emitted %d, want the 2 aircraft outside the area", sink2.count())
	}
	for _, o := range sink2.obs {
		if o.SourceEntityID == "ICAO-AB1234" || !o.Background {
			t.Errorf("unexpected %+v", o)
		}
	}
	h := d.Health()
	if h.Idle || h.Detail["backgroundPolls"] != int64(2) || h.Detail["backgroundAircraft"] != 2 {
		t.Errorf("health = %+v", h)
	}
}

func TestBackgroundRunsWithoutViewers(t *testing.T) {
	srv, hits, _, _ := fixtureServer(t)
	d := build(t, srv, `{"intervalSec":3600, "backgroundIntervalSec": 120}`)
	sink := start(t, d)
	if !waitFor(func() bool { return sink.count() == 3 }, 2*time.Second) {
		t.Fatalf("background poll did not run: %d emitted, %d requests", sink.count(), hits.Load())
	}
	if d.Health().Idle || d.Health().Detail["nextBackgroundAt"] == nil {
		t.Errorf("health = %+v", d.Health())
	}
}
