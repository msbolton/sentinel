package alerts

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
	"google.golang.org/protobuf/proto"

	"github.com/sentinel/go-common/kafka"
	sentinelv1 "github.com/sentinel/proto-gen/sentinel/v1"

	"github.com/sentinel/analytics-service/internal/detect"
	"github.com/sentinel/analytics-service/internal/store"
)

type fakeProducer struct {
	topic, key, producer string
	msg                  proto.Message
}

func (f *fakeProducer) ProduceProto(topic, key string, msg proto.Message, producer string, _ map[string]string) error {
	f.topic, f.key, f.msg, f.producer = topic, key, msg, producer
	return nil
}

// fakeMarks answers the entity lookup with fixed values.
type fakeMarks struct {
	exercise       bool
	classification string
	err            error
}

func (f fakeMarks) EntityMarks(context.Context, string) (bool, string, error) {
	return f.exercise, f.classification, f.err
}

// publishWith publishes a finding at the given level through a publisher whose
// lookup answers m, and returns the alert that reached the producer.
func publishWith(t *testing.T, m fakeMarks, level string) *sentinelv1.Alert {
	t.Helper()
	fp := &fakeProducer{}
	p := NewPublisher(fp, m, zap.NewNop())
	f := &store.Finding{
		ID: "11111111-1111-4111-8111-111111111111", EntityID: "22222222-2222-4222-8222-222222222222",
		Kind: detect.KindWentDark, Severity: "HIGH", Classification: level, FirstAt: time.Now(),
	}
	if err := p.PublishFinding(context.Background(), f); err != nil {
		t.Fatal(err)
	}
	return fp.msg.(*sentinelv1.AlertEvent).Alert
}

func TestPublishFindingStampsTheEntitysExerciseMark(t *testing.T) {
	if a := publishWith(t, fakeMarks{exercise: true, classification: "UNCLASSIFIED"}, "UNCLASSIFIED"); !a.Exercise {
		t.Error("an exercise entity's finding was published without the mark")
	}
	if a := publishWith(t, fakeMarks{classification: "UNCLASSIFIED"}, "UNCLASSIFIED"); a.Exercise {
		t.Error("a live entity's finding was published marked exercise")
	}
}

func TestPublishFindingForAnUnknownEntityIsNotExercise(t *testing.T) {
	a := publishWith(t, fakeMarks{err: pgx.ErrNoRows}, "CONFIDENTIAL")
	if a.Exercise || a.Classification != sentinelv1.Classification_CLASSIFICATION_CONFIDENTIAL {
		t.Errorf("exercise=%v classification=%v", a.Exercise, a.Classification)
	}
}

func TestPublishFindingFailsClosedWhenTheLookupFails(t *testing.T) {
	a := publishWith(t, fakeMarks{err: errors.New("connection reset")}, "SECRET")
	if !a.Exercise {
		t.Error("a failed lookup must publish the alert as exercise")
	}
	if a.Classification != sentinelv1.Classification_CLASSIFICATION_SECRET {
		t.Errorf("classification = %v, want the finding's own level", a.Classification)
	}
}

func TestPublishFindingTakesTheHigherOfTheFindingAndEntityLevels(t *testing.T) {
	// A finding about a SECRET entity, created from a seeded fix that read
	// UNCLASSIFIED, goes out SECRET.
	a := publishWith(t, fakeMarks{classification: "SECRET"}, "UNCLASSIFIED")
	if a.Classification != sentinelv1.Classification_CLASSIFICATION_SECRET {
		t.Errorf("classification = %v, want SECRET", a.Classification)
	}
	// The entity's level never lowers the finding's.
	a = publishWith(t, fakeMarks{classification: "UNCLASSIFIED"}, "TOP_SECRET")
	if a.Classification != sentinelv1.Classification_CLASSIFICATION_TOP_SECRET {
		t.Errorf("classification = %v, want TOP_SECRET", a.Classification)
	}
}

