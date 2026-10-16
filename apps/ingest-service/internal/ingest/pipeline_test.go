package ingest

import (
	"context"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/sentinel/ingest-service/internal/config"
	"github.com/sentinel/ingest-service/internal/metrics"
	"github.com/sentinel/ingest-service/internal/models"
	"github.com/sentinel/ingest-service/internal/obs"
)

var (
	testMetricsOnce sync.Once
	testMetrics     *metrics.Metrics
)

func sharedMetrics() *metrics.Metrics {
	testMetricsOnce.Do(func() { testMetrics = metrics.New() })
	return testMetrics
}

type fakePublisher struct {
	mu     sync.Mutex
	obs    []*obs.Observation
	dead   []string
	reason []string
}

func (f *fakePublisher) Emit(_ context.Context, o *obs.Observation) error {
	f.mu.Lock()
	f.obs = append(f.obs, o)
	f.mu.Unlock()
	return nil
}

func (f *fakePublisher) DeadLetter(_, _, _ string, raw []byte, reason string, _ error) {
	f.mu.Lock()
	f.dead = append(f.dead, string(raw))
	f.reason = append(f.reason, reason)
	f.mu.Unlock()
}

func newTestPipeline(t *testing.T) (*Pipeline, *fakePublisher) {
	t.Helper()
	cfg := &config.Config{WorkerPoolSize: 2, BatchSize: 10, FlushIntervalMs: 10, RawPayloadMaxBytes: 64}
	pub := &fakePublisher{}
	p := NewPipeline(cfg, pub, zap.NewNop(), sharedMetrics())
	p.Start()
	return p, pub
}

func TestPipelinePublishesObservations(t *testing.T) {
	p, pub := newTestPipeline(t)
	sbs := "MSG,3,1,1,ABC123,1,2024/01/01,00:00:00.000,2024/01/01,00:00:00.000,,35000,,,41.9,-87.9,,,,,,0"
	p.Input() <- &models.IngestMessage{SourceType: models.SourceTCP, FeedID: "site-tcp", Payload: []byte(sbs), ReceivedAt: time.Now()}
	p.Input() <- &models.IngestMessage{SourceType: models.SourceMQTT, FeedID: "site-mqtt", Payload: []byte(`{"entity_id":"trk-1","entity_type":"vehicle","latitude":10,"longitude":20,"timestamp":"2026-10-15T12:00:00Z"}`), ReceivedAt: time.Now()}
	p.Stop()

	if len(pub.obs) != 2 {
		t.Fatalf("observations = %d", len(pub.obs))
	}
	byFeed := map[string]*obs.Observation{}
	for _, o := range pub.obs {
		byFeed[o.FeedID] = o
	}
	a := byFeed["site-tcp"]
	if a == nil || a.Decoder != obs.DecoderSBS || a.Source != "ADS_B" || a.Identities[0] != (obs.Identity{Type: obs.IdentityICAO24, Value: "ABC123"}) || !a.HasPosition || string(a.Raw) != sbs[:64] || !a.RawTruncated {
		t.Errorf("sbs observation = %+v", a)
	}
	b := byFeed["site-mqtt"]
	if b == nil || b.Decoder != obs.DecoderJSON || b.EntityType != "VEHICLE" || b.Identities[0].Type != obs.IdentityFeedLocal || b.SourceTimestamp.Year() != 2026 {
		t.Errorf("json observation = %+v", b)
	}
}

func TestPipelineDeadLettersUndecodableAndBlankPayloads(t *testing.T) {
	p, pub := newTestPipeline(t)
	p.Input() <- &models.IngestMessage{SourceType: models.SourceTCP, FeedID: "f", Payload: []byte("   \n")}
	p.Input() <- &models.IngestMessage{SourceType: models.SourceTCP, FeedID: "f", Payload: []byte("not a known format")}
	p.Stop()
	if len(pub.obs) != 0 || len(pub.dead) != 2 {
		t.Fatalf("obs=%d dead=%v", len(pub.obs), pub.dead)
	}
	for _, r := range pub.reason {
		if r != "parse_error" {
			t.Errorf("reason = %s", r)
		}
	}
}

func TestPipelineStaticReportHasNoPosition(t *testing.T) {
	p, pub := newTestPipeline(t)
	// AIS type 5 static report split over two fragments (from the parser tests).
	p.Input() <- &models.IngestMessage{SourceType: models.SourceTCP, FeedID: "ais", Payload: []byte("!AIVDM,2,1,3,B,55P5TL01VIaAL@7WKO@mBplU@<PDhh000000001S;AJ::4A80?4i@E53,0*3E")}
	p.Input() <- &models.IngestMessage{SourceType: models.SourceTCP, FeedID: "ais", Payload: []byte("!AIVDM,2,2,3,B,1@0000000000000,2*55")}
	p.Stop()
	if len(pub.obs) != 1 {
		t.Fatalf("observations = %d (dead=%v)", len(pub.obs), pub.dead)
	}
	o := pub.obs[0]
	if o.HasPosition || o.Identities[0].Type != obs.IdentityMMSI || o.AIS == nil {
		t.Errorf("static observation = %+v", o)
	}
}
