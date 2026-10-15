package batch

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/sentinel/go-common/kafka"
	"github.com/sentinel/entity-service/internal/redis"
	"github.com/sentinel/entity-service/internal/store"
	"go.uber.org/zap"
)

const (
	maxBatchSize  = 200
	flushInterval = 100 * time.Millisecond
)

// IngestMessage represents a raw ingest message from Kafka.
type IngestMessage struct {
	EntityID         string                 `json:"entity_id"`
	EntityType       string                 `json:"entity_type"`
	Name             string                 `json:"name"`
	Source           string                 `json:"source"`
	FeedID           *string                `json:"feed_id"`
	Latitude         float64                `json:"latitude"`
	Longitude        float64                `json:"longitude"`
	Altitude         float64                `json:"altitude"`
	Heading          float64                `json:"heading"`
	SpeedKnots       float64                `json:"speed_knots"`
	Course           float64                `json:"course"`
	Timestamp        string                 `json:"timestamp"`
	Pitch            *float64               `json:"pitch"`
	Roll             *float64               `json:"roll"`
	VelocityNorth    *float64               `json:"velocity_north"`
	VelocityEast     *float64               `json:"velocity_east"`
	VelocityUp       *float64               `json:"velocity_up"`
	AccelNorth       *float64               `json:"accel_north"`
	AccelEast        *float64               `json:"accel_east"`
	AccelUp          *float64               `json:"accel_up"`
	CircularError    *float64               `json:"circular_error"`
	TrackEnvironment *string                `json:"track_environment"`
	Affiliation      *string                `json:"affiliation"`
	CountryOfOrigin  *string                `json:"country_of_origin"`
	DimensionLength  *float64               `json:"dimension_length"`
	DimensionWidth   *float64               `json:"dimension_width"`
	AISData          map[string]interface{} `json:"ais_data"`
	ADSBData         map[string]interface{} `json:"adsb_data"`
	TLEData          map[string]interface{} `json:"tle_data"`
	Link16Data       map[string]interface{} `json:"link16_data"`
	CoTData          map[string]interface{} `json:"cot_data"`
	UAVData          map[string]interface{} `json:"uav_data"`
}

// Buffer accumulates ingest messages and flushes them in batches.
type Buffer struct {
	entityStore *store.EntityStore
	producer    *kafka.Producer
	geoCache    *redis.GeoCache
	logger      *zap.Logger

	mu   sync.Mutex
	buf  []IngestMessage
	done chan struct{}
}

// NewBuffer creates a new ingest message buffer.
func NewBuffer(entityStore *store.EntityStore, producer *kafka.Producer, geoCache *redis.GeoCache, logger *zap.Logger) *Buffer {
	b := &Buffer{
		entityStore: entityStore,
		producer:    producer,
		geoCache:    geoCache,
		logger:      logger,
		buf:         make([]IngestMessage, 0, maxBatchSize),
		done:        make(chan struct{}),
	}
	go b.flushLoop()
	return b
}

// Add adds a message to the buffer. If the buffer is full, it triggers an immediate flush.
func (b *Buffer) Add(msg IngestMessage) {
	b.mu.Lock()
	b.buf = append(b.buf, msg)
	shouldFlush := len(b.buf) >= maxBatchSize
	b.mu.Unlock()

	if shouldFlush {
		b.flush()
	}
}

func (b *Buffer) flushLoop() {
	ticker := time.NewTicker(flushInterval)
	defer ticker.Stop()
	for {
		select {
		case <-b.done:
			b.flush()
			return
		case <-ticker.C:
			b.flush()
		}
	}
}

func (b *Buffer) flush() {
	b.mu.Lock()
	if len(b.buf) == 0 {
		b.mu.Unlock()
		return
	}
	batch := b.buf
	b.buf = make([]IngestMessage, 0, maxBatchSize)
	b.mu.Unlock()

	b.processBatch(batch)
}

