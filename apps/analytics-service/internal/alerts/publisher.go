// Package alerts publishes findings on alerts.events, where the alert
// service stores them like any other producer's and the gateway, the
// alerts list and outbound connections carry them.
package alerts

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/sentinel/go-common/classification"
	"github.com/sentinel/go-common/kafka"
	"github.com/sentinel/proto-gen/pbjson"
	sentinelv1 "github.com/sentinel/proto-gen/sentinel/v1"

	"github.com/sentinel/analytics-service/internal/detect"
	"github.com/sentinel/analytics-service/internal/store"
)

// ProducerName is the producer header and AlertEvent.producer value.
const ProducerName = "analytics-service"

// Producer is the part of the Kafka producer the publisher uses.
type Producer interface {
	ProduceProto(topic, key string, msg proto.Message, producer string, extra map[string]string) error
}

// EntityMarks reads the entity's exercise mark and level. An unknown entity
// is reported as pgx.ErrNoRows.
type EntityMarks interface {
	EntityMarks(ctx context.Context, id string) (exercise bool, classification string, err error)
}

// Publisher builds and sends alert events.
type Publisher struct {
	producer Producer
	marks    EntityMarks
	logger   *zap.Logger
}

// NewPublisher creates a publisher over the shared Kafka producer. Each
// alert is stamped from marks with its entity's exercise flag and level.
func NewPublisher(producer Producer, marks EntityMarks, logger *zap.Logger) *Publisher {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Publisher{producer: producer, marks: marks, logger: logger}
}

// PublishFinding sends the finding as a triggered alert whose id is the
// finding's id. The alert carries its entity's exercise mark, and the
// higher of the finding's level and the entity's, because the finding's own
// level can be stale (fixes seeded after a restart are UNCLASSIFIED) and the
// outbound pump reads this topic without the alert service in between.
func (p *Publisher) PublishFinding(ctx context.Context, f *store.Finding) error {
	ev := Event(f)
	p.stamp(ctx, f, ev.Alert)
	if err := p.producer.ProduceProto(kafka.TopicAlertEvents, f.EntityID, ev, ProducerName, nil); err != nil {
		return fmt.Errorf("publish finding %s: %w", f.ID, err)
	}
	return nil
}

// stamp sets the alert's exercise flag and classification from the entity.
// A missing entity is not exercise. Any other lookup failure is treated as
// exercise so an outbound connection never receives what it might owe a
// refusal; the finding's own level is then all there is to go on.
func (p *Publisher) stamp(ctx context.Context, f *store.Finding, alert *sentinelv1.Alert) {
	exercise, level, err := p.marks.EntityMarks(ctx, f.EntityID)
	switch {
	case err == nil:
	case errors.Is(err, pgx.ErrNoRows):
		exercise, level = false, ""
	default:
		p.logger.Warn("entity marks lookup failed; alert marked exercise",
			zap.String("findingId", f.ID), zap.String("entityId", f.EntityID), zap.Error(err))
		exercise, level = true, ""
	}
	alert.Exercise = exercise
	if classification.Rank(level) > classification.Rank(f.Classification) {
		alert.Classification = classificationEnum(level)
	}
}

func classificationEnum(level string) sentinelv1.Classification {
	c, _ := pbjson.ParseEnum[sentinelv1.Classification](classification.Normalize(level))
	return c
}

// AlertType maps a finding kind to the alert type consumers already know.
func AlertType(kind string) sentinelv1.AlertType {
	switch kind {
	case detect.KindUnusualMovement:
		return sentinelv1.AlertType_ALERT_TYPE_SPEED_ANOMALY
	case detect.KindWentDark:
		return sentinelv1.AlertType_ALERT_TYPE_ENTITY_LOST
	case detect.KindReappeared:
		return sentinelv1.AlertType_ALERT_TYPE_PATTERN_DETECTED
	default:
		return sentinelv1.AlertType_ALERT_TYPE_PATTERN_DEVIATION
	}
}

// Event builds the wire message for a finding.
func Event(f *store.Finding) *sentinelv1.AlertEvent {
	sev, _ := pbjson.ParseEnum[sentinelv1.AlertSeverity](f.Severity)
	level, _ := pbjson.ParseEnum[sentinelv1.Classification](f.Classification)
	alert := &sentinelv1.Alert{
		AlertId:        f.ID,
		AlertType:      AlertType(f.Kind),
		Severity:       sev,
		Title:          title(f),
		Description:    f.Summary,
		EntityId:       f.EntityID,
		CreatedAt:      timestamppb.New(f.FirstAt),
		Classification: level,
		Metadata: map[string]string{
			"findingId": f.ID,
			"kind":      f.Kind,
			"score":     fmt.Sprintf("%.3f", f.Score),
			"hits":      fmt.Sprintf("%d", f.Hits),
			"status":    f.Status,
		},
	}
	if f.Position != nil {
		alert.Position = &sentinelv1.Coordinate{Latitude: f.Position.Lat, Longitude: f.Position.Lon}
	}
	ev := &sentinelv1.AlertEvent{
		Alert:       alert,
		EventType:   sentinelv1.AlertEventType_ALERT_EVENT_TYPE_TRIGGERED,
		Producer:    ProducerName,
		AnomalyType: f.Kind,
		Score:       f.Score,
		EventTime:   timestamppb.Now(),
	}
	if v, ok := scalar(f.Expected); ok {
		ev.ExpectedValue = v
	}
	if v, ok := scalar(f.Actual); ok {
		ev.ActualValue = v
	}
	return ev
}

func title(f *store.Finding) string {
	switch f.Kind {
	case detect.KindUnusualPlace:
		return "Unusual place"
	case detect.KindUnusualTime:
		return "Unusual time"
	case detect.KindUnusualMovement:
		return "Unusual movement"
	case detect.KindWentDark:
		return "Went dark"
	case detect.KindReappeared:
		return "Reappeared"
	}
	return f.Kind
}

// scalar picks the one number that best summarises a finding's expected or
// actual side, for consumers that only carry a value.
func scalar(m map[string]any) (float64, bool) {
	for _, key := range []string{"distanceM", "thresholdM", "impliedKnots", "maxKnots", "value", "mean", "gapSec", "thresholdSec", "slotShare", "plausibleM"} {
		if v, ok := m[key]; ok {
			switch n := v.(type) {
			case float64:
				return n, true
			case int:
				return float64(n), true
			case int64:
				return float64(n), true
			}
		}
	}
	return 0, false
}
