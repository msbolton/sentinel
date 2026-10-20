package client

import (
	"bytes"
	"context"
	"errors"
	"fmt"
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
	ingestv1 "github.com/sentinel/proto-gen/sentinel/ingest/v1"
	sentinelv1 "github.com/sentinel/proto-gen/sentinel/v1"

	"github.com/sentinel/ew-sensor/internal/config"
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

func testConfig() config.Config {
	return config.Config{
		Connection: config.Connection{Addr: "passthrough:///bufnet", Token: testToken, Insecure: true},
		Emplacement: config.Emplacement{
			Name: "Test ESM", Lat: 50, Lon: -5, Alt: 12, Boresight: 270, Width: 360, RangeKm: 100, MinRangeKm: 1,
			Mobility: config.MobilityFixed, AzimuthReference: config.AzimuthTrueNorth,
		},
		Sensor:       config.Sensor{Rate: 100, Pd: 1, SigmaM: 100},
		Ownship:      config.Ownship{Path: config.PathCircle, SpeedKts: 12, RadiusKm: 5},
		EmitterCount: 3,
		Seed:         42,
	}
}

func newHarness(t *testing.T, f *fake, mut func(*config.Config, *Options)) *harness {
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
		StateEvery:    10 * time.Millisecond,
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
	c, err := New(h.cfg, opts)
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

// ---------------------------------------------------------------- 1. upsert + stream

func TestUpsertThenStreamObservations(t *testing.T) {
	f := newFake()
	f.session = func(int) (*ingestv1.GetSessionResponse, error) {
		return &ingestv1.GetSessionResponse{FeedId: "feed-1", SenderManagedEmplacements: true, Exercise: true}, nil
	}
	h := newHarness(t, f, func(c *config.Config, _ *Options) { c.Sensor.Classification = "SECRET" })
	h.start()

	refs := map[string]bool{}
	for i := 0; i < 20; i++ {
		g := f.next(t)
		if !isObs(g.r) {
			t.Fatalf("a FIXED sensor sent a non-observation report: %v", g.r)
		}
		if g.r.GetEmplacementId() != "emp-1" {
			t.Fatalf("report emplacement_id = %q, want the upserted id", g.r.GetEmplacementId())
		}
		if refs[g.r.GetClientRef()] || g.r.GetClientRef() == "" {
			t.Fatalf("client_ref %q repeated or empty", g.r.GetClientRef())
		}
		refs[g.r.GetClientRef()] = true
	}
	for i := 0; i < 10; i++ {
		h.ackWhere(func(a *ingestv1.Ack) bool { return a.GetStatus() == ingestv1.AckStatus_ACK_STATUS_OK })
	}
	if st := h.c.Stats(); st.OK < 10 || st.Sent < 20 || st.Reconnects != 0 {
		t.Fatalf("stats = %+v", st)
	}
	if err := h.stop(); err != nil || ExitCode(err) != ExitOK {
		t.Fatalf("Run after cancel = %v", err)
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.upserts) != 1 {
		t.Fatalf("upserts = %d", len(f.upserts))
	}
	e := f.upserts[0]
	want := &sentinelv1.Emplacement{
		Name: "Test ESM", Description: "ew-sensor simulator",
		SensorType: sentinelv1.SensorType_SENSOR_TYPE_RF, Mobility: sentinelv1.Mobility_MOBILITY_FIXED,
		Position:         &sentinelv1.Coordinate{Latitude: 50, Longitude: -5, AltitudeMeters: proto.Float64(12)},
		BoresightDeg:     270,
		AzimuthReference: sentinelv1.AzimuthReference_AZIMUTH_REFERENCE_TRUE_NORTH,
		Coverage:         &sentinelv1.Coverage{MinRangeM: 1000, MaxRangeM: 100000, AzimuthWidthDeg: 360},
		Classification:   sentinelv1.Classification_CLASSIFICATION_SECRET,
		Exercise:         true, // the session's exercise flag
	}
	if !proto.Equal(e, want) {
		t.Fatalf("upserted %v\nwant %v", e, want)
	}
	if a := f.auth; len(a) != 1 || a[0] != "Bearer "+testToken {
		t.Fatalf("authorization metadata = %q", a)
	}
	if !strings.Contains(h.logs.String(), "seed") && !strings.Contains(h.logs.String(), "session") {
		t.Fatalf("no session log line: %s", h.logs.String())
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
			if err := h.stop(); err != nil {
				t.Fatalf("Run = %v", err)
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if len(f.upserts) != 1 || f.upserts[0].GetExercise() != exercise {
				t.Fatalf("upserts = %v, want one with exercise=%v", f.upserts, exercise)
			}
		})
	}
}

// ---------------------------------------------------------------- 2. emplacement resolution

func boundEmplacement(id string) *sentinelv1.Emplacement {
	return &sentinelv1.Emplacement{
		Id: id, Name: "Bound " + id, Active: true,
		Mobility: sentinelv1.Mobility_MOBILITY_FIXED, SensorType: sentinelv1.SensorType_SENSOR_TYPE_RF,
		Position:         &sentinelv1.Coordinate{Latitude: 51, Longitude: 1, AltitudeMeters: proto.Float64(30)},
		BoresightDeg:     90,
		AzimuthReference: sentinelv1.AzimuthReference_AZIMUTH_REFERENCE_TRUE_NORTH,
		Coverage:         &sentinelv1.Coverage{MaxRangeM: 40000, AzimuthWidthDeg: 360},
	}
}

func sessionWith(senderManaged bool, emps ...*sentinelv1.Emplacement) func(int) (*ingestv1.GetSessionResponse, error) {
	return func(int) (*ingestv1.GetSessionResponse, error) {
		return &ingestv1.GetSessionResponse{FeedId: "feed-1", SenderManagedEmplacements: senderManaged, Emplacements: emps}, nil
	}
}

// assertNearServerSite checks fixes are generated around the server's
// emplacement (51,1; 40 km), not the configured site (50,-5).
func assertNearServerSite(t *testing.T, r *ingestv1.Report) {
	t.Helper()
	p := r.GetObservation().GetPosition()
	_, _, rng := geo.LLAToAER(geo.LLA{Lat: 51, Lon: 1}, geo.LLA{Lat: p.GetLatitude(), Lon: p.GetLongitude()})
	if rng > 45000 {
		t.Fatalf("fix %.4f,%.4f is %.0f m from the server's emplacement; the server geometry was not used", p.GetLatitude(), p.GetLongitude(), rng)
	}
}

func TestSingleBoundEmplacementFallback(t *testing.T) {
	f := newFake()
	f.session = sessionWith(false, boundEmplacement("EMP-B"))
	h := newHarness(t, f, nil)
	h.start()
	for i := 0; i < 6; i++ {
		g := f.next(t)
		if g.r.GetEmplacementId() != "EMP-B" {
			t.Fatalf("emplacement_id = %q, want EMP-B", g.r.GetEmplacementId())
		}
		assertNearServerSite(t, g.r)
	}
	if _, upserts, _ := f.counts(); upserts != 0 {
		t.Fatalf("upserted %d times without sender-managed emplacements", upserts)
	}
}

func TestEmplacementResolutionErrors(t *testing.T) {
	cases := map[string]struct {
		session func(int) (*ingestv1.GetSessionResponse, error)
		id      string
		want    string
	}{
		"none bound":        {session: sessionWith(false), want: "--emplacement-id"},
		"two bound":         {session: sessionWith(false, boundEmplacement("A"), boundEmplacement("B")), want: "--emplacement-id"},
		"id not in session": {session: sessionWith(true, boundEmplacement("A")), id: "missing-id", want: "missing-id"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFake()
			f.session = tc.session
			h := newHarness(t, f, func(c *config.Config, _ *Options) { c.Emplacement.EmplacementID = tc.id })
			h.start()
			err := h.wait()
			if err == nil || ExitCode(err) != ExitFatal || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Run = %v (exit %d), want exit %d mentioning %q", err, ExitCode(err), ExitFatal, tc.want)
			}
			if _, _, streams := f.counts(); streams != 0 {
				t.Fatal("streamed without an emplacement")
			}
		})
	}
}

