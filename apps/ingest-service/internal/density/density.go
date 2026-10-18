// Package density produces the density grids drivers keep for views too
// large to store objects one by one, to the density.events topic keyed by
// feed id. The API gateway relays them to map clients cleared for the
// feed's classification.
package density

import (
	"fmt"

	"github.com/confluentinc/confluent-kafka-go/v2/kafka"
	"google.golang.org/protobuf/proto"

	"github.com/sentinel/proto-gen/pbjson"
	sentinelv1 "github.com/sentinel/proto-gen/sentinel/v1"

	"github.com/sentinel/ingest-service/internal/obs"
	"github.com/sentinel/ingest-service/internal/source/driver"
)

// Producer is the part of the Kafka producer this package uses.
type Producer interface {
	Produce(msg *kafka.Message) error
}

// Publisher produces grids to one topic.
type Publisher struct {
	producer Producer
	topic    string
}

// New builds a publisher for topic.
func New(p Producer, topic string) *Publisher {
	return &Publisher{producer: p, topic: topic}
}

// For returns the publisher one feed's driver gets: every grid carries the
// feed id and at least the feed's classification.
func (p *Publisher) For(feedID, classification string) driver.DensityPublisher {
	if classification == "" {
		classification = obs.ClassUnclassified
	}
	level, _ := pbjson.ParseEnum[sentinelv1.Classification](classification)
	return &feedPublisher{p: p, feedID: feedID, level: level}
}

type feedPublisher struct {
	p      *Publisher
	feedID string
	level  sentinelv1.Classification
}

func (f *feedPublisher) PublishDensity(grid *sentinelv1.DensityGrid) error {
	grid.FeedId = f.feedID
	if grid.Classification < f.level {
		grid.Classification = f.level
	}
	value, err := proto.Marshal(grid)
	if err != nil {
		return fmt.Errorf("encoding density grid: %w", err)
	}
	topic := f.p.topic
	return f.p.producer.Produce(&kafka.Message{
		TopicPartition: kafka.TopicPartition{Topic: &topic, Partition: kafka.PartitionAny},
		Key:            []byte(f.feedID),
		Value:          value,
		Opaque:         f.feedID,
	})
}
