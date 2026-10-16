package httpjson

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"go.uber.org/zap"

	"github.com/sentinel/ingest-service/internal/aoi"
	"github.com/sentinel/ingest-service/internal/obs"
	"github.com/sentinel/ingest-service/internal/source/driver"
)

type memSink struct {
	mu      sync.Mutex
	obs     []*obs.Observation
	success []int
	dead    []string
	skipped []string
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
	s.skipped = append(s.skipped, reason)
}
func (s *memSink) Succeeded(n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.success = append(s.success, n)
}
func (s *memSink) Blocked(time.Duration) {}
func (s *memSink) count() int            { s.mu.Lock(); defer s.mu.Unlock(); return len(s.obs) }

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

// request is what the fixture server saw.
type request struct {
	Method string
	URL    string
	Header http.Header
	Body   string
}

type server struct {
	*httptest.Server
	hits      atomic.Int32
	remaining atomic.Int32
	status    atomic.Int32
	body      atomic.Value
	mu        sync.Mutex
	reqs      []request
}

func (s *server) requests() []request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]request(nil), s.reqs...)
}

const fixture = `{"now": 1700000000, "ac": [
  {"hex": "ab1234", "flight": "UAL123 ", "lat": 41.9742, "lon": -87.9045, "alt_baro": 36000, "gs": 450, "track": 90},
  {"hex": "cd5678", "flight": "DAL456", "lat": 40.6413, "lon": -73.7781, "alt_baro": 18000, "gs": 300, "track": 270},
  {"hex": "ef9012", "lat": 52.52, "lon": 13.405, "alt_baro": 10000}
]}`

func newServer(t *testing.T) *server {
	t.Helper()
	s := &server{}
	s.remaining.Store(-1)
	s.status.Store(http.StatusOK)
	s.body.Store(fixture)
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		s.mu.Lock()
		s.reqs = append(s.reqs, request{Method: r.Method, URL: r.URL.String(), Header: r.Header.Clone(), Body: string(b)})
		s.mu.Unlock()
		s.hits.Add(1)
		if n := s.remaining.Load(); n >= 0 {
			w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(int(n)))
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(int(s.status.Load()))
		_, _ = w.Write([]byte(s.body.Load().(string)))
	}))
	t.Cleanup(s.Close)
	return s
}

const decoderOptions = `{"records":"ac","identityType":"ICAO24","fields":{"id":"hex","latitude":"lat","longitude":"lon","altitude":"alt_baro","speed":"gs","heading":"track","callsign":"flight"}}`

