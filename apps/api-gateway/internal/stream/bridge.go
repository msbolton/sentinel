package stream

import (
	"encoding/json"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/sentinel/go-common/kafka"
)

// Topics the bridge subscribes to.
var bridgeTopics = []string{
	kafka.TopicEntityPosition,
	kafka.TopicEntityCreated,
	kafka.TopicEntityUpdated,
	kafka.TopicEntityDeleted,
	kafka.TopicEntityStale,
	kafka.TopicEntityAgedOut,
	kafka.TopicEntityRestored,
	kafka.TopicAlertGeofence,
	kafka.TopicAlertAnomaly,
}

// Bridge consumes entity and alert topics and forwards them to websocket clients.
type Bridge struct {
	hub      *Hub
	batcher  *Batcher
	consumer *kafka.Consumer
	logger   *zap.Logger

	stopOnce sync.Once
	stop     chan struct{}
	done     chan struct{}
}

// NewBridge creates a consumer in its own group starting at the latest offset,
// so a new gateway instance never replays history to its first client.
func NewBridge(brokers, groupID string, hub *Hub, batcher *Batcher, logger *zap.Logger) (*Bridge, error) {
	consumer, err := kafka.NewConsumerWithOffset(brokers, groupID, "sentinel-gateway-ws", "latest", logger)
	if err != nil {
		return nil, err
	}
	if err := consumer.Subscribe(bridgeTopics); err != nil {
		consumer.Close()
		return nil, err
	}
	return &Bridge{hub: hub, batcher: batcher, consumer: consumer, logger: logger,
		stop: make(chan struct{}), done: make(chan struct{})}, nil
}

// Start runs the poll loop.
func (b *Bridge) Start() {
	go func() {
		defer close(b.done)
		b.logger.Info("ws kafka bridge started", zap.Strings("topics", bridgeTopics))
		for {
			select {
			case <-b.stop:
				return
			default:
			}
			msg, err := b.consumer.Poll(100)
			if err != nil {
				b.logger.Error("kafka poll failed", zap.Error(err))
				time.Sleep(time.Second)
				continue
			}
			if msg == nil || msg.TopicPartition.Topic == nil {
				continue
			}
			b.Handle(*msg.TopicPartition.Topic, msg.Value)
		}
	}()
}

// Stop ends the poll loop and closes the consumer.
func (b *Bridge) Stop() {
	b.stopOnce.Do(func() { close(b.stop) })
	<-b.done
	_ = b.consumer.Close()
}

// Handle routes one Kafka message. It is the pure, testable core of the bridge.
func (b *Bridge) Handle(topic string, value []byte) {
	if len(value) == 0 {
		return
	}
	switch topic {
	case kafka.TopicEntityPosition, kafka.TopicEntityUpdated:
		b.enqueueEntity(value, "updated")
	case kafka.TopicEntityCreated:
		b.enqueueEntity(value, "created")
	case kafka.TopicEntityDeleted:
		var raw rawDeletedEvent
		if err := json.Unmarshal(value, &raw); err != nil {
			b.logger.Debug("bad deleted event", zap.Error(err))
			return
		}
		ev := EntityEvent{
			Type:      "deleted",
			Entity:    Entity{ID: raw.EntityID, Metadata: map[string]interface{}{}, Affiliations: []string{}},
			Timestamp: orNow(raw.Timestamp),
			Bulk:      raw.Bulk,
			Count:     raw.Count,
		}
		b.hub.BroadcastAll(MsgEntityDeleted, ev)
	case kafka.TopicEntityStale, kafka.TopicEntityAgedOut, kafka.TopicEntityRestored:
		if !json.Valid(value) {
			b.logger.Debug("bad ageout event", zap.String("topic", topic))
			return
		}
		// Coalesced into one array frame per flush; see Batcher.AddRaw.
		b.batcher.AddRaw(topic, append(json.RawMessage(nil), value...))
	case kafka.TopicAlertGeofence, kafka.TopicAlertAnomaly:
		var raw rawAlertEvent
		if err := json.Unmarshal(value, &raw); err != nil || raw.ID == "" {
			b.logger.Debug("bad alert event", zap.Error(err))
			return
		}
		b.hub.BroadcastAll(MsgAlertNew, raw.toClientAlert())
	default:
		b.logger.Warn("unhandled topic", zap.String("topic", topic))
	}
}

func (b *Bridge) enqueueEntity(value []byte, eventType string) {
	var raw rawEntityEvent
	if err := json.Unmarshal(value, &raw); err != nil {
		b.logger.Debug("bad entity event", zap.Error(err))
		return
	}
	ev, ok := raw.toPositioned(eventType)
	if !ok {
		b.logger.Debug("entity event without id or position dropped")
		return
	}
	b.batcher.Add(ev)
}

