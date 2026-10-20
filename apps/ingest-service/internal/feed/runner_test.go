package feed

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/sentinel/ingest-service/internal/metrics"
	"github.com/sentinel/ingest-service/internal/obs"
	"github.com/sentinel/ingest-service/internal/publish"
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
	m := metrics.New(nil)
	r := NewRunner("feed-x", "stream", drv, pub, m, zap.NewNop(), RunnerOptions{RawMax: 4})
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
	if st.Received != 3 || st.Observations != 2 || st.EntitiesLast != 2 || st.ErrorsTotal != 1 || st.LastSuccessAt.IsZero() || st.LastError != "bad" {
		t.Errorf("stats = %+v", st)
	}
	// Received counts what the driver emitted, the duplicate included;
	// observations_total what was published after it was suppressed.
	for name, c := range map[string]struct {
		got  float64
		want float64
	}{
		"observations_received_total": {testutil.ToFloat64(m.ObservationsReceived.WithLabelValues("feed-x", "stream")), 3},
		"observations_total":          {testutil.ToFloat64(m.ObservationsTotal.WithLabelValues("feed-x", "stream")), 2},
		"duplicates_total":            {testutil.ToFloat64(m.DuplicatesTotal.WithLabelValues("feed-x")), 1},
	} {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v", name, c.got, c.want)
		}
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

// Only service code sets EmplacementID, so an EMPLACEMENT identity is kept
// only when it names that emplacement. A sender's own is dropped before the
// canonical key is chosen, so it can neither fuse into an emplacement's entity
// nor hide its track behind one.
func TestRunnerDropsEmplacementIdentitiesTheServiceDidNotSet(t *testing.T) {
	ts := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	pub := &memPublisher{}
	drv := &scriptedDriver{run: func(ctx context.Context, sink driver.Sink) error {
		forged := fix("a", 40, -80, ts)
		forged.AddIdentity(obs.IdentityEmplacement, "e-1")
		forged.AddIdentity(obs.IdentityMMSI, "235000001")
		if err := sink.Emit(ctx, forged); err != nil {
			return err
		}
		bare := fix("b", 41, -80, ts)
		bare.AddIdentity(obs.IdentityEmplacement, "e-2")
		if err := sink.Emit(ctx, bare); err != nil {
			return err
		}
		own := fix("e-3", 42, -80, ts)
		own.EmplacementID = "e-3"
		own.AddIdentity(obs.IdentityEmplacement, "e-3")
		own.AddIdentity(obs.IdentityEmplacement, "e-4")
		return sink.Emit(ctx, own)
	}}
	r := NewRunner("f", "d", drv, pub, nil, nil, RunnerOptions{})
	if err := r.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := pub.observations()
	if len(got) != 3 {
		t.Fatalf("%d observations, want 3", len(got))
	}
	if want := []obs.Identity{{Type: obs.IdentityMMSI, Value: "235000001"}}; !sameIdentities(got[0].Identities, want) {
		t.Errorf("forged identities = %v, want %v", got[0].Identities, want)
	}
	if want := []obs.Identity{{Type: obs.IdentityFeedLocal, Value: "f:b"}}; !sameIdentities(got[1].Identities, want) {
		t.Errorf("bare identities = %v, want %v", got[1].Identities, want)
	}
	if want := []obs.Identity{{Type: obs.IdentityEmplacement, Value: "e-3"}}; !sameIdentities(got[2].Identities, want) {
		t.Errorf("service identities = %v, want %v", got[2].Identities, want)
	}
}

func sameIdentities(a, b []obs.Identity) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// A feed whose driver turns repeat suppression off forwards a report that
// repeats the last position within the window; an ordinary feed drops it.
func TestRunnerRepeatSuppressionCanBeTurnedOff(t *testing.T) {
	ts := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name string
		off  bool
		want int
	}{{"ordinary feed", false, 1}, {"suppression off", true, 2}} {
		t.Run(tc.name, func(t *testing.T) {
			pub := &memPublisher{}
			drv := &scriptedDriver{run: func(ctx context.Context, sink driver.Sink) error {
				first := fix("e-1", 50, -4, ts)
				first.EmplacementID = "e-1"
				if err := sink.Emit(ctx, first); err != nil {
					return err
				}
				second := fix("e-1", 50, -4, ts.Add(100*time.Millisecond))
				second.EmplacementID = "e-1"
				second.DisplayName = "renamed"
				return sink.Emit(ctx, second)
			}}
			r := NewRunner("f", "emplacement", drv, pub, nil, nil, RunnerOptions{NoRepeatSuppression: tc.off})
			if err := r.Run(context.Background()); err != nil {
				t.Fatal(err)
			}
			if got := len(pub.observations()); got != tc.want {
				t.Fatalf("%d observations forwarded, want %d", got, tc.want)
			}
		})
	}
}

