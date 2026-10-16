package events

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/sentinel/go-common/kafka"
	"github.com/sentinel/proto-gen/pbjson"
	sentinelv1 "github.com/sentinel/proto-gen/sentinel/v1"
)

// ProducerName is the producer header value.
const ProducerName = "entity-service"

// Producer is what the publisher needs from the Kafka producer.
type Producer interface {
	ProduceProto(topic, key string, msg proto.Message, producer string, extra map[string]string) error
	ProduceRaw(topic string, key, value []byte, headers map[string]string) error
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

// Publisher writes EntityEvents to entities.events and, while enabled, the
// legacy JSON events older consumers read.
type Publisher struct {
	producer Producer
	legacy   bool
	logger   *zap.Logger
}

// NewPublisher creates a publisher; legacy keeps the JSON topics fed.
func NewPublisher(p Producer, legacy bool, logger *zap.Logger) *Publisher {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Publisher{producer: p, legacy: legacy, logger: logger}
}

// Created announces a new entity.
func (p *Publisher) Created(entity *sentinelv1.Entity, ref *sentinelv1.ObservationRef) {
	p.emit(entity.Id, "created", &sentinelv1.EntityEvent{EntityId: entity.Id, Event: &sentinelv1.EntityEvent_Created{Created: &sentinelv1.EntityCreated{Entity: entity, Observation: ref}}})
	if p.legacy {
		p.raw(kafka.TopicEntityCreated, entity.Id, legacyCreated(entity))
	}
}

// Position announces an accepted fix.
func (p *Publisher) Position(entity *sentinelv1.Entity, ref *sentinelv1.ObservationRef) {
	p.emit(entity.Id, "position", &sentinelv1.EntityEvent{EntityId: entity.Id, Event: &sentinelv1.EntityEvent_Position{Position: &sentinelv1.EntityPosition{Entity: entity, Observation: ref}}})
	if p.legacy {
		p.raw(kafka.TopicEntityPosition, entity.Id, legacyPosition(entity))
	}
}

// Restored announces that a stale or aged-out entity reported again. It is
// published before the Position that restored it.
func (p *Publisher) Restored(entity *sentinelv1.Entity, previousState string) {
	prev := parseEnum[sentinelv1.AgeoutState](previousState)
	p.emit(entity.Id, "restored", &sentinelv1.EntityEvent{EntityId: entity.Id, Event: &sentinelv1.EntityEvent_Restored{Restored: &sentinelv1.EntityRestored{Entity: entity, PreviousState: prev}}})
	if p.legacy {
		p.raw(kafka.TopicEntityRestored, entity.Id, legacyRestored(entity, previousState))
	}
}

// Updated announces a metadata change.
func (p *Publisher) Updated(entity *sentinelv1.Entity, changed []string) {
	p.emit(entity.Id, "updated", &sentinelv1.EntityEvent{EntityId: entity.Id, Event: &sentinelv1.EntityEvent_Updated{Updated: &sentinelv1.EntityUpdated{Entity: entity, ChangedFields: changed}}})
	if p.legacy {
		p.raw(kafka.TopicEntityUpdated, entity.Id, legacyCreated(entity))
	}
}

// Deleted announces a delete; bulk with an empty id means everything.
func (p *Publisher) Deleted(entityID string, bulk bool, count int) {
	key := entityID
	if bulk {
		key = "all"
	}
	p.emit(key, "deleted", &sentinelv1.EntityEvent{EntityId: entityID, Event: &sentinelv1.EntityEvent_Deleted{Deleted: &sentinelv1.EntityDeleted{Bulk: bulk, Count: int32(count)}}})
	if p.legacy && !bulk {
		p.raw(kafka.TopicEntityDeleted, entityID, map[string]interface{}{"id": entityID})
	}
}

// Stale announces a LIVE to STALE transition.
func (p *Publisher) Stale(entityID string, a Ageout) {
	p.emit(entityID, "stale", &sentinelv1.EntityEvent{EntityId: entityID, Event: &sentinelv1.EntityEvent_Stale{Stale: ageoutProto(a)}})
	if p.legacy {
		p.raw(kafka.TopicEntityStale, entityID, legacyAgeout(entityID, a, "STALE"))
	}
}

// AgedOut announces a STALE to AGED_OUT transition.
func (p *Publisher) AgedOut(entityID string, a Ageout) {
	p.emit(entityID, "aged_out", &sentinelv1.EntityEvent{EntityId: entityID, Event: &sentinelv1.EntityEvent_AgedOut{AgedOut: ageoutProto(a)}})
	if p.legacy {
		p.raw(kafka.TopicEntityAgedOut, entityID, legacyAgeout(entityID, a, "AGED_OUT"))
	}
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

func (p *Publisher) raw(topic, key string, payload map[string]interface{}) {
	b, err := json.Marshal(payload)
	if err != nil {
		return
	}
	if err := p.producer.ProduceRaw(topic, []byte(key), b, nil); err != nil {
		p.logger.Warn("legacy event not enqueued", zap.String("topic", topic), zap.Error(err))
	}
}

// Legacy payloads reproduce the JSON shapes consumers read before the
// typed stream existed.

func legacyPosition(e *sentinelv1.Entity) map[string]interface{} {
	m := map[string]interface{}{
		"entity_id":    e.Id,
		"entity_type":  pbjson.EnumName(e.EntityType),
		"source":       pbjson.EnumName(e.Source),
		"heading":      e.Heading,
		"speed_knots":  e.SpeedKnots,
		"course":       e.Course,
		"timestamp":    tsString(e.LastObservedAt),
		"last_seen_at": tsString(e.LastSeenAt),
	}
	if e.Position != nil {
		m["latitude"] = e.Position.Latitude
		m["longitude"] = e.Position.Longitude
		m["altitude"] = e.Position.AltitudeMeters
	}
	if e.Kinematics != nil && e.Kinematics.Velocity != nil {
		m["velocity_north"] = e.Kinematics.Velocity.North
		m["velocity_east"] = e.Kinematics.Velocity.East
		m["velocity_up"] = e.Kinematics.Velocity.Up
	}
	return m
}

func legacyCreated(e *sentinelv1.Entity) map[string]interface{} {
	m := legacyPosition(e)
	delete(m, "entity_id")
	m["id"] = e.Id
	m["name"] = e.Name
	m["classification"] = pbjson.EnumName(e.Classification)
	m["created_at"] = tsString(e.CreatedAt)
	m["updated_at"] = tsString(e.UpdatedAt)
	return m
}

func legacyRestored(e *sentinelv1.Entity, previous string) map[string]interface{} {
	m := map[string]interface{}{
		"entity_id":      e.Id,
		"entity_type":    pbjson.EnumName(e.EntityType),
		"previous_state": previous,
		"timestamp":      tsString(e.LastObservedAt),
	}
	if e.Position != nil {
		m["latitude"] = e.Position.Latitude
		m["longitude"] = e.Position.Longitude
	}
	return m
}

func legacyAgeout(entityID string, a Ageout, state string) map[string]interface{} {
	m := map[string]interface{}{
		"entity_id":    entityID,
		"entity_type":  a.EntityType,
		"source":       a.Source,
		"feed_id":      a.FeedID,
		"ageout_state": state,
		"threshold_ms": a.ThresholdMs,
		"timestamp":    time.Now().UTC().Format(time.RFC3339),
	}
	if !a.LastSeenAt.IsZero() {
		m["last_seen_at"] = a.LastSeenAt.UTC().Format(time.RFC3339Nano)
	}
	return m
}

func tsString(t *timestamppb.Timestamp) interface{} {
	if t == nil {
		return nil
	}
	return t.AsTime().UTC().Format(time.RFC3339Nano)
}
