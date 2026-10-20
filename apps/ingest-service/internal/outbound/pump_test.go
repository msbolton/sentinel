package outbound

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	ckafka "github.com/confluentinc/confluent-kafka-go/v2/kafka"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	commonkafka "github.com/sentinel/go-common/kafka"
	sentinelv1 "github.com/sentinel/proto-gen/sentinel/v1"

	"github.com/sentinel/ingest-service/internal/metrics"
	"github.com/sentinel/ingest-service/internal/policy"
	"github.com/sentinel/ingest-service/internal/source/driver"
)

// fakeConsumer hands batches to the handler on demand.
type fakeConsumer struct {
	topics  []string
	handle  commonkafka.BatchHandler
	ready   chan struct{}
	closed  bool
	mu      sync.Mutex
	groupID string
}

func (f *fakeConsumer) Subscribe(topics []string) error { f.topics = topics; return nil }
func (f *fakeConsumer) Run(ctx context.Context, handle commonkafka.BatchHandler) error {
	f.mu.Lock()
	f.handle = handle
	f.mu.Unlock()
	close(f.ready)
	<-ctx.Done()
	return nil
}
func (f *fakeConsumer) Close() error { f.closed = true; return nil }

func (f *fakeConsumer) deliver(t *testing.T, msgs ...*ckafka.Message) {
	t.Helper()
	select {
	case <-f.ready:
	case <-time.After(time.Second):
		t.Fatal("consumer never started")
	}
	f.mu.Lock()
	h := f.handle
	f.mu.Unlock()
	if err := h(context.Background(), msgs); err != nil {
		t.Fatalf("handler returned %v", err)
	}
}

// recorder is a driver.Consumer that records what it was handed.
type recorder struct {
	mu       sync.Mutex
	updates  []driver.EntityUpdate
	alerts   []*sentinelv1.AlertEvent
	fail     error
	notified chan struct{}
}

func newRecorder() *recorder { return &recorder{notified: make(chan struct{}, 100)} }

func (r *recorder) HandleEntity(_ context.Context, u driver.EntityUpdate) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.fail != nil {
		return r.fail
	}
	r.updates = append(r.updates, u)
	r.notified <- struct{}{}
	return nil
}

func (r *recorder) HandleAlert(_ context.Context, ev *sentinelv1.AlertEvent) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.fail != nil {
		return r.fail
	}
	r.alerts = append(r.alerts, ev)
	r.notified <- struct{}{}
	return nil
}

func (r *recorder) wait(t *testing.T, n int) []driver.EntityUpdate {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		r.mu.Lock()
		got := len(r.updates) + len(r.alerts)
		updates := append([]driver.EntityUpdate(nil), r.updates...)
		r.mu.Unlock()
		if got >= n {
			return updates
		}
		select {
		case <-r.notified:
		case <-deadline:
			t.Fatalf("got %d deliveries, want %d", got, n)
		}
	}
}

func entityMsg(t *testing.T, eventType, id, feed string, lat float64, at time.Time, opts ...func(*sentinelv1.Entity)) *ckafka.Message {
	t.Helper()
	e := &sentinelv1.Entity{Id: id, EntityType: sentinelv1.EntityType_ENTITY_TYPE_AIRCRAFT, FeedId: feed, LastFeedId: feed, Position: &sentinelv1.Coordinate{Latitude: lat, Longitude: 0}}
	for _, o := range opts {
		o(e)
	}
	ev := &sentinelv1.EntityEvent{EntityId: id, EventTime: timestamppb.New(at)}
	switch eventType {
	case "created":
		ev.Event = &sentinelv1.EntityEvent_Created{Created: &sentinelv1.EntityCreated{Entity: e}}
	case "position":
		ev.Event = &sentinelv1.EntityEvent_Position{Position: &sentinelv1.EntityPosition{Entity: e}}
	case "deleted":
		ev.Event = &sentinelv1.EntityEvent_Deleted{Deleted: &sentinelv1.EntityDeleted{}}
	case "aged_out":
		ev.Event = &sentinelv1.EntityEvent_AgedOut{AgedOut: &sentinelv1.EntityAgeout{}}
	case "stale":
		ev.Event = &sentinelv1.EntityEvent_Stale{Stale: &sentinelv1.EntityAgeout{}}
	}
	b, err := proto.Marshal(ev)
	if err != nil {
		t.Fatal(err)
	}
	topic := commonkafka.TopicEntityEvents
	return &ckafka.Message{TopicPartition: ckafka.TopicPartition{Topic: &topic}, Value: b, Headers: []ckafka.Header{{Key: commonkafka.HeaderEventType, Value: []byte(eventType)}}}
}

