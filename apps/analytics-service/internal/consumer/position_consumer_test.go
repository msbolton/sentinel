package consumer

import (
	"context"
	"testing"
	"time"

	ckafka "github.com/confluentinc/confluent-kafka-go/v2/kafka"
	"go.uber.org/zap"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/sentinel/go-common/kafka"
	sentinelv1 "github.com/sentinel/proto-gen/sentinel/v1"

	"github.com/sentinel/analytics-service/internal/model"
	"github.com/sentinel/analytics-service/internal/stats"
	"github.com/sentinel/analytics-service/internal/store"
)

type recSink struct{ fixes []model.Fix }

func (r *recSink) OnFix(_ context.Context, f model.Fix) { r.fixes = append(r.fixes, f) }

type recCoverage struct{ cells []store.CellObs }

func (r *recCoverage) UpsertCells(_ context.Context, cells []store.CellObs) error {
	r.cells = append(r.cells, cells...)
	return nil
}

func message(t *testing.T, kind string, ev *sentinelv1.EntityEvent) *ckafka.Message {
	t.Helper()
	topic := kafka.TopicEntityEvents
	value, err := proto.Marshal(ev)
	if err != nil {
		t.Fatal(err)
	}
	return &ckafka.Message{
		TopicPartition: ckafka.TopicPartition{Topic: &topic},
		Value:          value,
		Headers:        []ckafka.Header{{Key: kafka.HeaderEventType, Value: []byte(kind)}},
	}
}

func positionEvent(id, feed string, lat, lon float64, at time.Time) *sentinelv1.EntityEvent {
	return &sentinelv1.EntityEvent{Event: &sentinelv1.EntityEvent_Position{Position: &sentinelv1.EntityPosition{
		Entity:      &sentinelv1.Entity{Id: id, EntityType: sentinelv1.EntityType_ENTITY_TYPE_AIRCRAFT, Position: &sentinelv1.Coordinate{Latitude: lat, Longitude: lon}},
		Observation: &sentinelv1.ObservationRef{FeedId: feed, SourceTimestamp: timestamppb.New(at)},
	}}}
}

func createdEvent(id, feed string, lat, lon float64, at time.Time) *sentinelv1.EntityEvent {
	return &sentinelv1.EntityEvent{Event: &sentinelv1.EntityEvent_Created{Created: &sentinelv1.EntityCreated{
		Entity:      &sentinelv1.Entity{Id: id, EntityType: sentinelv1.EntityType_ENTITY_TYPE_AIRCRAFT, Position: &sentinelv1.Coordinate{Latitude: lat, Longitude: lon}},
		Observation: &sentinelv1.ObservationRef{FeedId: feed, SourceTimestamp: timestamppb.New(at)},
	}}}
}

func TestHandleRoutesFixesAndCoalescesCoverage(t *testing.T) {
	sink := &recSink{}
	cov := &recCoverage{}
	c := &PositionConsumer{sink: sink, coverage: cov, cellDeg: 0.05, counters: stats.New(), logger: zap.NewNop()}
	t0 := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)

	msgs := []*ckafka.Message{
		message(t, "position", positionEvent("a", "opensky", 10.01, 20.01, t0)),
		message(t, "position", positionEvent("b", "opensky", 10.02, 20.02, t0.Add(time.Minute))), // same cell, later
		message(t, "created", createdEvent("c", "adsb", 10.01, 20.01, t0)),
		message(t, "deleted", &sentinelv1.EntityEvent{Event: &sentinelv1.EntityEvent_Deleted{Deleted: &sentinelv1.EntityDeleted{}}}),
		{TopicPartition: ckafka.TopicPartition{Topic: &[]string{kafka.TopicEntityEvents}[0]}, Value: []byte("garbage"), Headers: []ckafka.Header{{Key: kafka.HeaderEventType, Value: []byte("position")}}},
	}
	if err := c.Handle(context.Background(), msgs); err != nil {
		t.Fatalf("Handle returned %v; it must never fail a batch", err)
	}
	if len(sink.fixes) != 3 {
		t.Fatalf("sink got %d fixes, want 3 (two positions and one created)", len(sink.fixes))
	}
	if !sink.fixes[2].Created {
		t.Error("created event not flagged")
	}
	if len(cov.cells) != 2 {
		t.Fatalf("coverage got %d cells, want 2 (opensky and adsb in one cell)", len(cov.cells))
	}
	for _, cell := range cov.cells {
		if cell.FeedID == "opensky" && !cell.At.Equal(t0.Add(time.Minute)) {
			t.Errorf("opensky cell time = %v, want the later report", cell.At)
		}
	}
	snap := c.counters.Snapshot()
	if snap["positions"] != int64(3) || snap["coverageWrites"] != int64(2) {
		t.Errorf("counters = %v", snap)
	}
}
