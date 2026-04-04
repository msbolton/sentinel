package evaluator

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/sentinel/go-common/kafka"
	"github.com/sentinel/alert-service/internal/store"
	"go.uber.org/zap"
)

// Evaluator evaluates alert rules against entity positions.
type Evaluator struct {
	store    *store.AlertStore
	producer *kafka.Producer
	logger   *zap.Logger
}

// NewEvaluator creates a new alert evaluator.
func NewEvaluator(store *store.AlertStore, producer *kafka.Producer, logger *zap.Logger) *Evaluator {
	return &Evaluator{store: store, producer: producer, logger: logger}
}

// EvaluatePosition evaluates geofence and speed rules for a position event.
func (e *Evaluator) EvaluatePosition(ctx context.Context, entityID string, lat, lng float64, entityType string, speedKnots *float64) {
	e.evaluateGeofence(ctx, entityID, lat, lng, entityType)
	if speedKnots != nil {
		e.evaluateSpeed(ctx, entityID, *speedKnots, entityType)
	}
}

func (e *Evaluator) evaluateGeofence(ctx context.Context, entityID string, lat, lng float64, entityType string) {
	rules, err := e.store.GetEnabledRules(ctx, "GEOFENCE", &entityType)
	if err != nil {
		e.logger.Error("failed to get geofence rules", zap.Error(err))
		return
	}

	for _, rule := range rules {
		polygonRaw, ok := rule.Config["polygon"]
		if !ok {
			continue
		}

		triggerOn := "BOTH"
		if t, ok := rule.Config["triggerOn"].(string); ok {
			triggerOn = t
		}

		// Build polygon WKT from config.
		polygonJSON, err := json.Marshal(polygonRaw)
		if err != nil {
			continue
		}

		var coords [][]float64
		if err := json.Unmarshal(polygonJSON, &coords); err != nil {
			continue
		}

		if len(coords) < 3 {
			continue
		}

		// Close the ring if needed.
		if coords[0][0] != coords[len(coords)-1][0] || coords[0][1] != coords[len(coords)-1][1] {
			coords = append(coords, coords[0])
		}

		// Build WKT polygon string.
		points := make([]string, len(coords))
		for i, c := range coords {
			points[i] = fmt.Sprintf("%f %f", c[0], c[1])
		}
		wkt := fmt.Sprintf("POLYGON((%s))", joinStrings(points, ","))

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

		var alertType string
		switch {
		case contains && (triggerOn == "ENTRY" || triggerOn == "BOTH"):
			alertType = "GEOFENCE_ENTRY"
		case !contains && (triggerOn == "EXIT" || triggerOn == "BOTH"):
			alertType = "GEOFENCE_EXIT"
		default:
			continue
		}

		alert, err := e.store.CreateAlert(ctx, store.CreateAlertParams{
			AlertType:   alertType,
			Severity:    rule.Severity,
			Title:       fmt.Sprintf("Geofence %s: %s", alertType, rule.Name),
			Description: fmt.Sprintf("Entity %s triggered %s for rule %s", entityID, alertType, rule.Name),
			EntityID:    entityID,
			Lat:         &lat,
			Lng:         &lng,
			RuleID:      &rule.ID,
		})
		if err != nil {
			e.logger.Error("failed to create geofence alert", zap.Error(err))
			continue
		}

		e.publishAlert(kafka.TopicAlertGeofence, entityID, alert)
		e.logger.Info("geofence alert created",
			zap.String("alertId", alert.ID),
			zap.String("alertType", alertType),
			zap.String("entityId", entityID),
		)
	}
}

func (e *Evaluator) evaluateSpeed(ctx context.Context, entityID string, speedKnots float64, entityType string) {
	rules, err := e.store.GetEnabledRules(ctx, "SPEED_THRESHOLD", &entityType)
	if err != nil {
		e.logger.Error("failed to get speed rules", zap.Error(err))
		return
	}

	for _, rule := range rules {
		var triggered bool
		var description string

		if maxSpeed, ok := rule.Config["maxSpeedKnots"].(float64); ok && speedKnots > maxSpeed {
			triggered = true
			description = fmt.Sprintf("Entity %s speed %.1f kn exceeds max %.1f kn", entityID, speedKnots, maxSpeed)
		}

		if minSpeed, ok := rule.Config["minSpeedKnots"].(float64); ok && speedKnots < minSpeed {
			triggered = true
			description = fmt.Sprintf("Entity %s speed %.1f kn below min %.1f kn", entityID, speedKnots, minSpeed)
		}

		if !triggered {
			continue
		}

		alert, err := e.store.CreateAlert(ctx, store.CreateAlertParams{
			AlertType:   "SPEED_ANOMALY",
			Severity:    rule.Severity,
			Title:       fmt.Sprintf("Speed anomaly: %s", rule.Name),
			Description: description,
			EntityID:    entityID,
			RuleID:      &rule.ID,
		})
		if err != nil {
			e.logger.Error("failed to create speed alert", zap.Error(err))
			continue
		}

		e.publishAlert(kafka.TopicAlertAnomaly, entityID, alert)
		e.logger.Info("speed alert created",
			zap.String("alertId", alert.ID),
			zap.String("entityId", entityID),
		)
	}
}

func (e *Evaluator) publishAlert(topic, entityID string, alert *store.AlertRecord) {
	payload := map[string]interface{}{
		"alertId":   alert.ID,
		"alertType": alert.AlertType,
		"severity":  alert.Severity,
		"title":     alert.Title,
		"entityId":  alert.EntityID,
		"createdAt": alert.CreatedAt,
	}
	if alert.Description != nil {
		payload["description"] = *alert.Description
	}
	if alert.Position != nil {
		payload["position"] = alert.Position
	}
	if alert.RuleID != nil {
		payload["ruleId"] = *alert.RuleID
	}

	data, err := json.Marshal(payload)
	if err != nil {
		e.logger.Error("failed to marshal alert payload", zap.Error(err))
		return
	}

	if err := e.producer.ProduceRaw(topic, []byte(entityID), data, nil); err != nil {
		e.logger.Error("failed to publish alert", zap.String("topic", topic), zap.Error(err))
	}
}

func joinStrings(s []string, sep string) string {
	result := ""
	for i, v := range s {
		if i > 0 {
			result += sep
		}
		result += v
	}
	return result
}
