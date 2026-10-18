package celestrak

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/sentinel/ingest-service/internal/aoi"
	"github.com/sentinel/ingest-service/internal/obs"
	"github.com/sentinel/ingest-service/internal/source/decoder/tlesgp4"
	"github.com/sentinel/ingest-service/internal/source/driver"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

type memSink struct {
	mu      sync.Mutex
	obs     []*obs.Observation
	dead    []string
	skipped map[string]int
	success int
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
func (s *memSink) Skipped(reason string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.skipped == nil {
		s.skipped = map[string]int{}
	}
	s.skipped[reason]++
}
func (s *memSink) Succeeded(int)         { s.mu.Lock(); s.success++; s.mu.Unlock() }
func (s *memSink) Blocked(time.Duration) {}
func (s *memSink) count() int            { s.mu.Lock(); defer s.mu.Unlock(); return len(s.obs) }
func (s *memSink) ids() map[string]int {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]int{}
	for _, o := range s.obs {
		out[o.SourceEntityID]++
	}
	return out
}

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

// newDriver builds a driver against a test server with fast timers.
func newDriver(t *testing.T, srvURL, config string) *Driver {
	t.Helper()
	cfg := strings.TrimSuffix(strings.TrimSpace(config), "}")
	if !strings.Contains(config, "backgroundIntervalSec") {
		cfg += `, "backgroundIntervalSec": 0` // most tests are about the viewport
	}
	cfg += `, "propagationIntervalSec": 1, "baseUrl": "` + srvURL + `/gp.php", "supplementalUrl": "` + srvURL + `/sup-gp.php"}`
	d, err := New(driver.Spec{FeedID: "celestrak", Config: json.RawMessage(cfg)}, driver.Deps{Logger: zap.NewNop()})
	if err != nil {
		t.Fatal(err)
	}
	drv := d.(*Driver)
	drv.step = 30 * time.Millisecond
	drv.jitter = func() time.Duration { return 0 }
	return drv
}

// worldView makes every satellite visible, for tests not about the area.
func worldView(drv *Driver) {
	drv.SetAreaOfInterest(aoi.Area{Active: true, Clients: 1, Boxes: []aoi.BBox{{North: 90, South: -90, East: 180, West: -180}}})
}

func run(t *testing.T, drv *Driver, sink driver.Sink) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = drv.Run(ctx, sink) }()
	return func() { cancel(); <-done }
}

func TestFetchesJSONThenPropagates(t *testing.T) {
	var hits atomic.Int32
	var queries []string
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		queries = append(queries, r.URL.RawQuery)
		mu.Unlock()
		if hits.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable) // first fetch fails: retried with backoff
			return
		}
		_, _ = w.Write(fixture(t, "stations.json"))
	}))
	defer srv.Close()
	drv := newDriver(t, srv.URL, `{"groups":["stations"]}`)
	worldView(drv)
	sink := &memSink{}
	defer run(t, drv, sink)()

	if !waitFor(func() bool { return sink.count() >= 4 }, 5*time.Second) {
		t.Fatalf("expected two propagation rounds of two satellites, got %d (fetch hits %d)", sink.count(), hits.Load())
	}
	if hits.Load() < 2 {
		t.Errorf("fetch should have been retried after the 503: %d", hits.Load())
	}
	mu.Lock()
	q := queries[0]
	mu.Unlock()
	if q != "GROUP=stations&FORMAT=json" {
		t.Errorf("query = %q", q)
	}
	o := sink.obs[0]
	if o.Source != "CELESTRAK" || o.Decoder != "tle-sgp4" || o.EntityType != obs.EntitySatellite || !o.HasPosition || o.TLE == nil {
		t.Errorf("observation = %+v", o)
	}
	if o.TLE.Epoch == "" {
		t.Error("epoch missing from the elements")
	}
	h := drv.Health()
	if !h.Connected || h.Detail["satellites"] != 2 || h.Detail["pinned"] != 0 || h.LastError != "" || h.Detail["fetchedAt"] == nil || h.Detail["nextFetchAt"] == nil {
		t.Errorf("health = %+v", h)
	}
}