func TestPublishFindingBuildsAnAlertWithTheFindingId(t *testing.T) {
	fp := &fakeProducer{}
	p := NewPublisher(fp, fakeMarks{classification: "SECRET"}, zap.NewNop())
	first := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	f := &store.Finding{
		ID: "11111111-1111-4111-8111-111111111111", EntityID: "22222222-2222-4222-8222-222222222222",
		Kind: detect.KindUnusualPlace, Severity: "HIGH", Score: 4.2, Summary: "11 km from place 1", Status: store.StatusOpen,
		Expected: map[string]any{"thresholdM": 2000.0}, Actual: map[string]any{"distanceM": 11000.0},
		Position: &store.GeoPoint{Lat: 40.1, Lon: -74}, Classification: "SECRET", Hits: 3, FirstAt: first,
	}
	if err := p.PublishFinding(context.Background(), f); err != nil {
		t.Fatal(err)
	}
	if fp.topic != kafka.TopicAlertEvents || fp.key != f.EntityID || fp.producer != ProducerName {
		t.Errorf("topic %q key %q producer %q", fp.topic, fp.key, fp.producer)
	}
	// Round-trip through the wire format as the alert service would.
	raw, err := proto.Marshal(fp.msg)
	if err != nil {
		t.Fatal(err)
	}
	var ev sentinelv1.AlertEvent
	if err := proto.Unmarshal(raw, &ev); err != nil {
		t.Fatal(err)
	}
	a := ev.Alert
	if a.AlertId != f.ID || a.EntityId != f.EntityID {
		t.Errorf("ids: %s %s", a.AlertId, a.EntityId)
	}
	if a.AlertType != sentinelv1.AlertType_ALERT_TYPE_PATTERN_DEVIATION || a.Severity != sentinelv1.AlertSeverity_ALERT_SEVERITY_HIGH {
		t.Errorf("type %v severity %v", a.AlertType, a.Severity)
	}
	if a.Classification != sentinelv1.Classification_CLASSIFICATION_SECRET {
		t.Errorf("classification = %v", a.Classification)
	}
	if a.Title != "Unusual place" || a.Description != f.Summary || a.Position == nil || a.Position.Latitude != 40.1 {
		t.Errorf("alert = %+v", a)
	}
	if a.Metadata["findingId"] != f.ID || a.Metadata["kind"] != detect.KindUnusualPlace || a.Metadata["hits"] != "3" {
		t.Errorf("metadata = %v", a.Metadata)
	}
	if !a.CreatedAt.AsTime().Equal(first) {
		t.Errorf("createdAt = %v", a.CreatedAt.AsTime())
	}
	if ev.EventType != sentinelv1.AlertEventType_ALERT_EVENT_TYPE_TRIGGERED || ev.Producer != ProducerName || ev.AnomalyType != detect.KindUnusualPlace {
		t.Errorf("event type %v producer %q anomaly %q", ev.EventType, ev.Producer, ev.AnomalyType)
	}
	if ev.Score != 4.2 || ev.ExpectedValue != 2000 || ev.ActualValue != 11000 {
		t.Errorf("score %v expected %v actual %v", ev.Score, ev.ExpectedValue, ev.ActualValue)
	}
}

func TestAlertTypeMapping(t *testing.T) {
	cases := map[string]sentinelv1.AlertType{
		detect.KindUnusualPlace:    sentinelv1.AlertType_ALERT_TYPE_PATTERN_DEVIATION,
		detect.KindUnusualTime:     sentinelv1.AlertType_ALERT_TYPE_PATTERN_DEVIATION,
		detect.KindUnusualMovement: sentinelv1.AlertType_ALERT_TYPE_SPEED_ANOMALY,
		detect.KindWentDark:        sentinelv1.AlertType_ALERT_TYPE_ENTITY_LOST,
		detect.KindReappeared:      sentinelv1.AlertType_ALERT_TYPE_PATTERN_DETECTED,
	}
	for kind, want := range cases {
		if got := AlertType(kind); got != want {
			t.Errorf("%s -> %v, want %v", kind, got, want)
		}
	}
}
