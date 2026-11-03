package kafka

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/confluentinc/confluent-kafka-go/v2/kafka"
	"go.uber.org/zap"
)

// fakeRaw is a scripted rawConsumer: Poll hands out its events in order and
// nil once they run out; commit calls go to commitFn.
type fakeRaw struct {
	mu        sync.Mutex
	events    []kafka.Event
	commitFn  func(offsets []kafka.TopicPartition) error
	commits   [][]kafka.TopicPartition
	topics    []string
	cb        kafka.RebalanceCb
	closed    int
	subscribe error
}

func (f *fakeRaw) Poll(int) kafka.Event {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.events) == 0 {
		time.Sleep(time.Millisecond)
		return nil
	}
	ev := f.events[0]
	f.events = f.events[1:]
	return ev
}

func (f *fakeRaw) push(evs ...kafka.Event) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, evs...)
}

func (f *fakeRaw) CommitOffsets(offsets []kafka.TopicPartition) ([]kafka.TopicPartition, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.commits = append(f.commits, offsets)
	if f.commitFn != nil {
		if err := f.commitFn(offsets); err != nil {
			return nil, err
		}
	}
	return offsets, nil
}

func (f *fakeRaw) SubscribeTopics(topics []string, cb kafka.RebalanceCb) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.topics, f.cb = topics, cb
	return f.subscribe
}

func (f *fakeRaw) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed++
	return nil
}

func (f *fakeRaw) commitCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.commits)
}

func (f *fakeRaw) closedCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closed
}

func runOpts() BatchOptions {
	return BatchOptions{
		MaxMessages:      2,
		MaxWait:          5 * time.Millisecond,
		MaxRetries:       1,
		RetryBackoff:     time.Millisecond,
		CommitRetryFor:   time.Second,
		ReconnectBackoff: time.Millisecond,
		StaleAfter:       time.Minute,
		DisconnectGrace:  time.Nanosecond,
	}
}

// startRun runs the consumer in the background with a handler that counts
// the messages it sees. It returns a function that waits until at least n
// messages were handled, failing the test on timeout.
func startRun(t *testing.T, b *BatchConsumer) (ctx context.Context, cancel context.CancelFunc, done chan error, seen func(n int)) {
	t.Helper()
	ctx, cancel = context.WithCancel(context.Background())
	done = make(chan error, 1)
	var mu sync.Mutex
	count := 0
	go func() {
		done <- b.Run(ctx, func(_ context.Context, msgs []*kafka.Message) error {
			mu.Lock()
			count += len(msgs)
			mu.Unlock()
			return nil
		})
	}()
	seen = func(n int) {
		t.Helper()
		waitFor(t, func() bool { mu.Lock(); defer mu.Unlock(); return count >= n })
	}
	return ctx, cancel, done, seen
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition not met in time")
}

func finish(t *testing.T, cancel context.CancelFunc, done chan error) {
	t.Helper()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
}

func events(ms []*kafka.Message) []kafka.Event {
	out := make([]kafka.Event, len(ms))
	for i, m := range ms {
		out[i] = m
	}
	return out
}

