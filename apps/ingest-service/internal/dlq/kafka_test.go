package dlq

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/confluentinc/confluent-kafka-go/v2/kafka"

	"github.com/sentinel/ingest-service/internal/obs"
	"github.com/sentinel/ingest-service/internal/publish"
)

// readerOver is a KafkaReader whose consumer is the given fake. Nothing here
// creates a Kafka client or contacts a broker.
func readerOver(open func() (source, error)) *KafkaReader {
	r := NewKafkaReader("broker.invalid:9092", "observations.dlq", 2000, nil)
	r.open = open
	return r
}

// await waits for ch, and fails the test rather than hang if it never closes.
func await(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}

func oneLetterTopic() *fakeSource {
	return &fakeSource{parts: map[int32]fakePartition{0: partitionOf(msg("feed-a", at(1), "a"))}, eof: true}
}

func TestKafkaReaderOpensItsConsumerOnFirstUseAndOnce(t *testing.T) {
	opens := 0
	src := oneLetterTopic()
	r := readerOver(func() (source, error) { opens++; return src, nil })
	if opens != 0 {
		t.Fatalf("the consumer was created by the constructor (%d), not on first use", opens)
	}
	for i := 0; i < 3; i++ {
		letters, scanned, partial, err := r.Recent(context.Background(), "feed-a", 5)
		if err != nil || len(letters) != 1 || scanned != 1 || partial {
			t.Fatalf("read %d: %v, %d, %v, %v", i, letters, scanned, partial, err)
		}
	}
	if opens != 1 {
		t.Errorf("created the consumer %d times, want once", opens)
	}
}

// A consumer that could not be created is tried again by the next read, so a
// Kafka that was down when the first read came does not break the endpoint for
// good.
func TestKafkaReaderTriesAgainAfterAFailedOpen(t *testing.T) {
	boom := errors.New("invalid configuration")
	calls := 0
	r := readerOver(func() (source, error) {
		calls++
		if calls == 1 {
			return nil, boom
		}
		return oneLetterTopic(), nil
	})
	if _, _, _, err := r.Recent(context.Background(), "feed-a", 5); !errors.Is(err, boom) {
		t.Fatalf("first read: %v, want the open error", err)
	}
	if letters, _, _, err := r.Recent(context.Background(), "feed-a", 5); err != nil || len(letters) != 1 {
		t.Errorf("second read: %v, %v", letters, err)
	}
}

// guardedSource notices two reads running on the consumer at once. A consumer
// has one assignment, so the second would undo the first.
type guardedSource struct {
	*fakeSource
	busy, overlaps atomic.Int32
}

func (g *guardedSource) Bounds(ctx context.Context) ([]bounds, error) {
	if g.busy.Add(1) > 1 {
		g.overlaps.Add(1)
	}
	time.Sleep(5 * time.Millisecond)
	return g.fakeSource.Bounds(ctx)
}

func (g *guardedSource) Unassign() error {
	g.busy.Add(-1)
	return g.fakeSource.Unassign()
}

func TestKafkaReaderReadsOneRequestAtATime(t *testing.T) {
	src := &guardedSource{fakeSource: oneLetterTopic()}
	r := readerOver(func() (source, error) { return src, nil })
	var wg sync.WaitGroup
	errs := make(chan error, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			letters, _, _, err := r.Recent(context.Background(), "feed-a", 5)
			if err == nil && len(letters) != 1 {
				err = errors.New("wrong letters")
			}
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Error(err)
		}
	}
	if n := src.overlaps.Load(); n != 0 {
		t.Errorf("%d reads overlapped on the one consumer", n)
	}
}

// slowSource holds a read in Bounds until released.
type slowSource struct {
	*fakeSource
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func newSlowSource() *slowSource {
	return &slowSource{fakeSource: oneLetterTopic(), entered: make(chan struct{}), release: make(chan struct{})}
}

func (s *slowSource) Bounds(ctx context.Context) ([]bounds, error) {
	s.once.Do(func() { close(s.entered) })
	<-s.release
	return s.fakeSource.Bounds(ctx)
}

// A request queued behind a read gives up when its own time is up. It does not
// wait for as long as the reads ahead of it take.
func TestKafkaReaderQueuedReadGivesUpAtItsDeadline(t *testing.T) {
	src := newSlowSource()
	r := readerOver(func() (source, error) { return src, nil })
	r.deadline = 300 * time.Millisecond

	first := make(chan struct{})
	go func() {
		defer close(first)
		_, _, _, _ = r.Recent(context.Background(), "feed-a", 5)
	}()
	await(t, src.entered, "the first read to start")

	start := time.Now()
	_, _, _, err := r.Recent(context.Background(), "feed-a", 5)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("queued read: %v, want context.DeadlineExceeded", err)
	}
	if took := time.Since(start); took < 250*time.Millisecond || took > 3*time.Second {
		t.Errorf("queued read gave up after %v, want about its 300ms deadline", took)
	}
	// The queued request's caller going away ends the wait as well.
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)
	if _, _, _, err := r.Recent(ctx, "feed-a", 5); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled while queued: %v, want context.Canceled", err)
	}

	close(src.release)
	await(t, first, "the first read to finish")
}

