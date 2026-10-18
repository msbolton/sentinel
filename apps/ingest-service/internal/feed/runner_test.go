package feed

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/sentinel/ingest-service/internal/metrics"
	"github.com/sentinel/ingest-service/internal/obs"
	"github.com/sentinel/ingest-service/internal/source/driver"
)

type memPublisher struct {
	mu   sync.Mutex
	obs  []*obs.Observation
	dlq  []obs.DeadLetter
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
func (p *memPublisher) DeadLetter(dl obs.DeadLetter) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.dlq = append(p.dlq, dl)
}
func (p *memPublisher) deadLetters() []obs.DeadLetter {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]obs.DeadLetter(nil), p.dlq...)
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
	// A driver that says nothing of who sent the payload leaves the sender, the
	// decoder and the receive time empty for the publisher to fill in.
	dls := pub.deadLetters()
	if len(dls) != 1 {
		t.Fatalf("dlq = %+v, want one dead letter", dls)
	}
	if dl := dls[0]; dl.FeedID != "feed-x" || dl.Driver != "stream" || dl.Reason != "parse_error" || string(dl.Raw) != "garbage" ||
		dl.Cause == nil || dl.Cause.Error() != "bad" || dl.Decoder != "" || dl.Sender != "" || !dl.ReceivedAt.IsZero() {
		t.Errorf("dead letter = %+v", dl)
	}
}

// A driver that knows who sent a payload says so through DeadLetterFrom, and
// the publisher gets the sender, the decoder and the time the frame arrived
// beside the runner's own feed and driver. The plain DeadLetter still works.
func TestRunnerDeadLetterProvenance(t *testing.T) {
	pub := &memPublisher{}
	core, logs := observer.New(zapcore.DebugLevel)
	received := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	cause := errors.New("bad")
	drv := &scriptedDriver{run: func(ctx context.Context, sink driver.Sink) error {
		ps, ok := sink.(driver.ProvenanceSink)
		if !ok {
			return errors.New("the runner is not a driver.ProvenanceSink")
		}
		ps.DeadLetterFrom([]byte("garbage"), "parse_error", cause, driver.Provenance{Decoder: "sbs", Sender: "10.0.0.5:4000", ReceivedAt: received})
		sink.DeadLetter([]byte("<html>"), "bad_response", errors.New("not json"))
		return nil
	}}
	r := NewRunner("feed-x", "stream", drv, pub, metrics.New(nil), zap.New(core), RunnerOptions{})
	if err := r.Run(context.Background()); err != nil {
		t.Fatal(err)
	}

	dls := pub.deadLetters()
	if len(dls) != 2 {
		t.Fatalf("dlq = %+v, want two dead letters", dls)
	}
	from := dls[0]
	if from.FeedID != "feed-x" || from.Driver != "stream" || from.Decoder != "sbs" || from.Sender != "10.0.0.5:4000" ||
		from.Reason != "parse_error" || from.Cause != cause || string(from.Raw) != "garbage" || !from.ReceivedAt.Equal(received) {
		t.Errorf("dead letter with provenance = %+v", from)
	}
	plain := dls[1]
	if plain.FeedID != "feed-x" || plain.Driver != "stream" || plain.Reason != "bad_response" || string(plain.Raw) != "<html>" ||
		plain.Decoder != "" || plain.Sender != "" || !plain.ReceivedAt.IsZero() {
		t.Errorf("plain dead letter = %+v, want no decoder, sender or receive time", plain)
	}

	// Both are accounted the same way.
	if st := r.Stats(); st.ErrorsTotal != 2 || st.LastError != "not json" {
		t.Errorf("stats = %+v, want two errors, the last being not json", st)
	}
	entries := logs.FilterMessage("frame dead-lettered").All()
	if len(entries) != 2 {
		t.Fatalf("%d dead-letter log entries, want 2", len(entries))
	}
	if ctx := entries[0].ContextMap(); ctx["sender"] != "10.0.0.5:4000" || ctx["decoder"] != "sbs" || ctx["reason"] != "parse_error" {
		t.Errorf("log fields = %v, want the sender and decoder named", ctx)
	}
}

func TestRunnerStampsClassification(t *testing.T) {
	ts := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name, feedLevel, obsLevel, want string
	}{
		{"default unclassified", "", "", obs.ClassUnclassified},
		{"feed default applied", obs.ClassSecret, "", obs.ClassSecret},
		{"decoder may raise", obs.ClassConfidential, obs.ClassTopSecret, obs.ClassTopSecret},
		{"decoder never lowers", obs.ClassSecret, obs.ClassUnclassified, obs.ClassSecret},
		{"unknown level ranks lowest", obs.ClassConfidential, "ULTRA", obs.ClassConfidential},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pub := &memPublisher{}
			drv := &scriptedDriver{run: func(ctx context.Context, sink driver.Sink) error {
				o := fix("a", 40, -80, ts)
				o.Classification = tc.obsLevel
				return sink.Emit(ctx, o)
			}}
			r := NewRunner("f", "d", drv, pub, nil, nil, RunnerOptions{Classification: tc.feedLevel})
			if err := r.Run(context.Background()); err != nil {
				t.Fatal(err)
			}
			got := pub.observations()
			if len(got) != 1 || got[0].Classification != tc.want {
				t.Fatalf("classification = %q, want %q", got[0].Classification, tc.want)
			}
		})
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
