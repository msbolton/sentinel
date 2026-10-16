package feed

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/sentinel/ingest-service/internal/metrics"
	"github.com/sentinel/ingest-service/internal/obs"
	"github.com/sentinel/ingest-service/internal/source/driver"
)

type memPublisher struct {
	mu   sync.Mutex
	obs  []*obs.Observation
	dlq  []string
	fail error
}

func (p *memPublisher) Emit(_ context.Context, o *obs.Observation) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.fail != nil {
		return p.fail
	}
	p.obs = append(p.obs, o)
	return nil
}
func (p *memPublisher) DeadLetter(feedID, driverID, decoder string, raw []byte, reason string, cause error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.dlq = append(p.dlq, feedID+"/"+driverID+"/"+reason)
}
func (p *memPublisher) observations() []*obs.Observation {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]*obs.Observation(nil), p.obs...)
}

type scriptedDriver struct {
	run    func(ctx context.Context, sink driver.Sink) error
	health driver.Health
}

func (d *scriptedDriver) Run(ctx context.Context, sink driver.Sink) error { return d.run(ctx, sink) }
func (d *scriptedDriver) Health() driver.Health                           { return d.health }

func fix(id string, lat, lon float64, ts time.Time) *obs.Observation {
	o := &obs.Observation{SourceEntityID: id, Decoder: "test", Source: "GPS", SourceTimestamp: ts, Raw: []byte("0123456789")}
	o.SetPosition(lat, lon)
	return o
}

func TestRunnerStampsProvenanceAndDedupes(t *testing.T) {
	pub := &memPublisher{}
	ts := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	drv := &scriptedDriver{run: func(ctx context.Context, sink driver.Sink) error {
		_ = sink.Emit(ctx, fix("a", 40, -80, ts))
		_ = sink.Emit(ctx, fix("a", 40, -80, ts)) // duplicate
		_ = sink.Emit(ctx, fix("b", 41, -80, ts))
		sink.Succeeded(2)
		sink.DeadLetter([]byte("garbage"), "parse_error", errors.New("bad"))
		return nil
	}}
	r := NewRunner("feed-x", "stream", drv, pub, metrics.New(nil), zap.NewNop(), RunnerOptions{RawMax: 4})
	if err := r.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := pub.observations()
	if len(got) != 2 {
		t.Fatalf("observations = %d, want 2 (one duplicate suppressed)", len(got))
	}
	o := got[0]
	if o.FeedID != "feed-x" || o.Driver != "stream" || o.ID == "" || o.ReceivedAt.IsZero() {
		t.Errorf("provenance = %+v", o)
	}
	if len(o.Identities) != 1 || o.Identities[0] != (obs.Identity{Type: obs.IdentityFeedLocal, Value: "feed-x:a"}) {
		t.Errorf("identities = %v", o.Identities)
	}
	if string(o.Raw) != "0123" || !o.RawTruncated {
		t.Errorf("raw = %q truncated=%v", o.Raw, o.RawTruncated)
	}
	st := r.Stats()
	if st.FramesTotal != 3 || st.Observations != 2 || st.EntitiesLast != 2 || st.ErrorsTotal != 1 || st.LastSuccessAt.IsZero() || st.LastError != "bad" {
		t.Errorf("stats = %+v", st)
	}
	if len(pub.dlq) != 1 || pub.dlq[0] != "feed-x/stream/parse_error" {
		t.Errorf("dlq = %v", pub.dlq)
	}
}

func TestRunnerRawPolicies(t *testing.T) {
	for policy, want := range map[string]string{RawNone: "", RawFull: "0123456789", RawBounded: "0123"} {
		pub := &memPublisher{}
		drv := &scriptedDriver{run: func(ctx context.Context, sink driver.Sink) error {
			return sink.Emit(ctx, fix("a", 1, 1, time.Now()))
		}}
		r := NewRunner("f", "d", drv, pub, nil, nil, RunnerOptions{RawPolicy: policy, RawMax: 4})
		_ = r.Run(context.Background())
		if got := string(pub.observations()[0].Raw); got != want {
			t.Errorf("%s: raw = %q, want %q", policy, got, want)
		}
	}
}

func TestRunnerRecoversDriverPanic(t *testing.T) {
	drv := &scriptedDriver{run: func(context.Context, driver.Sink) error { panic("boom") }}
	r := NewRunner("f", "d", drv, &memPublisher{}, nil, nil, RunnerOptions{})
	err := r.Run(context.Background())
	if err == nil || err.Error() != "driver panic: boom" {
		t.Errorf("err = %v", err)
	}
}

func TestRunnerPropagatesPublishError(t *testing.T) {
	pub := &memPublisher{fail: errors.New("closed")}
	drv := &scriptedDriver{run: func(ctx context.Context, sink driver.Sink) error {
		return sink.Emit(ctx, fix("a", 1, 1, time.Now()))
	}}
	r := NewRunner("f", "d", drv, pub, nil, nil, RunnerOptions{})
	if err := r.Run(context.Background()); err == nil || err.Error() != "closed" {
		t.Errorf("err = %v", err)
	}
}
