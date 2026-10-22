package stream

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	ckafka "github.com/confluentinc/confluent-kafka-go/v2/kafka"
	"go.uber.org/zap"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/sentinel/go-common/kafka"
	"github.com/sentinel/proto-gen/pbjson"

	"github.com/sentinel/api-gateway/internal/auth"
	sentinelv1 "github.com/sentinel/proto-gen/sentinel/v1"
)

// Topics the bridge subscribes to.
var bridgeTopics = []string{kafka.TopicEntityEvents, kafka.TopicAlertEvents, kafka.TopicDensityEvents}

// Bridge consumes the typed entity and alert streams and forwards them to
// websocket clients in the client's JSON shape. It is the one place the
// wire contract is adapted for the browser.
type Bridge struct {
	hub     *Hub
	batcher *Batcher
	bc      *kafka.BatchConsumer
	logger  *zap.Logger

	stopOnce sync.Once
	cancel   context.CancelFunc
	done     chan struct{}
}

// NewBridge creates a consumer in its own group starting at the latest offset,
// so a new gateway instance never replays history to its first client.
func NewBridge(brokers, groupID string, hub *Hub, batcher *Batcher, logger *zap.Logger) (*Bridge, error) {
	bc, err := kafka.NewBatchConsumer(brokers, groupID, "sentinel-gateway-ws", "latest",
		kafka.BatchOptions{MaxMessages: 500, MaxWait: 50 * time.Millisecond}, logger)
	if err != nil {
		return nil, err
	}
	if err := bc.Subscribe(bridgeTopics); err != nil {
		_ = bc.Close()
		return nil, err
	}
	return &Bridge{hub: hub, batcher: batcher, bc: bc, logger: logger, done: make(chan struct{})}, nil
}

// Start runs the consume loop.
func (b *Bridge) Start() {
	ctx, cancel := context.WithCancel(context.Background())
	b.cancel = cancel
	go func() {
		defer close(b.done)
		b.logger.Info("ws kafka bridge started", zap.Strings("topics", bridgeTopics))
		if err := b.bc.Run(ctx, func(_ context.Context, msgs []*ckafka.Message) error {
			for _, m := range msgs {
				if m.TopicPartition.Topic != nil {
					b.Handle(*m.TopicPartition.Topic, m.Value)
				}
			}
			return nil
		}); err != nil {
			b.logger.Error("ws kafka bridge stopped", zap.Error(err))
		}
	}()
}

// Stop ends the loop and closes the consumer.
func (b *Bridge) Stop() {
	b.stopOnce.Do(func() {
		if b.cancel != nil {
			b.cancel()
			<-b.done
		}
		if b.bc != nil {
			_ = b.bc.Close()
		}
	})
}

// Handle routes one Kafka message. It is the pure, testable core of the bridge.
func (b *Bridge) Handle(topic string, value []byte) {
	if len(value) == 0 {
		return
	}
	switch topic {
	case kafka.TopicEntityEvents:
		var ev sentinelv1.EntityEvent
		if err := proto.Unmarshal(value, &ev); err != nil {
			b.logger.Debug("bad entity event", zap.Error(err))
			return
		}
		b.hub.noteEvent()
		b.handleEntityEvent(&ev)
	case kafka.TopicAlertEvents:
		var ev sentinelv1.AlertEvent
		if err := proto.Unmarshal(value, &ev); err != nil || ev.Alert == nil || ev.Alert.AlertId == "" {
			b.logger.Debug("bad alert event", zap.Error(err))
			return
		}
		b.hub.BroadcastRanked(MsgAlertNew, alertFromProto(&ev), auth.ClassificationRank(enumOrEmpty(ev.Alert.Classification)))
	case kafka.TopicDensityEvents:
		var g sentinelv1.DensityGrid
		if err := proto.Unmarshal(value, &g); err != nil || g.FeedId == "" {
			b.logger.Debug("bad density grid", zap.Error(err))
			return
		}
		b.hub.BroadcastRanked(MsgDensity, densityFromProto(&g), auth.ClassificationRank(enumOrEmpty(g.Classification)))
	default:
		b.logger.Warn("unhandled topic", zap.String("topic", topic))
	}
}

