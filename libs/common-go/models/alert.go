package models

// AlertSeverity constants.
const (
	AlertSeverityInfo     = "INFO"
	AlertSeverityLow      = "LOW"
	AlertSeverityMedium   = "MEDIUM"
	AlertSeverityHigh     = "HIGH"
	AlertSeverityCritical = "CRITICAL"
)

// AlertType constants.
const (
	AlertTypeGeofenceBreach           = "GEOFENCE_BREACH"
	AlertTypeSpeedAnomaly             = "SPEED_ANOMALY"
	AlertTypePatternDeviation         = "PATTERN_DEVIATION"
	AlertTypeProximity                = "PROXIMITY"
	AlertTypeNewEntity                = "NEW_ENTITY"
	AlertTypeGeofenceEntry            = "GEOFENCE_ENTRY"
	AlertTypeGeofenceExit             = "GEOFENCE_EXIT"
	AlertTypePatternMatch             = "PATTERN_MATCH"
	AlertTypeCustom                   = "CUSTOM"
	AlertTypeEntityLost               = "ENTITY_LOST"
	AlertTypePatternDetected          = "PATTERN_DETECTED"
	AlertTypeLinkChange               = "LINK_CHANGE"
	AlertTypeClassificationChange     = "CLASSIFICATION_CHANGE"
	AlertTypeSystem                   = "SYSTEM"
)

// RuleType constants.
const (
	RuleTypeGeofence       = "GEOFENCE"
	RuleTypeSpeedThreshold = "SPEED_THRESHOLD"
	RuleTypeProximity      = "PROXIMITY"
	RuleTypePattern        = "PATTERN"
)

// Alert represents an alert record.
type Alert struct {
	AlertID          string                 `json:"alertId"`
	AlertType        string                 `json:"alertType"`
	Severity         string                 `json:"severity"`
	Title            string                 `json:"title"`
	Description      string                 `json:"description"`
	EntityID         string                 `json:"entityId"`
	RelatedEntityIDs []string               `json:"relatedEntityIds"`
	Position         *Coordinate            `json:"position,omitempty"`
	RuleID           *string                `json:"ruleId,omitempty"`
	Metadata         map[string]string      `json:"metadata"`
	CreatedAt        string                 `json:"createdAt"`
	AcknowledgedAt   *string                `json:"acknowledgedAt,omitempty"`
	AcknowledgedBy   *string                `json:"acknowledgedBy,omitempty"`
	ResolvedAt       *string                `json:"resolvedAt,omitempty"`
}

// AlertRule represents an alert rule configuration.
type AlertRule struct {
	ID                   string                 `json:"id"`
	Name                 string                 `json:"name"`
	RuleType             string                 `json:"ruleType"`
	Config               map[string]interface{} `json:"config"`
	MonitoredEntityTypes []string               `json:"monitoredEntityTypes"`
	Severity             string                 `json:"severity"`
	Enabled              bool                   `json:"enabled"`
	CreatedAt            string                 `json:"createdAt"`
	UpdatedAt            string                 `json:"updatedAt"`
}