func (b *Buffer) processBatch(batch []IngestMessage) {
	ctx := context.Background()

	// Collect sourceEntityIDs for bulk lookup.
	sourceIDs := make([]string, 0, len(batch))
	for _, msg := range batch {
		sid := resolveSourceEntityID(msg)
		if sid != "" {
			sourceIDs = append(sourceIDs, sid)
		}
	}

	existing, err := b.entityStore.FindBySourceEntityIDs(ctx, sourceIDs)
	if err != nil {
		b.logger.Error("bulk lookup failed", zap.Error(err))
		return
	}

	var positionUpdates []store.BulkPositionUpdate
	var newEntities []IngestMessage

	for _, msg := range batch {
		sid := resolveSourceEntityID(msg)
		info := existing[sid]

		if info == nil {
			newEntities = append(newEntities, msg)
			continue
		}

		platformData := buildPlatformData(msg)
		kinematics := buildKinematics(msg)

		positionUpdates = append(positionUpdates, store.BulkPositionUpdate{
			ID:               info.ID,
			Lng:              msg.Longitude,
			Lat:              msg.Latitude,
			Heading:          &msg.Heading,
			SpeedKnots:       &msg.SpeedKnots,
			Course:           &msg.Course,
			Altitude:         &msg.Altitude,
			PlatformData:     platformData,
			Kinematics:       kinematics,
			TrackEnvironment: msg.TrackEnvironment,
			CircularError:    msg.CircularError,
		})

		// Emit position event.
		b.emitPositionEvent(info.ID, info.EntityType, msg)

		// Update geo cache.
		b.geoCache.Add(ctx, info.ID, msg.Longitude, msg.Latitude)

		// Emit restored event if entity was stale/aged out.
		if info.AgeoutState == "STALE" || info.AgeoutState == "AGED_OUT" {
			b.emitRestoredEvent(info.ID, info.EntityType, info.AgeoutState, msg)
		}
	}

	// Bulk update existing entities.
	if len(positionUpdates) > 0 {
		if err := b.entityStore.BulkUpdatePositions(ctx, positionUpdates); err != nil {
			b.logger.Error("bulk position update failed", zap.Error(err), zap.Int("count", len(positionUpdates)))
		}
	}

	// Create new entities.
	for _, msg := range newEntities {
		b.createEntity(ctx, msg)
	}
}

func (b *Buffer) createEntity(ctx context.Context, msg IngestMessage) {
	entityType := mapEntityType(msg.EntityType)
	source := mapSource(msg.Source)
	sid := resolveSourceEntityID(msg)
	platformData := buildPlatformData(msg)
	kinematics := buildKinematics(msg)

	rec := &store.EntityRecord{
		EntityType:       entityType,
		Name:             msg.Name,
		Source:           source,
		Classification:   "UNCLASSIFIED",
		FeedID:           msg.FeedID,
		Position:         &store.GeoPoint{Lat: msg.Latitude, Lon: msg.Longitude},
		Heading:          &msg.Heading,
		SpeedKnots:       &msg.SpeedKnots,
		Course:           &msg.Course,
		Altitude:         &msg.Altitude,
		Metadata:         map[string]interface{}{},
		Affiliations:     []string{},
		SourceEntityID:   &sid,
		TrackEnvironment: msg.TrackEnvironment,
		CircularError:    msg.CircularError,
		PlatformData:     platformData,
		Kinematics:       kinematics,
		CountryOfOrigin:  msg.CountryOfOrigin,
		DimensionLength:  msg.DimensionLength,
		DimensionWidth:   msg.DimensionWidth,
		Affiliation:      msg.Affiliation,
	}

	created, err := b.entityStore.Create(ctx, rec)
	if err != nil {
		b.logger.Error("create entity failed", zap.String("sourceEntityId", sid), zap.Error(err))
		return
	}

	b.emitCreatedEvent(created, msg)
	b.geoCache.Add(ctx, created.ID, msg.Longitude, msg.Latitude)
}

func (b *Buffer) emitPositionEvent(entityID, entityType string, msg IngestMessage) {
	payload, _ := json.Marshal(map[string]interface{}{
		"entity_id":   entityID,
		"entity_type": entityType,
		"latitude":    msg.Latitude,
		"longitude":   msg.Longitude,
		"altitude":    msg.Altitude,
		"heading":     msg.Heading,
		"speed_knots": msg.SpeedKnots,
		"course":      msg.Course,
		"source":      msg.Source,
		"timestamp":   msg.Timestamp,
	})
	b.producer.ProduceRaw(kafka.TopicEntityPosition, []byte(entityID), payload, nil)
}

