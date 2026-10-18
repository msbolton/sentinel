package aisstream

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	gws "github.com/gorilla/websocket"

	sentinelv1 "github.com/sentinel/proto-gen/sentinel/v1"

	"github.com/sentinel/ingest-service/internal/aoi"
	"github.com/sentinel/ingest-service/internal/obs"
	"github.com/sentinel/ingest-service/internal/source/driver"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// --- sink ---

type memSink struct {
	mu      sync.Mutex
	obs     []*obs.Observation
	dead    []string
	skipped map[string]int
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
func (s *memSink) Succeeded(int)         {}
func (s *memSink) Blocked(time.Duration) {}
func (s *memSink) count() int            { s.mu.Lock(); defer s.mu.Unlock(); return len(s.obs) }
func (s *memSink) skips(reason string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.skipped[reason]
}
func (s *memSink) last() *obs.Observation {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.obs[len(s.obs)-1]
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// --- fake aisstream.io ---

type fakeConn struct {
	ws   *gws.Conn
	wmu  sync.Mutex
	subs chan subscription
	gone chan struct{}
}

func (c *fakeConn) send(t *testing.T, data []byte) {
	t.Helper()
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if err := c.ws.WriteMessage(gws.BinaryMessage, data); err != nil {
		t.Fatal(err)
	}
}

func (c *fakeConn) nextSub(t *testing.T) subscription {
	t.Helper()
	select {
	case s := <-c.subs:
		return s
	case <-time.After(5 * time.Second):
		t.Fatal("no subscription arrived")
	}
	return subscription{}
}

func (c *fakeConn) noSub(t *testing.T, within time.Duration) {
	t.Helper()
	select {
	case s := <-c.subs:
		t.Fatalf("unexpected subscription %+v", s.BoundingBoxes)
	case <-time.After(within):
	}
}

type fakeServer struct {
	srv *httptest.Server
	url string

	mu      sync.Mutex
	conns   []*fakeConn
	confirm bool
	reply   []byte // sent instead of a confirmation when set
}

func newFakeServer(t *testing.T) *fakeServer {
	f := &fakeServer{confirm: true}
	up := gws.Upgrader{EnableCompression: true}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		c := &fakeConn{ws: ws, subs: make(chan subscription, 100), gone: make(chan struct{})}
		f.mu.Lock()
		f.conns = append(f.conns, c)
		confirm, reply := f.confirm, f.reply
		f.mu.Unlock()
		defer close(c.gone)
		for {
			_, data, err := ws.ReadMessage()
			if err != nil {
				return
			}
			var s subscription
			if json.Unmarshal(data, &s) != nil {
				continue
			}
			c.subs <- s
			switch {
			case reply != nil:
				c.send(t, reply)
			case confirm:
				c.send(t, []byte(`{"Message":{"CompressionEnabled":true},"MessageType":"SubscriptionConfirmation"}`))
			}
		}
	}))
	f.url = "ws" + strings.TrimPrefix(f.srv.URL, "http")
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeServer) conn(t *testing.T, i int) *fakeConn {
	t.Helper()
	waitFor(t, "a connection", func() bool { f.mu.Lock(); defer f.mu.Unlock(); return len(f.conns) > i })
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.conns[i]
}

func (f *fakeServer) count() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.conns) }

// --- driver harness ---

func newDriver(t *testing.T, url, cfg string) *Driver {
	t.Helper()
	if cfg == "" {
		cfg = "{}"
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(cfg), &m); err != nil {
		t.Fatal(err)
	}
	m["url"] = url
	raw, _ := json.Marshal(m)
	d, err := New(driver.Spec{FeedID: "aisstream", Driver: ID, Config: raw, Secrets: map[string]string{"apiKey": "test-key"}}, driver.Deps{})
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
	go func() { defer close(done); d.Run(ctx, sink) }()
	t.Cleanup(func() { cancel(); <-done })
	return sink
}

func area(boxes ...aoi.BBox) aoi.Area {
	return aoi.Area{Active: true, Clients: 1, Boxes: boxes, ExpiresAt: time.Now().Add(time.Minute)}
}

var (
	channel = aoi.BBox{North: 51.46, South: 49.51, East: 2.03, West: -1.97}
	world   = aoi.BBox{North: 90, South: -90, East: 180, West: -180}
)

