package kafka

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/confluentinc/confluent-kafka-go/v2/kafka"
	"go.uber.org/zap"
)

// BatchHandler processes one batch. Returning nil commits every message in
// the batch; returning an error triggers the retry and bisection policy in
// BatchOptions.
type BatchHandler func(ctx context.Context, msgs []*kafka.Message) error

// DeadLetterFunc receives a single message the handler keeps rejecting. It
// should produce the message to a dead-letter topic; returning an error
// stops the consumer, because the alternative is silently dropping data.
type DeadLetterFunc func(ctx context.Context, msg *kafka.Message, cause error) error

// BatchOptions tunes BatchConsumer.
type BatchOptions struct {
	MaxMessages  int           // batch size cap (default 500)
	MaxWait      time.Duration // flush a partial batch after this long (default 100 ms)
	MaxRetries   int           // handler retries per batch before bisecting (default 3)
	RetryBackoff time.Duration // first retry delay, doubled each time (default 200 ms)
	DeadLetter   DeadLetterFunc

	// CommitRetryFor bounds how long a failed commit is retried (default 1
	// minute). A commit that fails because the broker is away is retried so
	// the batch is not redelivered once it returns; past the bound the batch
	// is left uncommitted and redelivered, which at-least-once allows.
	CommitRetryFor time.Duration
	// ReconnectBackoff is the delay before the client is recreated after a
	// fatal error, doubled per attempt up to 30 s (default 1 s).
	ReconnectBackoff time.Duration
	// StaleAfter is how long the consumer may go without polling, or without
	// a partition assignment, before Health reports it (default 1 minute).
	StaleAfter time.Duration
	// DisconnectGrace is how long the client may report no broker connection
	// before Health reports it (default 15 s), so a blip does not flap
	// readiness.
	DisconnectGrace time.Duration
}

func (o BatchOptions) withDefaults() BatchOptions {
	if o.MaxMessages <= 0 {
		o.MaxMessages = 500
	}
	if o.MaxWait <= 0 {
		o.MaxWait = 100 * time.Millisecond
	}
	if o.MaxRetries < 0 {
		o.MaxRetries = 0
	} else if o.MaxRetries == 0 {
		o.MaxRetries = 3
	}
	if o.RetryBackoff <= 0 {
		o.RetryBackoff = 200 * time.Millisecond
	}
	if o.CommitRetryFor <= 0 {
		o.CommitRetryFor = time.Minute
	}
	if o.ReconnectBackoff <= 0 {
		o.ReconnectBackoff = time.Second
	}
	if o.StaleAfter <= 0 {
		o.StaleAfter = time.Minute
	}
	if o.DisconnectGrace <= 0 {
		o.DisconnectGrace = 15 * time.Second
	}
	return o
}

const (
	maxCommitBackoff    = 5 * time.Second
	maxReconnectBackoff = 30 * time.Second
	// statsIntervalMs is how often librdkafka reports its broker connection
	// state, which is what tells Health an outage is over on a quiet topic.
	statsIntervalMs = 5000
)

// rawConsumer is the part of *kafka.Consumer the batch consumer uses. Tests
// hand in a fake, since librdkafka cannot run without a broker.
type rawConsumer interface {
	Poll(timeoutMs int) kafka.Event
	CommitOffsets(offsets []kafka.TopicPartition) ([]kafka.TopicPartition, error)
	SubscribeTopics(topics []string, cb kafka.RebalanceCb) error
	Close() error
}

// BatchConsumer delivers messages to a handler in batches and commits
// offsets only after the handler succeeds, which gives at-least-once
// delivery: a crash mid-batch redelivers the batch, so handlers must be
// idempotent. A batch that keeps failing is bisected until the poison
// message is isolated and handed to DeadLetter.
//
// Run outlives the broker: a lost connection is polled through until
// librdkafka reconnects and rejoins the group, a commit that fails while the
// broker is away is retried, and a client in a fatal state is closed and
// recreated with backoff. Run returns only when ctx ends or a batch is
// rejected with nowhere to put it. Health says whether any of that is
// currently going wrong.
type BatchConsumer struct {
	open   func() (rawConsumer, error)
	logger *zap.Logger
	opts   BatchOptions
	live   *liveness

	mu       sync.Mutex // guards consumer, topics and closed
	consumer rawConsumer
	topics   []string
	closed   bool
}

