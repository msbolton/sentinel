package models

// Entity represents the full entity model matching the database schema.
type Entity struct {
	ID                     string                 `json:"id"`
	EntityType             string                 `json:"entityType"`
	Name                   string                 `json:"name"`
	Description            *string                `json:"description,omitempty"`
	Source                 string                 `json:"source"`
	Classification         string                 `json:"classification"`
	FeedID                 *string                `json:"feedId,omitempty"`
	Position               *Coordinate            `json:"position,omitempty"`
	Heading                *float64               `json:"heading,omitempty"`
	SpeedKnots             *float64               `json:"speedKnots,omitempty"`
	Course                 *float64               `json:"course,omitempty"`
	MilStd2525dSymbol      *string                `json:"milStd2525dSymbol,omitempty"`
	Metadata               map[string]interface{} `json:"metadata"`
	Affiliations           []string               `json:"affiliations"`
	CreatedAt              string                 `json:"createdAt"`
	UpdatedAt              string                 `json:"updatedAt"`
	LastSeenAt             *string                `json:"lastSeenAt,omitempty"`
	Affiliation            *string                `json:"affiliation,omitempty"`
	IdentityConfidence     *int                   `json:"identityConfidence,omitempty"`
	Characterization       *string                `json:"characterization,omitempty"`
	TrackEnvironment       *string                `json:"trackEnvironment,omitempty"`
	TrackProcessingState   *string                `json:"trackProcessingState,omitempty"`
	Altitude               *float64               `json:"altitude,omitempty"`
	Orientation            *Orientation           `json:"orientation,omitempty"`
	Kinematics             *KinematicState         `json:"kinematics,omitempty"`
	OperationalStatus      *string                `json:"operationalStatus,omitempty"`
	DamageAssessment       *string                `json:"damageAssessment,omitempty"`
	DamageConfidence       *int                   `json:"damageConfidence,omitempty"`
	DimensionLength        *float64               `json:"dimensionLength,omitempty"`
	DimensionWidth         *float64               `json:"dimensionWidth,omitempty"`
	DimensionHeight        *float64               `json:"dimensionHeight,omitempty"`
	CountryOfOrigin        *string                `json:"countryOfOrigin,omitempty"`
	PlatformData           *PlatformData          `json:"platformData,omitempty"`
	SourceEntityID         *string                `json:"sourceEntityId,omitempty"`
	CircularError          *float64               `json:"circularError,omitempty"`
	LastObservationSource  *string                `json:"lastObservationSource,omitempty"`
	AgeoutState            string                 `json:"ageoutState,omitempty"`
	Deleted                bool                   `json:"deleted,omitempty"`
}
