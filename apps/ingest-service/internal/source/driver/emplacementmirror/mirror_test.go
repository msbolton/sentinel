package emplacementmirror

import (
	"context"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/sentinel/ingest-service/internal/emplacement"
	"github.com/sentinel/ingest-service/internal/obs"
	"github.com/sentinel/ingest-service/internal/source/driver"
)

type memSource struct {
	mu   sync.Mutex
	rows map[string]emplacement.Emplacement
	fns  []func(string)
}

func (m *memSource) All() []emplacement.Emplacement {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []emplacement.Emplacement
	for _, e := range m.rows {
		out = append(out, e)
	}
	return out
}
func (m *memSource) Get(id string) (emplacement.Emplacement, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.rows[id]
	return e, ok
}
func (m *memSource) ByFeed(feedID string) []emplacement.Emplacement {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []emplacement.Emplacement
	for _, e := range m.rows {
		if e.FeedID != nil && *e.FeedID == feedID {
			out = append(out, e)
		}
	}
	return out
}
func (m *memSource) Ready() <-chan struct{} {
	r := make(chan struct{})
	close(r)
	return r
}
func (m *memSource) OnChange(fn func(string)) func() {
	m.mu.Lock()
	m.fns = append(m.fns, fn)
	m.mu.Unlock()
	return func() {}
}
func (m *memSource) change(e emplacement.Emplacement) {
	m.mu.Lock()
	m.rows[e.ID] = e
	fns := append([]func(string){}, m.fns...)
	m.mu.Unlock()
	for _, fn := range fns {
		fn(e.ID)
	}
}

type memSink struct {
	mu  sync.Mutex
	got []*obs.Observation
}

func (s *memSink) Emit(_ context.Context, o *obs.Observation) error {
	s.mu.Lock()
	s.got = append(s.got, o)
	s.mu.Unlock()
	return nil
}
func (s *memSink) DeadLetter([]byte, string, error) {}
func (s *memSink) Skipped(string)                   {}
func (s *memSink) Succeeded(int)                    {}
func (s *memSink) Blocked(time.Duration)            {}
func (s *memSink) ids() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, o := range s.got {
		out = append(out, o.EmplacementID)
	}
	return out
}

func place(id, mobility string, active bool, withFix bool) emplacement.Emplacement {
	e := emplacement.Emplacement{ID: id, Name: "E " + id, Mobility: mobility, SensorType: "RADAR", Active: active, Classification: "CONFIDENTIAL", BoresightDeg: 90, AzimuthReference: emplacement.AzimuthTrueNorth, Exercise: true}
	if withFix {
		e.Position = &emplacement.Point{Lat: 50, Lon: -4}
	}
	return e
}

func TestObservationOfAnEmplacement(t *testing.T) {
	alt := 30.0
	e := place("e-1", emplacement.MobilityFixed, true, true)
	e.Altitude = &alt
	at := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	o := Observation(e, at)
	if o.EntityType != obs.EntitySensor || o.Source != "RADAR" || o.Decoder != Decoder || o.SourceEntityID != "e-1" ||
		o.EmplacementID != "e-1" || !o.Exercise || o.Classification != "CONFIDENTIAL" || o.DisplayName != "E e-1" ||
		!o.HasPosition || o.Latitude != 50 || *o.Altitude != 30 || *o.Heading != 90 || !o.SourceTimestamp.Equal(at) ||
		o.TrackEnvironment != obs.EnvGround {
		t.Fatalf("observation = %+v", o)
	}
	if id, ok := o.CanonicalIdentity(); !ok || id.Type != obs.IdentityEmplacement || id.Value != "e-1" {
		t.Fatalf("identity = %v", o.Identities)
	}
	alt = 99
	if *o.Altitude != 30 {
		t.Error("the observation must not share the emplacement's altitude")
	}
}

// The boresight is a heading only when it is measured from true north. One
// measured from the platform is relative to the platform's own heading, which
// the platform's reports carry; reporting it as a heading would have the map
// add it to itself.
func TestObservationHeadingFollowsTheAzimuthReference(t *testing.T) {
	at := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	north := place("e-1", emplacement.MobilityMobile, true, true)
	if o := Observation(north, at); o.Heading == nil || *o.Heading != 90 {
		t.Errorf("TRUE_NORTH heading = %v, want 90", o.Heading)
	}
	platform := place("e-2", emplacement.MobilityMobile, true, true)
	platform.AzimuthReference = emplacement.AzimuthPlatform
	if o := Observation(platform, at); o.Heading != nil {
		t.Errorf("PLATFORM heading = %v, want none", *o.Heading)
	}
}

