package client

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/sentinel/go-common/geo"
	"github.com/sentinel/go-common/terrain"
	ingestv1 "github.com/sentinel/proto-gen/sentinel/ingest/v1"
	sentinelv1 "github.com/sentinel/proto-gen/sentinel/v1"

	"github.com/sentinel/sensor-sim/internal/config"
	"github.com/sentinel/sensor-sim/internal/detect"
	"github.com/sentinel/sensor-sim/internal/eoir"
	"github.com/sentinel/sensor-sim/internal/radar"
	"github.com/sentinel/sensor-sim/internal/scenario"
)

// waitTimeout bounds every wait in these tests; nothing should come near it.
const waitTimeout = 10 * time.Second

// ---------------------------------------------------------------- fake server

type got struct {
	stream int
	r      *ingestv1.Report
}

type bidi = grpc.BidiStreamingServer[ingestv1.StreamReportsRequest, ingestv1.StreamReportsResponse]

// fake is an IngestService a test scripts call by call.
type fake struct {
	ingestv1.UnimplementedIngestServiceServer

	mu           sync.Mutex
	sessionCalls int
	upsertCalls  int
	streams      int
	upserts      []*sentinelv1.Emplacement
	auth         []string

	session func(call int) (*ingestv1.GetSessionResponse, error)
	upsert  func(call int, e *sentinelv1.Emplacement) (*ingestv1.UpsertEmplacementResponse, error)
	handler func(s *srvStream) error // nil: ack every report OK

	reports chan got
}

func newFake() *fake {
	return &fake{reports: make(chan got, 1<<14)}
}

func (f *fake) record(ctx context.Context) {
	md, _ := metadata.FromIncomingContext(ctx)
	f.mu.Lock()
	f.auth = md.Get("authorization")
	f.mu.Unlock()
}

func (f *fake) authorization() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.auth...)
}

func (f *fake) counts() (session, upsert, streams int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sessionCalls, f.upsertCalls, f.streams
}

func (f *fake) GetSession(ctx context.Context, _ *ingestv1.GetSessionRequest) (*ingestv1.GetSessionResponse, error) {
	f.record(ctx)
	f.mu.Lock()
	f.sessionCalls++
	n, h := f.sessionCalls, f.session
	f.mu.Unlock()
	if h == nil {
		return &ingestv1.GetSessionResponse{FeedId: "feed-1", SenderManagedEmplacements: true}, nil
	}
	return h(n)
}

func (f *fake) UpsertEmplacement(ctx context.Context, req *ingestv1.UpsertEmplacementRequest) (*ingestv1.UpsertEmplacementResponse, error) {
	f.record(ctx)
	f.mu.Lock()
	f.upsertCalls++
	n, h := f.upsertCalls, f.upsert
	f.upserts = append(f.upserts, req.GetEmplacement())
	f.mu.Unlock()
	if h == nil {
		e := proto.Clone(req.GetEmplacement()).(*sentinelv1.Emplacement)
		e.Id = "emp-1"
		e.Active = true
		return &ingestv1.UpsertEmplacementResponse{Emplacement: e}, nil
	}
	return h(n, req.GetEmplacement())
}

func (f *fake) StreamReports(stream bidi) error {
	f.record(stream.Context())
	f.mu.Lock()
	f.streams++
	s := &srvStream{f: f, n: f.streams, bidi: stream}
	h := f.handler
	f.mu.Unlock()
	if h == nil {
		return s.ackAll()
	}
	return h(s)
}

type srvStream struct {
	f *fake
	n int
	bidi
}

// recv takes the next report and publishes it to the test.
func (s *srvStream) recv() (*ingestv1.Report, error) {
	req, err := s.Recv()
	if err != nil {
		return nil, err
	}
	select {
	case s.f.reports <- got{stream: s.n, r: req.GetReport()}:
	case <-s.Context().Done():
		return nil, s.Context().Err()
	}
	return req.GetReport(), nil
}

func (s *srvStream) ack(r *ingestv1.Report, st ingestv1.AckStatus, reason string) error {
	return s.Send(&ingestv1.StreamReportsResponse{Ack: &ingestv1.Ack{ClientRef: r.GetClientRef(), Status: st, Reason: reason}})
}

func (s *srvStream) ackAll() error {
	for {
		r, err := s.recv()
		if err != nil {
			return nil
		}
		if err := s.ack(r, ingestv1.AckStatus_ACK_STATUS_OK, ""); err != nil {
			return nil
		}
	}
}

// next returns the next report the server took.
func (f *fake) next(t *testing.T) got {
	t.Helper()
	select {
	case g := <-f.reports:
		return g
	case <-time.After(waitTimeout):
		t.Fatal("timed out waiting for a report")
		return got{}
	}
}

// until returns the first report that matches, and the ones before it. It
// fails once waitTimeout has passed overall, so a client that keeps
// streaming without ever sending the awaited report cannot hang the test.
func (f *fake) until(t *testing.T, what string, match func(got) bool) (got, []got) {
	t.Helper()
	deadline := time.After(waitTimeout)
	var skipped []got
	for {
		select {
		case g := <-f.reports:
			if match(g) {
				return g, skipped
			}
			skipped = append(skipped, g)
		case <-deadline:
			t.Fatalf("timed out waiting for %s", what)
			return got{}, nil
		}
	}
}

// ---------------------------------------------------------------- harness

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

type harness struct {
	t     *testing.T
	f     *fake
	c     *Client
	clock *fakeClock
	logs  *syncBuf
	acks  chan *ingestv1.Ack
	cfg   config.Config

	cancel context.CancelFunc
	done   chan error
}

// testSite is where the test sensors stand.
var testSite = geo.LLA{Lat: 50, Lon: -5, Alt: 12}

