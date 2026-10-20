package evaluator

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/sentinel/go-common/kafka"
	"github.com/sentinel/proto-gen/pbjson"
	sentinelv1 "github.com/sentinel/proto-gen/sentinel/v1"

	"github.com/sentinel/alert-service/internal/store"
)

// ProducerName is the producer header on alert events this service emits.
const ProducerName = "alert-service"

// Evaluator evaluates alert rules against entity positions.
type Evaluator struct {
	store    *store.AlertStore
	producer *kafka.Producer
	logger   *zap.Logger
	geofence *ruleStateTracker
	speed    *ruleStateTracker

	// Lookups against the entity table, as fields so tests can stand in for it.
	entitiesExercise func(ctx context.Context, ids []string) (bool, error)
	entityLevel      func(ctx context.Context, id string) (string, error)
}

// NewEvaluator creates a new alert evaluator.
func NewEvaluator(store *store.AlertStore, producer *kafka.Producer, logger *zap.Logger) *Evaluator {
	return &Evaluator{
		store: store, producer: producer, logger: logger, geofence: newRuleStateTracker(), speed: newRuleStateTracker(),
		entitiesExercise: store.EntitiesExercise, entityLevel: store.EntityClassification,
	}
}

// isExercise reports whether an alert about these entities is exercise data:
// the producer already said so, or any of the entities carries the mark. An
// alert that cannot be checked counts as exercise (fail closed), so one the
// service could not vouch for never leaves on a connection that refuses
// exercise data.
func (e *Evaluator) isExercise(ctx context.Context, marked bool, ids ...string) bool {
	if marked {
		return true
	}
	exercise, err := e.entitiesExercise(ctx, ids)
	if err != nil {
		e.logger.Warn("exercise check failed, marking the alert exercise", zap.Strings("entityIds", ids), zap.Error(err))
		return true
	}
	return exercise
}

// EvaluatePosition evaluates geofence and speed rules for a position event.
// Alerts inherit the entity's classification at the moment they fire.
func (e *Evaluator) EvaluatePosition(ctx context.Context, entityID string, lat, lng float64, entityType string, speedKnots *float64, classification string) {
	e.evaluateGeofence(ctx, entityID, lat, lng, entityType, classification)
	if speedKnots != nil {
		e.evaluateSpeed(ctx, entityID, *speedKnots, entityType, classification)
	}
}

func (e *Evaluator) evaluateGeofence(ctx context.Context, entityID string, lat, lng float64, entityType, classification string) {
	rules, err := e.store.GetEnabledRules(ctx, "GEOFENCE", &entityType)
	if err != nil {
		e.logger.Error("failed to get geofence rules", zap.Error(err))
		return
	}

	for _, rule := range rules {
		polygonRaw, ok := rule.Config["polygon"]
		if !ok {
			e.evaluateRing(ctx, rule, entityID, lat, lng, classification)
			continue
		}
		coords, ok := parsePolygon(polygonRaw)
		if !ok {
			e.logger.Warn("geofence rule has no usable polygon", zap.String("ruleId", rule.ID))
			continue
		}
		wkt := polygonWKT(coords)
		triggerOn := triggerMode(rule.Config)

		// Check containment using PostGIS.
		var contains bool
		err = e.store.Pool().QueryRow(ctx, fmt.Sprintf(`
			SELECT ST_Contains(
				ST_SetSRID(ST_GeomFromText('%s'), 4326),
				ST_SetSRID(ST_MakePoint($1, $2), 4326)
			)
		`, wkt), lng, lat).Scan(&contains)
		if err != nil {
			e.logger.Error("geofence check failed", zap.String("ruleId", rule.ID), zap.Error(err))
			continue
		}

		// Fire on the crossing, not on every fix inside the zone.
		alertType := e.geofence.transition(rule.ID, entityID, contains, time.Now(), func(prev *bool, cur bool) string {
			return geofenceTransition(prev, cur, triggerOn)
		})
		if alertType == "" {
			continue
		}

		e.raiseGeofenceAlert(ctx, rule, alertType, entityID, lat, lng, classification)
	}
}

