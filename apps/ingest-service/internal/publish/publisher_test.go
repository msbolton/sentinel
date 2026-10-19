package publish

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/confluentinc/confluent-kafka-go/v2/kafka"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
	"google.golang.org/protobuf/proto"

	sentinelv1 "github.com/sentinel/proto-gen/sentinel/v1"

	"github.com/sentinel/ingest-service/internal/obs"
)

type fakeProducer struct {
	mu       sync.Mutex
	msgs     []*kafka.Message
	calls    atomic.Int32 // how many times Produce was called, whatever it answered
	fullLeft atomic.Int32 // how many times to answer "queue full"
	fail     error
}

func (f *fakeProducer) Produce(msg *kafka.Message) error {
	f.calls.Add(1)
	if f.fullLeft.Load() > 0 {
		f.fullLeft.Add(-1)
		return kafka.NewError(kafka.ErrQueueFull, "queue full", false)
	}
	if f.fail != nil {
		return f.fail
	}
	f.mu.Lock()
	f.msgs = append(f.msgs, msg)
	f.mu.Unlock()
	return nil
}

func (f *fakeProducer) byTopic(topic string) []*kafka.Message {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*kafka.Message
	for _, m := range f.msgs {
		if *m.TopicPartition.Topic == topic {
			out = append(out, m)
		}
	}
	return out
}

func observation(feed, icao string, seq int) *obs.Observation {
	return &obs.Observation{ID: fmt.Sprintf("%s-%d", icao, seq), FeedID: feed, SourceEntityID: "ICAO-" + icao, Decoder: "sbs", DisplayName: fmt.Sprintf("%d", seq),
		Identities: []obs.Identity{{Type: obs.IdentityICAO24, Value: icao}}, HasPosition: true, Latitude: 1, Longitude: 2}
}

func TestEmitPreservesPerKeyOrderAcrossConcurrentCallers(t *testing.T) {
	fp := &fakeProducer{}
	pub := New(fp, nil, nil, Options{Shards: 4, Buffer: 16, ReplicaID: "r1", FencingToken: 5})
	pub.Start()
	keys := []string{"AAA111", "BBB222", "CCC333"}
	const perKey = 200
	var wg sync.WaitGroup
	for _, k := range keys {
		wg.Add(1)
		go func(k string) {
			defer wg.Done()
			for i := 0; i < perKey; i++ {
				if err := pub.Emit(context.Background(), observation("f", k, i)); err != nil {
					t.Error(err)
				}
			}
		}(k)
	}
	wg.Wait()
	pub.Close()

	msgs := fp.byTopic("observations.raw")
	if len(msgs) != perKey*len(keys) {
		t.Fatalf("produced %d, want %d", len(msgs), perKey*len(keys))
	}
	last := map[string]int{}
	for _, m := range msgs {
		var o sentinelv1.Observation
		if err := proto.Unmarshal(m.Value, &o); err != nil {
			t.Fatal(err)
		}
		key := string(m.Key)
		var seq int
		fmt.Sscanf(o.DisplayName, "%d", &seq)
		if prev, ok := last[key]; ok && seq != prev+1 {
			t.Fatalf("key %s out of order: %d after %d", key, seq, prev)
		}
		last[key] = seq
		if o.ReplicaId != "r1" || o.FencingToken != 5 {
			t.Fatalf("replica/fence not stamped: %s %d", o.ReplicaId, o.FencingToken)
		}
		if m.Opaque != "f" || string(m.Key) != "ICAO24:"+o.Identities[0].Value {
			t.Fatalf("opaque/key = %v %s", m.Opaque, m.Key)
		}
	}
	if len(last) != len(keys) {
		t.Fatalf("keys seen = %v", last)
	}
	if err := pub.Emit(context.Background(), observation("f", "X", 0)); !errors.Is(err, ErrClosed) {
		t.Fatalf("emit after close = %v", err)
	}
}

func TestQueueFullIsRetriedNotDropped(t *testing.T) {
	fp := &fakeProducer{}
	fp.fullLeft.Store(3)
	pub := New(fp, nil, nil, Options{Shards: 1, Buffer: 1, QueueFullWait: time.Millisecond})
	pub.Start()
	if err := pub.Emit(context.Background(), observation("f", "AAA111", 0)); err != nil {
		t.Fatal(err)
	}
	pub.Close()
	if got := len(fp.byTopic("observations.raw")); got != 1 {
		t.Fatalf("produced %d after queue-full retries, want 1", got)
	}
}