func TestEmplacementIDUsesServerGeometry(t *testing.T) {
	f := newFake()
	f.session = sessionWith(true, boundEmplacement("Other"), boundEmplacement("4F0E-ABC"))
	h := newHarness(t, f, func(c *config.Config, _ *Options) { c.Emplacement.EmplacementID = "  4f0e-abc " })
	h.start()
	for i := 0; i < 6; i++ {
		g := f.next(t)
		if g.r.GetEmplacementId() != "4F0E-ABC" {
			t.Fatalf("emplacement_id = %q, want the session's id", g.r.GetEmplacementId())
		}
		assertNearServerSite(t, g.r)
	}
	if _, upserts, _ := f.counts(); upserts != 0 {
		t.Fatal("upserted although --emplacement-id was given")
	}
}

// ---------------------------------------------------------------- 3. mobile

func TestMobileSendsStateFirstOnEveryStream(t *testing.T) {
	f := newFake()
	f.upsert = func(_ int, e *sentinelv1.Emplacement) (*ingestv1.UpsertEmplacementResponse, error) {
		e = proto.Clone(e).(*sentinelv1.Emplacement)
		e.Id = "ship-1"
		return &ingestv1.UpsertEmplacementResponse{Emplacement: e}, nil
	}
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
	h := newHarness(t, f, func(c *config.Config, _ *Options) { c.Emplacement.Mobility = config.MobilityMobile })
	h.start()

	checkState := func(g got) {
		t.Helper()
		if !isState(g.r) {
			t.Fatalf("stream %d began with %v, want an EmplacementState", g.stream, g.r)
		}
		if g.r.GetEmplacementId() != "ship-1" || g.r.GetEmplacementState().GetEmplacementId() != "ship-1" {
			t.Fatalf("state ids = %q / %q", g.r.GetEmplacementId(), g.r.GetEmplacementState().GetEmplacementId())
		}
	}
	first := f.next(t)
	if first.stream != 1 {
		t.Fatalf("first report on stream %d", first.stream)
	}
	checkState(first)
	// Drain stream 1 until stream 2's first report.
	g, _ := f.until(t, "a second stream", func(g got) bool { return g.stream != 1 })
	if g.stream != 2 {
		t.Fatalf("reconnected on stream %d", g.stream)
	}
	checkState(g)
	// States keep coming at the state interval on the new stream.
	for i := 0; i < 2; i++ {
		f.until(t, "a periodic state", func(g got) bool { return isState(g.r) })
	}
	logs := h.logs.String()
	if !strings.Contains(logs, "code=Unavailable") || !strings.Contains(logs, "connection moving") {
		t.Fatalf("the retry cause was not logged:\n%s", logs)
	}
	if st := h.c.Stats(); st.Reconnects != 1 {
		t.Fatalf("reconnects = %d, want 1", st.Reconnects)
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
					case first == nil:
						first = r.GetObservation()
						st, reason = ingestv1.AckStatus_ACK_STATUS_REJECTED, "unknown_emplacement"
					case twice && proto.Equal(r.GetObservation(), first):
						st, reason = ingestv1.AckStatus_ACK_STATUS_REJECTED, "unknown_emplacement"
					}
					if err := s.ack(r, st, reason); err != nil {
						return nil
					}
				}
			}
			h := newHarness(t, f, nil)
			h.start()
			orig := f.next(t)
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
	for _, w := range []string{"already has that name", "--name", "--emplacement-id"} {
		if !strings.Contains(err.Error(), w) {
			t.Errorf("error %q does not mention %q", err, w)
		}
	}
	if _, upserts, streams := f.counts(); upserts != 1 || streams != 0 {
		t.Fatalf("upserts %d streams %d, want 1 and 0", upserts, streams)
	}
}

