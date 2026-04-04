package opensearch

import "encoding/json"

// EntityDocument represents an entity in the OpenSearch index.
type EntityDocument struct {
	ID                  string                 `json:"id"`
	Name                string                 `json:"name"`
	Description         *string                `json:"description,omitempty"`
	EntityType          string                 `json:"entityType"`
	Source              *string                `json:"source,omitempty"`
	Classification      *string                `json:"classification,omitempty"`
	Position            *GeoPoint              `json:"position,omitempty"`
	Affiliations        []string               `json:"affiliations,omitempty"`
	Metadata            map[string]interface{} `json:"metadata,omitempty"`
	CreatedAt           *string                `json:"createdAt,omitempty"`
	UpdatedAt           *string                `json:"updatedAt,omitempty"`
	LastSeenAt          *string                `json:"lastSeenAt,omitempty"`
	Affiliation         *string                `json:"affiliation,omitempty"`
	TrackEnvironment    *string                `json:"trackEnvironment,omitempty"`
	OperationalStatus   *string                `json:"operationalStatus,omitempty"`
	CountryOfOrigin     *string                `json:"countryOfOrigin,omitempty"`
	SourceEntityID      *string                `json:"sourceEntityId,omitempty"`
	PlatformIdentifiers *PlatformIdentifiers   `json:"platformIdentifiers,omitempty"`
}

// GeoPoint represents a geographic point for OpenSearch.
type GeoPoint struct {
	Lat float64 `json:"lat"`
	Lon float64 `json:"lon"`
}

// PlatformIdentifiers holds platform-specific identifiers.
type PlatformIdentifiers struct {
	MMSI         *string `json:"mmsi,omitempty"`
	IMO          *string `json:"imo,omitempty"`
	ICAOHex      *string `json:"icaoHex,omitempty"`
	Registration *string `json:"registration,omitempty"`
	Callsign     *string `json:"callsign,omitempty"`
	NoradID      *int    `json:"noradId,omitempty"`
	Squawk       *string `json:"squawk,omitempty"`
}

// SearchQuery represents parameters for a full-text search.
type SearchQuery struct {
	Q               string
	North           *float64
	South           *float64
	East            *float64
	West            *float64
	Types           []string
	Sources         []string
	Classifications []string
	Page            int
	PageSize        int
}

// NearbyQuery represents parameters for a geo-distance search.
type NearbyQuery struct {
	Lat      float64
	Lng      float64
	RadiusKm float64
	Q        string
	Page     int
	PageSize int
}

// SearchResult represents the search response.
type SearchResult struct {
	Total    int              `json:"total"`
	Page     int              `json:"page"`
	PageSize int              `json:"pageSize"`
	Hits     []EntityDocument `json:"hits"`
	Facets   *Facets          `json:"facets,omitempty"`
}

// Facets represents aggregation counts.
type Facets struct {
	EntityTypes     map[string]int `json:"entityTypes"`
	Sources         map[string]int `json:"sources"`
	Classifications map[string]int `json:"classifications"`
}

// SuggestResult represents a single autocomplete suggestion.
type SuggestResult struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	EntityType string `json:"entityType"`
}

// osSearchResponse is the raw OpenSearch search response structure.
type osSearchResponse struct {
	Hits struct {
		Total struct {
			Value int `json:"value"`
		} `json:"total"`
		Hits []struct {
			ID     string          `json:"_id"`
			Source json.RawMessage `json:"_source"`
		} `json:"hits"`
	} `json:"hits"`
	Aggregations map[string]json.RawMessage `json:"aggregations"`
}