// evaluateRing checks a range-ring rule: the fix is inside when the anchor
// track is live and the fix lies within the radius of its last position. The
// anchor's own fixes never trigger its ring.
func (e *Evaluator) evaluateRing(ctx context.Context, rule store.AlertRuleRecord, entityID string, lat, lng float64, classification string) {
	anchorID, radiusM, ok := ringConfig(rule.Config)
	if !ok || anchorID == entityID {
		return
	}
	aLat, aLng, ageoutState, found, err := e.store.GetEntityPosition(ctx, anchorID)
	if err != nil {
		e.logger.Error("ring anchor lookup failed", zap.String("ruleId", rule.ID), zap.Error(err))
		return
	}
	inside, ok := ringObservation(found, ageoutState, aLat, aLng, lat, lng, radiusM)
	if !ok {
		return // no live anchor, no observation: the entry/exit state is left as it was
	}
	triggerOn := triggerMode(rule.Config)
	alertType := e.geofence.transition(rule.ID, entityID, inside, time.Now(), func(prev *bool, cur bool) string {
		return geofenceTransition(prev, cur, triggerOn)
	})
	if alertType == "" {
		return
	}
	e.raiseGeofenceAlert(ctx, rule, alertType, entityID, lat, lng, classification)
}

// raiseGeofenceAlert stores and publishes an entry or exit alert for a rule.
func (e *Evaluator) raiseGeofenceAlert(ctx context.Context, rule store.AlertRuleRecord, alertType, entityID string, lat, lng float64, classification string) {
	alert, err := e.store.CreateAlert(ctx, e.geofenceAlertParams(ctx, rule, alertType, entityID, lat, lng, classification))
	if err != nil {
		e.logger.Error("failed to create geofence alert", zap.Error(err))
		return
	}
	e.publishAlert(entityID, alert)
	e.logger.Info("geofence alert created",
		zap.String("alertId", alert.ID),
		zap.String("alertType", alertType),
		zap.String("entityId", entityID),
	)
}

// geofenceAlertParams is what a rule's entry or exit alert stores.
func (e *Evaluator) geofenceAlertParams(ctx context.Context, rule store.AlertRuleRecord, alertType, entityID string, lat, lng float64, classification string) store.CreateAlertParams {
	return store.CreateAlertParams{
		AlertType:      alertType,
		Severity:       rule.Severity,
		Title:          fmt.Sprintf("Geofence %s: %s", alertType, rule.Name),
		Description:    fmt.Sprintf("Entity %s triggered %s for rule %s", entityID, alertType, rule.Name),
		EntityID:       entityID,
		Lat:            &lat,
		Lng:            &lng,
		RuleID:         &rule.ID,
		Classification: classification,
		Exercise:       e.isExercise(ctx, false, entityID),
	}
}

// speedAlertParams is what a speed rule's alert stores.
func (e *Evaluator) speedAlertParams(ctx context.Context, rule store.AlertRuleRecord, description, entityID, classification string) store.CreateAlertParams {
	return store.CreateAlertParams{
		AlertType:      "SPEED_ANOMALY",
		Severity:       rule.Severity,
		Title:          fmt.Sprintf("Speed anomaly: %s", rule.Name),
		Description:    description,
		EntityID:       entityID,
		RuleID:         &rule.ID,
		Classification: classification,
		Exercise:       e.isExercise(ctx, false, entityID),
	}
}

func (e *Evaluator) evaluateSpeed(ctx context.Context, entityID string, speedKnots float64, entityType, classification string) {
	rules, err := e.store.GetEnabledRules(ctx, "SPEED_THRESHOLD", &entityType)
	if err != nil {
		e.logger.Error("failed to get speed rules", zap.Error(err))
		return
	}

	for _, rule := range rules {
		triggered, description := evaluateSpeedRule(entityID, rule.Config, speedKnots)
		// Fire when the entity starts violating the threshold, not on every fix.
		fired := e.speed.transition(rule.ID, entityID, triggered, time.Now(), func(prev *bool, cur bool) string {
			if cur && (prev == nil || !*prev) {
				return "SPEED_ANOMALY"
			}
			return ""
		})
		if fired == "" {
			continue
		}

		alert, err := e.store.CreateAlert(ctx, e.speedAlertParams(ctx, rule, description, entityID, classification))
		if err != nil {
			e.logger.Error("failed to create speed alert", zap.Error(err))
			continue
		}

		e.publishAlert(entityID, alert)
		e.logger.Info("speed alert created",
			zap.String("alertId", alert.ID),
			zap.String("entityId", entityID),
		)
	}
}

// publishAlert emits a typed AlertEvent to alerts.events.
func (e *Evaluator) publishAlert(entityID string, alert *store.AlertRecord) {
	ev := &sentinelv1.AlertEvent{
		Alert:     AlertProto(alert),
		EventType: sentinelv1.AlertEventType_ALERT_EVENT_TYPE_TRIGGERED,
		Producer:  ProducerName,
		EventTime: timestamppb.Now(),
	}
	if err := e.producer.ProduceProto(kafka.TopicAlertEvents, entityID, ev, ProducerName, nil); err != nil {
		e.logger.Error("failed to publish alert", zap.Error(err))
	}
}