// testScenario has three vehicles 2 to 4 km west of the site, parked (one
// waypoint each), so every scan sees the same truth.
func testScenario() scenario.Scenario {
	return scenario.Scenario{
		Epoch: time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC),
		Targets: []scenario.Target{
			{ID: "v1", Kind: "vehicle", Waypoints: []scenario.LatLon{{Lat: 50, Lon: -5.03}}},
			{ID: "v2", Kind: "vehicle", Waypoints: []scenario.LatLon{{Lat: 50.01, Lon: -5.04}}},
			{ID: "v3", Kind: "vehicle", Waypoints: []scenario.LatLon{{Lat: 49.99, Lon: -5.05}}},
		},
	}
}

func testConfig() config.Config {
	return config.Config{
		Connection: config.Connection{Addr: "passthrough:///bufnet", Token: testToken, Insecure: true},
		Sensor: config.Sensor{
			Kind: config.KindRadar, Name: "Test radar", Lat: testSite.Lat, Lon: testSite.Lon, Alt: f64(testSite.Alt),
			Boresight: 270, Width: 360, RangeKm: 10, MinRangeKm: 1,
			Rate: 100, Pd: 1, SigmaAzDeg: 0.5, SigmaElDeg: 0.5, SigmaRangeM: 10,
			HeartbeatInterval: 5 * time.Second, Band: "VISIBLE",
		},
		Scenario: testScenario(),
		Seed:     42,
	}
}

// eoirConfig turns cfg into a camera looking west over the targets, mounted
// 6 m above flat ground at 0 m.
func eoirConfig(c *config.Config) {
	c.Sensor.Kind = config.KindEOIR
	c.Sensor.Name = "Test camera"
	c.Sensor.Alt = nil
	c.Sensor.HeightAboveGroundM = f64(6)
	c.Sensor.Width = 60
	c.Sensor.RangeKm = 8
	c.Sensor.MinRangeKm = 0
	c.Terrain.CacheDir = "/unused"
}

func f64(v float64) *float64 { return &v }

// testSource builds what main would for cfg, on flat ground at 0 m: the
// radar with no terrain, the camera with a static one.
func testSource(cfg config.Config) Source {
	site := geo.LLA{Lat: cfg.Sensor.Lat, Lon: cfg.Sensor.Lon}
	if cfg.Sensor.Alt != nil {
		site.Alt = *cfg.Sensor.Alt
	}
	if h := cfg.Sensor.HeightAboveGroundM; h != nil {
		site.Alt = *h // the ground is at 0 m
	}
	var ter *terrain.Service
	var det detect.Detector
	if cfg.Sensor.Kind == config.KindEOIR {
		ter = terrain.NewStatic(func(float64, float64) float64 { return 0 })
		det = eoir.New(cfg.Sensor, site, ter, cfg.Seed)
	} else {
		det = radar.New(cfg.Sensor, site, cfg.Seed)
	}
	sc := cfg.Scenario
	return Source{
		Site:     site,
		Truth:    func(ctx context.Context, now time.Time) ([]scenario.Truth, error) { return sc.TruthAt(ctx, ter, now) },
		Detector: det,
	}
}

func newHarness(t *testing.T, f *fake, mut func(*config.Config, *Options)) *harness {
	return newHarnessSrc(t, f, mut, nil)
}