// NewBatchConsumer creates a consumer with auto-commit disabled. offsetReset
// is "earliest" or "latest".
func NewBatchConsumer(brokers, groupID, clientID, offsetReset string, opts BatchOptions, logger *zap.Logger) (*BatchConsumer, error) {
	cfg := &kafka.ConfigMap{
		"bootstrap.servers":        brokers,
		"group.id":                 groupID,
		"client.id":                clientID,
		"auto.offset.reset":        offsetReset,
		"enable.auto.commit":       false,
		"enable.auto.offset.store": false,
		"statistics.interval.ms":   statsIntervalMs,
	}
	open := func() (rawConsumer, error) { return kafka.NewConsumer(cfg) }
	return newBatchConsumer(open, opts, logger)
}

func newBatchConsumer(open func() (rawConsumer, error), opts BatchOptions, logger *zap.Logger) (*BatchConsumer, error) {
	c, err := open()
	if err != nil {
		return nil, fmt.Errorf("creating kafka consumer: %w", err)
	}
	opts = opts.withDefaults()
	return &BatchConsumer{
		open:     open,
		consumer: c,
		logger:   logger,
		opts:     opts,
		live:     newLiveness(opts.StaleAfter, opts.DisconnectGrace),
	}, nil
}

// Subscribe subscribes to the given topics.
func (b *BatchConsumer) Subscribe(topics []string) error {
	b.mu.Lock()
	b.topics = append([]string(nil), topics...)
	c := b.consumer
	b.mu.Unlock()
	return c.SubscribeTopics(topics, b.onRebalance)
}

// Health reports why the consumer is not healthy: its loop has stopped or
// is not polling, the group has given it no partitions, or the client has
// had no broker for longer than DisconnectGrace. It is nil while consuming
// normally, and is meant for the service's readiness check.
func (b *BatchConsumer) Health() error {
	return b.live.check()
}

// onRebalance only observes: with no Assign call of its own, the client
// applies the assignment itself after the callback returns.
func (b *BatchConsumer) onRebalance(_ *kafka.Consumer, ev kafka.Event) error {
	switch e := ev.(type) {
	case kafka.AssignedPartitions:
		b.live.assign()
		b.logger.Info("kafka partitions assigned", zap.Int("partitions", len(e.Partitions)))
	case kafka.RevokedPartitions:
		b.live.revoke()
		b.logger.Info("kafka partitions revoked", zap.Int("partitions", len(e.Partitions)))
	}
	return nil
}

func (b *BatchConsumer) current() rawConsumer {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.consumer
}

// Run polls until ctx is cancelled. Each batch is passed to handle; offsets
// are committed after handle (or the dead-letter path) succeeds. It returns
// early only when a batch is rejected and cannot be dead-lettered.
func (b *BatchConsumer) Run(ctx context.Context, handle BatchHandler) error {
	b.live.start()
	err := b.run(ctx, handle)
	if ctx.Err() != nil && errors.Is(err, ctx.Err()) {
		// Cancelled mid-retry: an orderly stop, not a failure.
		err = nil
	}
	b.live.stop(err)
	return err
}

func (b *BatchConsumer) run(ctx context.Context, handle BatchHandler) error {
	for {
		batch, err := b.collect(ctx)
		if err != nil {
			// Only a fatal client error gets here. Whatever was collected
			// is uncommitted and comes back after the new client joins.
			if err := b.recreate(ctx, err); err != nil {
				return err
			}
			continue
		}
		if len(batch) == 0 {
			if ctx.Err() != nil {
				return nil
			}
			continue
		}
		if err := processBatch(ctx, batch, handle, b.opts, b.logger); err != nil {
			return err
		}
		if err := b.commit(ctx, batch); err != nil {
			return err
		}
	}
}