// build makes a driver whose url is srv.URL + path, merging extra config.
func build(t *testing.T, srv *server, path, cfg string, secrets map[string]string) *Driver {
	t.Helper()
	c := map[string]any{}
	if cfg != "" {
		if err := json.Unmarshal([]byte(cfg), &c); err != nil {
			t.Fatal(err)
		}
	}
	c["url"] = srv.URL + path
	if _, ok := c["decoder"]; !ok {
		c["decoder"] = map[string]any{"options": json.RawMessage(decoderOptions)}
	}
	if _, ok := c["minPollSpacingSec"]; !ok {
		c["minPollSpacingSec"] = 0
	}
	raw, _ := json.Marshal(c)
	d, err := New(driver.Spec{FeedID: "feed-1", Driver: ID, Config: raw, Secrets: secrets}, driver.Deps{Logger: zap.NewNop()})
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

var box = aoi.BBox{South: 40, West: -90, North: 45, East: -80}

func pollOnce(t *testing.T, d *Driver, area aoi.Area) *memSink {
	t.Helper()
	sink := &memSink{}
	d.sink = sink
	if err := d.poll(context.Background(), area); err != nil {
		t.Fatal(err)
	}
	return sink
}

func TestPlaceholdersForBox(t *testing.T) {
	srv := newServer(t)
	d := build(t, srv, "/point/{lat}/{lon}/{radius_nm}?km={radius_km}&n={north}&s={south}&e={east}&w={west}&bbox={bbox}&t={now_unix}", `{"intervalSec":3600}`, nil)
	d.now = func() time.Time { return time.Unix(1700000000, 0) }
	sink := pollOnce(t, d, aoi.Area{Active: true, Boxes: []aoi.BBox{box}})
	reqs := srv.requests()
	if len(reqs) != 1 {
		t.Fatalf("requests = %d", len(reqs))
	}
	// Centre 42.5,-85; half-diagonal from the centre to 45,-80 is ~ 272 nm, capped at 250.
	want := "/point/42.5000/-85.0000/250?km=463&n=45.0000&s=40.0000&e=-80.0000&w=-90.0000&bbox=40.0000,-90.0000,45.0000,-80.0000&t=1700000000"
	if reqs[0].URL != want {
		t.Errorf("url = %q\n want %q", reqs[0].URL, want)
	}
	if reqs[0].Method != http.MethodGet || reqs[0].Header.Get("Accept") != "application/json" {
		t.Errorf("request = %+v", reqs[0])
	}
	if sink.count() != 3 {
		t.Fatalf("observations = %d", sink.count())
	}
	o := sink.obs[0]
	if o.SourceEntityID != "ab1234" || o.Source != "UNSPECIFIED" || o.Decoder != "json" || !o.HasPosition || o.Latitude != 41.9742 {
		t.Errorf("observation = %+v", o)
	}
	if len(o.Identities) != 2 || o.Identities[0] != (obs.Identity{Type: obs.IdentityICAO24, Value: "AB1234"}) || o.Identities[1] != (obs.Identity{Type: obs.IdentityCallsign, Value: "UAL123"}) {
		t.Errorf("identities = %+v", o.Identities)
	}
	if len(sink.success) != 1 || sink.success[0] != 3 {
		t.Errorf("success = %v", sink.success)
	}
	if h := d.Health(); h.Idle || h.LastError != "" || h.Detail["host"] != strings.TrimPrefix(srv.URL, "http://") {
		t.Errorf("health = %+v", h)
	}
}

func TestPlaceholdersForGlobeAndSmallRadius(t *testing.T) {
	srv := newServer(t)
	d := build(t, srv, "/q?bbox={bbox}&r={radius_nm}&lat={lat}&lon={lon}", `{"maxRadiusNm":100}`, nil)
	pollOnce(t, d, aoi.Area{})
	// An inactive or expired area is polled as the globe too.
	pollOnce(t, d, aoi.Area{Active: false, Boxes: []aoi.BBox{box}})
	pollOnce(t, d, aoi.Area{Active: true, Boxes: []aoi.BBox{box}, ExpiresAt: time.Now().Add(-time.Second)})
	pollOnce(t, d, aoi.Area{Active: true, Boxes: []aoi.BBox{{South: 0, North: 0.01, West: 0, East: 0.01}}})
	reqs := srv.requests()
	if len(reqs) != 4 {
		t.Fatalf("requests = %d", len(reqs))
	}
	global := "/q?bbox=-90.0000,-180.0000,90.0000,180.0000&r=100&lat=0.0000&lon=0.0000"
	for i := 0; i < 3; i++ {
		if reqs[i].URL != global {
			t.Errorf("request %d = %q, want %q", i, reqs[i].URL, global)
		}
	}
	if reqs[3].URL != "/q?bbox=0.0000,0.0000,0.0100,0.0100&r=1&lat=0.0050&lon=0.0050" {
		t.Errorf("small box url = %q", reqs[3].URL)
	}
}

func TestPostBodyAndHeaders(t *testing.T) {
	srv := newServer(t)
	d := build(t, srv, "/search", `{"method":"POST","body":{"filter":{"lat":"{lat}","lon":"{lon}"},"limit":10},"headers":{"X-Client":"sentinel","X-Box":"{bbox}"}}`, nil)
	pollOnce(t, d, aoi.Area{Active: true, Boxes: []aoi.BBox{box}})
	reqs := srv.requests()
	if len(reqs) != 1 || reqs[0].Method != http.MethodPost || reqs[0].URL != "/search" {
		t.Fatalf("requests = %+v", reqs)
	}
	if reqs[0].Body != `{"filter":{"lat":"42.5000","lon":"-85.0000"},"limit":10}` {
		t.Errorf("body = %s", reqs[0].Body)
	}
	if h := reqs[0].Header; h.Get("Content-Type") != "application/json" || h.Get("X-Client") != "sentinel" || h.Get("X-Box") != "40.0000,-90.0000,45.0000,-80.0000" {
		t.Errorf("headers = %v", h)
	}

	// A string body is sent verbatim after substitution, so numbers stay numbers.
	d = build(t, srv, "/search", `{"method":"POST","body":"{\"lat\":{lat},\"lon\":{lon}}"}`, nil)
	pollOnce(t, d, aoi.Area{Active: true, Boxes: []aoi.BBox{box}})
	if got := srv.requests()[1].Body; got != `{"lat":42.5000,"lon":-85.0000}` {
		t.Errorf("string body = %s", got)
	}
	if d.Health().Detail["method"] != "POST" {
		t.Errorf("health detail = %v", d.Health().Detail)
	}
}

func TestAuthKinds(t *testing.T) {
	srv := newServer(t)
	d := build(t, srv, "/", `{"auth":{"kind":"bearer"}}`, map[string]string{"token": "tok"})
	pollOnce(t, d, aoi.Area{})
	d = build(t, srv, "/", `{"auth":{"kind":"basic","username":"u"}}`, map[string]string{"password": "p"})
	pollOnce(t, d, aoi.Area{})
	d = build(t, srv, "/", `{"auth":{"kind":"header","headerName":"X-API-Key"}}`, map[string]string{"token": "key"})
	pollOnce(t, d, aoi.Area{})
	reqs := srv.requests()
	if len(reqs) != 3 {
		t.Fatalf("requests = %d", len(reqs))
	}
	if reqs[0].Header.Get("Authorization") != "Bearer tok" {
		t.Errorf("bearer: %v", reqs[0].Header)
	}
	if u, p, ok := (&http.Request{Header: reqs[1].Header}).BasicAuth(); !ok || u != "u" || p != "p" {
		t.Errorf("basic: %v", reqs[1].Header)
	}
	if reqs[2].Header.Get("X-API-Key") != "key" {
		t.Errorf("header: %v", reqs[2].Header)
	}
	if _, err := New(driver.Spec{Config: json.RawMessage(`{"url":"https://x/y","auth":{"kind":"bearer"},"decoder":{"options":` + decoderOptions + `}}`)}, driver.Deps{}); err == nil {
		t.Error("bearer without the token secret must be rejected")
	}
}

func TestOAuthClientCredentials(t *testing.T) {
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.FormValue("client_id") != "id" || r.FormValue("client_secret") != "sec" || r.FormValue("scope") != "read" {
			t.Errorf("bad token request %v", r.Form)
		}
		_, _ = w.Write([]byte(`{"access_token":"tok","expires_in":3600}`))
	}))
	defer tokenServer.Close()
	srv := newServer(t)
	d := build(t, srv, "/", `{"auth":{"kind":"oauth2_client_credentials","clientId":"id","tokenUrl":"`+tokenServer.URL+`","scopes":"read"}}`, map[string]string{"clientSecret": "sec"})
	pollOnce(t, d, aoi.Area{})
	if got := srv.requests()[0].Header.Get("Authorization"); got != "Bearer tok" {
		t.Errorf("Authorization = %q", got)
	}
}

