package feed

import (
	"context"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/sentinel/ingest-service/internal/emplacement"
	"github.com/sentinel/ingest-service/internal/metrics"
	"github.com/sentinel/ingest-service/internal/obs"
	"github.com/sentinel/ingest-service/internal/source/driver"
)

type siteSource struct {
	mu   sync.Mutex
	rows map[string]emplacement.Emplacement
	fns  []func(string)
}

func newSiteSource(rows ...emplacement.Emplacement) *siteSource {
	s := &siteSource{rows: map[string]emplacement.Emplacement{}}
	for _, e := range rows {
		s.rows[e.ID] = e
	}
	return s
}

func (s *siteSource) All() []emplacement.Emplacement {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []emplacement.Emplacement
	for _, e := range s.rows {
		out = append(out, e)
	}
	return out
}
func (s *siteSource) Get(id string) (emplacement.Emplacement, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.rows[id]
	return e, ok
}
func (s *siteSource) ByFeed(feedID string) []emplacement.Emplacement {
	var out []emplacement.Emplacement
	for _, e := range s.All() {
		if e.FeedID != nil && *e.FeedID == feedID {
			out = append(out, e)
		}
	}
	return out
}
func (s *siteSource) Ready() <-chan struct{} {
	r := make(chan struct{})
	close(r)
	return r
}
func (s *siteSource) OnChange(fn func(string)) func() {
	s.mu.Lock()
	s.fns = append(s.fns, fn)
	s.mu.Unlock()
	return func() {}
}
func (s *siteSource) change(e emplacement.Emplacement) {
	s.mu.Lock()
	s.rows[e.ID] = e
	fns := append([]func(string){}, s.fns...)
	s.mu.Unlock()
	for _, fn := range fns {
		fn(e.ID)
	}
}

type siteSink struct {
	mu        sync.Mutex
	got       []*obs.Observation
	succeeded int
}

func (s *siteSink) Emit(_ context.Context, o *obs.Observation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.got = append(s.got, o)
	return nil
}
func (s *siteSink) DeadLetter([]byte, string, error) {}
func (s *siteSink) Skipped(string)                   {}
func (s *siteSink) Succeeded(int)                    { s.mu.Lock(); s.succeeded++; s.mu.Unlock() }
func (s *siteSink) Blocked(time.Duration)            {}
func (s *siteSink) all() []*obs.Observation {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*obs.Observation(nil), s.got...)
}

func site(id, feed, mobility string, active bool) emplacement.Emplacement {
	f := feed
	return emplacement.Emplacement{ID: id, Name: id, Mobility: mobility, SensorType: "RADAR", FeedID: &f, Active: active,
		Position: &emplacement.Point{Lat: 50, Lon: -4}, AzimuthReference: emplacement.AzimuthTrueNorth, Classification: "UNCLASSIFIED"}
}

// A connection that does not heartbeat its emplacements reports its own
// active fixed sites, at start, on a change and every interval, and nothing
// else; its reports never count as the connection succeeding.
func TestSitePublisherReportsItsConnectionsFixedSites(t *testing.T) {
	src := newSiteSource(site("a", "tcp-1", emplacement.MobilityFixed, true), site("b", "tcp-1", emplacement.MobilityFixed, false),
		site("c", "tcp-1", emplacement.MobilityMobile, true), site("d", "tcp-2", emplacement.MobilityFixed, true))
	sink := &siteSink{}
	p := NewSitePublisher("tcp-1", src, 20*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); p.Run(ctx, sink) }()
	if !waitFor(func() bool { return len(sink.all()) >= 2 }, 2*time.Second) {
		t.Fatal("no start report and tick")
	}
	renamed := site("a", "tcp-1", emplacement.MobilityFixed, true)
	renamed.Name = "A2"
	src.change(renamed)
	if !waitFor(func() bool { all := sink.all(); return all[len(all)-1].DisplayName == "A2" }, 2*time.Second) {
		t.Fatal("the change was not reported")
	}
	cancel()
	<-done
	for _, o := range sink.all() {
		if o.SourceEntityID != "a" || o.EntityType != obs.EntitySensor || o.Decoder != emplacement.SiteDecoder || o.EmplacementID != "a" {
			t.Errorf("reported %+v", o)
		}
	}
	if sink.succeeded != 0 {
		t.Error("site reports counted as the connection succeeding")
	}
}

// The supervisor gives a connection whose driver does not heartbeat a site
// publisher, so its sites are the connection's entities.
func TestSupervisorPublishesTheSitesOfAConnectionThatDoesNotHeartbeat(t *testing.T) {
	src := newSiteSource(site("s-1", "a", emplacement.MobilityFixed, true))
	pub := &memPublisher{}
	reg := newFakeRegistry()
	sup := New(reg, pub, zap.NewNop(), metrics.New(nil), Options{ReplicaID: "r1", Emplacements: src,
		Factory: func(driver.Spec, driver.Deps) (driver.Driver, error) { return newMockDriver(), nil }})
	reg.set(feedSpec("a", 1, true))
	sup.Reconcile(context.Background())
	t.Cleanup(sup.shutdown)
	if !waitFor(func() bool {
		for _, o := range pub.observations() {
			if o.SourceEntityID == "s-1" && o.FeedID == "a" {
				return true
			}
		}
		return false
	}, 2*time.Second) {
		t.Fatal("the site was not published under its connection")
	}
}

func TestHeartbeatsComesFromTheDefinition(t *testing.T) {
	driver.Register(driver.Definition{ID: "test-heartbeats", Heartbeats: true,
		New: func(driver.Spec, driver.Deps) (driver.Driver, error) { return nil, nil }})
	if !heartbeats("test-heartbeats") || heartbeats("stream") || heartbeats("no-such-driver") {
		t.Error("heartbeats does not follow Definition.Heartbeats")
	}
}

// A reduced publisher's sites go out again within entity-service's RADAR
// stale default (30 s), so they never flip to STALE between reports.
func TestSitesEveryIsWithinTheRadarStaleDefault(t *testing.T) {
	if SitesEvery >= 30*time.Second {
		t.Fatalf("SitesEvery = %v, want below 30 s", SitesEvery)
	}
}