// collect polls until the batch is full, MaxWait has elapsed since the first
// message, or ctx is done. Transport errors are logged and polled through,
// because librdkafka reconnects on its own; only a fatal error is returned.
func (b *BatchConsumer) collect(ctx context.Context) ([]*kafka.Message, error) {
	var batch []*kafka.Message
	var deadline time.Time
	c := b.current()
	for len(batch) < b.opts.MaxMessages {
		if ctx.Err() != nil {
			return batch, nil
		}
		timeout := 100
		if !deadline.IsZero() {
			remaining := time.Until(deadline)
			if remaining <= 0 {
				break
			}
			timeout = int(remaining / time.Millisecond)
			if timeout < 1 {
				timeout = 1
			}
		}
		ev := c.Poll(timeout)
		b.live.polled()
		switch e := ev.(type) {
		case nil:
			if !deadline.IsZero() && time.Now().After(deadline) {
				return batch, nil
			}
		case *kafka.Message:
			b.live.progressed()
			if len(batch) == 0 {
				deadline = time.Now().Add(b.opts.MaxWait)
			}
			batch = append(batch, e)
		case *kafka.Stats:
			b.observeStats(e.String())
		case kafka.Error:
			if e.IsFatal() {
				return batch, fmt.Errorf("fatal kafka error: %w", e)
			}
			if e.Code() == kafka.ErrAllBrokersDown {
				b.live.brokersDown()
			}
			b.logger.Warn("kafka consumer error", zap.Error(e))
		}
	}
	return batch, nil
}

func (b *BatchConsumer) observeStats(stats string) {
	up, known, err := brokersConnected(stats)
	if err != nil || known == 0 {
		return
	}
	if up > 0 {
		b.live.brokersUp()
	} else {
		b.live.brokersDown()
	}
}

// commit commits offset+1 of the last message per partition, retrying with
// backoff while the broker is away. It returns only ctx's error: a commit
// that cannot succeed from this member (the group moved on) or that has
// failed for CommitRetryFor is logged and left for redelivery.
func (b *BatchConsumer) commit(ctx context.Context, batch []*kafka.Message) error {
	offsets := latestOffsets(batch)
	deadline := time.Now().Add(b.opts.CommitRetryFor)
	backoff := b.opts.RetryBackoff
	for attempt := 1; ; attempt++ {
		b.live.commitStarted()
		_, err := b.current().CommitOffsets(offsets)
		b.live.commitEnded()
		if err == nil {
			b.live.progressed()
			if attempt > 1 {
				b.logger.Info("kafka offsets committed after retry", zap.Int("attempts", attempt))
			}
			return nil
		}
		var ke kafka.Error
		isKafka := errors.As(err, &ke)
		switch {
		case isKafka && ke.IsFatal():
			b.logger.Error("kafka offsets not committed; batch will be redelivered", zap.Int("attempts", attempt), zap.Error(err))
			return b.recreate(ctx, err)
		case isKafka && commitLost(ke.Code()), time.Now().After(deadline):
			b.logger.Error("kafka offsets not committed; batch will be redelivered", zap.Int("attempts", attempt), zap.Error(err))
			return nil
		}
		if isKafka && brokerAway(ke.Code()) {
			b.live.brokersDown()
		}
		b.logger.Warn("kafka commit failed; retrying", zap.Int("attempt", attempt), zap.Duration("backoff", backoff), zap.Error(err))
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
		if backoff *= 2; backoff > maxCommitBackoff {
			backoff = maxCommitBackoff
		}
	}
}

// commitLost reports the errors that mean this member can never commit the
// batch: the group has moved on without it, it holds no assignment, or the
// topic or group is not its to use. Retrying would only wait out the bound.
func commitLost(code kafka.ErrorCode) bool {
	switch code {
	case kafka.ErrIllegalGeneration, kafka.ErrUnknownMemberID, kafka.ErrFencedInstanceID,
		kafka.ErrState, kafka.ErrNoOffset, kafka.ErrUnknownTopicOrPart,
		kafka.ErrTopicAuthorizationFailed, kafka.ErrGroupAuthorizationFailed:
		return true
	}
	return false
}