func connDetail(d *Driver, name string) map[string]any {
	c, _ := d.Health().Detail["connections"].(map[string]any)[name].(map[string]any)
	return c
}

// --- tests ---

func TestNewValidates(t *testing.T) {
	spec := func(cfg string, secrets map[string]string) driver.Spec {
		return driver.Spec{FeedID: "f", Driver: ID, Config: json.RawMessage(cfg), Secrets: secrets}
	}
	key := map[string]string{"apiKey": "k"}
	many := make([]string, 201)
	for i := range many {
		many[i] = `"` + strings.Repeat("1", 6) + string(rune('0'+i/100)) + string(rune('0'+i/10%10)) + string(rune('0'+i%10)) + `"`
	}
	for name, s := range map[string]driver.Spec{
		"no key":       spec(`{}`, nil),
		"bad mmsi":     spec(`{"watchlistMmsi": ["12345"]}`, key),
		"too many":     spec(`{"watchlistMmsi": [`+strings.Join(many, ",")+`]}`, key),
		"unknown type": spec(`{"messageTypes": ["SafetyBroadcastMessage"]}`, key),
	} {
		if _, err := New(s, driver.Deps{}); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
	d, err := New(spec(`{"watchlistMmsi": ["235100247", "235100247"]}`, key), driver.Deps{})
	if err != nil {
		t.Fatal(err)
	}
	if w := d.(*Driver).watchlist; len(w) != 1 {
		t.Errorf("watchlist = %v, want duplicates removed", w)
	}
}

func TestSubscribesToTheViewportWithTheKey(t *testing.T) {
	f := newFakeServer(t)
	d := newDriver(t, f.url, "")
	start(t, d)
	d.SetAreaOfInterest(area(channel))

	c := f.conn(t, 0)
	s := c.nextSub(t)
	if s.APIKey != "test-key" {
		t.Errorf("key = %q", s.APIKey)
	}
	want := [][2][2]float64{{{49.5, -2}, {51.5, 2.1}}}
	if len(s.BoundingBoxes) != 1 || s.BoundingBoxes[0] != want[0] {
		t.Errorf("boxes = %v, want %v ([lat, lon] corners rounded outward)", s.BoundingBoxes, want)
	}
	if len(s.FiltersShipMMSI) != 0 || len(s.FilterMessageTypes) != len(DefaultMessageTypes) {
		t.Errorf("filters = %v / %v", s.FiltersShipMMSI, s.FilterMessageTypes)
	}
	waitFor(t, "confirmation", func() bool { c := connDetail(d, connArea); return c != nil && c["confirmed"] == true })
	if c := connDetail(d, connArea); c["compression"] != true || c["connected"] != true {
		t.Errorf("connection = %v", c)
	}
	if h := d.Health(); h.Idle || h.Detail["mode"] != modeDetail {
		t.Errorf("health = %+v", h)
	}
}

func TestIdleWithoutViewersOrWatchlist(t *testing.T) {
	f := newFakeServer(t)
	d := newDriver(t, f.url, "")
	start(t, d)
	time.Sleep(200 * time.Millisecond)
	if f.count() != 0 {
		t.Errorf("connected with nobody looking")
	}
	if h := d.Health(); !h.Idle || h.Detail["mode"] != modeIdle || !d.AreaStatus().Idle {
		t.Errorf("health = %+v", h)
	}
}

func TestViewportChangesResubscribeAtMostEveryInterval(t *testing.T) {
	old := minUpdateInterval
	minUpdateInterval = 300 * time.Millisecond
	t.Cleanup(func() { minUpdateInterval = old })

	f := newFakeServer(t)
	d := newDriver(t, f.url, "")
	start(t, d)
	d.SetAreaOfInterest(area(channel))
	c := f.conn(t, 0)
	c.nextSub(t)

	// Two quick pans: one resubscription, with the latest boxes.
	d.SetAreaOfInterest(area(aoi.BBox{North: 55, South: 53, East: 5, West: 3}))
	d.SetAreaOfInterest(area(aoi.BBox{North: 60, South: 58, East: 10, West: 8}))
	s := c.nextSub(t)
	if s.BoundingBoxes[0] != [2][2]float64{{58, 8}, {60, 10}} {
		t.Errorf("resubscribed to %v", s.BoundingBoxes)
	}
	c.noSub(t, 500*time.Millisecond)

	// A pan inside the same tenth of a degree changes nothing.
	d.SetAreaOfInterest(area(aoi.BBox{North: 59.99, South: 58.01, East: 9.99, West: 8.01}))
	c.noSub(t, 500*time.Millisecond)
	if f.count() != 1 {
		t.Errorf("connections = %d, want the one connection reused", f.count())
	}
}

func TestLeavingTheMapClosesTheAreaConnection(t *testing.T) {
	f := newFakeServer(t)
	d := newDriver(t, f.url, "")
	start(t, d)
	d.SetAreaOfInterest(area(channel))
	c := f.conn(t, 0)
	c.nextSub(t)

	d.SetAreaOfInterest(aoi.Area{})
	select {
	case <-c.gone:
	case <-time.After(5 * time.Second):
		t.Fatal("area connection still open")
	}
	waitFor(t, "connection forgotten", func() bool { return connDetail(d, connArea) == nil })
}

func TestWatchlistIsTrackedEverywhere(t *testing.T) {
	f := newFakeServer(t)
	d := newDriver(t, f.url, `{"watchlistMmsi": ["235100247"]}`)
	sink := start(t, d)

	c := f.conn(t, 0)
	s := c.nextSub(t)
	if len(s.BoundingBoxes) != 1 || s.BoundingBoxes[0] != globalBoxes()[0] || len(s.FiltersShipMMSI) != 1 || s.FiltersShipMMSI[0] != "235100247" {
		t.Errorf("watchlist subscription = %+v", s)
	}
	if d.Health().Idle {
		t.Error("a feed with a watchlist is never idle")
	}
	// Nobody is looking, yet the watchlisted vessel is stored.
	c.send(t, fixture(t, "StandardClassBPositionReport"))
	waitFor(t, "watchlist observation", func() bool { return sink.count() == 1 })
	if o := sink.last(); o.SourceEntityID != "MMSI-235100247" || !o.HasPosition {
		t.Errorf("observation = %+v", o)
	}
}

func TestMessagesBecomeObservations(t *testing.T) {
	f := newFakeServer(t)
	d := newDriver(t, f.url, `{"maxAreaSqDeg": 0}`)
	sink := start(t, d)
	d.SetAreaOfInterest(area(world))
	c := f.conn(t, 0)
	c.nextSub(t)

	for _, name := range []string{"ShipStaticData", "PositionReport", "StandardClassBPositionReport", "ExtendedClassBPositionReport", "StaticDataReportA", "StaticDataReportB", "AidsToNavigationReport"} {
		before := sink.count()
		c.send(t, fixture(t, name))
		waitFor(t, name, func() bool { return sink.count() == before+1 })
		o := sink.last()
		if o.Decoder != ID || o.Source != "AIS" || o.AIS == nil || o.SourceTimestamp.IsZero() {
			t.Errorf("%s: observation = %+v", name, o)
		}
		switch name {
		case "PositionReport":
			// No static report seen: the name comes from aisstream.io's metadata.
			if o.DisplayName != "VICTORIA OF WIGHT" || o.AIS.VesselName != "VICTORIA OF WIGHT" || !o.HasPosition || *o.SpeedKnots != 5.6 || *o.Heading != 224 {
				t.Errorf("position = %+v %+v", o, o.AIS)
			}
			if o.SourceTimestamp.Year() != 2026 || o.SourceTimestamp.Location() != time.UTC {
				t.Errorf("time = %v", o.SourceTimestamp)
			}
		case "ShipStaticData":
			if o.DisplayName != "PONTUS HIGHWAY" || o.AIS.Destination != "GBSOU" || o.AIS.ShipTypeName != "Cargo" || o.AIS.IMO != "9974113" || o.HasPosition {
				t.Errorf("static = %q %+v", o.DisplayName, o.AIS)
			}
		case "AidsToNavigationReport":
			if o.EntityType != obs.EntityUnknown || o.DisplayName != "NAB TOWER" {
				t.Errorf("aid to navigation = %+v", o)
			}
		}
	}

	c.send(t, fixture(t, "SafetyBroadcastMessage"))
	waitFor(t, "skip", func() bool { return sink.skips("type_SafetyBroadcastMessage") == 1 })
	c.send(t, []byte(`{"MessageType": "PositionReport", "Message": {"PositionReport": {"Valid": "nope"}}}`))
	waitFor(t, "dead letter", func() bool { sink.mu.Lock(); defer sink.mu.Unlock(); return len(sink.dead) == 1 })
	if h := d.Health(); h.Detail["errors"] != int64(1) || h.Detail["emitted"] != int64(7) {
		t.Errorf("detail = %v", h.Detail)
	}
}

func TestOnlyVesselsInsideAModestViewAreStored(t *testing.T) {
	f := newFakeServer(t)
	d := newDriver(t, f.url, "")
	sink := start(t, d)
	d.SetAreaOfInterest(area(channel))
	c := f.conn(t, 0)
	c.nextSub(t)

	// A position that arrives after a pan away is not stored.
	c.send(t, fixture(t, "ExtendedClassBPositionReport")) // off Java
	waitFor(t, "out of area", func() bool { return sink.skips("out_of_area") == 1 })

	// A view larger than the cap: nothing from the area is stored.
	d.SetAreaOfInterest(area(aoi.BBox{North: 70, South: 20, East: 40, West: -30}))
	waitFor(t, "too large", func() bool { return d.Health().Detail["mode"] == modeDensity })
	c.send(t, fixture(t, "PositionReport"))
	c.send(t, fixture(t, "ShipStaticData"))
	waitFor(t, "not stored", func() bool { return sink.skips("area_density") == 2 })
	if sink.count() != 0 {
		t.Errorf("stored %d observations", sink.count())
	}
}

func TestThinning(t *testing.T) {
	th := newThinner(15*time.Second, 3*time.Minute, 0.5)
	t0 := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	pos := func(id string, sog float64, status string) *obs.Observation {
		o := &obs.Observation{SourceEntityID: id, OperationalStatus: status, SpeedKnots: obs.Float(sog)}
		o.SetPosition(50, 0)
		return o
	}
	steps := []struct {
		o    *obs.Observation
		at   time.Duration
		want bool
	}{
		{pos("a", 12, ""), 0, true},
		{pos("a", 12, ""), 10 * time.Second, false},
		{pos("a", 12, ""), 15 * time.Second, true},
		{pos("b", 0.2, ""), 0, true},
		{pos("b", 0.2, ""), time.Minute, false},
		{pos("b", 0.2, ""), 3 * time.Minute, true},
		{pos("c", 3, "MOORED"), 0, true},
		{pos("c", 3, "MOORED"), 2 * time.Minute, false},
		{&obs.Observation{SourceEntityID: "a"}, 16 * time.Second, true}, // static: always
	}
	for i, s := range steps {
		if got := th.allow(s.o, t0.Add(s.at)); got != s.want {
			t.Errorf("step %d (%s at %v): allow = %v", i, s.o.SourceEntityID, s.at, got)
		}
	}
	all := newThinner(0, 0, 0.5)
	if !all.allow(pos("a", 1, ""), t0) || !all.allow(pos("a", 1, ""), t0) {
		t.Error("zero intervals store everything")
	}
}

func TestUnconfirmedSubscriptionReconnects(t *testing.T) {
	old := confirmWait
	confirmWait = 150 * time.Millisecond
	t.Cleanup(func() { confirmWait = old })

	f := newFakeServer(t)
	f.confirm = false
	d := newDriver(t, f.url, "")
	start(t, d)
	d.SetAreaOfInterest(area(channel))
	waitFor(t, "error", func() bool {
		c := connDetail(d, connArea)
		msg, _ := c["lastError"].(string)
		return strings.Contains(msg, "not confirmed")
	})
	waitFor(t, "reconnect", func() bool { return f.count() >= 2 })
	if !strings.Contains(d.Health().LastError, "not confirmed") {
		t.Errorf("health error = %q", d.Health().LastError)
	}
}

func TestServerErrorIsReported(t *testing.T) {
	f := newFakeServer(t)
	f.reply = []byte(`{"error": "Api Key Is Not Valid"}`)
	d := newDriver(t, f.url, "")
	start(t, d)
	d.SetAreaOfInterest(area(channel))
	waitFor(t, "error", func() bool { return strings.Contains(d.Health().LastError, "Api Key Is Not Valid") })
}

func TestAFullBufferDropsInsteadOfBlocking(t *testing.T) {
	d := newDriver(t, "ws://unused", "")
	d.frames = make(chan frame, 1)
	d.enqueue(frame{})
	d.enqueue(frame{})
	if got := d.Health().Detail["dropped"]; got != int64(1) {
		t.Errorf("dropped = %v", got)
	}
}

type gridSink struct {
	mu    sync.Mutex
	grids []*sentinelv1.DensityGrid
}

func (g *gridSink) PublishDensity(grid *sentinelv1.DensityGrid) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.grids = append(g.grids, grid)
	return nil
}

