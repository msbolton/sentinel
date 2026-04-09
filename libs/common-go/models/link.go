package models

// LinkType constants.
const (
	LinkTypeCommunication   = "COMMUNICATION"
	LinkTypeAssociation     = "ASSOCIATION"
	LinkTypeColocation      = "COLOCATION"
	LinkTypeFinancial       = "FINANCIAL"
	LinkTypeOrganizational  = "ORGANIZATIONAL"
	LinkTypeMovementPattern = "MOVEMENT_PATTERN"
	LinkTypeCommandControl  = "COMMAND_CONTROL"
	LinkTypeGeographic      = "GEOGRAPHIC"
	LinkTypeFamilial        = "FAMILIAL"
	LinkTypeLogistic        = "LOGISTIC"
	LinkTypeOperational     = "OPERATIONAL"
	LinkTypeIdentity        = "IDENTITY"
)

// Link represents a relationship between two entities.
type Link struct {
	LinkID         string            `json:"linkId"`
	SourceEntityID string            `json:"sourceEntityId"`
	TargetEntityID string            `json:"targetEntityId"`
	LinkType       string            `json:"linkType"`
	Confidence     float64           `json:"confidence"`
	Description    *string           `json:"description,omitempty"`
	Evidence       []string          `json:"evidence"`
	FirstObserved  *string           `json:"firstObserved,omitempty"`
	LastObserved   *string           `json:"lastObserved,omitempty"`
	Metadata       map[string]string `json:"metadata"`
}

// GraphNode represents a node in the entity graph.
type GraphNode struct {
	EntityID   string      `json:"entityId"`
	EntityType string      `json:"entityType"`
	Name       string      `json:"name"`
	Position   *Coordinate `json:"position,omitempty"`
}

// GraphEdge represents an edge in the entity graph.
type GraphEdge struct {
	Link Link `json:"link"`
}
