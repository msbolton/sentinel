package kafka

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/confluentinc/confluent-kafka-go/v2/kafka"
	"go.uber.org/zap"
)

func msgs(n int) []*kafka.Message {
	topic := "t"
	out := make([]*kafka.Message, n)
	for i := range out {
		out[i] = &kafka.Message{TopicPartition: kafka.TopicPartition{Topic: &topic, Partition: 0, Offset: kafka.Offset(i)}, Value: []byte{byte(i)}}
	}
	return out
}

func fastOpts(dlq DeadLetterFunc) BatchOptions {
	return BatchOptions{MaxRetries: 1, RetryBackoff: time.Millisecond, DeadLetter: dlq}.withDefaults()
}

func TestProcessBatchSucceedsFirstTry(t *testing.T) {
	calls := 0
	err := processBatch(context.Background(), msgs(5), func(context.Context, []*kafka.Message) error { calls++; return nil }, fastOpts(nil), zap.NewNop())
	if err != nil || calls != 1 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
}

func TestProcessBatchRetriesThenSucceeds(t *testing.T) {
	calls := 0
	h := func(context.Context, []*kafka.Message) error {
		calls++
		if calls == 1 {
			return errors.New("transient")
		}
		return nil
	}
	if err := processBatch(context.Background(), msgs(5), h, fastOpts(nil), zap.NewNop()); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("calls=%d", calls)
	}
}

func TestProcessBatchIsolatesPoisonMessage(t *testing.T) {
	poison := func(_ context.Context, batch []*kafka.Message) error {
		for _, m := range batch {
			if m.Value[0] == 6 {
				return errors.New("bad message 6")
			}
		}
		return nil
	}
	var dead []int
	dlq := func(_ context.Context, m *kafka.Message, cause error) error {
		dead = append(dead, int(m.Value[0]))
		return nil
	}
	if err := processBatch(context.Background(), msgs(10), poison, fastOpts(dlq), zap.NewNop()); err != nil {
		t.Fatal(err)
	}
	if len(dead) != 1 || dead[0] != 6 {
		t.Fatalf("dead-lettered %v, want [6]", dead)
	}
}

func TestProcessBatchWithoutDeadLetterStops(t *testing.T) {
	h := func(context.Context, []*kafka.Message) error { return errors.New("always") }
	err := processBatch(context.Background(), msgs(1), h, fastOpts(nil), zap.NewNop())
	if err == nil {
		t.Fatal("expected an error when no dead-letter sink is configured")
	}
}

func TestProcessBatchHonoursCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	h := func(context.Context, []*kafka.Message) error { cancel(); return errors.New("fail after cancel") }
	err := processBatch(ctx, msgs(3), h, BatchOptions{MaxRetries: 3, RetryBackoff: time.Hour}.withDefaults(), zap.NewNop())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v, want context.Canceled", err)
	}
}

func TestProtoHeadersMerge(t *testing.T) {
	// Any proto.Message works; use a Timestamp from the runtime.
	h := ProtoHeaders(timestampMessage(), "svc", map[string]string{HeaderFeedID: "opensky"})
	if h[HeaderSchema] != "google.protobuf.Timestamp" || h[HeaderProducer] != "svc" || h[HeaderFeedID] != "opensky" || h[HeaderContentType] != ContentTypeProtobuf {
		t.Fatalf("headers = %v", h)
	}
}