func (b *Buffer) emitCreatedEvent(entity *store.EntityRecord, msg IngestMessage) {
	payload, _ := json.Marshal(map[string]interface{}{
		"id":             entity.ID,
		"entity_type":    entity.EntityType,
		"name":           entity.Name,
		"source":         entity.Source,
		"classification": entity.Classification,
		"latitude":       msg.Latitude,
		"longitude":      msg.Longitude,
		"created_at":     entity.CreatedAt,
		"updated_at":     entity.UpdatedAt,
	})
	b.producer.ProduceRaw(kafka.TopicEntityCreated, []byte(entity.ID), payload, nil)
}

func (b *Buffer) emitRestoredEvent(entityID, entityType, previousState string, msg IngestMessage) {
	payload, _ := json.Marshal(map[string]interface{}{
		"entity_id":      entityID,
		"entity_type":    entityType,
		"previous_state": previousState,
		"latitude":       msg.Latitude,
		"longitude":      msg.Longitude,
		"timestamp":      msg.Timestamp,
	})
	b.producer.ProduceRaw(kafka.TopicEntityRestored, []byte(entityID), payload, nil)
}

// Stop flushes remaining messages and stops the buffer.
func (b *Buffer) Stop() {
	close(b.done)
}

func resolveSourceEntityID(msg IngestMessage) string {
	return msg.EntityID
}

func mapEntityType(t string) string {
	switch strings.ToLower(t) {
	case "aircraft":
		return "AIRCRAFT"
	case "vessel":
		return "VESSEL"
	case "vehicle":
		return "VEHICLE"
	case "person":
		return "PERSON"
	case "satellite":
		return "SATELLITE"
	case "drone":
		return "DRONE"
	case "equipment":
		return "EQUIPMENT"
	default:
		return "UNKNOWN"
	}
}

func mapSource(s string) string {
	switch strings.ToLower(s) {
	case "adsb", "ads_b", "ads-b":
		return "ADS_B"
	case "adsblol", "adsb_lol":
		return "ADSB_LOL"
	case "opensky":
		return "OPENSKY"
	case "celestrak":
		return "CELESTRAK"
	case "ais":
		return "AIS"
	case "link16":
		return "LINK16"
	case "gps":
		return "GPS"
	default:
		return "UNKNOWN"
	}
}

func buildPlatformData(msg IngestMessage) map[string]interface{} {
	if msg.AISData != nil {
		return map[string]interface{}{"ais": msg.AISData}
	}
	if msg.ADSBData != nil {
		return map[string]interface{}{"adsb": msg.ADSBData}
	}
	if msg.TLEData != nil {
		return map[string]interface{}{"tle": msg.TLEData}
	}
	if msg.Link16Data != nil {
		return map[string]interface{}{"link16": msg.Link16Data}
	}
	if msg.CoTData != nil {
		return map[string]interface{}{"cot": msg.CoTData}
	}
	if msg.UAVData != nil {
		return map[string]interface{}{"uav": msg.UAVData}
	}
	return nil
}

func buildKinematics(msg IngestMessage) map[string]interface{} {
	k := map[string]interface{}{}
	hasData := false

	if msg.VelocityNorth != nil || msg.VelocityEast != nil || msg.VelocityUp != nil {
		k["velocity"] = map[string]interface{}{
			"north": msg.VelocityNorth,
			"east":  msg.VelocityEast,
			"up":    msg.VelocityUp,
		}
		hasData = true
	}

	if msg.AccelNorth != nil || msg.AccelEast != nil || msg.AccelUp != nil {
		k["acceleration"] = map[string]interface{}{
			"north": msg.AccelNorth,
			"east":  msg.AccelEast,
			"up":    msg.AccelUp,
		}
		hasData = true
	}

	if !hasData {
		return nil
	}
	return k
}
