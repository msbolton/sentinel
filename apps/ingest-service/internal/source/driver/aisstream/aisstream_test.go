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

func connDetail(d *Driver) map[string]any {
	c, _ := d.Health().Detail["connection"].(map[string]any)
	return c
}

// --- tests ---

func TestNewValidates(t *testing.T) {
	spec := func(cfg string, secrets map[string]string) driver.Spec {
		return driver.Spec{FeedID: "f", Driver: ID, Config: json.RawMessage(cfg), Secrets: secrets}
	}
	key := map[string]string{"apiKey": "k"}
	for name, s := range map[string]driver.Spec{
		"no key":       spec(`{}`, nil),
		"bad mmsi":     spec(`{"watchlistMmsi": ["12345"]}`, key),
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
	if w := d.(*Driver).watchlist; len(w) != 1 || !w["235100247"] {
		t.Errorf("watchlist = %v", w)
	}
	if bg := d.(*Driver).thin.background; bg != 5*time.Minute {
		t.Errorf("background = %v", bg)
	}
}

func TestSubscribesToTheWholeWorldOnce(t *testing.T) {
	f := newFakeServer(t)
	d := newDriver(t, f.url, "")
	start(t, d)

	// Nobody looking: still one connection, to the whole world.
	c := f.conn(t, 0)
	s := c.nextSub(t)
	if s.APIKey != "test-key" || len(s.BoundingBoxes) != 1 || s.BoundingBoxes[0] != globalBoxes()[0] || len(s.FiltersShipMMSI) != 0 || len(s.FilterMessageTypes) != len(DefaultMessageTypes) {
		t.Errorf("subscription = %+v", s)
	}
	waitFor(t, "confirmation", func() bool { c := connDetail(d); return c != nil && c["confirmed"] == true })
	if c := connDetail(d); c["compression"] != true {
		t.Errorf("connection = %v", c)
	}
	if h := d.Health(); h.Idle || !h.Connected || d.AreaStatus().Idle {
		t.Errorf("health = %+v", h)
	}

	// A viewport changes rates, not the subscription.
	d.SetAreaOfInterest(area(channel))
	c.noSub(t, 300*time.Millisecond)
	if f.count() != 1 {
		t.Errorf("connections = %d", f.count())
	}
}

func TestMessagesBecomeObservations(t *testing.T) {
	f := newFakeServer(t)
	d := newDriver(t, f.url, "")
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
		if o.HasPosition && o.Background {
			t.Errorf("%s: a vessel in view is a full-rate fix", name)
		}
		switch name {
		case "PositionReport":
			if o.DisplayName != "VICTORIA OF WIGHT" || *o.SpeedKnots != 5.6 || *o.Heading != 224 || o.SourceTimestamp.Location() != time.UTC {
				t.Errorf("position = %+v %+v", o, o.AIS)
			}
		case "ShipStaticData":
			if o.DisplayName != "PONTUS HIGHWAY" || o.AIS.Destination != "GBSOU" || o.AIS.ShipTypeName != "Cargo" || o.HasPosition {
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

func TestVesselsOutOfViewAreBackgroundFixes(t *testing.T) {
	f := newFakeServer(t)
	d := newDriver(t, f.url, `{"watchlistMmsi": ["235100247"]}`)
	sink := start(t, d)
	d.SetAreaOfInterest(area(channel))
	c := f.conn(t, 0)
	c.nextSub(t)

	c.send(t, fixture(t, "PositionReport")) // in the Channel: full rate
	waitFor(t, "in view", func() bool { return sink.count() == 1 })
	if sink.last().Background {
		t.Error("a vessel in view must be a full-rate fix")
	}
	c.send(t, fixture(t, "ExtendedClassBPositionReport")) // off Java: background
	waitFor(t, "out of view", func() bool { return sink.count() == 2 })
	if !sink.last().Background {
		t.Error("a vessel out of view must be a background fix")
	}
	c.send(t, fixture(t, "ShipStaticData")) // no position, not watched: background
	waitFor(t, "static", func() bool { return sink.count() == 3 })
	if !sink.last().Background {
		t.Error("static data of an unwatched vessel is a background update")
	}

	// The watchlist is full rate wherever it is, even with nobody looking.
	d.SetAreaOfInterest(aoi.Area{})
	c.send(t, fixture(t, "StandardClassBPositionReport"))
	waitFor(t, "watched", func() bool { return sink.count() == 4 })
	if sink.last().Background {
		t.Error("a watched vessel must be a full-rate fix")
	}
	// A second background report within five minutes is thinned.
	c.send(t, fixture(t, "ExtendedClassBPositionReport"))
	waitFor(t, "thinned", func() bool { return sink.skips("thinned") == 1 })
	h := d.Health()
	if h.Detail["detail"] != int64(2) || h.Detail["background"] != int64(2) || h.Detail["viewing"] != false {
		t.Errorf("detail = %v", h.Detail)
	}
}

func TestThinning(t *testing.T) {
	th := newThinner(15*time.Second, 3*time.Minute, 5*time.Minute, 0.5)
	t0 := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	pos := func(id string, sog float64, status string, background bool) *obs.Observation {
		o := &obs.Observation{SourceEntityID: id, OperationalStatus: status, SpeedKnots: obs.Float(sog), Background: background}
		o.SetPosition(50, 0)
		return o
	}
	steps := []struct {
		o    *obs.Observation
		at   time.Duration
		want bool
	}{
		{pos("a", 12, "", false), 0, true},
		{pos("a", 12, "", false), 10 * time.Second, false},
		{pos("a", 12, "", false), 15 * time.Second, true},
		{pos("b", 0.2, "", false), 0, true},
		{pos("b", 0.2, "", false), time.Minute, false},
		{pos("b", 0.2, "", false), 3 * time.Minute, true},
		{pos("c", 3, "MOORED", false), 0, true},
		{pos("c", 3, "MOORED", false), 2 * time.Minute, false},
		{pos("d", 12, "", true), 0, true},
		{pos("d", 12, "", true), 4 * time.Minute, false},
		{pos("d", 12, "", true), 5 * time.Minute, true},
		{&obs.Observation{SourceEntityID: "a"}, 16 * time.Second, true}, // static: always
	}
	for i, s := range steps {
		if got := th.allow(s.o, t0.Add(s.at)); got != s.want {
			t.Errorf("step %d (%s at %v): allow = %v", i, s.o.SourceEntityID, s.at, got)
		}
	}
	all := newThinner(0, 0, 0, 0.5)
	if !all.allow(pos("a", 1, "", true), t0) || !all.allow(pos("a", 1, "", true), t0) {
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
	waitFor(t, "error", func() bool {
		c := connDetail(d)
		msg, _ := c["lastError"].(string)
		return strings.Contains(msg, "not confirmed")
	})
	waitFor(t, "reconnect", func() bool { return f.count() >= 2 })
}

func TestServerErrorIsReported(t *testing.T) {
	f := newFakeServer(t)
	f.reply = []byte(`{"error": "Api Key Is Not Valid"}`)
	d := newDriver(t, f.url, "")
	start(t, d)
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
