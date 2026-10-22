package client

import (
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/sentinel/go-common/geo"
	ingestv1 "github.com/sentinel/proto-gen/sentinel/ingest/v1"
	sentinelv1 "github.com/sentinel/proto-gen/sentinel/v1"

	"github.com/sentinel/sensor-sim/internal/config"
	"github.com/sentinel/sensor-sim/internal/scenario"
)

// The ESM kind's client tests: ew-sensor's, run against the same fake
// ingest service as the radar and camera, so they keep asserting what
// ew-sensor did.

// esmConfig turns cfg into ew-sensor's test sensor: a fixed, omni ESM site
// out to 100 km with three synthesised emitters.
func esmConfig(c *config.Config) {
	c.Sensor = config.Sensor{
		Kind: config.KindESM, Name: "Test ESM", Lat: 50, Lon: -5, Alt: f64(12),
		Boresight: 270, Width: 360, RangeKm: 100, MinRangeKm: 1,
		Mobility: config.MobilityFixed, AzimuthReference: config.AzimuthTrueNorth,
		Rate: 100, Pd: 1, SigmaM: 100, HeartbeatInterval: 5 * time.Second,
	}
	c.Ownship = config.Ownship{Path: config.PathCircle, SpeedKts: 12, RadiusKm: 5}
	c.Scenario = scenario.Scenario{}
	c.EmitterCount = 3
}

func newESMHarness(t *testing.T, f *fake, mut func(*config.Config, *Options)) *harness {
	return newHarness(t, f, func(c *config.Config, o *Options) {
		esmConfig(c)
		if mut != nil {
			mut(c, o)
		}
	})
}

