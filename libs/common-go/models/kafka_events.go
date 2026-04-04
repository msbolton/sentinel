package models

// EntityPositionEvent is the Kafka payload for events.entity.position.
// Uses snake_case JSON tags to match the existing NestJS output.
type EntityPositionEvent struct {
	EntityID       string  `json:"entity_id"`
	EntityType     string  `json:"entity_type"`
	Latitude       float64 `json:"latitude"`
	Longitude      float64 `json:"longitude"`
	AltitudeMeters float64 `json:"altitude_meters,omitempty"`
	Heading        float64 `json:"heading,omitempty"`
	SpeedKnots     float64 `json:"speed_knots,omitempty"`
	Course         float64 `json:"course,omitempty"`
	Source         string  `json:"source,omitempty"`
	Classification string  `json:"classification,omitempty"`
	Timestamp      string  `json:"timestamp"`
}

// EntityCreatedEvent is the Kafka payload for events.entity.created.
type EntityCreatedEvent struct {
	EntityID       string                 `json:"entity_id"`
	EntityType     string                 `json:"entity_type"`
	Name           string                 `json:"name,omitempty"`
	Latitude       float64                `json:"latitude,omitempty"`
	Longitude      float64                `json:"longitude,omitempty"`
	AltitudeMeters float64                `json:"altitude_meters,omitempty"`
	Heading        float64                `json:"heading,omitempty"`
	SpeedKnots     float64                `json:"speed_knots,omitempty"`
	Classification string                 `json:"classification,omitempty"`
	Source         string                 `json:"source,omitempty"`
	Timestamp      string                 `json:"timestamp"`
	Metadata       map[string]interface{} `json:"metadata,omitempty"`
}

// EntityUpdatedEvent is the Kafka payload for events.entity.updated.
type EntityUpdatedEvent = EntityCreatedEvent

// EntityDeletedEvent is the Kafka payload for events.entity.deleted.
type EntityDeletedEvent struct {
	EntityID  string `json:"entity_id,omitempty"`
	Bulk      bool   `json:"bulk,omitempty"`
	Count     int    `json:"count,omitempty"`
	Timestamp string `json:"timestamp"`
}

// EntityAgeoutEvent is the Kafka payload for events.entity.stale and events.entity.agedout.
type EntityAgeoutEvent struct {
	EntityID    string `json:"entity_id"`
	EntityType  string `json:"entity_type"`
	Source      string `json:"source"`
	FeedID      string `json:"feed_id,omitempty"`
	AgeoutState string `json:"ageout_state"`
	LastSeenAt  string `json:"last_seen_at"`
	ThresholdMs int    `json:"threshold_ms"`
	Timestamp   string `json:"timestamp"`
}

// EntityRestoredEvent is the Kafka payload for events.entity.restored.
type EntityRestoredEvent = EntityAgeoutEvent