// newHarnessSrc is newHarness with a hook that can change the source.
func newHarnessSrc(t *testing.T, f *fake, mut func(*config.Config, *Options), srcMut func(*Source)) *harness {
	t.Helper()
	dial := serveBufconn(t, f)
	h := &harness{
		t: t, f: f,
		clock: &fakeClock{t: time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)},
		logs:  &syncBuf{},
		acks:  make(chan *ingestv1.Ack, 1<<14),
		cfg:   testConfig(),
	}
	opts := Options{
		Logger:        slog.New(slog.NewTextHandler(h.logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
		DialOptions:   []grpc.DialOption{dial},
		Now:           h.clock.Now,
		Jitter:        func() float64 { return 0.5 },
		BackoffMin:    time.Millisecond,
		BackoffMax:    5 * time.Millisecond,
		StatsEvery:    time.Hour,
		RetryDelay:    time.Millisecond,
		RateLimitHold: 10 * time.Second,
		GraceWindow:   5 * time.Second,
		OnAck: func(a *ingestv1.Ack) {
			select {
			case h.acks <- a:
			default:
			}
		},
	}
	if mut != nil {
		mut(&h.cfg, &opts)
	}
	src := testSource(h.cfg)
	if srcMut != nil {
		srcMut(&src)
	}
	c, err := New(h.cfg, src, opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	h.c = c
	t.Cleanup(func() {
		h.stop()
		if strings.Contains(h.logs.String(), testToken) {
			t.Errorf("the token was logged:\n%s", h.logs.String())
		}
	})
	return h
}

func (h *harness) start() {
	ctx, cancel := context.WithCancel(context.Background())
	h.cancel = cancel
	h.done = make(chan error, 1)
	go func() { h.done <- h.c.Run(ctx) }()
}

// stop cancels the run and returns what Run returned.
func (h *harness) stop() error {
	if h.cancel == nil {
		return nil
	}
	h.cancel()
	return h.wait()
}

// wait returns what Run returned, without cancelling it.
func (h *harness) wait() error {
	h.t.Helper()
	select {
	case err := <-h.done:
		h.done <- err // so a later stop sees it too
		return err
	case <-time.After(waitTimeout):
		h.t.Fatal("Run did not return")
		return nil
	}
}

// ackWhere waits for an ack the client has processed that matches.
func (h *harness) ackWhere(match func(*ingestv1.Ack) bool) *ingestv1.Ack {
	h.t.Helper()
	deadline := time.After(waitTimeout)
	for {
		select {
		case a := <-h.acks:
			if match(a) {
				return a
			}
		case <-deadline:
			h.t.Fatal("timed out waiting for an ack")
			return nil
		}
	}
}

// waitFor polls a condition on the client's state until it holds.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(waitTimeout)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

func isObs(r *ingestv1.Report) bool   { return r.GetObservation() != nil }
func isState(r *ingestv1.Report) bool { return r.GetEmplacementState() != nil }

// upserted waits for the run to stop and returns the one upserted emplacement.
func (h *harness) upserted() *sentinelv1.Emplacement {
	h.t.Helper()
	if err := h.stop(); err != nil {
		h.t.Fatalf("Run after cancel = %v", err)
	}
	h.f.mu.Lock()
	defer h.f.mu.Unlock()
	if len(h.f.upserts) != 1 {
		h.t.Fatalf("upserts = %d, want 1", len(h.f.upserts))
	}
	return h.f.upserts[0]
}

// ---------------------------------------------------------------- 1. upsert + stream

func TestUpsertThenStreamObservations(t *testing.T) {
	f := newFake()
	f.session = func(int) (*ingestv1.GetSessionResponse, error) {
		return &ingestv1.GetSessionResponse{FeedId: "feed-1", SenderManagedEmplacements: true, Exercise: true}, nil
	}
	h := newHarness(t, f, nil)
	h.start()

	first := f.next(t)
	if !isState(first.r) || first.r.GetEmplacementId() != "emp-1" {
		t.Fatalf("the first report is %v, want the emplacement's heartbeat", first.r)
	}
	refs := map[string]bool{first.r.GetClientRef(): true}
	for n := 0; n < 20; {
		g := f.next(t)
		if isState(g.r) {
			continue
		}
		if g.r.GetEmplacementId() != "emp-1" {
			t.Fatalf("report emplacement_id = %q, want the upserted id", g.r.GetEmplacementId())
		}
		if refs[g.r.GetClientRef()] || g.r.GetClientRef() == "" {
			t.Fatalf("client_ref %q repeated or empty", g.r.GetClientRef())
		}
		refs[g.r.GetClientRef()] = true
		o := g.r.GetObservation()
		if o.GetPolar().GetRangeM() <= 0 || o.GetPolar().GetDirectionOnly() || o.GetPlatform().GetRadar() == nil || o.GetPosition() != nil {
			t.Fatalf("radar observation %v, want a polar measurement with range and a radar detail", o)
		}
		n++
	}
	for i := 0; i < 10; i++ {
		h.ackWhere(func(a *ingestv1.Ack) bool { return a.GetStatus() == ingestv1.AckStatus_ACK_STATUS_OK })
	}
	if st := h.c.Stats(); st.OK < 10 || st.Sent < 20 || st.Reconnects != 0 {
		t.Fatalf("stats = %+v", st)
	}

	e := h.upserted()
	want := &sentinelv1.Emplacement{
		Name: "Test radar", Description: "sensor-sim radar",
		SensorType: sentinelv1.SensorType_SENSOR_TYPE_RADAR, Mobility: sentinelv1.Mobility_MOBILITY_FIXED,
		Position:         &sentinelv1.Coordinate{Latitude: 50, Longitude: -5, AltitudeMeters: proto.Float64(12)},
		BoresightDeg:     270,
		AzimuthReference: sentinelv1.AzimuthReference_AZIMUTH_REFERENCE_TRUE_NORTH,
		Coverage:         &sentinelv1.Coverage{MinRangeM: 1000, MaxRangeM: 10000, AzimuthWidthDeg: 360},
		Exercise:         true, // the session's exercise flag
	}
	if !proto.Equal(e, want) {
		t.Fatalf("upserted %v\nwant %v", e, want)
	}
	if a := f.authorization(); len(a) != 1 || a[0] != "Bearer "+testToken {
		t.Fatalf("authorization metadata = %q", a)
	}
}

// A camera's emplacement is an EO_IR sensor with its mounting height, the
// resolved site altitude and its elevation window.
func TestUpsertEOIRCarriesHeightAboveGround(t *testing.T) {
	f := newFake()
	h := newHarness(t, f, func(c *config.Config, _ *Options) {
		eoirConfig(c)
		c.Sensor.ElevationMaxDeg = f64(40)
	})
	h.start()
	f.next(t)
	e := h.upserted()
	want := &sentinelv1.Emplacement{
		Name: "Test camera", Description: "sensor-sim EO/IR camera",
		SensorType: sentinelv1.SensorType_SENSOR_TYPE_EO_IR, Mobility: sentinelv1.Mobility_MOBILITY_FIXED,
		Position:         &sentinelv1.Coordinate{Latitude: 50, Longitude: -5, AltitudeMeters: proto.Float64(6)},
		BoresightDeg:     270,
		AzimuthReference: sentinelv1.AzimuthReference_AZIMUTH_REFERENCE_TRUE_NORTH,
		Coverage: &sentinelv1.Coverage{
			MaxRangeM: 8000, AzimuthWidthDeg: 60,
			ElevationMinDeg: proto.Float64(config.DefaultCameraElevMinDeg), ElevationMaxDeg: proto.Float64(40),
		},
		HeightAboveGroundM: proto.Float64(6),
	}
	if !proto.Equal(e, want) {
		t.Fatalf("upserted %v\nwant %v", e, want)
	}
}

// A radar sends only the elevation limits it was given.
func TestUpsertRadarElevationLimitsOnlyWhenSet(t *testing.T) {
	f := newFake()
	h := newHarness(t, f, func(c *config.Config, _ *Options) { c.Sensor.ElevationMaxDeg = f64(30) })
	h.start()
	f.next(t)
	cv := h.upserted().GetCoverage()
	if cv.ElevationMinDeg != nil || cv.GetElevationMaxDeg() != 30 {
		t.Fatalf("coverage %v, want only elevation_max_deg 30", cv)
	}
}

func TestEOIRSendsDirectionOnlyObservations(t *testing.T) {
	f := newFake()
	h := newHarness(t, f, func(c *config.Config, _ *Options) { eoirConfig(c) })
	h.start()
	if first := f.next(t); !isState(first.r) {
		t.Fatalf("the first report is %v, want the heartbeat", first.r)
	}
	seen := map[string]bool{}
	for n := 0; n < 9; {
		g := f.next(t)
		if isState(g.r) {
			continue
		}
		o := g.r.GetObservation()
		p := o.GetPolar()
		if !p.GetDirectionOnly() || p.GetRangeM() != 0 || p.ElevationDeg == nil || o.GetPosition() != nil {
			t.Fatalf("camera observation polar %v position %v, want direction only with an elevation and no position", p, o.GetPosition())
		}
		if o.GetPlatform().GetEoir() == nil || o.GetPlatform().GetRadar() != nil {
			t.Fatalf("camera observation platform %v, want an EO/IR detail", o.GetPlatform())
		}
		if g.r.GetEmplacementId() != "emp-1" {
			t.Fatalf("emplacement_id %q", g.r.GetEmplacementId())
		}
		seen[o.GetSourceEntityId()] = true
		n++
	}
	if len(seen) != 3 {
		t.Fatalf("saw %v, want the three targets", seen)
	}
}

// The upserted emplacement inherits the connection's exercise flag: the
// service keeps the flag as sent, and the emplacement's own SENSOR entity,
// which the outbound exercise policy checks, takes it from the row.
func TestUpsertCarriesConnectionExerciseFlag(t *testing.T) {
	for _, exercise := range []bool{false, true} {
		t.Run(fmt.Sprintf("exercise=%v", exercise), func(t *testing.T) {
			f := newFake()
			f.session = func(int) (*ingestv1.GetSessionResponse, error) {
				return &ingestv1.GetSessionResponse{FeedId: "feed-1", SenderManagedEmplacements: true, Exercise: exercise}, nil
			}
			h := newHarness(t, f, nil)
			h.start()
			f.next(t)
			if e := h.upserted(); e.GetExercise() != exercise {
				t.Fatalf("upserted %v, want exercise=%v", e, exercise)
			}
		})
	}
}

// ---------------------------------------------------------------- 2. emplacement resolution

// sensor-sim always creates its own emplacement, so a connection that does
// not let senders manage emplacements is a configuration error.
func TestNotSenderManagedIsFatal(t *testing.T) {
	f := newFake()
	f.session = func(int) (*ingestv1.GetSessionResponse, error) {
		return &ingestv1.GetSessionResponse{FeedId: "feed-1", Emplacements: []*sentinelv1.Emplacement{{Id: "A", Active: true}}}, nil
	}
	h := newHarness(t, f, nil)
	h.start()
	err := h.wait()
	if ExitCode(err) != ExitFatal || !strings.Contains(err.Error(), "senderManagedEmplacements") {
		t.Fatalf("Run = %v (exit %d), want exit %d naming senderManagedEmplacements", err, ExitCode(err), ExitFatal)
	}
	if _, upserts, streams := f.counts(); upserts != 0 || streams != 0 {
		t.Fatalf("upserts %d streams %d, want none", upserts, streams)
	}
}

// ---------------------------------------------------------------- 3. heartbeats

// Every stream begins with the emplacement's heartbeat, the reconnected one too.
func TestHeartbeatFirstOnEveryStream(t *testing.T) {
	f := newFake()
	f.handler = func(s *srvStream) error {
		if s.n > 1 {
			return s.ackAll()
		}
		obs := 0
		for obs < 3 {
			r, err := s.recv()
			if err != nil {
				return err
			}
			if isObs(r) {
				obs++
			}
			_ = s.ack(r, ingestv1.AckStatus_ACK_STATUS_OK, "")
		}
		return status.Error(codes.Unavailable, "connection moving: reconnect")
	}
	h := newHarness(t, f, func(c *config.Config, _ *Options) { c.Sensor.HeartbeatInterval = 7 * time.Second })
	h.start()

	checkState := func(g got) {
		t.Helper()
		if !isState(g.r) {
			t.Fatalf("stream %d began with %v, want an EmplacementState", g.stream, g.r)
		}
		st := g.r.GetEmplacementState()
		if g.r.GetEmplacementId() != "emp-1" || st.GetEmplacementId() != "emp-1" || st.GetHeartbeatIntervalSec() != 7 {
			t.Fatalf("state %v (report emplacement %q), want emp-1 declaring 7 s", st, g.r.GetEmplacementId())
		}
		if p := st.GetPosition(); p.GetLatitude() != 50 || p.GetLongitude() != -5 || p.GetAltitudeMeters() != 12 {
			t.Fatalf("state position %v, want the site", p)
		}
	}
	first := f.next(t)
	if first.stream != 1 {
		t.Fatalf("first report on stream %d", first.stream)
	}
	checkState(first)
	g, _ := f.until(t, "a second stream", func(g got) bool { return g.stream != 1 })
	if g.stream != 2 {
		t.Fatalf("reconnected on stream %d", g.stream)
	}
	checkState(g)
	logs := h.logs.String()
	if !strings.Contains(logs, "code=Unavailable") || !strings.Contains(logs, "connection moving") {
		t.Fatalf("the retry cause was not logged:\n%s", logs)
	}
	if st := h.c.Stats(); st.Reconnects != 1 {
		t.Fatalf("reconnects = %d, want 1", st.Reconnects)
	}
}

// A scan whose terrain work outlasts the scan timeout is skipped and
// counted; heartbeats carry on.
func TestSlowTerrainSkipsTheScan(t *testing.T) {
	f := newFake()
	var sawDeadline sync.Once
	deadlined := make(chan struct{})
	h := newHarnessSrc(t, f, func(_ *config.Config, o *Options) {
		o.ScanTimeout = 5 * time.Millisecond
		o.HeartbeatEvery = 10 * time.Millisecond
	}, func(s *Source) {
		s.Truth = func(ctx context.Context, _ time.Time) ([]scenario.Truth, error) {
			if _, ok := ctx.Deadline(); ok {
				sawDeadline.Do(func() { close(deadlined) })
			}
			<-ctx.Done()
			return nil, fmt.Errorf("terrain: %w", ctx.Err())
		}
	})
	h.start()
	f.next(t)
	select {
	case <-deadlined:
	case <-time.After(waitTimeout):
		t.Fatal("the scan's context had no deadline")
	}
	waitFor(t, "skipped scans", func() bool { return h.c.Stats().ScansSkipped >= 3 })
	for i := 0; i < 2; i++ {
		if g, _ := f.until(t, "a heartbeat", func(g got) bool { return isState(g.r) }); isObs(g.r) {
			t.Fatal("unreachable")
		}
	}
	if st := h.c.Stats(); st.Sent == 0 {
		t.Fatalf("stats = %+v", st)
	}
	if logs := h.logs.String(); strings.Count(logs, "scan skipped") != 1 {
		t.Fatalf("want one warning for a run of skipped scans:\n%s", logs)
	}
}

// ---------------------------------------------------------------- 4. brief retry

func TestUnknownEmplacementRetriedOnceInGraceWindow(t *testing.T) {
	for _, twice := range []bool{false, true} {
		name := map[bool]string{false: "retry accepted", true: "retry also rejected"}[twice]
		t.Run(name, func(t *testing.T) {
			f := newFake()
			var first *sentinelv1.Observation
			f.handler = func(s *srvStream) error {
				for {
					r, err := s.recv()
					if err != nil {
						return nil
					}
					st, reason := ingestv1.AckStatus_ACK_STATUS_OK, ""
					switch {
					case first == nil && isObs(r):
						first = r.GetObservation()
						st, reason = ingestv1.AckStatus_ACK_STATUS_REJECTED, "unknown_emplacement"
					case twice && isObs(r) && proto.Equal(r.GetObservation(), first):
						st, reason = ingestv1.AckStatus_ACK_STATUS_REJECTED, "unknown_emplacement"
					}
					if err := s.ack(r, st, reason); err != nil {
						return nil
					}
				}
			}
			h := newHarness(t, f, nil)
			h.start()
			orig, _ := f.until(t, "the first observation", func(g got) bool { return isObs(g.r) })
			again, _ := f.until(t, "the retry", func(g got) bool { return proto.Equal(g.r.GetObservation(), orig.r.GetObservation()) })
			if again.r.GetClientRef() == orig.r.GetClientRef() || again.stream != orig.stream {
				t.Fatalf("retry ref %q stream %d, original ref %q stream %d", again.r.GetClientRef(), again.stream, orig.r.GetClientRef(), orig.stream)
			}
			h.ackWhere(func(a *ingestv1.Ack) bool { return a.GetClientRef() == again.r.GetClientRef() })
			st := h.c.Stats()
			wantRejected := uint64(0)
			if twice {
				wantRejected = 1
			}
			if st.Retried != 1 || st.Rejected["unknown_emplacement"] != wantRejected {
				t.Fatalf("stats = %+v", st)
			}
		})
	}
}

func TestUnknownEmplacementOutsideGraceWindowCountsRejected(t *testing.T) {
	f := newFake()
	gate := make(chan struct{})
	f.handler = func(s *srvStream) error {
		r, err := s.recv()
		if err != nil {
			return nil
		}
		select {
		case <-gate:
		case <-s.Context().Done():
			return nil
		}
		if err := s.ack(r, ingestv1.AckStatus_ACK_STATUS_REJECTED, "unknown_emplacement"); err != nil {
			return nil
		}
		return s.ackAll()
	}
	h := newHarness(t, f, nil)
	h.start()
	f.next(t)
	h.clock.Advance(6 * time.Second)
	close(gate)
	h.ackWhere(func(a *ingestv1.Ack) bool { return a.GetReason() == "unknown_emplacement" })
	if st := h.c.Stats(); st.Retried != 0 || st.Rejected["unknown_emplacement"] != 1 {
		t.Fatalf("stats = %+v", st)
	}
}

func TestUpsertRetriedWhenSavedButNotCaughtUp(t *testing.T) {
	f := newFake()
	f.upsert = func(n int, e *sentinelv1.Emplacement) (*ingestv1.UpsertEmplacementResponse, error) {
		if n == 1 {
			return nil, status.Error(codes.Unavailable, "the emplacement was saved; retry once the service has caught up")
		}
		e = proto.Clone(e).(*sentinelv1.Emplacement)
		e.Id = "emp-2"
		return &ingestv1.UpsertEmplacementResponse{Emplacement: e}, nil
	}
	h := newHarness(t, f, nil)
	h.start()
	if g := f.next(t); g.r.GetEmplacementId() != "emp-2" {
		t.Fatalf("emplacement_id = %q", g.r.GetEmplacementId())
	}
	if _, upserts, _ := f.counts(); upserts != 2 {
		t.Fatalf("upserts = %d, want 2", upserts)
	}
	if !strings.Contains(h.logs.String(), "caught up") {
		t.Fatalf("retry cause not logged:\n%s", h.logs.String())
	}
}

// ---------------------------------------------------------------- 5. rate limits and ResourceExhausted

func TestRateLimitedAckHalvesRateForHoldThenRestores(t *testing.T) {
	f := newFake()
	f.handler = func(s *srvStream) error {
		r, err := s.recv()
		if err != nil {
			return nil
		}
		if err := s.ack(r, ingestv1.AckStatus_ACK_STATUS_REJECTED, "rate_limited"); err != nil {
			return nil
		}
		return s.ackAll()
	}
	h := newHarness(t, f, nil)
	h.start()
	if r := h.c.Rate(); r != 100 {
		t.Fatalf("initial rate = %v", r)
	}
	h.ackWhere(func(a *ingestv1.Ack) bool { return a.GetReason() == "rate_limited" })
	if r := h.c.Rate(); r != 50 {
		t.Fatalf("rate after rate_limited = %v, want 50", r)
	}
	waitFor(t, "the tick interval to double", func() bool { return h.c.TickInterval() == 20*time.Millisecond })
	// Still on the first stream: no disconnect.
	for i := 0; i < 5; i++ {
		if g := f.next(t); g.stream != 1 {
			t.Fatalf("report on stream %d after rate_limited", g.stream)
		}
	}
	h.clock.Advance(9 * time.Second)
	if r := h.c.Rate(); r != 50 {
		t.Fatalf("rate 9 s after rate_limited = %v, want 50", r)
	}
	h.clock.Advance(time.Second)
	if r := h.c.Rate(); r != 100 {
		t.Fatalf("rate 10 s after rate_limited = %v, want 100", r)
	}
	waitFor(t, "the tick interval to recover", func() bool { return h.c.TickInterval() == 10*time.Millisecond })
	if st := h.c.Stats(); st.Reconnects != 0 || st.Rejected["rate_limited"] != 1 {
		t.Fatalf("stats = %+v", st)
	}
}

func TestUpsertRateLimitedRetries(t *testing.T) {
	f := newFake()
	f.upsert = func(n int, e *sentinelv1.Emplacement) (*ingestv1.UpsertEmplacementResponse, error) {
		if n <= 2 {
			return nil, status.Error(codes.ResourceExhausted, "rate limited")
		}
		e = proto.Clone(e).(*sentinelv1.Emplacement)
		e.Id = "emp-3"
		return &ingestv1.UpsertEmplacementResponse{Emplacement: e}, nil
	}
	h := newHarness(t, f, nil)
	h.start()
	if g := f.next(t); g.r.GetEmplacementId() != "emp-3" {
		t.Fatalf("emplacement_id = %q", g.r.GetEmplacementId())
	}
	if _, upserts, _ := f.counts(); upserts != 3 {
		t.Fatalf("upserts = %d, want 3", upserts)
	}
}

func TestUpsertFatalErrors(t *testing.T) {
	cases := map[string]struct {
		err  error
		exit int
	}{
		"emplacement cap":    {status.Error(codes.ResourceExhausted, "this connection's senders manage as many emplacements as it allows"), ExitAuth},
		"not sender-managed": {status.Error(codes.PermissionDenied, "this connection does not let senders manage their emplacements"), ExitAuth},
		"above ceiling":      {status.Error(codes.PermissionDenied, "this connection is above the token's classification"), ExitAuth},
		"invalid":            {status.Error(codes.InvalidArgument, "coverage bad"), ExitFatal},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFake()
			f.upsert = func(int, *sentinelv1.Emplacement) (*ingestv1.UpsertEmplacementResponse, error) { return nil, tc.err }
			h := newHarness(t, f, nil)
			h.start()
			err := h.wait()
			if ExitCode(err) != tc.exit {
				t.Fatalf("Run = %v, exit %d, want %d", err, ExitCode(err), tc.exit)
			}
			if _, upserts, streams := f.counts(); upserts != 1 || streams != 0 {
				t.Fatalf("upserts %d streams %d, want 1 and 0", upserts, streams)
			}
		})
	}
}