func TestRequireAreaIdlesThenPolls(t *testing.T) {
	srv := newServer(t)
	d := build(t, srv, "/point/{lat}/{lon}/{radius_nm}", `{"intervalSec":3600,"requireArea":true}`, nil)
	sink := start(t, d)
	time.Sleep(100 * time.Millisecond)
	if srv.hits.Load() != 0 || !d.AreaStatus().Idle || !d.Health().Idle {
		t.Fatalf("expected no polls without an area, got %d idle=%v", srv.hits.Load(), d.AreaStatus().Idle)
	}
	st := d.SetAreaOfInterest(aoi.Area{Active: true, Clients: 1, Boxes: []aoi.BBox{box}, ExpiresAt: time.Now().Add(time.Minute)})
	if st.Idle || st.AreaSqDeg != 50 {
		t.Errorf("status = %+v", st)
	}
	if !waitFor(func() bool { return sink.count() == 3 }, 2*time.Second) {
		t.Fatalf("expected 3 observations after the area arrived, got %d", sink.count())
	}
	if u := srv.requests()[0].URL; u != "/point/42.5000/-85.0000/250" {
		t.Errorf("url = %q", u)
	}
	if st := d.SetAreaOfInterest(aoi.Area{Active: false}); !st.Idle {
		t.Errorf("inactive area should idle: %+v", st)
	}
}