// brokerAway reports the commit errors that mean no broker answered.
func brokerAway(code kafka.ErrorCode) bool {
	switch code {
	case kafka.ErrTransport, kafka.ErrAllBrokersDown, kafka.ErrWaitCoord, kafka.ErrTimedOut,
		kafka.ErrCoordinatorNotAvailable, kafka.ErrNotCoordinator:
		return true
	}
	return false
}

// latestOffsets is offset+1 of the last message per partition in the batch.
func latestOffsets(batch []*kafka.Message) []kafka.TopicPartition {
	latest := map[string]kafka.TopicPartition{}
	for _, m := range batch {
		tp := m.TopicPartition
		k := fmt.Sprintf("%s/%d", *tp.Topic, tp.Partition)
		if cur, ok := latest[k]; !ok || tp.Offset > cur.Offset {
			latest[k] = tp
		}
	}
	offsets := make([]kafka.TopicPartition, 0, len(latest))
	for _, tp := range latest {
		tp.Offset++
		offsets = append(offsets, tp)
	}
	return offsets
}

// recreate closes the client after a fatal error and opens a new one with
// the same configuration and subscription, backing off between attempts.
// It returns only ctx's error, or an error once Close has been called.
func (b *BatchConsumer) recreate(ctx context.Context, cause error) error {
	b.mu.Lock()
	old := b.consumer
	topics := b.topics
	b.mu.Unlock()
	b.logger.Error("kafka consumer in fatal state; recreating", zap.Error(cause))
	if err := old.Close(); err != nil {
		b.logger.Warn("closing failed kafka consumer", zap.Error(err))
	}
	backoff := b.opts.ReconnectBackoff
	for attempt := 1; ; attempt++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
		if backoff *= 2; backoff > maxReconnectBackoff {
			backoff = maxReconnectBackoff
		}
		c, err := b.open()
		if err == nil && len(topics) > 0 {
			if err = c.SubscribeTopics(topics, b.onRebalance); err != nil {
				_ = c.Close()
			}
		}
		if err != nil {
			b.logger.Warn("recreating kafka consumer failed", zap.Int("attempt", attempt), zap.Duration("backoff", backoff), zap.Error(err))
			continue
		}
		b.mu.Lock()
		if b.closed {
			b.mu.Unlock()
			_ = c.Close()
			return errors.New("kafka consumer closed")
		}
		b.consumer = c
		b.mu.Unlock()
		b.live.recreated()
		b.logger.Info("kafka consumer recreated", zap.Int("attempt", attempt), zap.Strings("topics", topics))
		return nil
	}
}

// Close shuts down the consumer.
func (b *BatchConsumer) Close() error {
	b.mu.Lock()
	b.closed = true
	c := b.consumer
	b.mu.Unlock()
	return c.Close()
}

// processBatch applies the retry-then-bisect policy. It is separate from the
// consumer so it can be tested without a broker.
func processBatch(ctx context.Context, batch []*kafka.Message, handle BatchHandler, opts BatchOptions, logger *zap.Logger) error {
	var lastErr error
	backoff := opts.RetryBackoff
	for attempt := 0; attempt <= opts.MaxRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
			}
			backoff *= 2
		}
		lastErr = handle(ctx, batch)
		if lastErr == nil {
			return nil
		}
		if errors.Is(lastErr, context.Canceled) {
			return lastErr
		}
		logger.Warn("batch handler failed", zap.Int("attempt", attempt+1), zap.Int("size", len(batch)), zap.Error(lastErr))
	}
	if len(batch) == 1 {
		if opts.DeadLetter == nil {
			return fmt.Errorf("message %s[%d]@%d rejected and no dead-letter sink configured: %w",
				*batch[0].TopicPartition.Topic, batch[0].TopicPartition.Partition, batch[0].TopicPartition.Offset, lastErr)
		}
		logger.Error("dead-lettering message", zap.Int64("offset", int64(batch[0].TopicPartition.Offset)), zap.Error(lastErr))
		return opts.DeadLetter(ctx, batch[0], lastErr)
	}
	mid := len(batch) / 2
	if err := processBatch(ctx, batch[:mid], handle, opts, logger); err != nil {
		return err
	}
	return processBatch(ctx, batch[mid:], handle, opts, logger)
}
