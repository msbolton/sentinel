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
	"google.golang.org/protobuf/proto"

	sentinelv1 "github.com/sentinel/proto-gen/sentinel/v1"

	"github.com/sentinel/ingest-service/internal/obs"
)

type fakeProducer struct {
	mu       sync.Mutex
	msgs     []*kafka.Message
	fullLeft atomic.Int32 // how many times to answer "queue full"
	fail     error
}

func (f *fakeProducer) Produce(msg *kafka.Message) error {
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