func TestRunnerORsTheConnectionsExercise(t *testing.T) {
	ts := time.Unix(1700000000, 0)
	for _, tc := range []struct {
		name          string
		conn, emitted bool
		want          bool
	}{
		{"live connection, live report", false, false, false},
		{"exercise connection marks every report", true, false, true},
		{"an exercise report stays exercise", false, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pub := &memPublisher{}
			drv := &scriptedDriver{run: func(ctx context.Context, sink driver.Sink) error {
				o := fix("a", 40, -80, ts)
				o.Exercise = tc.emitted
				return sink.Emit(ctx, o)
			}}
			r := NewRunner("f", "d", drv, pub, nil, nil, RunnerOptions{Exercise: tc.conn})
			if err := r.Run(context.Background()); err != nil {
				t.Fatal(err)
			}
			if got := pub.observations()[0].Exercise; got != tc.want {
				t.Fatalf("exercise = %v, want %v", got, tc.want)
			}
		})
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

// The cause of an undecodable payload is kept apart from the other errors a
// runner records: its text can repeat the payload, and the status shows it
// under a key of its own.
func TestRunnerKeepsTheLastDecodeErrorApart(t *testing.T) {
	const decodeText = `proto: (line 1:2): unknown field "launchCode"`
	drv := &scriptedDriver{run: func(ctx context.Context, sink driver.Sink) error {
		sink.DeadLetter([]byte("garbage"), "parse_error", errors.New(decodeText))
		return nil
	}}
	r := NewRunner("feed-x", "stream", drv, &memPublisher{}, metrics.New(nil), zap.NewNop(), RunnerOptions{})
	if err := r.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if st := r.Stats(); st.LastError != decodeText || st.LastDecodeError != decodeText {
		t.Fatalf("after a dead letter: last error %q, last decode error %q; want both %q", st.LastError, st.LastDecodeError, decodeText)
	}

	// A driver-level failure replaces the last error but not the decode error,
	// which stays what it was: the last undecodable payload.
	r.Failed(errors.New("connection lost"))
	if st := r.Stats(); st.LastError != "connection lost" || st.LastDecodeError != decodeText {
		t.Errorf("after a failure: last error %q, last decode error %q", st.LastError, st.LastDecodeError)
	}

	// A dead letter that says nothing of why leaves both as they were.
	r.DeadLetter([]byte("x"), "parse_error", nil)
	if st := r.Stats(); st.LastError != "connection lost" || st.LastDecodeError != decodeText {
		t.Errorf("after a dead letter with no cause: last error %q, last decode error %q", st.LastError, st.LastDecodeError)
	}

	// The next one replaces the decode error as it replaces the last error.
	r.DeadLetterFrom([]byte("y"), "missing_identity", errors.New("missing_identity: no identity"), driver.Provenance{Decoder: "sentinel"})
	if st := r.Stats(); st.LastError != "missing_identity: no identity" || st.LastDecodeError != "missing_identity: no identity" {
		t.Errorf("after another: last error %q, last decode error %q", st.LastError, st.LastDecodeError)
	}
}

// An observation refused because the publisher is closing is the shutdown, not
// a fault: it is no more worth a warning than the context ending is. Any other
// publish error still is.
func TestRunnerDoesNotWarnWhenThePublisherIsClosing(t *testing.T) {
	ts := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	for name, tc := range map[string]struct {
		err  error
		warn bool
	}{
		"publisher closed": {publish.ErrClosed, false},
		"wrapped":          {fmt.Errorf("emit: %w", publish.ErrClosed), false},
		"canceled":         {context.Canceled, false},
		"a real failure":   {errors.New("kafka down"), true},
	} {
		core, logs := observer.New(zapcore.DebugLevel)
		pub := &memPublisher{fail: tc.err}
		drv := &scriptedDriver{run: func(ctx context.Context, sink driver.Sink) error {
			_ = sink.Emit(ctx, fix("a", 40, -80, ts))
			return nil
		}}
		r := NewRunner("feed-x", "stream", drv, pub, metrics.New(nil), zap.New(core), RunnerOptions{})
		if err := r.Run(context.Background()); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got := logs.FilterMessage("observation not published").Len() > 0; got != tc.warn {
			t.Errorf("%s: warned = %v, want %v", name, got, tc.warn)
		}
	}
}

// The supervisor takes the flag from the driver's definition.
func TestNoRepeatSuppressionComesFromTheDefinition(t *testing.T) {
	driver.Register(driver.Definition{ID: "test-no-repeat-suppression", NoRepeatSuppression: true,
		New: func(driver.Spec, driver.Deps) (driver.Driver, error) { return nil, nil }})
	if !noRepeatSuppression("test-no-repeat-suppression") {
		t.Error("a driver that sets NoRepeatSuppression keeps suppression on")
	}
	if noRepeatSuppression("stream") || noRepeatSuppression("no-such-driver") {
		t.Error("suppression is off for a driver that did not ask")
	}
}