func TestEmitBlocksWhenShardFullAndHonoursContext(t *testing.T) {
	fp := &fakeProducer{}
	fp.fullLeft.Store(1 << 30) // producer never accepts: the shard stalls
	pub := New(fp, nil, nil, Options{Shards: 1, Buffer: 1, QueueFullWait: time.Millisecond})
	pub.Start()
	// First observation is taken by the shard goroutine, second fills the buffer.
	_ = pub.Emit(context.Background(), observation("f", "AAA111", 0))
	_ = pub.Emit(context.Background(), observation("f", "AAA111", 1))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if err := pub.Emit(ctx, observation("f", "AAA111", 2)); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected deadline exceeded, got %v", err)
	}
	fp.fullLeft.Store(0)
	pub.Close()
}

func headers(m *kafka.Message) map[string]string {
	out := map[string]string{}
	for _, h := range m.Headers {
		out[h.Key] = string(h.Value)
	}
	return out
}

func TestDeadLetterIsRateLimited(t *testing.T) {
	fp := &fakeProducer{}
	pub := New(fp, nil, nil, Options{DLQRatePerSec: 1, DLQBurst: 3})
	for i := 0; i < 10; i++ {
		pub.DeadLetter(obs.DeadLetter{FeedID: "feed-a", Driver: "tcp", Decoder: "sbs", Sender: "10.0.0.5:4000", Raw: []byte("garbage"), Reason: "parse_error", Cause: errors.New("bad")})
	}
	pub.DeadLetter(obs.DeadLetter{FeedID: "feed-b", Driver: "tcp", Decoder: "sbs", Raw: []byte("garbage"), Reason: "panic"})
	a := fp.byTopic("observations.dlq")
	if len(a) != 4 {
		t.Fatalf("dead letters = %d, want 3 (burst) + 1 (other feed)", len(a))
	}
	hdr := headers(a[0])
	if hdr["feed-id"] != "feed-a" || hdr["driver"] != "tcp" || hdr["reason"] != "parse_error" || hdr["error"] != "bad" ||
		hdr["decoder"] != "sbs" || hdr["sender"] != "10.0.0.5:4000" || string(a[0].Value) != "garbage" {
		t.Fatalf("headers = %v", hdr)
	}
	// A dead letter whose sender is unknown still carries the header, empty,
	// as it does for a decoder it cannot name.
	other := headers(a[3])
	if sender, ok := other["sender"]; other["feed-id"] != "feed-b" || !ok || sender != "" || other["error"] != "" {
		t.Errorf("headers of the other feed's dead letter = %v, want an empty sender and error", other)
	}
}

// received-at is when the frame arrived, so a dead letter that waited behind
// a busy decoder still says when its sender sent it. A payload that was never
// a frame (a polled response) has no such time and is stamped when it is
// dead-lettered.
func TestDeadLetterReceivedAt(t *testing.T) {
	fp := &fakeProducer{}
	pub := New(fp, nil, nil, Options{})
	frame := time.Date(2026, 10, 18, 12, 0, 0, 123456789, time.FixedZone("UTC+2", 2*3600))
	pub.DeadLetter(obs.DeadLetter{FeedID: "f", Reason: "parse_error", ReceivedAt: frame})
	before := time.Now()
	pub.DeadLetter(obs.DeadLetter{FeedID: "f", Reason: "bad_response"})
	after := time.Now()

	msgs := fp.byTopic("observations.dlq")
	if len(msgs) != 2 {
		t.Fatalf("dead letters = %d, want 2", len(msgs))
	}
	if got, want := headers(msgs[0])["received-at"], "2026-10-18T10:00:00.123456789Z"; got != want {
		t.Errorf("received-at of a frame = %q, want the frame's own time %q", got, want)
	}
	stamped, err := time.Parse(time.RFC3339Nano, headers(msgs[1])["received-at"])
	if err != nil {
		t.Fatalf("received-at of a payload with no frame: %v", err)
	}
	if stamped.Before(before) || stamped.After(after) {
		t.Errorf("received-at = %v, want the time of dead-lettering, between %v and %v", stamped, before, after)
	}
}

// stuckProducer is a producer that takes a call and never gives it back, until
// it is released: the worst a Kafka that cannot be reached can do to a shard.
type stuckProducer struct {
	release chan struct{}
	calls   atomic.Int32
}