func TestConditionalRequestsAndPrune(t *testing.T) {
	var hits atomic.Int32
	var headers []http.Header
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		headers = append(headers, r.Header.Clone())
		mu.Unlock()
		switch hits.Add(1) {
		case 1:
			w.Header().Set("ETag", `"v1"`)
			w.Header().Set("Last-Modified", "Wed, 04 Mar 2026 20:00:00 GMT")
			_, _ = w.Write(fixture(t, "stations.json"))
		case 2:
			w.WriteHeader(http.StatusNotModified)
		default:
			w.Header().Set("ETag", `"v2"`)
			_, _ = w.Write(fixture(t, "iss_only.json")) // GOES has left the group
		}
	}))
	defer srv.Close()
	drv := newDriver(t, srv.URL, `{"groups":["stations"]}`)
	drv.refresh = 40 * time.Millisecond
	sink := &memSink{}
	defer run(t, drv, sink)()

	if !waitFor(func() bool { return hits.Load() >= 3 }, 5*time.Second) {
		t.Fatalf("expected three fetches, got %d", hits.Load())
	}
	mu.Lock()
	second := headers[1]
	mu.Unlock()
	if second.Get("If-None-Match") != `"v1"` || second.Get("If-Modified-Since") != "Wed, 04 Mar 2026 20:00:00 GMT" {
		t.Errorf("second request lacked conditional headers: %v", second)
	}
	// After the third fetch only the ISS remains cached.
	if !waitFor(func() bool { return drv.Health().Detail["satellites"] == 1 }, 2*time.Second) {
		t.Errorf("GOES not pruned: %+v", drv.Health())
	}
	if h := drv.Health(); h.LastError != "" || !h.Connected {
		t.Errorf("health after a 304 = %+v", h)
	}
}

func TestSelectionsBuildRequests(t *testing.T) {
	var paths []string
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path+"?"+r.URL.RawQuery)
		mu.Unlock()
		// Pinned queries (catalog number, designator, name) and the ISS
		// supplemental file name the ISS alone; the group and special list
		// return both satellites.
		q := r.URL.RawQuery
		if strings.HasPrefix(q, "CATNR=") || strings.HasPrefix(q, "INTDES=") || strings.HasPrefix(q, "NAME=") || strings.HasPrefix(q, "FILE=") {
			_, _ = w.Write(fixture(t, "iss_only.json"))
			return
		}
		_, _ = w.Write(fixture(t, "stations.json"))
	}))
	defer srv.Close()
	drv := newDriver(t, srv.URL, `{"groups":["stations"],"catalogNumbers":[25544],"designators":["1998-067"],"names":["ISS (ZARYA)"],"special":["gpz"],"supplemental":["iss"]}`)
	worldView(drv)
	sink := &memSink{}
	defer run(t, drv, sink)()

	if !waitFor(func() bool { mu.Lock(); defer mu.Unlock(); return len(paths) >= 6 }, 5*time.Second) {
		t.Fatalf("expected six requests, got %v", paths)
	}
	mu.Lock()
	got := append([]string(nil), paths[:6]...)
	mu.Unlock()
	sort.Strings(got)
	want := []string{
		"/gp.php?CATNR=25544&FORMAT=json",
		"/gp.php?GROUP=stations&FORMAT=json",
		"/gp.php?INTDES=1998-067&FORMAT=json",
		"/gp.php?NAME=ISS+%28ZARYA%29&FORMAT=json",
		"/gp.php?SPECIAL=GPZ&FORMAT=json",
		"/sup-gp.php?FILE=iss&FORMAT=json",
	}
	sort.Strings(want)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("requests:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	// The ISS came through pinned selections; GOES only through the group.
	if !waitFor(func() bool { return drv.Health().Detail["pinned"] == 1 && drv.Health().Detail["satellites"] == 2 }, 2*time.Second) {
		t.Errorf("health = %+v", drv.Health())
	}
}

func TestBadRecordsAreDeadLetteredAndSixDigitIdsKept(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(fixture(t, "bad_record.json"))
	}))
	defer srv.Close()
	drv := newDriver(t, srv.URL, `{"groups":["active"]}`)
	worldView(drv)
	sink := &memSink{}
	defer run(t, drv, sink)()

	if !waitFor(func() bool { return sink.count() >= 1 }, 5*time.Second) {
		t.Fatal("nothing emitted")
	}
	sink.mu.Lock()
	dead := append([]string(nil), sink.dead...)
	first := sink.obs[0]
	sink.mu.Unlock()
	if len(dead) != 1 || dead[0] != "bad_elements" {
		t.Errorf("dead letters = %v", dead)
	}
	if first.SourceEntityID != "SAT-270001" || first.TLE.NoradID != 270001 || first.TLE.Line1[2:7] != "70001" {
		t.Errorf("six-digit id: %s %+v", first.SourceEntityID, first.TLE)
	}
}