func TestUpsertNameTakenSuggestsAWayOut(t *testing.T) {
	f := newFake()
	f.upsert = func(int, *sentinelv1.Emplacement) (*ingestv1.UpsertEmplacementResponse, error) {
		return nil, status.Error(codes.AlreadyExists, "an emplacement already has that name")
	}
	h := newHarness(t, f, nil)
	h.start()
	err := h.wait()
	if ExitCode(err) != ExitFatal {
		t.Fatalf("Run = %v, exit %d, want %d", err, ExitCode(err), ExitFatal)
	}
	for _, w := range []string{"already has that name", "--name"} {
		if !strings.Contains(err.Error(), w) {
			t.Errorf("error %q does not mention %q", err, w)
		}
	}
	if _, upserts, streams := f.counts(); upserts != 1 || streams != 0 {
		t.Fatalf("upserts %d streams %d, want 1 and 0", upserts, streams)
	}
}

func TestStreamResourceExhaustedReconnectsWithoutResend(t *testing.T) {
	f := newFake()
	f.handler = func(s *srvStream) error {
		if s.n > 1 {
			return s.ackAll()
		}
		for i := 0; i < 3; i++ {
			if _, err := s.recv(); err != nil {
				return err
			}
		}
		return status.Error(codes.ResourceExhausted, "grpc: received message larger than max (5000 vs. 4096)")
	}
	h := newHarness(t, f, nil)
	h.start()
	g, skipped := f.until(t, "a second stream", func(g got) bool { return g.stream != 1 })
	var before []*ingestv1.Report
	for _, s := range skipped {
		before = append(before, s.r)
	}
	after := []*ingestv1.Report{g.r}
	for len(after) < 9 {
		after = append(after, f.next(t).r)
	}
	for _, a := range after {
		for _, b := range before {
			if a.GetClientRef() == b.GetClientRef() || (isObs(a) && isObs(b) && proto.Equal(a.GetObservation(), b.GetObservation())) {
				t.Fatalf("report %q from the failed stream was resent", b.GetClientRef())
			}
		}
	}
	if !strings.Contains(h.logs.String(), "code=ResourceExhausted") {
		t.Fatalf("ResourceExhausted not logged:\n%s", h.logs.String())
	}
	if err := h.stop(); err != nil {
		t.Fatalf("ResourceExhausted must not be fatal: %v", err)
	}
}

