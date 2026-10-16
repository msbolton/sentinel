// Package consumer reads observations.raw in batches, hands each batch to
// the fusion processor, and commits offsets only after the batch is stored.
package consumer

import (
	"context"
	"fmt"
	"time"

	ckafka "github.com/confluentinc/confluent-kafka-go/v2/kafka"
	"go.uber.org/zap"
	"google.golang.org/protobuf/proto"

	"github.com/sentinel/go-common/kafka"
	sentinelv1 "github.com/sentinel/proto-gen/sentinel/v1"

	"github.com/sentinel/entity-service/internal/fusion"
)

// Options tunes the consumer.
type Options struct {
	Brokers     string
	MaxMessages int
	MaxWait     time.Duration
}

// ObservationConsumer drives the fusion processor from Kafka.
type ObservationConsumer struct {
	bc        *kafka.BatchConsumer
	processor *fusion.Processor
	producer  *kafka.Producer
	logger    *zap.Logger
}

// New creates and subscribes the consumer.
func New(opts Options, processor *fusion.Processor, producer *kafka.Producer, logger *zap.Logger) (*ObservationConsumer, error) {
	c := &ObservationConsumer{processor: processor, producer: producer, logger: logger}
	bc, err := kafka.NewBatchConsumer(opts.Brokers, kafka.GroupEntityService, "entity-service", "earliest", kafka.BatchOptions{
		MaxMessages: opts.MaxMessages,
		MaxWait:     opts.MaxWait,
		DeadLetter:  c.deadLetter,
	}, logger)
	if err != nil {
		return nil, err
	}
	if err := bc.Subscribe([]string{kafka.TopicObservationsRaw}); err != nil {
		return nil, err
	}
	c.bc = bc
	return c, nil
}

// Run consumes until ctx is cancelled.
func (c *ObservationConsumer) Run(ctx context.Context) error {
	c.logger.Info("observation consumer started", zap.String("topic", kafka.TopicObservationsRaw))
	return c.bc.Run(ctx, c.handle)
}

// Close shuts the consumer down.
func (c *ObservationConsumer) Close() error { return c.bc.Close() }

func (c *ObservationConsumer) handle(ctx context.Context, msgs []*ckafka.Message) error {
	observations := make([]*sentinelv1.Observation, 0, len(msgs))
	for _, m := range msgs {
		var o sentinelv1.Observation
		if err := proto.Unmarshal(m.Value, &o); err != nil {
			// Not retryable: dead-letter the one message and move on.
			if dlqErr := c.deadLetter(ctx, m, fmt.Errorf("decoding observation: %w", err)); dlqErr != nil {
				return dlqErr
			}
			continue
		}
		observations = append(observations, &o)
	}
	start := time.Now()
	stats, err := c.processor.ProcessBatch(ctx, observations)
	if err != nil {
		return err
	}
	c.logger.Debug("batch fused",
		zap.Int("reports", stats.Reports), zap.Int("accepted", stats.Accepted), zap.Int("created", stats.Created),
		zap.Int("restored", stats.Restored), zap.Any("rejected", stats.Rejected), zap.Duration("took", time.Since(start)))
	return nil
}

// deadLetter forwards a message the service cannot process to
// observations.dlq with the failure in the headers.
func (c *ObservationConsumer) deadLetter(_ context.Context, m *ckafka.Message, cause error) error {
	headers := map[string]string{
		kafka.HeaderProducer: "entity-service",
		kafka.HeaderError:    cause.Error(),
		"reason":             "consumer_error",
		"src-topic":          *m.TopicPartition.Topic,
		"src-partition":      fmt.Sprint(m.TopicPartition.Partition),
		"src-offset":         fmt.Sprint(m.TopicPartition.Offset),
	}
	for _, h := range m.Headers {
		if h.Key == kafka.HeaderFeedID {
			headers[kafka.HeaderFeedID] = string(h.Value)
		}
	}
	c.logger.Error("observation dead-lettered", zap.String("topic", *m.TopicPartition.Topic), zap.Int64("offset", int64(m.TopicPartition.Offset)), zap.Error(cause))
	return c.producer.ProduceRaw(kafka.TopicObservationsDLQ, m.Key, m.Value, headers)
}