// exercise marks an entity built by entityMsg as exercise data.
func exercise(e *sentinelv1.Entity) { e.Exercise = true }

func alertMsg(t *testing.T, entityID string, at time.Time, opts ...func(*sentinelv1.Alert)) *ckafka.Message {
	t.Helper()
	a := &sentinelv1.Alert{AlertId: "a1", EntityId: entityID, Severity: sentinelv1.AlertSeverity_ALERT_SEVERITY_HIGH, AlertType: sentinelv1.AlertType_ALERT_TYPE_SPEED_ANOMALY}
	for _, o := range opts {
		o(a)
	}
	ev := &sentinelv1.AlertEvent{EventTime: timestamppb.New(at), Alert: a}
	b, _ := proto.Marshal(ev)
	topic := commonkafka.TopicAlertEvents
	return &ckafka.Message{TopicPartition: ckafka.TopicPartition{Topic: &topic}, Value: b}
}

func startPump(t *testing.T, o Options, rec *recorder) (*Pump, *fakeConsumer, context.CancelFunc) {
	t.Helper()
	return startPumpWith(t, o, rec, nil)
}

// startPumpWith is startPump with metrics, for tests that assert on them.
func startPumpWith(t *testing.T, o Options, rec *recorder, m *metrics.Metrics) (*Pump, *fakeConsumer, context.CancelFunc) {
	t.Helper()
	fc := &fakeConsumer{ready: make(chan struct{})}
	factory := func(groupID, clientID string) (Consumer, error) { fc.groupID = groupID; return fc, nil }
	p := New(o, rec, factory, m, nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- p.Run(ctx) }()
	select {
	case <-fc.ready: // subscribed and running: the fake's fields are stable
	case err := <-done:
		t.Fatalf("pump stopped early: %v", err)
	case <-time.After(time.Second):
		t.Fatal("consumer never started")
	}
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("pump returned %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Error("pump did not stop")
		}
	})
	return p, fc, cancel
}

func TestPumpDeliversAllowedAndRemovesDeleted(t *testing.T) {
	rec := newRecorder()
	pol := policy.Policy{EntityTypes: []string{"AIRCRAFT"}, Boxes: []policy.Box{{North: 10, South: -10, East: 10, West: -10}}}
	p, fc, _ := startPump(t, Options{FeedID: "tak", ReplicaID: "r1", Policy: pol, Context: policy.Context{SelfFeedID: "tak"}, PerEntityMin: 0}, rec)
	if fc.groupID != "sentinel-conn-tak" || len(fc.topics) != 2 {
		t.Fatalf("group=%s topics=%v", fc.groupID, fc.topics)
	}
	now := time.Now()
	fc.deliver(t,
		entityMsg(t, "created", "in-1", "opensky", 1, now),
		entityMsg(t, "position", "out-1", "opensky", 50, now), // outside the box
		entityMsg(t, "created", "loop-1", "tak", 1, now),      // came from this connection
		entityMsg(t, "stale", "in-1", "opensky", 1, now),      // ignored
	)
	got := rec.wait(t, 1)
	if len(got) != 1 || got[0].Kind != driver.UpsertEntity || got[0].EntityID != "in-1" {
		t.Fatalf("updates = %+v", got)
	}
	st := p.Stats()
	if st.Delivered != 1 || st.Denied != 2 || st.Tracked != 1 {
		t.Fatalf("stats = %+v", st)
	}

	// A removal for a tracked entity reaches the remote; one for an unknown
	// entity does not.
	fc.deliver(t, entityMsg(t, "deleted", "never-sent", "opensky", 0, now), entityMsg(t, "aged_out", "in-1", "opensky", 0, now))
	got = rec.wait(t, 2)
	if len(got) != 2 || got[1].Kind != driver.RemoveEntity || got[1].EntityID != "in-1" || got[1].Reason != "aged_out" {
		t.Fatalf("updates = %+v", got)
	}
	if p.Stats().Tracked != 0 {
		t.Fatalf("tracked after removal = %d", p.Stats().Tracked)
	}
	if !fc.closed {
		t.Log("consumer closes on stop (checked in cleanup)")
	}
}

