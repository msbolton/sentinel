package kafka

import (
	"errors"
	"fmt"
	"sync"

	"github.com/confluentinc/confluent-kafka-go/v2/kafka"
	"go.uber.org/zap"

	"github.com/sentinel/ingest-service/internal/metrics"
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
		"bootstrap.servers":            brokers,
		"acks":                         "all",
		"retries":                      5,
		"retry.backoff.ms":             100,
		"linger.ms":                    10,
		"batch.num.messages":           500,
		"queue.buffering.max.messages": 100000,
		"compression.type":             "gzip",
		"enable.idempotence":           true,
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
			feedID, _ := e.Opaque.(string)
			if e.TopicPartition.Error != nil {
				if feedID != "" && p.metrics != nil {
					p.metrics.ObservationsDeliveryFailed.WithLabelValues(feedID).Inc()
				}
				p.logger.Error("kafka delivery failed",
					zap.String("topic", *e.TopicPartition.Topic),
					zap.String("feed", feedID),
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

// Produce enqueues one message. The returned error is librdkafka's: a
// kafka.Error with code ErrQueueFull when the local queue is full, which
// callers should treat as backpressure and retry.
func (p *Producer) Produce(msg *kafka.Message) error {
	return p.producer.Produce(msg, nil)
}

// IsQueueFull reports whether err is the local-queue-full condition.
func IsQueueFull(err error) bool {
	var kerr kafka.Error
	return errors.As(err, &kerr) && kerr.Code() == kafka.ErrQueueFull
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
