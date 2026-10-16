package kafka

import (
	"context"
	"errors"
	"fmt"
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
	return o
}

// BatchConsumer delivers messages to a handler in batches and commits
// offsets only after the handler succeeds, which gives at-least-once
// delivery: a crash mid-batch redelivers the batch, so handlers must be
// idempotent. A batch that keeps failing is bisected until the poison
// message is isolated and handed to DeadLetter.
type BatchConsumer struct {
	consumer *kafka.Consumer
	logger   *zap.Logger
	opts     BatchOptions
}

// NewBatchConsumer creates a consumer with auto-commit disabled. offsetReset
// is "earliest" or "latest".
func NewBatchConsumer(brokers, groupID, clientID, offsetReset string, opts BatchOptions, logger *zap.Logger) (*BatchConsumer, error) {
	c, err := kafka.NewConsumer(&kafka.ConfigMap{
		"bootstrap.servers":        brokers,
		"group.id":                 groupID,
		"client.id":                clientID,
		"auto.offset.reset":        offsetReset,
		"enable.auto.commit":       false,
		"enable.auto.offset.store": false,
	})
	if err != nil {
		return nil, fmt.Errorf("creating kafka consumer: %w", err)
	}
	return &BatchConsumer{consumer: c, logger: logger, opts: opts.withDefaults()}, nil
}

// Subscribe subscribes to the given topics.
func (b *BatchConsumer) Subscribe(topics []string) error {
	return b.consumer.SubscribeTopics(topics, nil)
}

// Run polls until ctx is cancelled or a fatal error occurs. Each batch is
// passed to handle; offsets are committed after handle (or the dead-letter
// path) succeeds.
func (b *BatchConsumer) Run(ctx context.Context, handle BatchHandler) error {
	for {
		batch, err := b.collect(ctx)
		if err != nil {
			return err
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
		if err := b.commit(batch); err != nil {
			return err
		}
	}
}

// collect polls until the batch is full, MaxWait has elapsed since the first
// message, or ctx is done.
func (b *BatchConsumer) collect(ctx context.Context) ([]*kafka.Message, error) {
	var batch []*kafka.Message
	var deadline time.Time
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
		ev := b.consumer.Poll(timeout)
		switch e := ev.(type) {
		case nil:
			if !deadline.IsZero() && time.Now().After(deadline) {
				return batch, nil
			}
		case *kafka.Message:
			if len(batch) == 0 {
				deadline = time.Now().Add(b.opts.MaxWait)
			}
			batch = append(batch, e)
		case kafka.Error:
			if e.IsFatal() {
				return batch, fmt.Errorf("fatal kafka error: %w", e)
			}
			b.logger.Warn("kafka consumer error", zap.Error(e))
		}
	}
	return batch, nil
}

// commit stores and commits offset+1 of the last message per partition.
func (b *BatchConsumer) commit(batch []*kafka.Message) error {
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
	if _, err := b.consumer.CommitOffsets(offsets); err != nil {
		return fmt.Errorf("committing offsets: %w", err)
	}
	return nil
}

// Close shuts down the consumer.
func (b *BatchConsumer) Close() error {
	return b.consumer.Close()
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