func TestKafkaReaderClose(t *testing.T) {
	t.Run("before any read, nothing was created", func(t *testing.T) {
		opens := 0
		r := readerOver(func() (source, error) { opens++; return oneLetterTopic(), nil })
		r.Close()
		r.Close()
		if opens != 0 {
			t.Errorf("created %d consumers for a reader that was never read", opens)
		}
		if _, _, _, err := r.Recent(context.Background(), "feed-a", 5); !errors.Is(err, ErrClosed) {
			t.Errorf("read after close: %v, want ErrClosed", err)
		}
		if opens != 0 {
			t.Error("a closed reader created a consumer")
		}
	})

	t.Run("after reads, the consumer is closed once", func(t *testing.T) {
		src := oneLetterTopic()
		r := readerOver(func() (source, error) { return src, nil })
		if _, _, _, err := r.Recent(context.Background(), "feed-a", 5); err != nil {
			t.Fatal(err)
		}
		r.Close()
		r.Close()
		if src.closed != 1 {
			t.Errorf("consumer closed %d times, want once", src.closed)
		}
		if _, _, _, err := r.Recent(context.Background(), "feed-a", 5); !errors.Is(err, ErrClosed) {
			t.Errorf("read after close: %v, want ErrClosed", err)
		}
	})

	// Shutdown closes the reader while a request may still be reading: the
	// consumer is not closed under it.
	t.Run("waits for a read in progress", func(t *testing.T) {
		src := newSlowSource()
		r := readerOver(func() (source, error) { return src, nil })
		reading := make(chan struct{})
		go func() {
			defer close(reading)
			_, _, _, _ = r.Recent(context.Background(), "feed-a", 5)
		}()
		await(t, src.entered, "the read to start")

		closed := make(chan struct{})
		go func() { r.Close(); close(closed) }()
		select {
		case <-closed:
			t.Fatal("Close returned while a read was running")
		case <-time.After(100 * time.Millisecond):
		}
		close(src.release)
		await(t, reading, "the read to finish")
		await(t, closed, "Close to return after the read finished")
		if src.fakeSource.closed != 1 {
			t.Errorf("consumer closed %d times, want once", src.fakeSource.closed)
		}
	})
}

// captureProducer keeps what the publisher produces.
type captureProducer struct{ msgs []*kafka.Message }

func (c *captureProducer) Produce(m *kafka.Message) error { c.msgs = append(c.msgs, m); return nil }

// The publisher writes dead letters and this package reads them, and they
// agree on the key and the header names only by convention. Here a message
// made by the publisher itself is read back.
func TestLetterReadsWhatThePublisherWrites(t *testing.T) {
	prod := &captureProducer{}
	pub := publish.New(prod, nil, nil, publish.Options{})
	received := time.Date(2026, 10, 18, 11, 59, 58, 250000000, time.UTC)
	pub.DeadLetter(obs.DeadLetter{
		FeedID: "pier-tcp", Driver: "tcp", Decoder: "sentinel", Sender: "10.0.0.5:51234", Reason: "parse_error",
		Cause: errors.New("not a sentinel.v1.Observation in JSON: bad"), Raw: []byte("not json"), ReceivedAt: received,
	})
	if len(prod.msgs) != 1 {
		t.Fatalf("%d messages produced, want 1", len(prod.msgs))
	}
	// What a consumer is handed: the partition and offset the broker gave the
	// message and the time it was recorded, a little after the frame arrived.
	m := prod.msgs[0]
	m.TopicPartition.Partition, m.TopicPartition.Offset = 2, 42
	m.Timestamp = received.Add(40 * time.Millisecond)

	ev := eventFrom(m)
	if ev.key != "pier-tcp" {
		t.Errorf("key = %q, want the feed id", ev.key)
	}
	got := letterOf(ev)
	want := Letter{
		At: received, Reason: "parse_error", Error: "not a sentinel.v1.Observation in JSON: bad",
		Driver: "tcp", Decoder: "sentinel", Sender: "10.0.0.5:51234",
		Size: 8, Payload: []byte("not json"), Partition: 2, Offset: 42,
	}
	if !got.At.Equal(want.At) {
		t.Errorf("at = %v, want %v: when the frame arrived, not when Kafka recorded the message", got.At, want.At)
	}
	got.At, want.At = time.Time{}, time.Time{}
	if fmt.Sprintf("%+v", got) != fmt.Sprintf("%+v", want) {
		t.Errorf("letter\n got %+v\nwant %+v", got, want)
	}
	if !ev.at.Equal(m.Timestamp) {
		t.Errorf("the event's time = %v, want the message's %v", ev.at, m.Timestamp)
	}
}

func TestEventFromAMessageWithOnlyAValue(t *testing.T) {
	ev := eventFrom(&kafka.Message{TopicPartition: kafka.TopicPartition{Partition: 1, Offset: 9}, Value: []byte("x")})
	if ev.partition != 1 || ev.offset != 9 || ev.key != "" || string(ev.value) != "x" || len(ev.headers) != 0 || !ev.at.IsZero() || ev.eof {
		t.Errorf("event = %+v", ev)
	}
}

// The consumer's configuration is checked when librdkafka builds it, so a
// misspelt key would only show on the first request. Nothing is contacted
// here: no brokers are configured.
func TestKafkaSourceAcceptsItsConfiguration(t *testing.T) {
	s, err := newKafkaSource("", "observations.dlq")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Assign(map[int32]int64{0: 5, 1: 0}); err != nil {
		t.Errorf("assign: %v", err)
	}
	if ev, ok, err := s.Next(time.Millisecond); ok || err != nil {
		t.Errorf("next with nothing to read: %+v, %v, %v", ev, ok, err)
	}
	if err := s.Unassign(); err != nil {
		t.Errorf("unassign: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Errorf("close: %v", err)
	}
}