// The emplacement driver's reports are never suppressed as repeats: an edit
// right after a create or another edit keeps its position and must still
// reach the entity.
func TestDefinitionTurnsRepeatSuppressionOff(t *testing.T) {
	d, ok := driver.Lookup(ID)
	if !ok || !d.NoRepeatSuppression {
		t.Fatalf("definition found %v, NoRepeatSuppression %v; want both", ok, d.NoRepeatSuppression)
	}
}

func TestSourceFor(t *testing.T) {
	for in, want := range map[string]string{"RADAR": "RADAR", "AIS_RECEIVER": "AIS", "ADSB_RECEIVER": "ADS_B", "HUMAN": "HUMINT", "EO_IR": "MASINT", "RF": "SIGINT", "ACOUSTIC": "MASINT", "OTHER": "MASINT"} {
		if got := SourceFor(in); got != want {
			t.Errorf("SourceFor(%s) = %s, want %s", in, got, want)
		}
	}
}

func TestRunReportsAtStartOnChangeAndOnHeartbeat(t *testing.T) {
	src := &memSource{rows: map[string]emplacement.Emplacement{
		"fixed":    place("fixed", emplacement.MobilityFixed, true, true),
		"mobile":   place("mobile", emplacement.MobilityMobile, true, true),
		"lost":     place("lost", emplacement.MobilityMobile, true, false),
		"inactive": place("inactive", emplacement.MobilityFixed, false, true),
	}}
	sink := &memSink{}
	d := &Driver{src: src, heartbeat: 50 * time.Millisecond, logger: zap.NewNop(), now: time.Now}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- d.Run(ctx, sink) }()

	waitFor(t, func() bool { return len(sink.ids()) >= 2 })
	start := sink.ids()[:2]
	if !(contains(start, "fixed") && contains(start, "mobile")) {
		t.Fatalf("start reported %v, want fixed and mobile (not lost, not inactive)", start)
	}
	waitFor(t, func() bool { return count(sink.ids(), "fixed") >= 2 })
	if n := count(sink.ids(), "mobile"); n != 1 {
		t.Fatalf("a mobile emplacement is reported on change only, got %d reports", n)
	}
	moved := place("mobile", emplacement.MobilityMobile, true, true)
	moved.Position.Lat = 51
	src.change(moved)
	waitFor(t, func() bool { return count(sink.ids(), "mobile") == 2 })
	src.change(place("inactive", emplacement.MobilityFixed, false, true))
	time.Sleep(80 * time.Millisecond)
	if count(sink.ids(), "inactive") != 0 || count(sink.ids(), "lost") != 0 {
		t.Fatalf("inactive or position-less emplacements were reported: %v", sink.ids())
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("run returned %v on cancel", err)
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met in time")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func contains(s []string, v string) bool { return count(s, v) > 0 }
func count(s []string, v string) int {
	n := 0
	for _, x := range s {
		if x == v {
			n++
		}
	}
	return n
}

// A mobile emplacement bound to a connection is placed by that connection's
// reports; the stored position goes stale between Touch writes, which send no
// notice. One bound to no connection is still reported, at start and on change.
func TestBoundMobileEmplacementsAreLeftToTheirSenders(t *testing.T) {
	feed := "grpc-1"
	bound := place("bound", emplacement.MobilityMobile, true, true)
	bound.FeedID = &feed
	free := place("free", emplacement.MobilityMobile, true, true)
	boundFixed := place("bound-fixed", emplacement.MobilityFixed, true, true)
	boundFixed.FeedID = &feed
	src := &memSource{rows: map[string]emplacement.Emplacement{"bound": bound, "free": free, "bound-fixed": boundFixed}}
	sink := &memSink{}
	d := &Driver{src: src, heartbeat: time.Hour, logger: zap.NewNop(), now: time.Now}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- d.Run(ctx, sink) }()

	waitFor(t, func() bool { return len(sink.ids()) >= 2 })
	moved := bound
	moved.Position = &emplacement.Point{Lat: 51, Lon: -4}
	src.change(moved)
	src.change(free)
	waitFor(t, func() bool { return count(sink.ids(), "free") == 2 })
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("run returned %v on cancel", err)
	}
	if n := count(sink.ids(), "bound"); n != 0 {
		t.Errorf("a bound mobile emplacement was reported %d times, want 0", n)
	}
	if n := count(sink.ids(), "bound-fixed"); n != 1 {
		t.Errorf("a bound fixed emplacement was reported %d times at start, want 1", n)
	}
}
