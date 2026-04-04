package kafka

import (
	"encoding/json"
	"fmt"
	"sync"

	"github.com/confluentinc/confluent-kafka-go/v2/kafka"
	"go.uber.org/zap"
)

// Producer wraps a confluent-kafka-go producer with delivery report
// handling and graceful shutdown support.
type Producer struct {
	producer *kafka.Producer
	logger   *zap.Logger
	stopOnce sync.Once
	done     chan struct{}
}

// NewProducer initializes a Kafka producer connected to the specified brokers.
func NewProducer(brokers string, logger *zap.Logger) (*Producer, error) {
	p, err := kafka.NewProducer(&kafka.ConfigMap{
		"bootstrap.servers":            brokers,
		"acks":                         "all",
		"retries":                      5,
		"retry.backoff.ms":             100,
		"linger.ms":                    10,
		"batch.num.messages":           500,
		"queue.buffering.max.messages": 100000,
		"compression.type":            "gzip",
		"enable.idempotence":          true,
	})
	if err != nil {
		return nil, fmt.Errorf("creating kafka producer: %w", err)
	}

	prod := &Producer{
		producer: p,
		logger:   logger,
		done:     make(chan struct{}),
	}

	go prod.handleDeliveryReports()

	return prod, nil
}

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

// Produce sends a single message to the specified topic with the given key.
func (p *Producer) Produce(topic string, key string, value interface{}, headers map[string]string) error {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("marshaling message: %w", err)
	}

	var hdrs []kafka.Header
	for k, v := range headers {
		hdrs = append(hdrs, kafka.Header{Key: k, Value: []byte(v)})
	}

	return p.producer.Produce(&kafka.Message{
		TopicPartition: kafka.TopicPartition{
			Topic:     &topic,
			Partition: kafka.PartitionAny,
		},
		Key:     []byte(key),
		Value:   data,
		Headers: hdrs,
	}, nil)
}

// ProduceRaw sends a pre-serialized message to the specified topic.
func (p *Producer) ProduceRaw(topic string, key []byte, value []byte, headers map[string]string) error {
	var hdrs []kafka.Header
	for k, v := range headers {
		hdrs = append(hdrs, kafka.Header{Key: k, Value: []byte(v)})
	}

	return p.producer.Produce(&kafka.Message{
		TopicPartition: kafka.TopicPartition{
			Topic:     &topic,
			Partition: kafka.PartitionAny,
		},
		Key:     key,
		Value:   value,
		Headers: hdrs,
	}, nil)
}

// Flush waits for all outstanding messages to be delivered.
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