func TestOversizeReportDroppedLocally(t *testing.T) {
	f := newFake()
	f.session = func(int) (*ingestv1.GetSessionResponse, error) {
		return &ingestv1.GetSessionResponse{FeedId: "feed-1", SenderManagedEmplacements: true, MaxMessageBytes: 64}, nil
	}
	h := newHarness(t, f, nil)
	h.start()
	waitFor(t, "oversize drops", func() bool { return h.c.Stats().DroppedOversize >= 6 })
	if err := h.stop(); err != nil {
		t.Fatal(err)
	}
	for len(f.reports) > 0 {
		if g := <-f.reports; isObs(g.r) {
			t.Fatalf("the server received an oversize observation: %v", g.r)
		}
	}
	if !strings.Contains(h.logs.String(), "max_message_bytes") {
		t.Fatalf("drop not logged:\n%s", h.logs.String())
	}
}

// ---------------------------------------------------------------- 6. auth

func TestAuthFailuresStopWithoutReconnect(t *testing.T) {
	for _, code := range []codes.Code{codes.Unauthenticated, codes.PermissionDenied} {
		t.Run("stream "+code.String(), func(t *testing.T) {
			f := newFake()
			f.handler = func(s *srvStream) error {
				for i := 0; i < 2; i++ {
					r, err := s.recv()
					if err != nil {
						return nil
					}
					_ = s.ack(r, ingestv1.AckStatus_ACK_STATUS_OK, "")
				}
				return status.Error(code, "the ingest token was revoked")
			}
			h := newHarness(t, f, nil)
			h.start()
			err := h.wait()
			if ExitCode(err) != ExitAuth {
				t.Fatalf("Run = %v, exit %d, want %d", err, ExitCode(err), ExitAuth)
			}
			if _, _, streams := f.counts(); streams != 1 {
				t.Fatalf("streams = %d, want 1 (no reconnect)", streams)
			}
		})
		t.Run("session "+code.String(), func(t *testing.T) {
			f := newFake()
			f.session = func(int) (*ingestv1.GetSessionResponse, error) { return nil, status.Error(code, "no") }
			h := newHarness(t, f, nil)
			h.start()
			if err := h.wait(); ExitCode(err) != ExitAuth {
				t.Fatalf("Run = %v, want exit %d", err, ExitAuth)
			}
			if sessions, _, _ := f.counts(); sessions != 1 {
				t.Fatalf("GetSession calls = %d, want 1", sessions)
			}
		})
	}
}

