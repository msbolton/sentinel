package adsblol

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/sentinel/ingest-service/internal/aoi"
	"github.com/sentinel/ingest-service/internal/obs"
	"github.com/sentinel/ingest-service/internal/source/driver"
)

const fixture = `{"now": 1705320000.5, "ac": [
    {"hex": "ae63ed", "flight": "ROPER23 ", "r": "67-14828", "t": "UH60L", "lat": 38.859155, "lon": -121.938660, "alt_baro": 3400, "gs": 346.1, "track": 50.75},
    {"hex": "ae1234", "flight": "EVAC01  ", "lat": 34.052235, "lon": -118.243683, "alt_baro": "ground", "gs": 0, "track": 270.0}
]}`

type memSink struct {
	mu  sync.Mutex
	obs []*obs.Observation
}

func (s *memSink) Emit(_ context.Context, o *obs.Observation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.obs = append(s.obs, o)
	return nil
}
func (s *memSink) DeadLetter([]byte, string, error) {}
func (s *memSink) Skipped(string)                   {}
func (s *memSink) Succeeded(int)                    {}
func (s *memSink) Blocked(time.Duration)            {}
func (s *memSink) count() int                       { s.mu.Lock(); defer s.mu.Unlock(); return len(s.obs) }

func server(t *testing.T) (*httptest.Server, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		_, _ = w.Write([]byte(fixture))
	}))
	t.Cleanup(srv.Close)
	return srv, func() []string { mu.Lock(); defer mu.Unlock(); return append([]string(nil), paths...) }
}

func build(t *testing.T, srv *httptest.Server, cfg string) *Driver {
	t.Helper()
	var c map[string]any
	_ = json.Unmarshal([]byte(cfg), &c)
	if c == nil {
		c = map[string]any{}
	}
	c["baseUrl"] = srv.URL
	raw, _ := json.Marshal(c)
	d, err := New(driver.Spec{FeedID: "adsb-lol", Driver: ID, Config: raw}, driver.Deps{Logger: zap.NewNop()})
	if err != nil {
		t.Fatal(err)
	}
	return d.(*Driver)
}

func TestMilScopePollsImmediately(t *testing.T) {
	srv, paths := server(t)
	d := build(t, srv, `{"intervalSec":3600}`)
	sink := &memSink{}
	d.sink = sink
	if err := d.poll(context.Background(), aoi.Area{}); err != nil {
		t.Fatal(err)
	}
	if p := paths(); len(p) != 1 || p[0] != "/v2/mil" {
		t.Errorf("paths = %v", p)
	}
	if sink.count() != 2 || sink.obs[0].Source != "ADSB_LOL" || sink.obs[0].SourceEntityID != "ICAO-AE63ED" {
		t.Errorf("observations = %+v", sink.obs)
	}
	if d.Health().Idle || d.AreaStatus().Idle {
		t.Error("mil scope never idles")
	}
}

func TestAreaScopeUsesPointQueries(t *testing.T) {
	srv, paths := server(t)
	d := build(t, srv, `{"scope":"area","intervalSec":3600,"minPollSpacingSec":0}`)
	if !d.AreaStatus().Idle {
		t.Fatal("area scope must idle without an area")
	}
	sink := &memSink{}
	d.sink = sink
	area := aoi.Area{Active: true, Boxes: []aoi.BBox{{South: 38, West: -122, North: 39, East: -121}, {South: 33, West: -119, North: 35, East: -117}}}
	if err := d.poll(context.Background(), area); err != nil {
		t.Fatal(err)
	}
	p := paths()
	if len(p) != 2 || p[0] != "/v2/point/38.5000/-121.5000/39" {
		t.Errorf("paths = %v", p)
	}
	if sink.count() != 2 {
		t.Errorf("aircraft seen in both boxes must be emitted once: %d", sink.count())
	}
	// Radius is capped at the API maximum.
	huge := aoi.BBox{South: -60, West: -170, North: 60, East: 170}
	if u := d.pointURL(huge); u != srv.URL+"/v2/point/0.0000/0.0000/250" {
		t.Errorf("capped url = %q", u)
	}
	if r := RadiusNm(aoi.BBox{South: 0, West: 0, North: 1, East: 1}); math.Abs(r-42.43) > 0.5 {
		t.Errorf("radius of a 1°x1° box = %v nm", r)
	}
}

func TestRejectsUnknownScope(t *testing.T) {
	if _, err := New(driver.Spec{Config: json.RawMessage(`{"scope":"everything"}`)}, driver.Deps{Logger: zap.NewNop()}); err == nil {
		t.Error("expected an error")
	}
}