func (g *gridSink) last() *sentinelv1.DensityGrid {
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.grids) == 0 {
		return nil
	}
	return g.grids[len(g.grids)-1]
}

func TestCounterCountsDistinctVesselsPerCell(t *testing.T) {
	c := newCounter(1, 10*time.Minute)
	t0 := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	c.observe("a", 50.2, -1.5, t0)
	c.observe("a", 50.3, -1.4, t0.Add(time.Minute)) // the same vessel again: counted once
	c.observe("b", 50.9, -1.1, t0)
	c.observe("c", 51.5, 1.5, t0)
	c.observe("old", 50.5, -1.5, t0.Add(-11*time.Minute))
	c.observe("far", -6, 111, t0)

	cells, total := c.grid(t0.Add(time.Minute), []aoi.BBox{{North: 55, South: 45, East: 5, West: -5}})
	if total != 3 || len(cells) != 2 {
		t.Fatalf("total %d, cells %v", total, cells)
	}
	if cells[0].Latitude != 50.5 || cells[0].Longitude != -1.5 || cells[0].Count != 2 {
		t.Errorf("cell 0 = %v", cells[0])
	}
	if cells[1].Latitude != 51.5 || cells[1].Longitude != 1.5 || cells[1].Count != 1 {
		t.Errorf("cell 1 = %v", cells[1])
	}
	if _, kept := c.seen["old"]; kept {
		t.Error("a sighting older than the window must be forgotten")
	}
}