// ---------------------------------------------------------------- extras

func TestGetSessionUnavailableRetried(t *testing.T) {
	f := newFake()
	f.session = func(n int) (*ingestv1.GetSessionResponse, error) {
		if n < 3 {
			return nil, status.Error(codes.Unavailable, "the ingest service is still loading: retry")
		}
		return &ingestv1.GetSessionResponse{FeedId: "feed-1", SenderManagedEmplacements: true}, nil
	}
	h := newHarness(t, f, nil)
	h.start()
	f.next(t)
	if sessions, _, _ := f.counts(); sessions != 3 {
		t.Fatalf("GetSession calls = %d, want 3", sessions)
	}
	if !strings.Contains(h.logs.String(), "still loading") {
		t.Fatalf("retry cause not logged:\n%s", h.logs.String())
	}
}

func TestDeterministicForFixedSeed(t *testing.T) {
	run := func() []*sentinelv1.Observation {
		f := newFake()
		h := newHarness(t, f, func(c *config.Config, _ *Options) { c.Sensor.Pd = 0.7 })
		h.start()
		var out []*sentinelv1.Observation
		for len(out) < 30 {
			if g := f.next(t); isObs(g.r) {
				out = append(out, g.r.GetObservation())
			}
		}
		_ = h.stop()
		return out
	}
	a, b := run(), run()
	for i := range a {
		if !proto.Equal(a[i], b[i]) {
			t.Fatalf("observation %d differs between runs with the same seed:\n%v\n%v", i, a[i], b[i])
		}
	}
}

