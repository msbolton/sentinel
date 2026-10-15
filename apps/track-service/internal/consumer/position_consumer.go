package consumer

import (
	"encoding/json"
	"time"

	"go.uber.org/zap"

	commonkafka "github.com/sentinel/go-common/kafka"
	"github.com/sentinel/track-service/internal/batch"
	"github.com/sentinel/track-service/internal/models"
)

// PositionConsumer consumes events.entity.position messages from Kafka
// and records them as track points via the batch service.
type PositionConsumer struct {
	consumer *commonkafka.Consumer
	batcher  *batch.Batcher
	logger   *zap.Logger
	stopCh   chan struct{}
}

// NewPositionConsumer creates a Kafka consumer for position events.
func NewPositionConsumer(brokers string, batcher *batch.Batcher, logger *zap.Logger) (*PositionConsumer, error) {
	consumer, err := commonkafka.NewConsumer(
		brokers,
		commonkafka.GroupTrackService,
		"sentinel-track-service",
		logger,
	)
	if err != nil {
		return nil, err
	}

	if err := consumer.Subscribe([]string{commonkafka.TopicEntityPosition}); err != nil {
		consumer.Close()
		return nil, err
	}

	return &PositionConsumer{
		consumer: consumer,
		batcher:  batcher,
		logger:   logger,
		stopCh:   make(chan struct{}),
	}, nil
}

// Start begins consuming messages in a loop.
func (c *PositionConsumer) Start() {
	go c.consumeLoop()
}

// Stop signals the consumer to stop.
func (c *PositionConsumer) Stop() {
	close(c.stopCh)
	c.consumer.Close()
}

func (c *PositionConsumer) consumeLoop() {
	for {
		select {
		case <-c.stopCh:
			return
		default:
		}

		msg, err := c.consumer.Poll(100)
		if err != nil {
			c.logger.Error("kafka poll error", zap.Error(err))
			continue
		}
		if msg == nil {
			continue
		}

		var payload models.PositionEventPayload
		if err := json.Unmarshal(msg.Value, &payload); err != nil {
			c.logger.Error("failed to unmarshal position event",
				zap.Error(err),
				zap.ByteString("value", msg.Value),
			)
			continue
		}

		ts, err := time.Parse(time.RFC3339Nano, payload.Timestamp)
		if err != nil {
			ts = time.Now()
		}

		c.batcher.AddPoint(models.BufferedPoint{
			EntityID:      payload.EntityID,
			Latitude:      payload.Latitude,
			Longitude:     payload.Longitude,
			Heading:       payload.Heading,
			SpeedKnots:    payload.SpeedKnots,
			Course:        payload.Course,
			Source:        stringPtr(payload.Source),
			Timestamp:     ts,
			Altitude:      payload.Altitude,
			VelocityNorth: payload.VelocityNorth,
			VelocityEast:  payload.VelocityEast,
			VelocityUp:    payload.VelocityUp,
			CircularError: payload.CircularError,
		})
	}
}

func stringPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
