package events

import (
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/sentinel/go-common/kafka"
	sentinelv1 "github.com/sentinel/proto-gen/sentinel/v1"
)

// ProducerName is the producer header value.
const ProducerName = "entity-service"

// Producer is what the publisher needs from the Kafka producer.
type Producer interface {
	ProduceProto(topic, key string, msg proto.Message, producer string, extra map[string]string) error
}

// Ageout describes a stale or aged-out transition.
type Ageout struct {
	EntityType    string
	Source        string
	FeedID        string
	PreviousState string
	LastSeenAt    time.Time
	ThresholdMs   int64
}

// Publisher writes EntityEvents to entities.events.
type Publisher struct {
	producer Producer
	logger   *zap.Logger
}

// NewPublisher creates a publisher.
func NewPublisher(p Producer, logger *zap.Logger) *Publisher {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Publisher{producer: p, logger: logger}
}

// Created announces a new entity.
func (p *Publisher) Created(entity *sentinelv1.Entity, ref *sentinelv1.ObservationRef) {
	p.emit(entity.Id, "created", &sentinelv1.EntityEvent{EntityId: entity.Id, Event: &sentinelv1.EntityEvent_Created{Created: &sentinelv1.EntityCreated{Entity: entity, Observation: ref}}})
}

// Position announces an accepted fix.
func (p *Publisher) Position(entity *sentinelv1.Entity, ref *sentinelv1.ObservationRef) {
	p.emit(entity.Id, "position", &sentinelv1.EntityEvent{EntityId: entity.Id, Event: &sentinelv1.EntityEvent_Position{Position: &sentinelv1.EntityPosition{Entity: entity, Observation: ref}}})
}

// Restored announces that a stale or aged-out entity reported again. It is
// published before the Position that restored it.
func (p *Publisher) Restored(entity *sentinelv1.Entity, previousState string) {
	prev := parseEnum[sentinelv1.AgeoutState](previousState)
	p.emit(entity.Id, "restored", &sentinelv1.EntityEvent{EntityId: entity.Id, Event: &sentinelv1.EntityEvent_Restored{Restored: &sentinelv1.EntityRestored{Entity: entity, PreviousState: prev}}})
}

// Updated announces a metadata change.
func (p *Publisher) Updated(entity *sentinelv1.Entity, changed []string) {
	p.emit(entity.Id, "updated", &sentinelv1.EntityEvent{EntityId: entity.Id, Event: &sentinelv1.EntityEvent_Updated{Updated: &sentinelv1.EntityUpdated{Entity: entity, ChangedFields: changed}}})
}

// Deleted announces a delete; bulk with an empty id means everything.
func (p *Publisher) Deleted(entityID string, bulk bool, count int) {
	key := entityID
	if bulk {
		key = "all"
	}
	p.emit(key, "deleted", &sentinelv1.EntityEvent{EntityId: entityID, Event: &sentinelv1.EntityEvent_Deleted{Deleted: &sentinelv1.EntityDeleted{Bulk: bulk, Count: int32(count)}}})
}

// Stale announces a LIVE to STALE transition.
func (p *Publisher) Stale(entityID string, a Ageout) {
	p.emit(entityID, "stale", &sentinelv1.EntityEvent{EntityId: entityID, Event: &sentinelv1.EntityEvent_Stale{Stale: ageoutProto(a)}})
}

// AgedOut announces a STALE to AGED_OUT transition.
func (p *Publisher) AgedOut(entityID string, a Ageout) {
	p.emit(entityID, "aged_out", &sentinelv1.EntityEvent{EntityId: entityID, Event: &sentinelv1.EntityEvent_AgedOut{AgedOut: ageoutProto(a)}})
}

func ageoutProto(a Ageout) *sentinelv1.EntityAgeout {
	out := &sentinelv1.EntityAgeout{
		EntityType:    parseEnum[sentinelv1.EntityType](a.EntityType),
		Source:        parseEnum[sentinelv1.EntitySource](a.Source),
		FeedId:        a.FeedID,
		PreviousState: parseEnum[sentinelv1.AgeoutState](a.PreviousState),
		ThresholdMs:   a.ThresholdMs,
	}
	if !a.LastSeenAt.IsZero() {
		out.LastSeenAt = timestamppb.New(a.LastSeenAt)
	}
	return out
}

// emit stamps the envelope and produces it keyed by key.
func (p *Publisher) emit(key, eventType string, ev *sentinelv1.EntityEvent) {
	ev.EventId = uuid.NewString()
	ev.EventTime = timestamppb.Now()
	ev.Producer = ProducerName
	if err := p.producer.ProduceProto(kafka.TopicEntityEvents, key, ev, ProducerName, map[string]string{kafka.HeaderEventType: eventType}); err != nil {
		p.logger.Warn("entity event not enqueued", zap.String("type", eventType), zap.String("entity", ev.EntityId), zap.Error(err))
	}
}