func TestWithoutRequireAreaPollsImmediately(t *testing.T) {
	srv := newServer(t)
	d := build(t, srv, "/all", `{"intervalSec":3600}`, nil)
	sink := start(t, d)
	if !waitFor(func() bool { return sink.count() == 3 }, 2*time.Second) {
		t.Fatalf("expected an immediate global poll, got %d", sink.count())
	}
	if st := d.AreaStatus(); st.Idle || st.AreaSqDeg != aoi.GlobalArea {
		t.Errorf("status = %+v", st)
	}
}

func TestDedupeAcrossBoxes(t *testing.T) {
	srv := newServer(t)
	d := build(t, srv, "/box/{bbox}", "", nil)
	sink := pollOnce(t, d, aoi.Area{Active: true, Boxes: []aoi.BBox{
		{South: 10, West: 170, North: 20, East: 180},
		{South: 10, West: -180, North: 20, East: -170},
	}})
	reqs := srv.requests()
	if len(reqs) != 2 || reqs[0].URL != "/box/10.0000,170.0000,20.0000,180.0000" || reqs[1].URL != "/box/10.0000,-180.0000,20.0000,-170.0000" {
		t.Errorf("requests = %+v", reqs)
	}
	if sink.count() != 3 || sink.success[0] != 3 {
		t.Errorf("expected 3 deduplicated observations from 2 boxes, got %d (success %v)", sink.count(), sink.success)
	}
	if d.Health().Detail["polls"] != int64(0) { // poll called directly, not through the scheduler
		t.Errorf("detail = %v", d.Health().Detail)
	}
}

func TestDecodeErrorDeadLetters(t *testing.T) {
	srv := newServer(t)
	srv.body.Store(`{"ac": "not an array"}`)
	d := build(t, srv, "/", "", nil)
	sink := &memSink{}
	d.sink = sink
	err := d.poll(context.Background(), aoi.Area{})
	if err == nil || !strings.Contains(err.Error(), "parse response") {
		t.Fatalf("err = %v", err)
	}
	if len(sink.dead) != 1 || sink.dead[0] != "parse_error" || len(sink.success) != 0 {
		t.Errorf("dead = %v success = %v", sink.dead, sink.success)
	}
	if h := d.Health(); h.LastError == "" || h.Detail["lastError"] == "" {
		t.Errorf("health = %+v", h)
	}
	// A document without records is skipped, not dead-lettered, and the poll succeeds.
	srv.body.Store(`{"other": []}`)
	sink = pollOnce(t, d, aoi.Area{})
	if len(sink.dead) != 0 || len(sink.skipped) != 1 || sink.skipped[0] != "no_records" || len(sink.success) != 1 || sink.success[0] != 0 {
		t.Errorf("skipped = %v dead = %v success = %v", sink.skipped, sink.dead, sink.success)
	}
	if d.Health().LastError != "" {
		t.Errorf("last error should clear after a success: %q", d.Health().LastError)
	}
}

func TestHTTPErrorSurfacesInHealth(t *testing.T) {
	srv := newServer(t)
	srv.status.Store(http.StatusTooManyRequests)
	srv.body.Store("slow down")
	d := build(t, srv, "/", "", nil)
	sink := &memSink{}
	d.sink = sink
	err := d.poll(context.Background(), aoi.Area{})
	if err == nil || !strings.Contains(err.Error(), "429") {
		t.Fatalf("err = %v", err)
	}
	if h := d.Health(); !strings.Contains(h.LastError, "429") || h.Connected {
		t.Errorf("health = %+v", h)
	}
	if sink.count() != 0 || len(sink.success) != 0 || len(sink.dead) != 0 {
		t.Errorf("sink = %+v", sink)
	}
}