func TestPumpAlertsFollowTracking(t *testing.T) {
	rec := newRecorder()
	p, fc, _ := startPump(t, Options{FeedID: "tak", ReplicaID: "r1", Policy: policy.Policy{Boxes: []policy.Box{{North: 10, South: -10, East: 10, West: -10}}}, Context: policy.Context{SelfFeedID: "tak"}}, rec)
	now := time.Now()
	fc.deliver(t, alertMsg(t, "unknown", now)) // untracked, no position: denied
	fc.deliver(t, entityMsg(t, "created", "e1", "opensky", 1, now), alertMsg(t, "e1", now))
	rec.wait(t, 2)
	rec.mu.Lock()
	alerts := len(rec.alerts)
	rec.mu.Unlock()
	if alerts != 1 || p.Stats().Denied != 1 {
		t.Fatalf("alerts=%d stats=%+v", alerts, p.Stats())
	}
}

func TestPumpDeniesAlertsAboutExerciseEntities(t *testing.T) {
	boxes := []policy.Box{{North: 10, South: -10, East: 10, West: -10}}
	inBox := func(a *sentinelv1.Alert) { a.Position = &sentinelv1.Coordinate{Latitude: 1, Longitude: 0} }
	now := time.Now()

	t.Run("denied by default", func(t *testing.T) {
		rec := newRecorder()
		m := metrics.New(prometheus.NewRegistry())
		p, fc, _ := startPumpWith(t, Options{FeedID: "tak", ReplicaID: "r1", Policy: policy.Policy{Boxes: boxes}, Context: policy.Context{SelfFeedID: "tak"}}, rec, m)
		// The entity is exercise and so is denied; the alert about it would
		// pass the untracked box rule, so only the exercise mark stops it.
		fc.deliver(t, entityMsg(t, "created", "e1", "opensky", 1, now, exercise))
		fc.deliver(t, alertMsg(t, "e1", now, inBox))
		// A live entity's untracked alert inside the boxes still passes.
		fc.deliver(t, alertMsg(t, "live", now, inBox))
		rec.wait(t, 1)
		rec.mu.Lock()
		alerts, updates := rec.alerts, len(rec.updates)
		rec.mu.Unlock()
		if len(alerts) != 1 || alerts[0].Alert.EntityId != "live" || updates != 0 {
			t.Fatalf("alerts=%v updates=%d stats=%+v", alerts, updates, p.Stats())
		}
		if st := p.Stats(); st.Denied != 2 || st.Tracked != 0 {
			t.Fatalf("stats=%+v", st)
		}
		if got := testutil.ToFloat64(m.OutboundDeniedTotal.WithLabelValues("tak", policy.ReasonExercise)); got != 2 {
			t.Fatalf("denied{exercise} = %v, want 2", got)
		}
		// Once the entity is gone the mark is forgotten with it.
		fc.deliver(t, entityMsg(t, "aged_out", "e1", "opensky", 1, now))
		fc.deliver(t, alertMsg(t, "e1", now, inBox))
		rec.wait(t, 2)
		if st := p.Stats(); st.Denied != 2 {
			t.Fatalf("after age-out: %+v", st)
		}
	})

	t.Run("sent when allowed", func(t *testing.T) {
		rec := newRecorder()
		p, fc, _ := startPump(t, Options{FeedID: "tak", ReplicaID: "r1", Policy: policy.Policy{Boxes: boxes, AllowExercise: true}, Context: policy.Context{SelfFeedID: "tak"}}, rec)
		fc.deliver(t, entityMsg(t, "created", "e1", "opensky", 1, now, exercise), alertMsg(t, "e1", now, inBox))
		rec.wait(t, 2)
		rec.mu.Lock()
		alerts := len(rec.alerts)
		rec.mu.Unlock()
		if alerts != 1 || p.Stats().Denied != 0 || p.Stats().Tracked != 1 {
			t.Fatalf("alerts=%d stats=%+v", alerts, p.Stats())
		}
	})
}