func (b *Bridge) handleEntityEvent(ev *sentinelv1.EntityEvent) {
	ts := orNow(tsString(ev.EventTime))
	switch e := ev.Event.(type) {
	case *sentinelv1.EntityEvent_Created:
		b.enqueueEntity(e.Created.Entity, "created", ts)
	case *sentinelv1.EntityEvent_Updated:
		b.enqueueEntity(e.Updated.Entity, "updated", ts)
	case *sentinelv1.EntityEvent_Position:
		b.enqueueEntity(e.Position.Entity, "updated", ts)
	case *sentinelv1.EntityEvent_Restored:
		en := e.Restored.Entity
		if en != nil {
			payload := map[string]interface{}{
				"entity_id":      en.Id,
				"entity_type":    pbjson.EnumName(en.EntityType),
				"previous_state": pbjson.EnumName(e.Restored.PreviousState),
				"timestamp":      ts,
			}
			if en.Position != nil {
				payload["latitude"] = en.Position.Latitude
				payload["longitude"] = en.Position.Longitude
			}
			b.batcher.AddRaw(MsgEntityRestored, mustJSON(payload))
			b.enqueueEntity(en, "updated", ts)
		}
	case *sentinelv1.EntityEvent_Deleted:
		b.hub.BroadcastAll(MsgEntityDeleted, EntityEvent{
			Type:      "deleted",
			Entity:    Entity{ID: ev.EntityId, Metadata: map[string]interface{}{}, Affiliations: []string{}},
			Timestamp: ts,
			Bulk:      e.Deleted.Bulk,
			Count:     int(e.Deleted.Count),
		})
	case *sentinelv1.EntityEvent_Stale:
		b.batcher.AddRaw(MsgEntityStale, mustJSON(ageoutPayload(ev.EntityId, e.Stale, "STALE", ts)))
	case *sentinelv1.EntityEvent_AgedOut:
		b.batcher.AddRaw(MsgEntityAgedOut, mustJSON(ageoutPayload(ev.EntityId, e.AgedOut, "AGED_OUT", ts)))
	default:
		b.logger.Debug("entity event with unknown case ignored", zap.String("entity", ev.EntityId))
	}
}

func (b *Bridge) enqueueEntity(en *sentinelv1.Entity, eventType, ts string) {
	if en == nil || en.Id == "" || en.Position == nil {
		b.logger.Debug("entity event without id or position dropped")
		return
	}
	entity := EntityFromProto(en)
	b.batcher.Add(PositionedEvent{
		Lat:   en.Position.Latitude,
		Lng:   en.Position.Longitude,
		Rank:  auth.ClassificationRank(enumOrEmpty(en.Classification)),
		Event: EntityEvent{Type: eventType, Entity: entity, Timestamp: firstNonEmpty(entity.LastObservedAt, ts)},
	})
}

// ageoutPayload keeps the frame shape the client already reads.
func ageoutPayload(entityID string, a *sentinelv1.EntityAgeout, state, ts string) map[string]interface{} {
	m := map[string]interface{}{
		"entity_id":    entityID,
		"entity_type":  pbjson.EnumName(a.EntityType),
		"source":       pbjson.EnumName(a.Source),
		"feed_id":      a.FeedId,
		"ageout_state": state,
		"threshold_ms": a.ThresholdMs,
		"timestamp":    ts,
	}
	if a.LastSeenAt != nil {
		m["last_seen_at"] = tsString(a.LastSeenAt)
	}
	return m
}

// EntityIdentity is a natural identifier carried to the client.
type EntityIdentity struct {
	Type  string `json:"type"`
	Value string `json:"value"`
}

// EntityFromProto converts the wire snapshot to the client's entity shape.
func EntityFromProto(en *sentinelv1.Entity) Entity {
	e := Entity{
		ID:                en.Id,
		EntityType:        pbjson.EnumName(en.EntityType),
		Name:              en.Name,
		Description:       en.Description,
		Source:            pbjson.EnumName(en.Source),
		Classification:    pbjson.EnumName(en.Classification),
		Heading:           en.Heading,
		SpeedKnots:        en.SpeedKnots,
		Course:            en.Course,
		MilStd2525dSymbol: en.MilStd_2525DSymbol,
		Metadata:          map[string]interface{}{},
		Affiliations:      en.Affiliations,
		Affiliation:       enumOrEmpty(en.Affiliation),
		CreatedAt:         orNow(tsString(en.CreatedAt)),
		UpdatedAt:         orNow(tsString(en.UpdatedAt)),
		LastSeenAt:        tsString(en.LastSeenAt),
		LastObservedAt:    tsString(en.LastObservedAt),
		TrackEnvironment:  enumOrEmpty(en.TrackEnvironment),
		CountryOfOrigin:   en.CountryOfOrigin,
		OperationalStatus: enumOrEmpty(en.OperationalStatus),
		CircularError:     en.CircularError,
		FeedID:            en.FeedId,
		LastFeedID:        en.LastFeedId,
		AgeoutState:       enumOrEmpty(en.AgeoutState),
		Exercise:          en.Exercise,
	}
	if e.Affiliations == nil {
		e.Affiliations = []string{}
	}
	for k, v := range en.Metadata {
		e.Metadata[k] = v
	}
	if en.Position != nil {
		e.Position = &Position{Latitude: en.Position.Latitude, Longitude: en.Position.Longitude, Altitude: en.Position.AltitudeMeters}
	}
	if en.Kinematics != nil {
		kin := map[string]interface{}{}
		if v := en.Kinematics.Velocity; v != nil {
			kin["velocity"] = map[string]interface{}{"north": v.North, "east": v.East, "up": v.Up}
		}
		if a := en.Kinematics.Acceleration; a != nil {
			kin["acceleration"] = map[string]interface{}{"north": a.North, "east": a.East, "up": a.Up}
		}
		if c := en.Kinematics.PositionCovariance; c != nil {
			kin["positionCovariance"] = map[string]interface{}{
				"pnPn": c.PnPn, "pnPe": c.PnPe, "pnPu": c.PnPu,
				"pePe": c.PePe, "pePu": c.PePu, "puPu": c.PuPu,
			}
		}
		if len(kin) > 0 {
			e.Kinematics = kin
		}
	}
	for _, s := range en.Sources {
		if s == nil || s.FeedId == "" {
			continue
		}
		e.Sources = append(e.Sources, EntitySourceRef{FeedID: s.FeedId, Source: enumOrEmpty(s.Source), LastObservedAt: tsString(s.LastObservedAt)})
	}
	if len(en.Platforms) > 0 {
		e.PlatformData = map[string]interface{}{}
		for kind, d := range en.Platforms {
			if d == nil {
				continue
			}
			raw, err := pbjson.Marshal(d)
			if err != nil {
				continue
			}
			var block map[string]interface{}
			if err := json.Unmarshal(raw, &block); err != nil {
				continue
			}
			if inner, ok := block[kind]; ok {
				e.PlatformData[kind] = inner
			} else {
				for _, inner := range block {
					e.PlatformData[kind] = inner
				}
			}
		}
	}
	for _, id := range en.Identities {
		if id != nil && id.Value != "" {
			e.Identities = append(e.Identities, EntityIdentity{Type: pbjson.EnumName(id.Type), Value: id.Value})
		}
	}
	return e
}

