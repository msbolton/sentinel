package detect

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/sentinel/analytics-service/internal/baseline"
	"github.com/sentinel/analytics-service/internal/store"
)

type memFindings struct {
	mu       sync.Mutex
	next     int
	open     map[string]*store.Finding // key entity:kind
	all      []*store.Finding
	closed   []string
	alertIDs []string
	highSev  string
}

func newMemFindings() *memFindings { return &memFindings{open: map[string]*store.Finding{}} }

func (m *memFindings) UpsertOpen(_ context.Context, p store.OpenParams) (*store.Finding, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := p.EntityID + ":" + p.Kind
	if f, ok := m.open[key]; ok {
		f.Hits++
		f.LastAt = p.EvidenceEnd
		if SeverityRank(p.Severity) > SeverityRank(f.Severity) {
			f.Severity = p.Severity
		}
		c := *f
		return &c, false, nil
	}
	m.next++
	f := &store.Finding{ID: fmt.Sprintf("f%d", m.next), EntityID: p.EntityID, Kind: p.Kind, Severity: p.Severity, Status: store.StatusOpen, Hits: 1, FirstAt: p.EvidenceStart, LastAt: p.EvidenceEnd, Classification: p.Classification}
	m.open[key] = f
	m.all = append(m.all, f)
	c := *f
	return &c, true, nil
}
func (m *memFindings) CloseOpen(_ context.Context, entityID, kind string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := entityID + ":" + kind
	if f, ok := m.open[key]; ok {
		f.Status = store.StatusClosed
		delete(m.open, key)
		m.closed = append(m.closed, f.ID)
	}
	return nil
}
func (m *memFindings) CloseStale(_ context.Context, before time.Time) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var n int64
	for key, f := range m.open {
		if f.LastAt.Before(before) {
			f.Status = store.StatusClosed
			delete(m.open, key)
			n++
		}
	}
	return n, nil
}
func (m *memFindings) SetAlertID(_ context.Context, id, alertID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.alertIDs = append(m.alertIDs, alertID)
	for _, f := range m.all {
		if f.ID == id {
			a := alertID
			f.AlertID = &a
		}
	}
	return nil
}

type memCoverage struct{ covered bool }

func (c memCoverage) HasReportsSince(context.Context, string, string, time.Time) (bool, error) {
	return c.covered, nil
}

type memPublisher struct {
	mu        sync.Mutex
	published []string
}

func (p *memPublisher) PublishFinding(_ context.Context, f *store.Finding) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.published = append(p.published, f.ID)
	return nil
}

type memCounters struct{ hits, opened, alerts int }

func (c *memCounters) FindingHit()     { c.hits++ }
func (c *memCounters) FindingOpened()  { c.opened++ }
func (c *memCounters) AlertPublished() { c.alerts++ }

type memBaselines struct{ b *baseline.Baseline }

func (m memBaselines) Get(context.Context, string) (*baseline.Baseline, error) { return m.b, nil }

func newEngine(b *baseline.Baseline, covered bool) (*Engine, *memFindings, *memPublisher, *memCounters) {
	findings := newMemFindings()
	pub := &memPublisher{}
	counters := &memCounters{}
	cache := baseline.NewCache(memBaselines{b: b}, time.Hour)
	e := NewEngine(cache, findings, memCoverage{covered: covered}, pub, counters, testCfg(), zap.NewNop())
	return e, findings, pub, counters
}

func TestEngineAbsorbsRepeatsAndPublishesOnce(t *testing.T) {
	e, findings, pub, counters := newEngine(matureBaseline(), true)
	ctx := context.Background()
	// Three fixes far from the place: one finding, three hits, one alert.
	for i := 0; i < 3; i++ {
		e.OnFix(ctx, fixAt(40.1, -74, mondayNoonUTC.Add(time.Duration(i)*time.Minute)))
	}
	if len(findings.all) != 1 || findings.all[0].Hits != 3 || findings.all[0].Kind != KindUnusualPlace {
		t.Fatalf("findings = %+v", findings.all)
	}
	if len(pub.published) != 1 || len(findings.alertIDs) != 1 || findings.alertIDs[0] != "f1" {
		t.Errorf("published %v, alert ids %v; want one alert whose id is the finding's", pub.published, findings.alertIDs)
	}
	if counters.hits != 3 || counters.opened != 1 || counters.alerts != 1 {
		t.Errorf("counters = %+v", counters)
	}
	if e.Tracked() != 1 {
		t.Errorf("tracked = %d", e.Tracked())
	}
}