func newStuckProducer(t *testing.T) *stuckProducer {
	s := &stuckProducer{release: make(chan struct{})}
	t.Cleanup(func() { close(s.release) }) // lets the stuck shard go when the test is over
	return s
}

func (s *stuckProducer) Produce(*kafka.Message) error {
	s.calls.Add(1)
	<-s.release
	return nil
}

// closeWithin runs CloseWithin and fails the test, rather than hanging it, if it
// does not return in time.
func closeWithin(t *testing.T, pub *Publisher, d time.Duration) (abandoned int, took time.Duration) {
	t.Helper()
	type result struct{ abandoned int }
	done := make(chan result, 1)
	start := time.Now()
	go func() { done <- result{pub.CloseWithin(d)} }()
	select {
	case r := <-done:
		return r.abandoned, time.Since(start)
	case <-time.After(5 * time.Second):
		t.Fatalf("CloseWithin(%v) had not returned after 5 s", d)
		return 0, 0
	}
}

// Shutdown with Kafka unreachable: the producer takes a call and never answers,
// so the shard never gets to the next observation and Close would wait for it
// forever. CloseWithin returns when its time is up, and says how many
// observations it gave up on: the one the producer holds and the ones queued.
func TestCloseWithinReturnsWhenTheProducerBlocksForever(t *testing.T) {
	sp := newStuckProducer(t)
	core, logs := observer.New(zapcore.WarnLevel)
	pub := New(sp, nil, zap.New(core), Options{Shards: 1, Buffer: 2})
	pub.Start()
	for i := 0; i < 3; i++ { // one in the producer's hands, two in the shard's queue
		if err := pub.Emit(context.Background(), observation("f", "AAA111", i)); err != nil {
			t.Fatal(err)
		}
	}
	if !waitFor(func() bool { return sp.calls.Load() == 1 }) {
		t.Fatal("the shard never called the producer")
	}

	abandoned, took := closeWithin(t, pub, 100*time.Millisecond)
	if took < 100*time.Millisecond {
		t.Errorf("CloseWithin(100ms) returned after %v: before its time was up", took)
	}
	if took > 2*time.Second {
		t.Errorf("CloseWithin(100ms) took %v, want it to return soon after its 100ms", took)
	}
	if abandoned != 3 {
		t.Errorf("abandoned = %d, want 3: the observation in the producer and the two queued", abandoned)
	}
	entries := logs.FilterMessage("publisher gave up on observations that were not handed to the producer").All()
	if len(entries) != 1 || entries[0].ContextMap()["abandoned"] != int64(3) {
		t.Errorf("log = %v, want one warning that says 3 were abandoned", logs.All())
	}
	if err := pub.Emit(context.Background(), observation("f", "AAA111", 9)); !errors.Is(err, ErrClosed) {
		t.Errorf("emit after CloseWithin = %v, want ErrClosed", err)
	}
	pub.Close() // a second close has nothing left to wait for
}

// A producer that answers "queue full" for ever, as one whose Kafka is down does
// once its queue has filled, keeps a shard retrying. CloseWithin stops the
// retrying, so that nothing calls the producer after it returns and the caller
// can close the producer.
func TestCloseWithinStopsTheShardsRetrying(t *testing.T) {
	fp := &fakeProducer{}
	fp.fullLeft.Store(1 << 30)
	// Room for all six on one shard, so that no Emit waits on a shard that is
	// stuck retrying, wherever the keys hash.
	pub := New(fp, nil, nil, Options{Shards: 2, Buffer: 8, QueueFullWait: time.Millisecond})
	pub.Start()
	for i := 0; i < 6; i++ {
		if err := pub.Emit(context.Background(), observation("f", fmt.Sprintf("K%d", i), i)); err != nil {
			t.Fatal(err)
		}
	}
	if !waitFor(func() bool { return fp.calls.Load() >= 10 }) {
		t.Fatal("the shards were not retrying")
	}

	abandoned, _ := closeWithin(t, pub, 50*time.Millisecond)
	if abandoned != 6 {
		t.Errorf("abandoned = %d, want all 6", abandoned)
	}
	after := fp.calls.Load()
	time.Sleep(100 * time.Millisecond) // many retries' worth
	if now := fp.calls.Load(); now != after {
		t.Errorf("the producer was called %d more times after CloseWithin returned", now-after)
	}
}

