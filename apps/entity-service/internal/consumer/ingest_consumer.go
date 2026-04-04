package consumer

import (
	"encoding/json"

	"github.com/sentinel/go-common/kafka"
	"github.com/sentinel/entity-service/internal/batch"
	"go.uber.org/zap"
)

// IngestConsumer consumes raw ingest messages from Kafka.
type IngestConsumer struct {
	buffer   *batch.Buffer
	consumer *kafka.Consumer
	logger   *zap.Logger
	done     chan struct{}
}

// NewIngestConsumer creates a new ingest consumer.
func NewIngestConsumer(brokers string, buffer *batch.Buffer, logger *zap.Logger) (*IngestConsumer, error) {
	consumer, err := kafka.NewConsumer(brokers, kafka.GroupEntityService, "entity-service", logger)
	if err != nil {
		return nil, err
	}

	if err := consumer.Subscribe([]string{kafka.TopicIngestRaw}); err != nil {
		return nil, err
	}

	return &IngestConsumer{
		buffer:   buffer,
		consumer: consumer,
		logger:   logger,
		done:     make(chan struct{}),
	}, nil
}

// Start begins consuming ingest messages in a goroutine.
func (c *IngestConsumer) Start() {
	go c.run()
}

func (c *IngestConsumer) run() {
	c.logger.Info("ingest consumer started")
	for {
		msg, err := c.consumer.Poll(100)
		if err != nil {
			select {
			case <-c.done:
				return
			default:
				continue
			}
		}
		if msg == nil {
			select {
			case <-c.done:
				return
			default:
				continue
			}
		}

		var ingest batch.IngestMessage
		if err := json.Unmarshal(msg.Value, &ingest); err != nil {
			c.logger.Error("failed to unmarshal ingest message", zap.Error(err))
			continue
		}

		c.buffer.Add(ingest)
	}
}

// Stop signals the consumer to stop.
func (c *IngestConsumer) Stop() {
	close(c.done)
	c.consumer.Close()
}