func TestRunRetriesCommitThroughOutage(t *testing.T) {
	raw := &fakeRaw{}
	failures := 0
	raw.commitFn = func([]kafka.TopicPartition) error {
		if failures < 3 {
			failures++
			return kafka.NewError(kafka.ErrWaitCoord, "Waiting for coordinator", false)
		}
		return nil
	}
	b, err := newBatchConsumer(func() (rawConsumer, error) { return raw, nil }, runOpts(), zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	raw.push(events(msgs(2))...)
	_, cancel, done, seen := startRun(t, b)
	seen(2)
	waitFor(t, func() bool { return raw.commitCount() == 4 })
	if failures != 3 {
		t.Fatalf("commit failed %d times, want 3", failures)
	}
	// The retry loop kept the batch and committed it once, offset+1.
	raw.mu.Lock()
	last := raw.commits[3]
	raw.mu.Unlock()
	if len(last) != 1 || last[0].Offset != 2 {
		t.Fatalf("committed %v, want offset 2", last)
	}
	// The loop is still alive: the next batch is delivered and committed.
	raw.push(events(msgs(1))...)
	seen(3)
	waitFor(t, func() bool { return raw.commitCount() == 5 })
	if err := b.Health(); err != nil {
		t.Fatalf("healthy consumer reported %v", err)
	}
	finish(t, cancel, done)
}

func TestRunMarksBrokerDownWhileCommitWaits(t *testing.T) {
	raw := &fakeRaw{}
	release := make(chan struct{})
	raw.commitFn = func([]kafka.TopicPartition) error {
		select {
		case <-release:
			return nil
		default:
			return kafka.NewError(kafka.ErrTransport, "Disconnected", false)
		}
	}
	b, err := newBatchConsumer(func() (rawConsumer, error) { return raw, nil }, runOpts(), zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	raw.push(events(msgs(1))...)
	_, cancel, done, seen := startRun(t, b)
	seen(1)
	waitFor(t, func() bool { return raw.commitCount() >= 2 })
	time.Sleep(time.Millisecond) // past DisconnectGrace
	if err := b.Health(); err == nil {
		t.Fatal("consumer with no broker reported healthy")
	}
	close(release)
	waitFor(t, func() bool { return b.Health() == nil })
	finish(t, cancel, done)
}

func TestRunDropsCommitTheGroupTookBack(t *testing.T) {
	raw := &fakeRaw{}
	calls := 0
	raw.commitFn = func([]kafka.TopicPartition) error {
		calls++
		if calls == 1 {
			return kafka.NewError(kafka.ErrIllegalGeneration, "generation", false)
		}
		return nil
	}
	b, err := newBatchConsumer(func() (rawConsumer, error) { return raw, nil }, runOpts(), zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	raw.push(events(msgs(1))...)
	_, cancel, done, seen := startRun(t, b)
	seen(1)
	waitFor(t, func() bool { return raw.commitCount() == 1 })
	// Not retried: the next batch goes straight through.
	raw.push(events(msgs(1))...)
	seen(2)
	waitFor(t, func() bool { return raw.commitCount() == 2 })
	if calls != 2 {
		t.Fatalf("commit called %d times, want 2", calls)
	}
	finish(t, cancel, done)
}

func TestRunGivesUpCommitAfterRetryBound(t *testing.T) {
	raw := &fakeRaw{}
	raw.commitFn = func([]kafka.TopicPartition) error {
		return kafka.NewError(kafka.ErrTransport, "Disconnected", false)
	}
	opts := runOpts()
	opts.CommitRetryFor = 10 * time.Millisecond
	b, err := newBatchConsumer(func() (rawConsumer, error) { return raw, nil }, opts, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	raw.push(events(msgs(1))...)
	_, cancel, done, seen := startRun(t, b)
	seen(1)
	// Past the bound the batch is left for redelivery and polling resumes.
	raw.push(events(msgs(1))...)
	seen(2)
	finish(t, cancel, done)
}

func TestRunPollsThroughTransportErrors(t *testing.T) {
	raw := &fakeRaw{}
	b, err := newBatchConsumer(func() (rawConsumer, error) { return raw, nil }, runOpts(), zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	raw.push(
		kafka.NewError(kafka.ErrTransport, "Disconnected while requesting ApiVersion", false),
		kafka.NewError(kafka.ErrAllBrokersDown, "all brokers down", false),
	)
	raw.push(events(msgs(1))...)
	_, cancel, done, seen := startRun(t, b)
	seen(1)
	if raw.closedCount() != 0 {
		t.Fatal("transport errors must not close the consumer")
	}
	// A message proves the broker is back.
	if err := b.Health(); err != nil {
		t.Fatalf("consumer receiving messages reported %v", err)
	}
	finish(t, cancel, done)
}

func TestRunRecreatesConsumerAfterFatalError(t *testing.T) {
	first := &fakeRaw{}
	second := &fakeRaw{}
	opened := 0
	open := func() (rawConsumer, error) {
		opened++
		switch opened {
		case 1:
			return first, nil
		case 2:
			return nil, errors.New("broker refused")
		default:
			return second, nil
		}
	}
	b, err := newBatchConsumer(open, runOpts(), zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Subscribe([]string{"t"}); err != nil {
		t.Fatal(err)
	}
	first.push(kafka.NewError(kafka.ErrFatal, "fatal", true))
	second.push(events(msgs(1))...)
	_, cancel, done, seen := startRun(t, b)
	seen(1)
	if first.closedCount() != 1 {
		t.Fatalf("failed consumer closed %d times, want 1", first.closedCount())
	}
	if opened != 3 {
		t.Fatalf("open called %d times, want 3 (one failed attempt)", opened)
	}
	second.mu.Lock()
	topics, cb := second.topics, second.cb
	second.mu.Unlock()
	if len(topics) != 1 || topics[0] != "t" || cb == nil {
		t.Fatalf("new consumer subscribed to %v with cb %v", topics, cb != nil)
	}
	waitFor(t, func() bool { return second.commitCount() == 1 })
	finish(t, cancel, done)
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	if first.closedCount() != 1 || second.closedCount() != 1 {
		t.Fatalf("Close closed the replacement %d times, want 1", second.closedCount())
	}
}

func TestRunReportsStoppedAfterRejectedBatch(t *testing.T) {
	raw := &fakeRaw{}
	b, err := newBatchConsumer(func() (rawConsumer, error) { return raw, nil }, runOpts(), zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	raw.push(events(msgs(1))...)
	err = b.Run(context.Background(), func(context.Context, []*kafka.Message) error { return errors.New("poison") })
	if err == nil {
		t.Fatal("rejected batch with no dead-letter sink must stop Run")
	}
	if h := b.Health(); h == nil || !errors.Is(h, err) {
		t.Fatalf("Health = %v, want the stop reason", h)
	}
}

func TestRebalanceCallbackTracksAssignment(t *testing.T) {
	raw := &fakeRaw{}
	b, err := newBatchConsumer(func() (rawConsumer, error) { return raw, nil }, runOpts(), zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	clock := &fakeClock{}
	clock.set(time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC))
	b.live.now = clock.now
	if err := b.Subscribe([]string{"t"}); err != nil {
		t.Fatal(err)
	}
	_, cancel, done, _ := startRun(t, b)
	waitFor(t, func() bool { return !healthSays(b, "not started") })
	// The loop polls on, so only the missing assignment goes stale.
	clock.advance(61 * time.Second)
	waitFor(t, func() bool { return healthSays(b, "no partition assignment") })
	tp := kafka.TopicPartition{Topic: new(string), Partition: 0}
	_ = raw.cb(nil, kafka.AssignedPartitions{Partitions: []kafka.TopicPartition{tp}})
	waitFor(t, func() bool { return b.Health() == nil })
	_ = raw.cb(nil, kafka.RevokedPartitions{Partitions: []kafka.TopicPartition{tp}})
	clock.advance(61 * time.Second)
	waitFor(t, func() bool { return healthSays(b, "no partition assignment") })
	finish(t, cancel, done)
}

func healthSays(b *BatchConsumer, substr string) bool {
	err := b.Health()
	return err != nil && strings.Contains(err.Error(), substr)
}