// StoreExternalAlert persists an alert another service raised (analytics
// anomalies) under its own id, so it shows up in the alerts API. A repeat
// delivery is a no-op.
func (e *Evaluator) StoreExternalAlert(ctx context.Context, ev *sentinelv1.AlertEvent) error {
	p, err := e.externalAlertParams(ctx, ev)
	if err != nil {
		return err
	}
	rec, err := e.store.CreateAlert(ctx, p)
	if err != nil {
		return err
	}
	if rec == nil {
		return nil // already stored
	}
	e.logger.Info("external alert stored", zap.String("alertId", rec.ID), zap.String("producer", ev.Producer))
	return nil
}

// externalAlertParams maps an alert another service raised to what is stored.
func (e *Evaluator) externalAlertParams(ctx context.Context, ev *sentinelv1.AlertEvent) (store.CreateAlertParams, error) {
	a := ev.Alert
	if a == nil || a.AlertId == "" || a.EntityId == "" {
		return store.CreateAlertParams{}, fmt.Errorf("alert event without alert id or entity id")
	}
	p := store.CreateAlertParams{
		ID:          a.AlertId,
		AlertType:   pbjson.EnumName(a.AlertType),
		Severity:    pbjson.EnumName(a.Severity),
		Title:       a.Title,
		Description: a.Description,
		EntityID:    a.EntityId,
		Metadata:    a.Metadata,
	}
	if p.Metadata == nil {
		p.Metadata = map[string]string{}
	}
	if ev.AnomalyType != "" {
		p.Metadata["anomalyType"] = ev.AnomalyType
		p.Metadata["score"] = fmt.Sprintf("%.3f", ev.Score)
	}
	if a.RuleId != "" {
		rule := a.RuleId
		p.RuleID = &rule
	}
	if a.Position != nil {
		lat, lng := a.Position.Latitude, a.Position.Longitude
		p.Lat, p.Lng = &lat, &lng
	}
	// A producer that does not classify its alerts (analytics) must not land
	// a SECRET entity's anomaly as UNCLASSIFIED: inherit from the entity.
	if a.Classification != sentinelv1.Classification_CLASSIFICATION_UNSPECIFIED {
		p.Classification = pbjson.EnumName(a.Classification)
	} else if level, err := e.entityLevel(ctx, a.EntityId); err == nil {
		p.Classification = level
	} else {
		e.logger.Warn("entity classification unavailable for external alert", zap.String("entityId", a.EntityId), zap.Error(err))
	}
	p.Exercise = e.isExercise(ctx, a.Exercise, append([]string{a.EntityId}, a.RelatedEntityIds...)...)
	return p, nil
}

// AlertProto converts a stored alert to the wire message.
func AlertProto(a *store.AlertRecord) *sentinelv1.Alert {
	out := &sentinelv1.Alert{
		AlertId:          a.ID,
		AlertType:        parseEnum[sentinelv1.AlertType](a.AlertType),
		Severity:         parseEnum[sentinelv1.AlertSeverity](a.Severity),
		Title:            a.Title,
		EntityId:         a.EntityID,
		RelatedEntityIds: a.RelatedEntityIDs,
		Metadata:         a.Metadata,
		AcknowledgedBy:   deref(a.AcknowledgedBy),
		Classification:   parseEnum[sentinelv1.Classification](a.Classification),
		Exercise:         a.Exercise,
	}
	if a.Description != nil {
		out.Description = *a.Description
	}
	if a.RuleID != nil {
		out.RuleId = *a.RuleID
	}
	if a.Position != nil {
		out.Position = &sentinelv1.Coordinate{Latitude: a.Position.Lat, Longitude: a.Position.Lon}
	}
	if t, ok := parseTime(a.CreatedAt); ok {
		out.CreatedAt = timestamppb.New(t)
	}
	if a.AcknowledgedAt != nil {
		if t, ok := parseTime(*a.AcknowledgedAt); ok {
			out.AcknowledgedAt = timestamppb.New(t)
		}
	}
	return out
}

func parseEnum[E interface {
	~int32
	Descriptor() protoreflect.EnumDescriptor
}](name string) E {
	v, _ := pbjson.ParseEnum[E](name)
	return v
}

var timeLayouts = []string{time.RFC3339Nano, "2006-01-02 15:04:05.999999999-07", "2006-01-02 15:04:05.999999999-07:00", "2006-01-02 15:04:05-07"}

func parseTime(s string) (time.Time, bool) {
	for _, l := range timeLayouts {
		if t, err := time.Parse(l, s); err == nil {
			return t.UTC(), true
		}
	}
	return time.Time{}, false
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