func TestWideViewsAreCountedNotStored(t *testing.T) {
	f := newFakeServer(t)
	d := newDriver(t, f.url, `{"watchlistMmsi": ["232015630"]}`)
	grids := &gridSink{}
	d.densityOut, d.densityEvery = grids, 50*time.Millisecond
	sink := start(t, d)
	watch := f.conn(t, 0)
	watch.nextSub(t)

	// Western Europe: well over the 2 500 sq deg cap.
	d.SetAreaOfInterest(area(aoi.BBox{North: 70, South: 20, East: 40, West: -30}))
	c := f.conn(t, 1)
	c.nextSub(t)
	c.send(t, fixture(t, "PositionReport"))
	c.send(t, fixture(t, "StandardClassBPositionReport"))
	c.send(t, fixture(t, "ExtendedClassBPositionReport")) // off Java: outside the view
	waitFor(t, "a grid with both vessels", func() bool { g := grids.last(); return g != nil && g.Total == 2 })
	g := grids.last()
	if g.Source != sentinelv1.EntitySource_ENTITY_SOURCE_AIS || g.EntityType != sentinelv1.EntityType_ENTITY_TYPE_VESSEL || g.CellDegrees != 1 || g.WindowSeconds != 600 || g.CapSquareDegrees != 2500 || len(g.Cells) != 2 {
		t.Errorf("grid = %+v", g)
	}
	if sink.count() != 0 {
		t.Errorf("stored %d observations from a wide view", sink.count())
	}

	// The watchlist is stored whatever the view.
	watch.send(t, fixture(t, "PositionReport"))
	waitFor(t, "watchlist observation", func() bool { return sink.count() == 1 })

	// Zoomed in: no more grids.
	d.SetAreaOfInterest(area(channel))
	waitFor(t, "detail mode", func() bool { return d.Health().Detail["mode"] == modeDetail })
	grids.mu.Lock()
	n := len(grids.grids)
	grids.mu.Unlock()
	time.Sleep(200 * time.Millisecond)
	grids.mu.Lock()
	defer grids.mu.Unlock()
	if len(grids.grids) > n+1 {
		t.Errorf("grids kept coming in detail mode: %d -> %d", n, len(grids.grids))
	}
	if d.Health().Detail["density"] == nil {
		t.Error("health should report the last grid")
	}
}