func TestPumpPolicyExitGraceAndLag(t *testing.T) {
	rec := newRecorder()
	p, fc, _ := startPump(t, Options{FeedID: "tak", ReplicaID: "r1", Policy: policy.Policy{Boxes: []policy.Box{{North: 10, South: -10, East: 10, West: -10}}}, Context: policy.Context{SelfFeedID: "tak"}, ExitGrace: 50 * time.Millisecond, MaxLag: time.Minute}, rec)
	now := time.Now()
	fc.deliver(t, entityMsg(t, "created", "e1", "opensky", 1, now))
	rec.wait(t, 1)
	// Leaves the box: denied, still tracked until the grace period ends.
	fc.deliver(t, entityMsg(t, "position", "e1", "opensky", 50, now))
	if st := p.Stats(); st.Tracked != 1 || st.Denied != 1 {
		t.Fatalf("after exit: %+v", st)
	}
	got := rec.wait(t, 2) // the policy removal after the grace period (the deliver loop ticks every second)
	if got[1].Kind != driver.RemoveEntity || got[1].Reason != "policy" {
		t.Fatalf("removal = %+v", got[1])
	}
	// Comes back: tracked and delivered again.
	fc.deliver(t, entityMsg(t, "position", "e1", "opensky", 2, now))
	got = rec.wait(t, 3)
	if got[2].Kind != driver.UpsertEntity {
		t.Fatalf("return = %+v", got[2])
	}
	// Too old to be worth replaying.
	fc.deliver(t, entityMsg(t, "position", "e2", "opensky", 2, now.Add(-2*time.Minute)))
	if st := p.Stats(); st.Dropped != 1 {
		t.Fatalf("lag drop: %+v", st)
	}
}

func TestPumpPausesWhileNotConnected(t *testing.T) {
	rec := newRecorder()
	rec.fail = driver.ErrNotConnected
	p, fc, _ := startPump(t, Options{FeedID: "tak", ReplicaID: "r1", Context: policy.Context{SelfFeedID: "tak"}}, rec)
	fc.deliver(t, entityMsg(t, "created", "e1", "opensky", 1, time.Now()))
	time.Sleep(150 * time.Millisecond)
	if st := p.Stats(); st.Delivered != 0 || st.DeliveryErrors != 0 || st.QueueDepth != 1 {
		t.Fatalf("while disconnected: %+v", st)
	}
	rec.mu.Lock()
	rec.fail = nil
	rec.mu.Unlock()
	rec.wait(t, 1) // delivered once the pause ends
	rec.mu.Lock()
	rec.fail = errors.New("boom")
	rec.mu.Unlock()
	fc.deliver(t, entityMsg(t, "created", "e2", "opensky", 1, time.Now()))
	deadline := time.After(2 * time.Second)
	for p.Stats().DeliveryErrors == 0 {
		select {
		case <-deadline:
			t.Fatal("delivery error not counted")
		case <-time.After(10 * time.Millisecond):
		}
	}
	if st := p.Stats(); st.LastError != "boom" {
		t.Fatalf("last error = %+v", st)
	}
}