func TestRateLimitHeaderSlowsPolling(t *testing.T) {
	srv := newServer(t)
	srv.remaining.Store(500)
	d := build(t, srv, "/", `{"intervalSec":10,"maxIntervalSec":60,"rateLimitHeader":"X-RateLimit-Remaining","creditFloor":200}`, nil)
	if st := d.AreaStatus(); st.CreditsRemaining != -1 || st.LowCredits {
		t.Errorf("credits should be unknown before the first poll: %+v", st)
	}
	pollOnce(t, d, aoi.Area{})
	if st := d.AreaStatus(); st.CreditsRemaining != 500 || st.LowCredits || st.EffectiveInterval != 10*time.Second {
		t.Errorf("plenty of quota: %+v", st)
	}
	srv.remaining.Store(150)
	pollOnce(t, d, aoi.Area{})
	if st := d.AreaStatus(); st.CreditsRemaining != 150 || !st.LowCredits || st.EffectiveInterval != 60*time.Second {
		t.Errorf("below the floor: %+v", st)
	}
	if d.Health().Detail["lowCredits"] != true {
		t.Errorf("health detail = %v", d.Health().Detail)
	}
	// The header on an error response counts too.
	srv.remaining.Store(10)
	srv.status.Store(http.StatusTooManyRequests)
	d.sink = &memSink{}
	_ = d.poll(context.Background(), aoi.Area{})
	if st := d.AreaStatus(); st.CreditsRemaining != 10 {
		t.Errorf("quota from the error response: %+v", st)
	}

	// Without rateLimitHeader the header is ignored.
	srv.status.Store(http.StatusOK)
	d = build(t, srv, "/", `{"intervalSec":10,"maxIntervalSec":60}`, nil)
	pollOnce(t, d, aoi.Area{})
	if st := d.AreaStatus(); st.CreditsRemaining != -1 || st.LowCredits {
		t.Errorf("header must be ignored when not configured: %+v", st)
	}
}

func TestLargeAreaStretchesInterval(t *testing.T) {
	srv := newServer(t)
	d := build(t, srv, "/", `{"intervalSec":10,"maxIntervalSec":60,"maxAreaSqDeg":100}`, nil)
	if st := d.SetAreaOfInterest(aoi.Area{Active: true, Boxes: []aoi.BBox{box}}); st.EffectiveInterval != 10*time.Second {
		t.Errorf("50 sq deg: %+v", st)
	}
	if st := d.SetAreaOfInterest(aoi.Area{Active: true, Boxes: []aoi.BBox{{South: 0, North: 30, West: 0, East: 10}}}); st.EffectiveInterval != 30*time.Second {
		t.Errorf("300 sq deg: %+v", st)
	}
	if st := d.SetAreaOfInterest(aoi.Area{Active: true, Boxes: []aoi.BBox{{South: 0, North: 50, West: 0, East: 50}}}); st.EffectiveInterval != 60*time.Second {
		t.Errorf("2500 sq deg capped: %+v", st)
	}
}

func TestNewRejectsBadConfigs(t *testing.T) {
	cases := map[string]string{
		"no url":            `{"decoder":{"options":` + decoderOptions + `}}`,
		"relative url":      `{"url":"/v2/point","decoder":{"options":` + decoderOptions + `}}`,
		"no decoder":        `{"url":"https://api.example.com/x"}`,
		"no fields":         `{"url":"https://api.example.com/x","decoder":{"options":{"records":"ac"}}}`,
		"no longitude":      `{"url":"https://api.example.com/x","decoder":{"options":{"fields":{"latitude":"lat"}}}}`,
		"bad method":        `{"url":"https://api.example.com/x","method":"PUT","decoder":{"options":` + decoderOptions + `}}`,
		"bad auth kind":     `{"url":"https://api.example.com/x","auth":{"kind":"magic"},"decoder":{"options":` + decoderOptions + `}}`,
		"basic no password": `{"url":"https://api.example.com/x","auth":{"kind":"basic","username":"u"},"decoder":{"options":` + decoderOptions + `}}`,
		"bad body":          `{"url":"https://api.example.com/x","method":"POST","body":{"a":1},"decoder":{"options":{"fields":{"latitude":"lat","longitude":"lon"}}}}x`,
	}
	for name, cfg := range cases {
		if _, err := New(driver.Spec{FeedID: "f", Config: json.RawMessage(cfg)}, driver.Deps{Logger: zap.NewNop()}); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
	d, err := New(driver.Spec{FeedID: "f", Config: json.RawMessage(`{"url":"https://api.example.com/v2/point/{lat}/{lon}/{radius_nm}","decoder":{"options":` + decoderOptions + `}}`)}, driver.Deps{Logger: zap.NewNop()})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := d.(aoi.AreaAware); !ok {
		t.Error("driver must be area-aware")
	}
	if st := d.(*Driver).AreaStatus(); st.EffectiveInterval != defaultInterval || st.Idle {
		t.Errorf("defaults: %+v", st)
	}
}

const exampleConfig = `{"url":"https://api.example.com/v2/point/{lat}/{lon}/{radius_nm}","intervalSec":15,"requireArea":true,"decoder":{"options":{"records":"ac","identityType":"ICAO24","fields":{"id":"hex","latitude":"lat","longitude":"lon","altitude":"alt_baro","speed":"gs","heading":"track","callsign":"flight"}}}}`

func compileSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(schema))
	if err != nil {
		t.Fatal(err)
	}
	c := jsonschema.NewCompiler()
	const url = "https://sentinel.local/schemas/httpjson.json"
	if err := c.AddResource(url, doc); err != nil {
		t.Fatal(err)
	}
	sch, err := c.Compile(url)
	if err != nil {
		t.Fatal(err)
	}
	return sch
}