func TestHealthRecordsErrorsAndRetryAfter(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("one download per update"))
	}))
	defer srv.Close()
	drv := newDriver(t, srv.URL, `{"groups":["active"]}`)
	sink := &memSink{}
	defer run(t, drv, sink)()

	if !waitFor(func() bool { return drv.Health().LastError != "" }, 2*time.Second) {
		t.Fatal("error never recorded")
	}
	h := drv.Health()
	if !strings.Contains(h.LastError, "403") || !strings.Contains(h.LastError, "one download per update") || h.Connected {
		t.Errorf("health = %+v", h)
	}
	if errs, ok := h.Detail["errors"].(map[string]string); !ok || errs["group:active"] == "" {
		t.Errorf("per-selection errors = %v", h.Detail["errors"])
	}
	if h.Detail["lastStatus"] != 403 {
		t.Errorf("lastStatus = %v", h.Detail["lastStatus"])
	}
	// Retry-After: 1 means the second attempt waits a second, not the 1 s→60 s backoff's first step.
	time.Sleep(600 * time.Millisecond)
	if hits.Load() != 1 {
		t.Errorf("retried before Retry-After elapsed: %d hits", hits.Load())
	}
	if !waitFor(func() bool { return hits.Load() >= 2 }, 2*time.Second) {
		t.Error("no retry after Retry-After")
	}
}

func TestRefreshFloorAndAlias(t *testing.T) {
	d, err := New(driver.Spec{FeedID: "x", Config: json.RawMessage(`{"tleRefreshHours": 6}`)}, driver.Deps{Logger: zap.NewNop()})
	if err != nil {
		t.Fatal(err)
	}
	if got := d.(*Driver).refresh; got != 6*time.Hour {
		t.Errorf("alias refresh = %v", got)
	}
	d, _ = New(driver.Spec{FeedID: "x", Config: json.RawMessage(`{}`)}, driver.Deps{Logger: zap.NewNop()})
	drv := d.(*Driver)
	if drv.refresh != 2*time.Hour || len(drv.sels) != 1 || drv.sels[0].Value != "active" {
		t.Errorf("defaults: refresh %v selections %+v", drv.refresh, drv.sels)
	}
	if got := parseRetryAfter("120", time.Now()); got != 2*time.Minute {
		t.Errorf("retry-after seconds = %v", got)
	}
	if got := parseRetryAfter("999999", time.Now()); got != maxRetryAfter {
		t.Errorf("retry-after cap = %v", got)
	}
	if got := parseRetryAfter("", time.Now()); got != 0 {
		t.Errorf("empty retry-after = %v", got)
	}
}

// positionNow propagates a fixture object at the driver's clock, so a test
// can draw a box around it.
func positionNow(t *testing.T, drv *Driver, norad int) (lat, lon float64) {
	t.Helper()
	drv.mu.Lock()
	c, ok := drv.objects[norad]
	drv.mu.Unlock()
	if !ok {
		t.Fatalf("object %d not cached", norad)
	}
	o := tlesgp4.Propagate(c.tle, drv.now())
	if o == nil {
		t.Fatalf("object %d did not propagate", norad)
	}
	return o.Latitude, o.Longitude
}

