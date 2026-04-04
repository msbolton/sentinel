package kafka

import (
	"fmt"

	"github.com/confluentinc/confluent-kafka-go/v2/kafka"
	"go.uber.org/zap"
)

// Consumer wraps a confluent-kafka-go consumer for subscribing to topics.
type Consumer struct {
	consumer *kafka.Consumer
	logger   *zap.Logger
}

// NewConsumer creates a Kafka consumer with the given group and configuration.
func NewConsumer(brokers, groupID, clientID string, logger *zap.Logger) (*Consumer, error) {
	c, err := kafka.NewConsumer(&kafka.ConfigMap{
		"bootstrap.servers":  brokers,
		"group.id":           groupID,
		"client.id":          clientID,
		"auto.offset.reset":  "earliest",
		"enable.auto.commit": true,
	})
	if err != nil {
		return nil, fmt.Errorf("creating kafka consumer: %w", err)
	}

	return &Consumer{
		consumer: c,
		logger:   logger,
	}, nil
}

// Subscribe subscribes to the given topics.
func (c *Consumer) Subscribe(topics []string) error {
	return c.consumer.SubscribeTopics(topics, nil)
}

// Poll returns the next message from the subscribed topics.
// Returns nil if no message is available within the timeout (ms).
func (c *Consumer) Poll(timeoutMs int) (*kafka.Message, error) {
	ev := c.consumer.Poll(timeoutMs)
	if ev == nil {
		return nil, nil
	}

	switch e := ev.(type) {
	case *kafka.Message:
		return e, nil
	case kafka.Error:
		if e.IsFatal() {
			return nil, fmt.Errorf("fatal kafka error: %w", e)
		}
		c.logger.Warn("kafka consumer error", zap.Error(e))
		return nil, nil
	default:
		return nil, nil
	}
}

// Close shuts down the consumer.
func (c *Consumer) Close() error {
	return c.consumer.Close()
}