func TestBoundEmplacementWithoutCoverageWarns(t *testing.T) {
	f := newFake()
	bare := boundEmplacement("EMP-B")
	bare.Coverage = nil
	f.session = sessionWith(false, bare)
	h := newHarness(t, f, nil)
	h.start()
	f.next(t)
	if !strings.Contains(h.logs.String(), "has no coverage on the server") {
		t.Fatalf("no coverage warning:\n%s", h.logs.String())
	}

	// A bound emplacement that has coverage does not warn.
	g := newFake()
	g.session = sessionWith(false, boundEmplacement("EMP-B"))
	h2 := newHarness(t, g, nil)
	h2.start()
	g.next(t)
	if strings.Contains(h2.logs.String(), "has no coverage on the server") {
		t.Fatalf("unexpected coverage warning:\n%s", h2.logs.String())
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
			if a.GetClientRef() == b.GetClientRef() || proto.Equal(a.GetObservation(), b.GetObservation()) {
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
	if st := h.c.Stats(); st.Sent != 0 {
		t.Fatalf("sent %d oversize reports", st.Sent)
	}
	if n := len(f.reports); n != 0 {
		t.Fatalf("server received %d reports", n)
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
			out = append(out, f.next(t).r.GetObservation())
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
	for _, k := range []string{"sent=", "ok=", "rejected=", "dead_lettered=", "reconnects=", "dropped_oversize="} {
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
