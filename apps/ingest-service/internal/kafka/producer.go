package kafka

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/confluentinc/confluent-kafka-go/v2/kafka"
	"go.uber.org/zap"

	"github.com/sentinel/ingest-service/internal/metrics"
	"github.com/sentinel/ingest-service/internal/models"
)

// Producer wraps a confluent-kafka-go producer with delivery report
// handling, batch production, and graceful shutdown support.
type Producer struct {
	producer *kafka.Producer
	logger   *zap.Logger
	metrics  *metrics.Metrics
	stopOnce sync.Once
	done     chan struct{}
}

// NewProducer initializes a Kafka producer connected to the specified brokers.
// It starts a background goroutine to handle delivery reports.
func NewProducer(brokers string, logger *zap.Logger, m *metrics.Metrics) (*Producer, error) {
	p, err := kafka.NewProducer(&kafka.ConfigMap{
		"bootstrap.servers":        brokers,
		"acks":                     "all",
		"retries":                  5,
		"retry.backoff.ms":         100,
		"linger.ms":               10,
		"batch.num.messages":       500,
		"queue.buffering.max.messages": 100000,
		"compression.type":        "lz4",
		"enable.idempotence":      true,
	})
	if err != nil {
		return nil, fmt.Errorf("creating kafka producer: %w", err)
	}

	prod := &Producer{
		producer: p,
		logger:   logger,
		metrics:  m,
		done:     make(chan struct{}),
	}

	go prod.handleDeliveryReports()

	return prod, nil
}

// handleDeliveryReports processes asynchronous delivery confirmations from Kafka.
// It runs until the producer is closed.
func (p *Producer) handleDeliveryReports() {
	defer close(p.done)

	for ev := range p.producer.Events() {
		switch e := ev.(type) {
		case *kafka.Message:
			if e.TopicPartition.Error != nil {
				p.logger.Error("kafka delivery failed",
					zap.String("topic", *e.TopicPartition.Topic),
					zap.Error(e.TopicPartition.Error),
				)
			}
		case kafka.Error:
			p.logger.Error("kafka producer error",
				zap.String("code", e.Code().String()),
				zap.Error(e),
			)
		}
	}
}

// ProduceBatch sends a batch of entity positions to the specified Kafka topic.
// Messages are keyed by entity_id to guarantee per-entity ordering within a partition.
func (p *Producer) ProduceBatch(topic string, messages []*models.EntityPosition) error {
	if len(messages) == 0 {
		return nil
	}

	start := time.Now()

	for _, msg := range messages {
		value, err := json.Marshal(msg)
		if err != nil {
			p.logger.Error("failed to marshal entity position",
				zap.String("entity_id", msg.EntityID),
				zap.Error(err),
			)
			continue
		}

		err = p.producer.Produce(&kafka.Message{
			TopicPartition: kafka.TopicPartition{
				Topic:     &topic,
				Partition: kafka.PartitionAny,
			},
			Key:   []byte(msg.EntityID),
			Value: value,
			Headers: []kafka.Header{
				{Key: "source", Value: []byte(msg.Source)},
				{Key: "entity_type", Value: []byte(msg.EntityType)},
			},
		}, nil)
		if err != nil {
			p.logger.Error("failed to enqueue kafka message",
				zap.String("entity_id", msg.EntityID),
				zap.Error(err),
			)
		}
	}

	p.metrics.KafkaProduceDuration.Observe(time.Since(start).Seconds())
	p.metrics.BatchSize.Observe(float64(len(messages)))

	return nil
}

// Flush waits for all outstanding messages to be delivered, up to the given timeout.
// Returns the number of messages still in the queue after the timeout.
func (p *Producer) Flush(timeoutMs int) int {
	return p.producer.Flush(timeoutMs)
}

// Close flushes remaining messages and shuts down the producer.
func (p *Producer) Close() {
	p.stopOnce.Do(func() {
		p.logger.Info("flushing kafka producer")
		remaining := p.producer.Flush(10000)
		if remaining > 0 {
			p.logger.Warn("kafka producer closed with unflushed messages",
				zap.Int("remaining", remaining),
			)
		}
		p.producer.Close()
		<-p.done
		p.logger.Info("kafka producer closed")
	})
}

// IsHealthy returns true if the producer is connected and operational.
func (p *Producer) IsHealthy() bool {
	_, err := p.producer.GetMetadata(nil, true, 5000)
	return err == nil
}