func alertFromProto(ev *sentinelv1.AlertEvent) ClientAlert {
	al := ev.Alert
	a := ClientAlert{
		ID: al.AlertId, Title: al.Title, Message: al.Description, Severity: pbjson.EnumName(al.Severity), AlertType: pbjson.EnumName(al.AlertType),
		EntityID: al.EntityId, RuleID: al.RuleId, Metadata: al.Metadata, CreatedAt: orNow(tsString(al.CreatedAt)),
		Acknowledged:   al.AcknowledgedAt != nil,
		Classification: enumOrEmpty(al.Classification),
	}
	a.UpdatedAt = a.CreatedAt
	if a.Metadata == nil {
		a.Metadata = map[string]string{}
	}
	if ev.AnomalyType != "" {
		a.Metadata["anomalyType"] = ev.AnomalyType
	}
	if al.Position != nil {
		a.Position = &Position{Latitude: al.Position.Latitude, Longitude: al.Position.Longitude}
	}
	return a
}

// ClientAlert matches apps/client/src/app/shared/models/alert.model.ts.
type ClientAlert struct {
	ID           string            `json:"id"`
	Title        string            `json:"title"`
	Message      string            `json:"message"`
	Severity     string            `json:"severity"`
	AlertType    string            `json:"alertType"`
	EntityID     string            `json:"entityId,omitempty"`
	RuleID       string            `json:"ruleId,omitempty"`
	Position     *Position         `json:"position,omitempty"`
	Acknowledged bool              `json:"acknowledged"`
	Metadata     map[string]string `json:"metadata"`
	CreatedAt    string            `json:"createdAt"`
	UpdatedAt    string            `json:"updatedAt"`
	// Classification inherited from the entity when the alert fired; empty
	// when the producer did not set it.
	Classification string `json:"classification,omitempty"`
}

func enumOrEmpty(e protoreflect.Enum) string {
	if e.Number() == 0 {
		return ""
	}
	return pbjson.EnumName(e)
}

func tsString(t *timestamppb.Timestamp) string {
	if t == nil {
		return ""
	}
	return t.AsTime().UTC().Format(time.RFC3339Nano)
}

func mustJSON(v interface{}) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func orNow(ts string) string {
	if ts != "" {
		return ts
	}
	return time.Now().UTC().Format(time.RFC3339)
}

func densityFromProto(g *sentinelv1.DensityGrid) ClientDensity {
	d := ClientDensity{
		FeedID: g.FeedId, Source: enumOrEmpty(g.Source), EntityType: enumOrEmpty(g.EntityType),
		CellDegrees: g.CellDegrees, WindowSeconds: g.WindowSeconds, CapSquareDegrees: g.CapSquareDegrees,
		GeneratedAt: orNow(tsString(g.GeneratedAt)), Classification: enumOrEmpty(g.Classification),
		Total: g.Total, Cells: make([][3]float64, 0, len(g.Cells)),
	}
	for _, c := range g.Cells {
		d.Cells = append(d.Cells, [3]float64{c.Latitude, c.Longitude, float64(c.Count)})
	}
	return d
}