func TestStatsLogged(t *testing.T) {
	f := newFake()
	h := newHarness(t, f, func(_ *config.Config, o *Options) { o.StatsEvery = 5 * time.Millisecond })
	h.start()
	f.next(t)
	waitFor(t, "a stats line", func() bool { return strings.Contains(h.logs.String(), "msg=stats") })
	line := h.logs.String()
	for _, k := range []string{"sent=", "ok=", "rejected=", "dead_lettered=", "reconnects=", "dropped_oversize=", "scans_skipped="} {
		if !strings.Contains(line, k) {
			t.Fatalf("stats line lacks %s:\n%s", k, line)
		}
	}
}

func TestExitCode(t *testing.T) {
	if ExitCode(nil) != ExitOK {
		t.Fatal("nil")
	}
	if ExitCode(errors.New("x")) != ExitFatal {
		t.Fatal("plain error")
	}
	if ExitCode(&ExitError{Code: ExitAuth, Err: errors.New("x")}) != ExitAuth {
		t.Fatal("auth")
	}
}

func TestBackoff(t *testing.T) {
	j := 0.0
	b := newBackoff(500*time.Millisecond, 30*time.Second, func() float64 { return j })
	var got []time.Duration
	for i := 0; i < 9; i++ {
		got = append(got, b.next())
	}
	if got[0] != 500*time.Millisecond || got[8] != 30*time.Second*8/10 {
		// j=0 is the low edge (x0.8), clamped to the minimum.
		t.Fatalf("backoff with jitter 0: %v", got)
	}
	for i := 1; i < len(got); i++ {
		if got[i] < got[i-1] {
			t.Fatalf("backoff shrank: %v", got)
		}
	}
	j = 1
	for i := 0; i < 20; i++ {
		if d := b.next(); d > 30*time.Second || d < 500*time.Millisecond {
			t.Fatalf("backoff %v outside [0.5s,30s]", d)
		}
	}
	b.reset()
	if d := b.next(); d > 600*time.Millisecond {
		t.Fatalf("after reset: %v", d)
	}
}

// A fixed site heartbeats first on every stream and then every interval,
// declaring the interval it was given.
func TestFixedSensorHeartbeatsFirstAndThenEveryInterval(t *testing.T) {
	f := newFake()
	h := newHarness(t, f, func(c *config.Config, o *Options) {
		c.Sensor.HeartbeatInterval = 7 * time.Second
		o.HeartbeatEvery = 20 * time.Millisecond
	})
	h.start()
	first := f.next(t)
	if !isState(first.r) || first.r.GetEmplacementState().GetHeartbeatIntervalSec() != 7 || first.r.GetEmplacementId() != "emp-1" {
		t.Fatalf("first report = %v, want a heartbeat declaring 7 s", first.r)
	}
	if g, _ := f.until(t, "a second heartbeat", func(g got) bool { return isState(g.r) }); g.stream != 1 {
		t.Fatalf("the second heartbeat came on stream %d, want the same stream", g.stream)
	}
}

