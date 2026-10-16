// Package consumer records accepted fixes from entities.events as track
// points.
package consumer

import (
	"context"
	"time"

	ckafka "github.com/confluentinc/confluent-kafka-go/v2/kafka"
	"go.uber.org/zap"
	"google.golang.org/protobuf/proto"

	commonkafka "github.com/sentinel/go-common/kafka"
	"github.com/sentinel/proto-gen/pbjson"
	sentinelv1 "github.com/sentinel/proto-gen/sentinel/v1"

	"github.com/sentinel/track-service/internal/batch"
	"github.com/sentinel/track-service/internal/models"
)

// PositionConsumer turns position and created events into track points.
type PositionConsumer struct {
	bc      *commonkafka.BatchConsumer
	batcher *batch.Batcher
	logger  *zap.Logger
	cancel  context.CancelFunc
	done    chan struct{}
}

// NewPositionConsumer creates and subscribes the consumer.
func NewPositionConsumer(brokers string, batcher *batch.Batcher, logger *zap.Logger) (*PositionConsumer, error) {
	bc, err := commonkafka.NewBatchConsumer(brokers, commonkafka.GroupTrackService, "sentinel-track-service", "earliest",
		commonkafka.BatchOptions{MaxMessages: 500, MaxWait: 200 * time.Millisecond}, logger)
	if err != nil {
		return nil, err
	}
	if err := bc.Subscribe([]string{commonkafka.TopicEntityEvents}); err != nil {
		_ = bc.Close()
		return nil, err
	}
	return &PositionConsumer{bc: bc, batcher: batcher, logger: logger, done: make(chan struct{})}, nil
}

// Start begins consuming in the background.
func (c *PositionConsumer) Start() {
	ctx, cancel := context.WithCancel(context.Background())
	c.cancel = cancel
	go func() {
		defer close(c.done)
		c.logger.Info("track position consumer started", zap.String("topic", commonkafka.TopicEntityEvents))
		if err := c.bc.Run(ctx, c.handle); err != nil {
			c.logger.Error("track position consumer stopped", zap.Error(err))
		}
	}()
}

// Stop ends consumption and closes the consumer.
func (c *PositionConsumer) Stop() {
	if c.cancel != nil {
		c.cancel()
		<-c.done
	}
	_ = c.bc.Close()
}

func (c *PositionConsumer) handle(_ context.Context, msgs []*ckafka.Message) error {
	for _, m := range msgs {
		kind := eventType(m)
		if kind != "position" && kind != "created" {
			continue
		}
		var ev sentinelv1.EntityEvent
		if err := proto.Unmarshal(m.Value, &ev); err != nil {
			c.logger.Warn("undecodable entity event skipped", zap.Int64("offset", int64(m.TopicPartition.Offset)), zap.Error(err))
			continue
		}
		if p, ok := PointFromEvent(&ev); ok {
			c.batcher.AddPoint(p)
		}
	}
	return nil
}

// eventType reads the event-type header without decoding the value.
func eventType(m *ckafka.Message) string {
	for _, h := range m.Headers {
		if h.Key == commonkafka.HeaderEventType {
			return string(h.Value)
		}
	}
	return ""
}

// PointFromEvent maps a created or position event to a track point. The
// point takes its timestamp from the observation that caused the event and
// its altitude from the entity's position.
func PointFromEvent(ev *sentinelv1.EntityEvent) (models.BufferedPoint, bool) {
	var entity *sentinelv1.Entity
	var ref *sentinelv1.ObservationRef
	switch e := ev.Event.(type) {
	case *sentinelv1.EntityEvent_Position:
		entity, ref = e.Position.Entity, e.Position.Observation
	case *sentinelv1.EntityEvent_Created:
		entity, ref = e.Created.Entity, e.Created.Observation
	default:
		return models.BufferedPoint{}, false
	}
	if entity == nil || entity.Position == nil || entity.Id == "" {
		return models.BufferedPoint{}, false
	}
	p := models.BufferedPoint{
		EntityID:      entity.Id,
		Latitude:      entity.Position.Latitude,
		Longitude:     entity.Position.Longitude,
		Altitude:      entity.Position.AltitudeMeters,
		Heading:       entity.Heading,
		SpeedKnots:    entity.SpeedKnots,
		Course:        entity.Course,
		CircularError: entity.CircularError,
		Source:        strPtr(pbjson.EnumName(entity.Source)),
		EntityType:    strPtr(pbjson.EnumName(entity.EntityType)),
		Timestamp:     time.Now().UTC(),
	}
	if entity.Kinematics != nil && entity.Kinematics.Velocity != nil {
		v := entity.Kinematics.Velocity
		p.VelocityNorth, p.VelocityEast, p.VelocityUp = &v.North, &v.East, &v.Up
	}
	switch {
	case ref != nil && ref.SourceTimestamp != nil:
		p.Timestamp = ref.SourceTimestamp.AsTime()
	case entity.LastObservedAt != nil:
		p.Timestamp = entity.LastObservedAt.AsTime()
	case ev.EventTime != nil:
		p.Timestamp = ev.EventTime.AsTime()
	}
	if ref != nil {
		p.FeedID = strPtr(ref.FeedId)
		p.SourceEntityID = strPtr(ref.SourceEntityId)
		p.ObservationID = strPtr(ref.ObservationId)
		if ref.Source != sentinelv1.EntitySource_ENTITY_SOURCE_UNSPECIFIED {
			p.Source = strPtr(pbjson.EnumName(ref.Source))
		}
	} else if entity.LastFeedId != "" {
		p.FeedID = strPtr(entity.LastFeedId)
	}
	return p, true
}

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
