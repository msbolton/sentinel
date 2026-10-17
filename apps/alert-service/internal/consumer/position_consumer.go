// Package consumer feeds the alert evaluator from entities.events and
// persists alerts other producers publish on alerts.events.
package consumer

import (
	"context"
	"time"

	ckafka "github.com/confluentinc/confluent-kafka-go/v2/kafka"
	"go.uber.org/zap"
	"google.golang.org/protobuf/proto"

	"github.com/sentinel/go-common/kafka"
	"github.com/sentinel/proto-gen/pbjson"
	sentinelv1 "github.com/sentinel/proto-gen/sentinel/v1"

	"github.com/sentinel/alert-service/internal/evaluator"
)

// PositionConsumer evaluates rules on position events and stores external alerts.
type PositionConsumer struct {
	evaluator *evaluator.Evaluator
	bc        *kafka.BatchConsumer
	logger    *zap.Logger
	cancel    context.CancelFunc
	done      chan struct{}
}

// NewPositionConsumer creates and subscribes the consumer.
func NewPositionConsumer(brokers string, eval *evaluator.Evaluator, logger *zap.Logger) (*PositionConsumer, error) {
	bc, err := kafka.NewBatchConsumer(brokers, kafka.GroupAlertService, "alert-service", "latest",
		kafka.BatchOptions{MaxMessages: 200, MaxWait: 200 * time.Millisecond}, logger)
	if err != nil {
		return nil, err
	}
	if err := bc.Subscribe([]string{kafka.TopicEntityEvents, kafka.TopicAlertEvents}); err != nil {
		_ = bc.Close()
		return nil, err
	}
	return &PositionConsumer{evaluator: eval, bc: bc, logger: logger, done: make(chan struct{})}, nil
}

// Start begins consuming in the background.
func (c *PositionConsumer) Start() {
	ctx, cancel := context.WithCancel(context.Background())
	c.cancel = cancel
	go func() {
		defer close(c.done)
		c.logger.Info("alert consumer started", zap.Strings("topics", []string{kafka.TopicEntityEvents, kafka.TopicAlertEvents}))
		if err := c.bc.Run(ctx, c.handle); err != nil {
			c.logger.Error("alert consumer stopped", zap.Error(err))
		}
	}()
}

// Stop ends consumption.
func (c *PositionConsumer) Stop() {
	if c.cancel != nil {
		c.cancel()
		<-c.done
	}
	_ = c.bc.Close()
}

func (c *PositionConsumer) handle(ctx context.Context, msgs []*ckafka.Message) error {
	for _, m := range msgs {
		switch *m.TopicPartition.Topic {
		case kafka.TopicEntityEvents:
			if header(m, kafka.HeaderEventType) != "position" {
				continue
			}
			var ev sentinelv1.EntityEvent
			if err := proto.Unmarshal(m.Value, &ev); err != nil {
				c.logger.Warn("undecodable entity event skipped", zap.Error(err))
				continue
			}
			pos := ev.GetPosition()
			if pos == nil || pos.Entity == nil || pos.Entity.Position == nil {
				continue
			}
			e := pos.Entity
			c.evaluator.EvaluatePosition(ctx, e.Id, e.Position.Latitude, e.Position.Longitude, pbjson.EnumName(e.EntityType), e.SpeedKnots, pbjson.EnumName(e.Classification))
		case kafka.TopicAlertEvents:
			if header(m, kafka.HeaderProducer) == evaluator.ProducerName {
				continue
			}
			var ev sentinelv1.AlertEvent
			if err := proto.Unmarshal(m.Value, &ev); err != nil {
				c.logger.Warn("undecodable alert event skipped", zap.Error(err))
				continue
			}
			if err := c.evaluator.StoreExternalAlert(ctx, &ev); err != nil {
				c.logger.Error("external alert not stored", zap.String("producer", ev.Producer), zap.Error(err))
			}
		}
	}
	return nil
}

func header(m *ckafka.Message, key string) string {
	for _, h := range m.Headers {
		if h.Key == key {
			return string(h.Value)
		}
	}
	return ""
}