func TestEmitsOnlyInsideTheArea(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(fixture(t, "stations.json"))
	}))
	defer srv.Close()
	drv := newDriver(t, srv.URL, `{"groups":["stations"]}`)
	drv.step = time.Hour // only explicit wakes propagate
	sink := &memSink{}
	defer run(t, drv, sink)()

	// With no viewport nothing is emitted and the feed is idle.
	if !waitFor(func() bool { return drv.Health().Detail["satellites"] == 2 }, 5*time.Second) {
		t.Fatal("elements not fetched")
	}
	time.Sleep(50 * time.Millisecond)
	if sink.count() != 0 || !drv.Health().Idle || !drv.AreaStatus().Idle {
		t.Fatalf("emitted %d with no viewport; idle=%v", sink.count(), drv.Health().Idle)
	}

	// A box around GOES: only GOES is emitted, and the change wakes a tick.
	sink.mu.Lock()
	skippedBefore := sink.skipped["out_of_area"]
	sink.mu.Unlock()
	lat, lon := positionNow(t, drv, 41866)
	st := drv.SetAreaOfInterest(aoi.Area{Active: true, Clients: 1, Boxes: []aoi.BBox{{North: lat + 5, South: lat - 5, East: lon + 5, West: lon - 5}}})
	if st.Idle || !st.Active {
		t.Errorf("status after push = %+v", st)
	}
	if !waitFor(func() bool { return sink.count() >= 1 }, 2*time.Second) {
		t.Fatal("no emission after the viewport arrived")
	}
	time.Sleep(50 * time.Millisecond)
	if ids := sink.ids(); ids["SAT-41866"] != 1 || ids["SAT-25544"] != 0 {
		t.Errorf("emitted %v, want only GOES", ids)
	}
	sink.mu.Lock()
	skipped := sink.skipped["out_of_area"]
	sink.mu.Unlock()
	if skipped != skippedBefore+1 {
		t.Errorf("out_of_area skips = %d, want %d (the ISS once)", skipped, skippedBefore+1)
	}

	// No clients: back to nothing.
	drv.SetAreaOfInterest(aoi.Area{Active: false})
	time.Sleep(50 * time.Millisecond)
	before := sink.count()
	drv.SetAreaOfInterest(aoi.Area{Active: false}) // unchanged: no wake
	time.Sleep(50 * time.Millisecond)
	if sink.count() != before {
		t.Error("an unchanged inactive area triggered emission")
	}
	if !drv.Health().Idle {
		t.Error("not idle with no clients")
	}

	// An expired lease counts as nobody looking.
	drv.SetAreaOfInterest(aoi.Area{Active: true, Clients: 1, Boxes: []aoi.BBox{{North: 90, South: -90, East: 180, West: -180}}, ExpiresAt: time.Now().Add(-time.Minute)})
	time.Sleep(50 * time.Millisecond)
	if sink.count() != before {
		t.Error("an expired area emitted")
	}
}

func TestPinnedObjectsAreAlwaysEmitted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.RawQuery, "CATNR=") {
			_, _ = w.Write(fixture(t, "iss_only.json"))
			return
		}
		_, _ = w.Write(fixture(t, "stations.json"))
	}))
	defer srv.Close()
	drv := newDriver(t, srv.URL, `{"groups":["stations"],"catalogNumbers":[25544]}`)
	sink := &memSink{}
	defer run(t, drv, sink)()

	if !waitFor(func() bool { return sink.count() >= 2 }, 5*time.Second) {
		t.Fatal("pinned object not emitted without a viewport")
	}
	if ids := sink.ids(); ids["SAT-41866"] != 0 || ids["SAT-25544"] < 2 {
		t.Errorf("emitted %v, want only the pinned ISS", ids)
	}
	h := drv.Health()
	if h.Idle || h.Detail["pinned"] != 1 || h.Detail["emitted"] != 1 {
		t.Errorf("health = %+v", h)
	}
	if drv.AreaStatus().Idle {
		t.Error("idle with a pinned object")
	}
}

func TestBackgroundEmitsEverySatelliteSlowly(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(fixture(t, "stations.json"))
	}))
	defer srv.Close()
	drv := newDriver(t, srv.URL, `{"groups":["stations"], "backgroundIntervalSec": 600}`)
	if drv.background != 10*time.Minute {
		t.Fatalf("background = %v", drv.background)
	}
	drv.step = 20 * time.Millisecond
	drv.background = 300 * time.Millisecond
	sink := &memSink{}
	defer run(t, drv, sink)()

	// Nobody looking: both satellites arrive as background fixes, then
	// nothing more until the background interval has passed.
	if !waitFor(func() bool { return sink.count() == 2 }, 5*time.Second) {
		t.Fatalf("emitted %d", sink.count())
	}
	time.Sleep(100 * time.Millisecond)
	sink.mu.Lock()
	n := len(sink.obs)
	for _, o := range sink.obs {
		if !o.Background {
			t.Errorf("%s should be a background fix", o.SourceEntityID)
		}
	}
	sink.mu.Unlock()
	if n != 2 {
		t.Errorf("emitted %d within the background interval, want 2", n)
	}
	if h := drv.Health(); h.Idle || drv.AreaStatus().Idle || h.Detail["backgroundEmitted"] != 2 {
		t.Errorf("health = %+v", h)
	}
	if !waitFor(func() bool { return sink.count() == 4 }, 5*time.Second) {
		t.Errorf("no second background round: %d", sink.count())
	}
}