// After no_heartbeat (the server lost the emplacement's liveness: a replica
// move, a heartbeat lost) the CLI heartbeats at once and sends the detection again,
// once.
func TestNoHeartbeatSendsAStateAndRetriesTheFixOnce(t *testing.T) {
	f := newFake()
	var refused *sentinelv1.Observation
	f.handler = func(s *srvStream) error {
		for {
			r, err := s.recv()
			if err != nil {
				return nil
			}
			st, reason := ingestv1.AckStatus_ACK_STATUS_OK, ""
			if refused == nil && isObs(r) {
				refused = r.GetObservation()
				st, reason = ingestv1.AckStatus_ACK_STATUS_REJECTED, "no_heartbeat"
			}
			if err := s.ack(r, st, reason); err != nil {
				return nil
			}
		}
	}
	h := newHarness(t, f, func(_ *config.Config, o *Options) { o.HeartbeatEvery = time.Hour })
	h.start()
	f.until(t, "the stream's first heartbeat", func(g got) bool { return isState(g.r) })
	orig, _ := f.until(t, "the first observation", func(g got) bool { return isObs(g.r) })
	f.until(t, "a heartbeat after the refusal", func(g got) bool { return isState(g.r) })
	again, _ := f.until(t, "the retried detection", func(g got) bool {
		return isObs(g.r) && proto.Equal(g.r.GetObservation(), orig.r.GetObservation())
	})
	if again.r.GetClientRef() == orig.r.GetClientRef() {
		t.Fatal("the retry reused the client_ref")
	}
	h.ackWhere(func(a *ingestv1.Ack) bool { return a.GetClientRef() == again.r.GetClientRef() })
	if st := h.c.Stats(); st.Rejected["no_heartbeat"] != 1 || st.Retried != 1 {
		t.Fatalf("stats = %+v", st)
	}
}

// Many refusals in a moment send one heartbeat, not one each.
func TestNoHeartbeatKicksAreDebounced(t *testing.T) {
	clock := &fakeClock{t: time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)}
	c, err := New(testConfig(), testSource(testConfig()), Options{Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Now: clock.Now, HeartbeatKickMin: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	a := &activeStream{gen: 1}
	refused := &ingestv1.Ack{ClientRef: "x", Status: ingestv1.AckStatus_ACK_STATUS_REJECTED, Reason: "no_heartbeat"}
	c.handleAck(a, refused)
	if !c.takeStateKick() {
		t.Fatal("no heartbeat asked for after a refusal")
	}
	c.handleAck(a, refused)
	if c.takeStateKick() {
		t.Fatal("a second refusal within a second asked for another")
	}
	clock.Advance(time.Second)
	c.handleAck(a, refused)
	if !c.takeStateKick() {
		t.Fatal("a refusal a second later asked for none")
	}
	if st := c.Stats(); st.Rejected["no_heartbeat"] != 3 {
		t.Fatalf("rejected = %v", st.Rejected)
	}
}

// Heartbeats are never rate limited, so the rate warning counts observations only.
func TestRateWarningCountsObservationsOnly(t *testing.T) {
	f := newFake()
	f.session = func(int) (*ingestv1.GetSessionResponse, error) {
		return &ingestv1.GetSessionResponse{FeedId: "feed-1", SenderManagedEmplacements: true, MaxMessagesPerSec: 300}, nil
	}
	h := newHarness(t, f, nil) // 100 Hz x 3 targets
	h.start()
	f.next(t)
	if strings.Contains(h.logs.String(), "expect rate_limited") {
		t.Fatalf("warned of a rate the limit allows:\n%s", h.logs.String())
	}
}

func TestVerboseLogsEverySendAndAck(t *testing.T) {
	f := newFake()
	h := newHarness(t, f, func(c *config.Config, _ *Options) { c.Verbose = true })
	h.start()
	f.until(t, "a heartbeat", func(g got) bool { return isState(g.r) })
	obs, _ := f.until(t, "an observation", func(g got) bool { return g.r.GetObservation() != nil })
	select {
	case <-h.acks:
	case <-time.After(waitTimeout):
		t.Fatal("no ack")
	}
	logs := h.logs.String()
	id := obs.r.GetObservation().GetSourceEntityId()
	var sendObs, sendHB, ack bool
	for _, line := range strings.Split(logs, "\n") {
		if strings.Contains(line, "msg=send ") {
			sendObs = sendObs || (strings.Contains(line, "kind=observation") && strings.Contains(line, "sourceEntityId")) && strings.Contains(line, id)
			sendHB = sendHB || strings.Contains(line, "kind=heartbeat")
		}
		ack = ack || (strings.Contains(line, "msg=ack ") && strings.Contains(line, "status=ACK_STATUS_OK"))
	}
	if !sendObs || !sendHB || !ack {
		t.Fatalf("obs=%v heartbeat=%v ack=%v in:\n%s", sendObs, sendHB, ack, logs)
	}
	t.Log(firstLine(logs, "msg=send "))
	t.Log(firstLine(logs, "msg=ack "))
}

func firstLine(s, sub string) string {
	for _, l := range strings.Split(s, "\n") {
		if strings.Contains(l, sub) {
			return l
		}
	}
	return ""
}

func TestNotVerboseLogsNoSendOrAck(t *testing.T) {
	f := newFake()
	buf := &syncBuf{}
	h := newHarness(t, f, func(_ *config.Config, o *Options) {
		o.Logger = slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	})
	h.start()
	f.until(t, "an observation", func(g got) bool { return g.r.GetObservation() != nil })
	select {
	case <-h.acks:
	case <-time.After(waitTimeout):
		t.Fatal("no ack")
	}
	if l := buf.String(); strings.Contains(l, "msg=send") || strings.Contains(l, "msg=ack") {
		t.Fatalf("a non-verbose run logged sends or acks:\n%s", l)
	}
}