func TestEngineMediumFindingIsNotPublishedUntilConfirmed(t *testing.T) {
	e, findings, pub, _ := newEngine(matureBaseline(), true)
	ctx := context.Background()
	e.OnFix(ctx, fixAt(40.03, -74, mondayNoonUTC)) // 3.3 km: MEDIUM
	if len(findings.all) != 1 || findings.all[0].Severity != SeverityMedium || len(pub.published) != 0 {
		t.Fatalf("findings %+v published %v", findings.all, pub.published)
	}
	e.Publish(ctx, findings.all[0]) // what the review endpoint does on CONFIRMED
	if len(pub.published) != 1 || findings.all[0].AlertID == nil {
		t.Errorf("confirm did not publish once: %v %v", pub.published, findings.all[0].AlertID)
	}
	e.Publish(ctx, findings.all[0])
	if len(pub.published) != 1 {
		t.Error("published twice")
	}
}

func TestEngineSkipsConfiguredTypes(t *testing.T) {
	e, findings, _, _ := newEngine(matureBaseline(), true)
	sat := fixAt(45, -74, mondayNoonUTC)
	sat.EntityType = "SATELLITE"
	e.OnFix(context.Background(), sat)
	if len(findings.all) != 0 {
		t.Error("a skipped type produced a finding")
	}
}

func TestEngineTickDarkThenReappear(t *testing.T) {
	e, findings, pub, _ := newEngine(matureBaseline(), true)
	ctx := context.Background()
	e.OnFix(ctx, fixAt(40, -74, mondayNoonUTC))
	e.now = func() time.Time { return mondayNoonUTC.Add(20 * time.Minute) }
	e.Tick(ctx)
	if len(findings.all) != 1 || findings.all[0].Kind != KindWentDark || findings.all[0].Severity != SeverityMedium {
		t.Fatalf("after 20 min: %+v", findings.all)
	}
	// Still dark an hour on: the same finding is raised to HIGH and published.
	e.now = func() time.Time { return mondayNoonUTC.Add(time.Hour) }
	e.Tick(ctx)
	if len(findings.all) != 1 || findings.all[0].Severity != SeverityHigh || findings.all[0].Hits != 2 || len(pub.published) != 1 {
		t.Fatalf("after an hour: %+v published %v", findings.all, pub.published)
	}
	// The entity reports again 100 km away: REAPPEARED HIGH, WENT_DARK closed.
	e.OnFix(ctx, fixAt(40.9, -74, mondayNoonUTC.Add(70*time.Minute)))
	if len(findings.closed) != 1 || findings.closed[0] != "f1" {
		t.Errorf("dark finding not closed: %v", findings.closed)
	}
	var reappeared *store.Finding
	for _, f := range findings.all {
		if f.Kind == KindReappeared {
			reappeared = f
		}
	}
	if reappeared == nil || reappeared.Severity != SeverityHigh {
		t.Errorf("reappeared = %+v", reappeared)
	}
	// Nothing else is dark now; a tick finds no new silence for a recent fix.
	e.now = func() time.Time { return mondayNoonUTC.Add(72 * time.Minute) }
	before := len(findings.all)
	e.Tick(ctx)
	if len(findings.all) != before {
		t.Errorf("tick after reappearance opened %d findings", len(findings.all)-before)
	}
}

func TestEngineTickWithoutCoverageStaysQuietAndClosesStale(t *testing.T) {
	e, findings, _, _ := newEngine(matureBaseline(), false)
	ctx := context.Background()
	e.OnFix(ctx, fixAt(40.1, -74, mondayNoonUTC)) // opens UNUSUAL_PLACE
	e.now = func() time.Time { return mondayNoonUTC.Add(20 * time.Minute) }
	e.Tick(ctx)
	for _, f := range findings.all {
		if f.Kind == KindWentDark {
			t.Error("went dark without coverage")
		}
	}
	// After the absorb window with no more evidence the place finding closes.
	e.now = func() time.Time { return mondayNoonUTC.Add(2 * time.Hour) }
	e.Tick(ctx)
	if len(findings.open) != 0 {
		t.Errorf("stale finding still open: %v", findings.open)
	}
	// Entities silent for a day are forgotten.
	e.now = func() time.Time { return mondayNoonUTC.Add(26 * time.Hour) }
	e.Tick(ctx)
	if e.Tracked() != 0 {
		t.Errorf("tracked = %d after a day of silence", e.Tracked())
	}
}

func TestEngineSeedPrimesLastFixes(t *testing.T) {
	e, findings, _, _ := newEngine(matureBaseline(), true)
	e.Seed([]store.LastFix{{EntityID: "e1", EntityType: "VEHICLE", FeedID: "f1", Lat: 40, Lon: -74, At: mondayNoonUTC}})
	e.now = func() time.Time { return mondayNoonUTC.Add(30 * time.Minute) }
	e.Tick(context.Background())
	if len(findings.all) != 1 || findings.all[0].Kind != KindWentDark {
		t.Errorf("seeded entity not judged dark: %+v", findings.all)
	}
}
