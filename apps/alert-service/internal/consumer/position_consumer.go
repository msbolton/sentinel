package consumer

import (
	"context"
	"encoding/json"

	"github.com/sentinel/go-common/kafka"
	"github.com/sentinel/alert-service/internal/evaluator"
	"go.uber.org/zap"
)

// PositionConsumer consumes entity position events for alert evaluation.
type PositionConsumer struct {
	evaluator *evaluator.Evaluator
	consumer  *kafka.Consumer
	logger    *zap.Logger
	done      chan struct{}
}

// NewPositionConsumer creates a new position consumer for alert evaluation.
func NewPositionConsumer(brokers string, eval *evaluator.Evaluator, logger *zap.Logger) (*PositionConsumer, error) {
	consumer, err := kafka.NewConsumer(brokers, kafka.GroupAlertService, "alert-service", logger)
	if err != nil {
		return nil, err
	}

	if err := consumer.Subscribe([]string{kafka.TopicEntityPosition}); err != nil {
		return nil, err
	}

	return &PositionConsumer{
		evaluator: eval,
		consumer:  consumer,
		logger:    logger,
		done:      make(chan struct{}),
	}, nil
}

// Start begins consuming position events in a goroutine.
func (c *PositionConsumer) Start() {
	go c.run()
}

func (c *PositionConsumer) run() {
	c.logger.Info("alert position consumer started")
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

		var payload positionEventPayload
		if err := json.Unmarshal(msg.Value, &payload); err != nil {
			c.logger.Error("failed to unmarshal position event", zap.Error(err))
			continue
		}

		c.evaluator.EvaluatePosition(
			context.Background(),
			payload.EntityID,
			payload.Latitude,
			payload.Longitude,
			payload.EntityType,
			payload.SpeedKnots,
		)
	}
}

// Stop signals the consumer to stop.
func (c *PositionConsumer) Stop() {
	close(c.done)
	c.consumer.Close()
}

type positionEventPayload struct {
	EntityID   string   `json:"entity_id"`
	EntityType string   `json:"entity_type"`
	Latitude   float64  `json:"latitude"`
	Longitude  float64  `json:"longitude"`
	SpeedKnots *float64 `json:"speed_knots"`
}