func TestESMUpsertThenStreamObservations(t *testing.T) {
	f := newFake()
	f.session = func(int) (*ingestv1.GetSessionResponse, error) {
		return &ingestv1.GetSessionResponse{FeedId: "feed-1", SenderManagedEmplacements: true, Exercise: true}, nil
	}
	h := newESMHarness(t, f, func(c *config.Config, _ *Options) { c.Sensor.Classification = "SECRET" })
	h.start()

	first := f.next(t)
	if !isState(first.r) || first.r.GetEmplacementId() != "emp-1" {
		t.Fatalf("a FIXED sensor's first report is %v, want its heartbeat", first.r)
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
		u := o.GetUncertainty()
		if o.GetEntityType() != sentinelv1.EntityType_ENTITY_TYPE_SIGNAL || o.GetPlatform().GetRf() == nil || o.GetPlatform().GetRf().GetFrequencyHz() <= 0 {
			t.Fatalf("observation %v, want a SIGNAL with an RF detail", o)
		}
		if o.GetPosition() == nil || o.GetPolar() != nil {
			t.Fatalf("observation %v, want a geolocated fix, not a polar measurement", o)
		}
		if u == nil || u.GetSemiMajor() < u.GetSemiMinor() || u.GetSemiMinor() <= 0 {
			t.Fatalf("observation uncertainty %v, want an error ellipse", u)
		}
		if o.GetClassification() != sentinelv1.Classification_CLASSIFICATION_SECRET || o.GetSourceTimestamp() != nil {
			t.Fatalf("observation %v, want SECRET and no source timestamp", o)
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
		Name: "Test ESM", Description: "sensor-sim ESM",
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
	if a := f.authorization(); len(a) != 1 || a[0] != "Bearer "+testToken {
		t.Fatalf("authorization metadata = %q", a)
	}
}

// --send-timestamps stamps the fixes and the heartbeat.
func TestESMSendTimestamps(t *testing.T) {
	f := newFake()
	h := newESMHarness(t, f, func(c *config.Config, _ *Options) { c.Sensor.SendTimestamps = true })
	h.start()
	if first := f.next(t); !isState(first.r) || first.r.GetEmplacementState().GetTime() == nil {
		t.Fatalf("first report %v, want a timestamped heartbeat", first.r)
	}
	g, _ := f.until(t, "a fix", func(g got) bool { return isObs(g.r) })
	if g.r.GetObservation().GetSourceTimestamp() == nil {
		t.Fatalf("fix %v has no source timestamp", g.r)
	}
}

// ---------------------------------------------------------------- emplacement resolution

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

func TestESMSingleBoundEmplacementFallback(t *testing.T) {
	f := newFake()
	f.session = sessionWith(false, boundEmplacement("EMP-B"))
	h := newESMHarness(t, f, nil)
	h.start()
	for i := 0; i < 6; {
		g := f.next(t)
		if g.r.GetEmplacementId() != "EMP-B" {
			t.Fatalf("emplacement_id = %q, want EMP-B", g.r.GetEmplacementId())
		}
		if isState(g.r) {
			continue // the heartbeat carries no fix to place
		}
		assertNearServerSite(t, g.r)
		i++
	}
	if _, upserts, _ := f.counts(); upserts != 0 {
		t.Fatalf("upserted %d times without sender-managed emplacements", upserts)
	}
}

func TestESMEmplacementResolutionErrors(t *testing.T) {
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
			h := newESMHarness(t, f, func(c *config.Config, _ *Options) { c.Sensor.EmplacementID = tc.id })
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

func TestESMEmplacementIDUsesServerGeometry(t *testing.T) {
	f := newFake()
	f.session = sessionWith(true, boundEmplacement("Other"), boundEmplacement("4F0E-ABC"))
	h := newESMHarness(t, f, func(c *config.Config, _ *Options) { c.Sensor.EmplacementID = "  4f0e-abc " })
	h.start()
	for i := 0; i < 6; {
		g := f.next(t)
		if g.r.GetEmplacementId() != "4F0E-ABC" {
			t.Fatalf("emplacement_id = %q, want the session's id", g.r.GetEmplacementId())
		}
		if isState(g.r) {
			continue // the heartbeat carries no fix to place
		}
		assertNearServerSite(t, g.r)
		i++
	}
	if _, upserts, _ := f.counts(); upserts != 0 {
		t.Fatal("upserted although --emplacement-id was given")
	}
}

func TestESMBoundEmplacementWithoutCoverageWarns(t *testing.T) {
	f := newFake()
	bare := boundEmplacement("EMP-B")
	bare.Coverage = nil
	f.session = sessionWith(false, bare)
	h := newESMHarness(t, f, nil)
	h.start()
	f.next(t)
	if !strings.Contains(h.logs.String(), "has no coverage on the server") {
		t.Fatalf("no coverage warning:\n%s", h.logs.String())
	}

	// A bound emplacement that has coverage does not warn.
	g := newFake()
	g.session = sessionWith(false, boundEmplacement("EMP-B"))
	h2 := newESMHarness(t, g, nil)
	h2.start()
	g.next(t)
	if strings.Contains(h2.logs.String(), "has no coverage on the server") {
		t.Fatalf("unexpected coverage warning:\n%s", h2.logs.String())
	}
}

// An ESM sensor whose name is taken can also report as the existing
// emplacement, so the error offers --emplacement-id as well as --name.
func TestESMUpsertNameTakenSuggestsAWayOut(t *testing.T) {
	f := newFake()
	f.upsert = func(int, *sentinelv1.Emplacement) (*ingestv1.UpsertEmplacementResponse, error) {
		return nil, status.Error(codes.AlreadyExists, "an emplacement already has that name")
	}
	h := newESMHarness(t, f, nil)
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

// ---------------------------------------------------------------- mobile

func TestESMMobileSendsStateFirstOnEveryStream(t *testing.T) {
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
	h := newESMHarness(t, f, func(c *config.Config, _ *Options) { c.Sensor.Mobility = config.MobilityMobile })
	h.start()

	checkState := func(g got) {
		t.Helper()
		if !isState(g.r) {
			t.Fatalf("stream %d began with %v, want an EmplacementState", g.stream, g.r)
		}
		st := g.r.GetEmplacementState()
		if g.r.GetEmplacementId() != "ship-1" || st.GetEmplacementId() != "ship-1" {
			t.Fatalf("state ids = %q / %q", g.r.GetEmplacementId(), st.GetEmplacementId())
		}
		if st.Heading == nil || st.GetVelocity() == nil || st.GetHeartbeatIntervalSec() != 5 {
			t.Fatalf("state %v, want a heading, a velocity and the 5 s interval", st)
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
	h.stop()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.upserts) != 1 || f.upserts[0].GetMobility() != sentinelv1.Mobility_MOBILITY_MOBILE ||
		f.upserts[0].GetSensorType() != sentinelv1.SensorType_SENSOR_TYPE_RF {
		t.Fatalf("upserts %v, want one MOBILE RF emplacement", f.upserts)
	}
}

// A mobile sensor's states come every StateEvery (1 s in production), not
// every heartbeat interval.
func TestESMMobileStatesAtTheStateRate(t *testing.T) {
	f := newFake()
	h := newESMHarness(t, f, func(c *config.Config, o *Options) {
		c.Sensor.Mobility = config.MobilityMobile
		c.Sensor.Rate = 1 // few fixes: the states dominate
		o.HeartbeatEvery = time.Hour
		o.StateEvery = 5 * time.Millisecond
	})
	h.start()
	var states []*sentinelv1.EmplacementState
	for len(states) < 4 {
		if g := f.next(t); isState(g.r) {
			states = append(states, g.r.GetEmplacementState())
		}
	}
	if h.c.opts.StateEvery != 5*time.Millisecond {
		t.Fatalf("StateEvery = %v", h.c.opts.StateEvery)
	}
	// The default is 1 Hz.
	var o Options
	o.defaults()
	if o.StateEvery != time.Second {
		t.Fatalf("default StateEvery = %v, want 1s", o.StateEvery)
	}
}

// A fixed ESM site heartbeats every heartbeat interval, not at the state rate.
func TestESMFixedHeartbeatsEveryInterval(t *testing.T) {
	f := newFake()
	h := newESMHarness(t, f, func(c *config.Config, o *Options) {
		c.Sensor.HeartbeatInterval = 7 * time.Second
		o.HeartbeatEvery = 20 * time.Millisecond
		o.StateEvery = time.Hour
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

// ---------------------------------------------------------------- determinism and limits

// The ESM sensor runs on its own simulation clock, advanced by the tick
// interval, so a fixed seed gives the same fixes whatever the wall clock.
func TestESMDeterministicForFixedSeed(t *testing.T) {
	run := func() []*sentinelv1.Observation {
		f := newFake()
		h := newESMHarness(t, f, func(c *config.Config, _ *Options) {
			c.Sensor.Pd = 0.7
			c.Sensor.Mobility = config.MobilityMobile
		})
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

// Heartbeats are never rate limited, so the rate warning counts one fix per
// emitter per tick.
func TestESMRateWarningCountsEmitters(t *testing.T) {
	for _, tc := range []struct {
		limit uint32
		warn  bool
	}{{300, false}, {299, true}} { // 100 Hz x 3 emitters
		f := newFake()
		f.session = func(int) (*ingestv1.GetSessionResponse, error) {
			return &ingestv1.GetSessionResponse{FeedId: "feed-1", SenderManagedEmplacements: true, MaxMessagesPerSec: tc.limit}, nil
		}
		h := newESMHarness(t, f, func(c *config.Config, _ *Options) { c.Sensor.Mobility = config.MobilityMobile })
		h.start()
		f.next(t)
		if got := strings.Contains(h.logs.String(), "expect rate_limited"); got != tc.warn {
			t.Fatalf("limit %d: warned %v, want %v:\n%s", tc.limit, got, tc.warn, h.logs.String())
		}
		h.stop()
	}
}

// A no_heartbeat refusal sends a state and retries the fix once, for the
// ESM kind as for the others.
func TestESMNoHeartbeatRetriesTheFixOnce(t *testing.T) {
	f := newFake()
	var refused bool
	f.handler = func(s *srvStream) error {
		for {
			r, err := s.recv()
			if err != nil {
				return nil
			}
			st, reason := ingestv1.AckStatus_ACK_STATUS_OK, ""
			if !refused && isObs(r) {
				refused = true
				st, reason = ingestv1.AckStatus_ACK_STATUS_REJECTED, "no_heartbeat"
			}
			if err := s.ack(r, st, reason); err != nil {
				return nil
			}
		}
	}
	h := newESMHarness(t, f, func(_ *config.Config, o *Options) { o.HeartbeatEvery = time.Hour })
	h.start()
	f.until(t, "the stream's first heartbeat", func(g got) bool { return isState(g.r) })
	orig, _ := f.until(t, "the first fix", func(g got) bool { return isObs(g.r) })
	f.until(t, "a heartbeat after the refusal", func(g got) bool { return isState(g.r) })
	again, _ := f.until(t, "the retried fix", func(g got) bool {
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

// The ESM sensor's clock is the simulation's, stepped by the tick interval:
// with the wall clock frozen, the mobile platform still moves along its path.
func TestESMSimulationClockStepsWithTheTicks(t *testing.T) {
	f := newFake()
	h := newESMHarness(t, f, func(c *config.Config, o *Options) {
		c.Sensor.Mobility = config.MobilityMobile
		o.StateEvery = 5 * time.Millisecond
	})
	h.start()
	first, _ := f.until(t, "a state", func(g got) bool { return isState(g.r) })
	p0 := first.r.GetEmplacementState().GetPosition()
	f.until(t, "a state from a later position", func(g got) bool {
		p := g.r.GetEmplacementState().GetPosition()
		return isState(g.r) && (p.GetLatitude() != p0.GetLatitude() || p.GetLongitude() != p0.GetLongitude())
	})
	if !h.clock.Now().Equal(time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)) {
		t.Fatal("the wall clock moved")
	}
}
