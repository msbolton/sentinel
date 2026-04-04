package models

// Coordinate represents a geographic position.
type Coordinate struct {
	Latitude       float64  `json:"latitude"`
	Longitude      float64  `json:"longitude"`
	AltitudeMeters *float64 `json:"altitudeMeters,omitempty"`
}

// BoundingBox represents a geographic bounding box.
type BoundingBox struct {
	North float64 `json:"north"`
	South float64 `json:"south"`
	East  float64 `json:"east"`
	West  float64 `json:"west"`
}

// ClassificationLevels maps classification strings to numeric levels.
var ClassificationLevels = map[string]int{
	ClassificationUnclassified: 0,
	ClassificationConfidential: 1,
	ClassificationSecret:       2,
	ClassificationTopSecret:    3,
}

// NavigationalStatus constants for AIS data.
const (
	NavStatusUnderWayUsingEngine      = "UNDER_WAY_USING_ENGINE"
	NavStatusAtAnchor                 = "AT_ANCHOR"
	NavStatusNotUnderCommand          = "NOT_UNDER_COMMAND"
	NavStatusRestrictedManoeuvrability = "RESTRICTED_MANOEUVRABILITY"
	NavStatusConstrainedByDraught     = "CONSTRAINED_BY_DRAUGHT"
	NavStatusMoored                   = "MOORED"
	NavStatusAground                  = "AGROUND"
	NavStatusEngagedInFishing         = "ENGAGED_IN_FISHING"
	NavStatusUnderWaySailing          = "UNDER_WAY_SAILING"
	NavStatusAISSART                  = "AIS_SART"
	NavStatusUnknown                  = "UNKNOWN"
)

// DamageAssessment constants.
const (
	DamageNone      = "NONE"
	DamageLight     = "LIGHT"
	DamageModerate  = "MODERATE"
	DamageHeavy     = "HEAVY"
	DamageDestroyed = "DESTROYED"
	DamageUnknown   = "UNKNOWN"
)

// CharacterizationState constants.
const (
	CharacterizationAssessed         = "ASSESSED"
	CharacterizationAssumed          = "ASSUMED"
	CharacterizationSuspected        = "SUSPECTED"
	CharacterizationUncharacterized  = "UNCHARACTERIZED"
)
