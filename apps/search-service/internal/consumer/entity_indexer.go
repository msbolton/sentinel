package consumer

import (
	"context"
	"encoding/json"

	"github.com/sentinel/go-common/kafka"
	"github.com/sentinel/search-service/internal/opensearch"
	"go.uber.org/zap"
)

// EntityIndexer consumes entity events from Kafka and indexes them in OpenSearch.
type EntityIndexer struct {
	osClient *opensearch.Client
	consumer *kafka.Consumer
	logger   *zap.Logger
	done     chan struct{}
}

// NewEntityIndexer creates a new entity indexer consumer.
func NewEntityIndexer(brokers string, osClient *opensearch.Client, logger *zap.Logger) (*EntityIndexer, error) {
	consumer, err := kafka.NewConsumer(brokers, kafka.GroupSearchService, "search-service", logger)
	if err != nil {
		return nil, err
	}

	topics := []string{
		kafka.TopicEntityCreated,
		kafka.TopicEntityUpdated,
		kafka.TopicEntityDeleted,
	}
	if err := consumer.Subscribe(topics); err != nil {
		return nil, err
	}

	return &EntityIndexer{
		osClient: osClient,
		consumer: consumer,
		logger:   logger,
		done:     make(chan struct{}),
	}, nil
}

// Start begins consuming entity events in a goroutine.
func (e *EntityIndexer) Start() {
	go e.run()
}

func (e *EntityIndexer) run() {
	e.logger.Info("entity indexer consumer started")
	for {
		msg, err := e.consumer.Poll(100)
		if err != nil {
			select {
			case <-e.done:
				return
			default:
				continue
			}
		}
		if msg == nil {
			select {
			case <-e.done:
				return
			default:
				continue
			}
		}

		topic := ""
		if msg.TopicPartition.Topic != nil {
			topic = *msg.TopicPartition.Topic
		}

		switch topic {
		case kafka.TopicEntityCreated, kafka.TopicEntityUpdated:
			e.handleEntityUpsert(topic, msg.Value)
		case kafka.TopicEntityDeleted:
			e.handleEntityDeleted(msg.Value)
		}
	}
}

func (e *EntityIndexer) handleEntityUpsert(topic string, data []byte) {
	var payload entityEventPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		e.logger.Error("failed to unmarshal entity event", zap.String("topic", topic), zap.Error(err))
		return
	}

	doc := opensearch.EntityDocument{
		ID:         payload.ID,
		Name:       payload.Name,
		EntityType: payload.EntityType,
	}

	if payload.Description != "" {
		doc.Description = &payload.Description
	}
	if payload.Source != "" {
		doc.Source = &payload.Source
	}
	if payload.Classification != "" {
		doc.Classification = &payload.Classification
	}
	if payload.Latitude != nil && payload.Longitude != nil {
		doc.Position = &opensearch.GeoPoint{Lat: *payload.Latitude, Lon: *payload.Longitude}
	}
	if len(payload.Affiliations) > 0 {
		doc.Affiliations = payload.Affiliations
	}
	if payload.Metadata != nil {
		doc.Metadata = payload.Metadata
	}
	if payload.CreatedAt != "" {
		doc.CreatedAt = &payload.CreatedAt
	}
	if payload.UpdatedAt != "" {
		doc.UpdatedAt = &payload.UpdatedAt
	}
	if payload.LastSeenAt != "" {
		doc.LastSeenAt = &payload.LastSeenAt
	}

	if err := e.osClient.IndexEntity(context.Background(), doc); err != nil {
		e.logger.Error("failed to index entity", zap.String("id", payload.ID), zap.String("topic", topic), zap.Error(err))
		return
	}

	e.logger.Info("entity indexed", zap.String("id", payload.ID), zap.String("topic", topic))
}

func (e *EntityIndexer) handleEntityDeleted(data []byte) {
	var payload struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		e.logger.Error("failed to unmarshal entity deleted event", zap.Error(err))
		return
	}

	e.osClient.DeleteEntity(context.Background(), payload.ID)
	e.logger.Info("entity deleted from index", zap.String("id", payload.ID))
}

// Stop signals the consumer to stop and closes it.
func (e *EntityIndexer) Stop() {
	close(e.done)
	e.consumer.Close()
}

// entityEventPayload matches the Kafka entity event JSON (snake_case).
type entityEventPayload struct {
	ID             string                 `json:"id"`
	Name           string                 `json:"name"`
	EntityType     string                 `json:"entity_type"`
	Description    string                 `json:"description"`
	Source         string                 `json:"source"`
	Classification string                 `json:"classification"`
	Latitude       *float64               `json:"latitude"`
	Longitude      *float64               `json:"longitude"`
	Affiliations   []string               `json:"affiliations"`
	Metadata       map[string]interface{} `json:"metadata"`
	CreatedAt      string                 `json:"created_at"`
	UpdatedAt      string                 `json:"updated_at"`
	LastSeenAt     string                 `json:"last_seen_at"`
}
