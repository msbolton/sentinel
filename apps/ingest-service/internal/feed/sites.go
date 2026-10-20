package feed

import (
	"context"
	"time"

	"github.com/sentinel/ingest-service/internal/emplacement"
	"github.com/sentinel/ingest-service/internal/source/driver"
)

// SitesEvery is how often a SitePublisher reports its sites again: within
// every global age-out threshold, so the sites stay live.
const SitesEvery = 60 * time.Second

// SitePublisher reports the fixed emplacements bound to a connection whose
// driver does not heartbeat them. That is every inbound driver but grpc
// until the TCP/UDP and TAK heartbeats exist.
//
// It reports each active one with a position as its SENSOR entity: when the
// connection starts, whenever its row changes, and every interval, so the
// entity stays searchable and anchors geofences. It is not a feed. It runs
// inside the connection's runner, so the entity is the connection's, at the
// connection's classification floor and with its exercise flag (the runner
// raises the one and ORs the other).
//
// Its reports carry no heartbeat state, so the map keeps these sites violet,
// and they never count as the connection succeeding. A mobile emplacement is
// left to its connection's own reports.
type SitePublisher struct {
	feedID string
	src    driver.EmplacementSource
	every  time.Duration
	now    func() time.Time
}

// NewSitePublisher reports feedID's sites from src every interval (SitesEvery when every is 0).
func NewSitePublisher(feedID string, src driver.EmplacementSource, every time.Duration) *SitePublisher {
	if every <= 0 {
		every = SitesEvery
	}
	return &SitePublisher{feedID: feedID, src: src, every: every, now: time.Now}
}

// Run reports until ctx ends. An Emit error ends it: the runner is stopping.
func (p *SitePublisher) Run(ctx context.Context, sink driver.Sink) {
	changes := make(chan string, 256)
	cancel := p.src.OnChange(func(id string) {
		select {
		case changes <- id:
		default: // full: the next tick catches up
		}
	})
	defer cancel()
	select {
	case <-p.src.Ready():
	case <-ctx.Done():
		return
	}
	if p.all(ctx, sink) != nil {
		return
	}
	ticker := time.NewTicker(p.every)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case id := <-changes:
			if e, ok := p.src.Get(id); ok && p.reports(e) {
				if sink.Emit(ctx, emplacement.Observation(e, p.now().UTC())) != nil {
					return
				}
			}
		case <-ticker.C:
			if p.all(ctx, sink) != nil {
				return
			}
		}
	}
}

func (p *SitePublisher) all(ctx context.Context, sink driver.Sink) error {
	now := p.now().UTC()
	for _, e := range p.src.ByFeed(p.feedID) {
		if p.reports(e) {
			if err := sink.Emit(ctx, emplacement.Observation(e, now)); err != nil {
				return err
			}
		}
	}
	return nil
}

// reports is whether p reports e: active, fixed, placed, and bound to p's connection.
func (p *SitePublisher) reports(e emplacement.Emplacement) bool {
	return e.Active && e.Mobility == emplacement.MobilityFixed && e.Position != nil && e.FeedID != nil && *e.FeedID == p.feedID
}