// When Kafka takes everything in time, CloseWithin is Close: it waits for the
// drain, loses nothing and reports that nothing was abandoned.
func TestCloseWithinDrainsWhenTheProducerKeepsUp(t *testing.T) {
	fp := &fakeProducer{}
	pub := New(fp, nil, nil, Options{Shards: 4, Buffer: 16})
	pub.Start()
	const n = 200
	for i := 0; i < n; i++ {
		if err := pub.Emit(context.Background(), observation("f", fmt.Sprintf("K%d", i%7), i)); err != nil {
			t.Fatal(err)
		}
	}
	abandoned, _ := closeWithin(t, pub, 10*time.Second)
	if abandoned != 0 {
		t.Errorf("abandoned = %d, want 0", abandoned)
	}
	if got := len(fp.byTopic("observations.raw")); got != n {
		t.Errorf("produced %d of %d", got, n)
	}
}

// waitFor polls cond for a few seconds.
func waitFor(cond func() bool) bool {
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(time.Millisecond) {
		if cond() {
			return true
		}
	}
	return cond()
}

// An Emit blocked on a full shard when the publisher closes is let go with
// ErrClosed. It used to panic with "send on closed channel", which at shutdown
// would have skipped everything that follows (leases, status rows). The shard
// is stuck retrying a full producer queue, as with Kafka down, so the Emits
// really are blocked when CloseWithin runs; run repeatedly under -race.
func TestEmitBlockedOnAFullShardReturnsErrClosedWhenThePublisherCloses(t *testing.T) {
	for round := 0; round < 50; round++ {
		fp := &fakeProducer{}
		fp.fullLeft.Store(1 << 30)
		pub := New(fp, nil, nil, Options{Shards: 1, Buffer: 1, QueueFullWait: time.Millisecond})
		pub.Start()
		// One observation retrying in the shard's hands and one in the buffer: the shard is full.
		for i := 0; i < 2; i++ {
			if err := pub.Emit(context.Background(), observation("f", "AAA111", i)); err != nil {
				t.Fatal(err)
			}
		}
		if !waitFor(func() bool { return fp.calls.Load() >= 1 }) {
			t.Fatal("the shard never called the producer")
		}
		const emitters = 8
		errs := make(chan error, emitters)
		started := make(chan struct{}, emitters)
		for i := 0; i < emitters; i++ {
			go func() {
				started <- struct{}{}
				errs <- pub.Emit(context.Background(), observation("f", "AAA111", 100+i))
			}()
		}
		for i := 0; i < emitters; i++ {
			<-started
		}
		abandoned, _ := closeWithin(t, pub, 20*time.Millisecond)
		for i := 0; i < emitters; i++ {
			select {
			case err := <-errs:
				if !errors.Is(err, ErrClosed) {
					t.Fatalf("round %d: a blocked Emit returned %v, want ErrClosed", round, err)
				}
			case <-time.After(5 * time.Second):
				t.Fatalf("round %d: an Emit was still blocked after CloseWithin", round)
			}
		}
		if abandoned != 2 {
			t.Fatalf("round %d: abandoned = %d, want the 2 that were accepted: an Emit that was refused is not one", round, abandoned)
		}
	}
}

// Nothing an Emit accepted is lost to the close: every observation Emit returned
// nil for is handed to the producer when the producer keeps up, however the
// Emits and the close interleave.
func TestEmitsRacingCloseLoseNothingThatWasAccepted(t *testing.T) {
	for round := 0; round < 50; round++ {
		fp := &fakeProducer{}
		pub := New(fp, nil, nil, Options{Shards: 4, Buffer: 4})
		pub.Start()
		var accepted atomic.Int64
		var wg sync.WaitGroup
		for g := 0; g < 8; g++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := 0; i < 200; i++ {
					switch err := pub.Emit(context.Background(), observation("f", fmt.Sprintf("K%d", g), i)); {
					case err == nil:
						accepted.Add(1)
					case errors.Is(err, ErrClosed):
						return
					default:
						t.Errorf("Emit returned %v", err)
						return
					}
				}
			}()
		}
		time.Sleep(time.Duration(round%5) * 100 * time.Microsecond)
		abandoned, _ := closeWithin(t, pub, 10*time.Second)
		wg.Wait()
		if got := int64(len(fp.byTopic("observations.raw"))); abandoned != 0 || got != accepted.Load() {
			t.Fatalf("round %d: %d accepted, %d produced, %d abandoned", round, accepted.Load(), got, abandoned)
		}
	}
}