// rawEntityEvent accepts both the NestJS-era snake_case payload and the
// shapes the Go entity-service publishes (id/altitude instead of
// entity_id/altitude_meters).
type rawEntityEvent struct {
	EntityID          string                 `json:"entity_id"`
	ID                string                 `json:"id"`
	EntityType        string                 `json:"entity_type"`
	Name              string                 `json:"name"`
	Latitude          *float64               `json:"latitude"`
	Longitude         *float64               `json:"longitude"`
	AltitudeMeters    *float64               `json:"altitude_meters"`
	Altitude          *float64               `json:"altitude"`
	Heading           *float64               `json:"heading"`
	SpeedKnots        *float64               `json:"speed_knots"`
	Course            *float64               `json:"course"`
	Classification    string                 `json:"classification"`
	Source            string                 `json:"source"`
	Timestamp         string                 `json:"timestamp"`
	CreatedAt         string                 `json:"created_at"`
	UpdatedAt         string                 `json:"updated_at"`
	Metadata          map[string]interface{} `json:"metadata"`
	TrackEnvironment  string                 `json:"track_environment"`
	CountryOfOrigin   string                 `json:"country_of_origin"`
	PlatformData      map[string]interface{} `json:"platform_data"`
	OperationalStatus string                 `json:"operational_status"`
	CircularError     *float64               `json:"circular_error"`
	VelocityNorth     *float64               `json:"velocity_north"`
	VelocityEast      *float64               `json:"velocity_east"`
	VelocityUp        *float64               `json:"velocity_up"`
	LastSeenAt        string                 `json:"last_seen_at"`
}

// kinematics builds the client's `kinematics.velocity` object when the event
// carries any velocity component; m/s, as the feeds report them.
func (r rawEntityEvent) kinematics() map[string]interface{} {
	if r.VelocityNorth == nil && r.VelocityEast == nil && r.VelocityUp == nil {
		return nil
	}
	velocity := map[string]interface{}{}
	if r.VelocityNorth != nil {
		velocity["north"] = *r.VelocityNorth
	}
	if r.VelocityEast != nil {
		velocity["east"] = *r.VelocityEast
	}
	if r.VelocityUp != nil {
		velocity["up"] = *r.VelocityUp
	}
	return map[string]interface{}{"velocity": velocity}
}

func (r rawEntityEvent) toPositioned(eventType string) (PositionedEvent, bool) {
	id := r.EntityID
	if id == "" {
		id = r.ID
	}
	if id == "" || r.Latitude == nil || r.Longitude == nil {
		return PositionedEvent{}, false
	}
	alt := r.AltitudeMeters
	if alt == nil {
		alt = r.Altitude
	}
	ts := orNow(firstNonEmpty(r.Timestamp, r.UpdatedAt, r.CreatedAt))
	meta := r.Metadata
	if meta == nil {
		meta = map[string]interface{}{}
	}
	return PositionedEvent{
		Lat: *r.Latitude,
		Lng: *r.Longitude,
		Event: EntityEvent{
			Type: eventType,
			Entity: Entity{
				ID:                id,
				EntityType:        r.EntityType,
				Name:              r.Name,
				Source:            r.Source,
				Classification:    r.Classification,
				Position:          &Position{Latitude: *r.Latitude, Longitude: *r.Longitude, Altitude: alt},
				Heading:           r.Heading,
				SpeedKnots:        r.SpeedKnots,
				Course:            r.Course,
				Metadata:          meta,
				Affiliations:      []string{},
				CreatedAt:         orNow(firstNonEmpty(r.CreatedAt, ts)),
				UpdatedAt:         ts,
				LastSeenAt:        firstNonEmpty(r.LastSeenAt, ts),
				Kinematics:        r.kinematics(),
				TrackEnvironment:  r.TrackEnvironment,
				CountryOfOrigin:   r.CountryOfOrigin,
				PlatformData:      r.PlatformData,
				OperationalStatus: r.OperationalStatus,
				CircularError:     r.CircularError,
			},
			Timestamp: ts,
		},
	}, true
}

type rawDeletedEvent struct {
	EntityID  string `json:"entity_id"`
	Bulk      bool   `json:"bulk"`
	Count     int    `json:"count"`
	Timestamp string `json:"timestamp"`
}

// rawAlertEvent is what apps/alert-service/internal/evaluator publishes.
type rawAlertEvent struct {
	ID          string `json:"alertId"`
	AlertType   string `json:"alertType"`
	Severity    string `json:"severity"`
	Title       string `json:"title"`
	Description string `json:"description"`
	EntityID    string `json:"entityId"`
	RuleID      string `json:"ruleId"`
	Position    *struct {
		Lat float64 `json:"lat"`
		Lon float64 `json:"lon"`
	} `json:"position"`
	Metadata  map[string]string `json:"metadata"`
	CreatedAt string            `json:"createdAt"`
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
}

func (r rawAlertEvent) toClientAlert() ClientAlert {
	a := ClientAlert{
		ID: r.ID, Title: r.Title, Message: r.Description, Severity: r.Severity, AlertType: r.AlertType,
		EntityID: r.EntityID, RuleID: r.RuleID, Metadata: r.Metadata, CreatedAt: orNow(r.CreatedAt),
	}
	a.UpdatedAt = a.CreatedAt
	if a.Metadata == nil {
		a.Metadata = map[string]string{}
	}
	if r.Position != nil {
		a.Position = &Position{Latitude: r.Position.Lat, Longitude: r.Position.Lon}
	}
	return a
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