func TestSchemaValidatesExampleAndRejectsBadConfigs(t *testing.T) {
	sch := compileSchema(t)
	validate := func(cfg string) error {
		inst, err := jsonschema.UnmarshalJSON(strings.NewReader(cfg))
		if err != nil {
			t.Fatal(err)
		}
		return sch.Validate(inst)
	}
	if err := validate(exampleConfig); err != nil {
		t.Errorf("example config rejected: %v", err)
	}
	good := []string{
		`{"url":"https://x/y","method":"POST","body":{"q":"{bbox}"},"headers":{"X-A":"b"},"auth":{"kind":"basic","username":"u"},"decoder":{"options":{"fields":{"latitude":"lat","longitude":"lon"}}},"rateLimitHeader":"X-RateLimit-Remaining","creditFloor":50}`,
		`{"url":"https://x/y","body":"{\"a\":{lat}}","auth":{"kind":"oauth2_client_credentials","tokenUrl":"https://x/token","clientId":"id"},"decoder":{"options":{"fields":{"latitude":"lat","longitude":"lon"}}}}`,
	}
	for _, cfg := range good {
		if err := validate(cfg); err != nil {
			t.Errorf("%s rejected: %v", cfg, err)
		}
	}
	bad := []string{
		`{}`,
		`{"decoder":{"options":{"fields":{"latitude":"lat","longitude":"lon"}}}}`,
		`{"url":"https://x/y"}`,
		`{"url":"https://x/y","decoder":{}}`,
		`{"url":"https://x/y","decoder":{"options":{"records":"ac"}}}`,
		`{"url":"https://x/y","decoder":{"options":{"fields":{"latitude":"lat"}}}}`,
		`{"url":"https://x/y","method":"PUT","decoder":{"options":{"fields":{"latitude":"lat","longitude":"lon"}}}}`,
		`{"url":"https://x/y","intervalSec":0,"decoder":{"options":{"fields":{"latitude":"lat","longitude":"lon"}}}}`,
		`{"url":"https://x/y","headers":{"X-A":1},"decoder":{"options":{"fields":{"latitude":"lat","longitude":"lon"}}}}`,
		`{"url":"https://x/y","auth":{"kind":"basic"},"decoder":{"options":{"fields":{"latitude":"lat","longitude":"lon"}}}}`,
		`{"url":"https://x/y","auth":{"kind":"header"},"decoder":{"options":{"fields":{"latitude":"lat","longitude":"lon"}}}}`,
		`{"url":"https://x/y","auth":{"kind":"oauth2_client_credentials"},"decoder":{"options":{"fields":{"latitude":"lat","longitude":"lon"}}}}`,
		`{"url":"https://x/y","auth":{"kind":"magic"},"decoder":{"options":{"fields":{"latitude":"lat","longitude":"lon"}}}}`,
	}
	for _, cfg := range bad {
		if err := validate(cfg); err == nil {
			t.Errorf("%s should be rejected", cfg)
		}
	}
}
