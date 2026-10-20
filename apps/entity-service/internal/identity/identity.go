// Package identity holds the natural-identifier types shared by the fusion
// logic and the stores, so neither has to import the other.
package identity

import "time"

// Key is a resolved identity: the bare IdentityType name and the value.
type Key struct {
	Type  string
	Value string
}

// String renders the key for maps and logs.
func (k Key) String() string { return k.Type + ":" + k.Value }

// Canonical reports whether the key's type is one fusion joins entities on.
func (k Key) Canonical() bool { return IsCanonical(k.Type) }

var canonicalTypes = map[string]bool{"ICAO24": true, "MMSI": true, "NORAD_ID": true, "COT_UID": true, "EMPLACEMENT": true}

// IsCanonical reports whether an identity type is a fusion key type.
func IsCanonical(t string) bool { return canonicalTypes[t] }

// NewEntity describes the entity to create for an unresolved key.
type NewEntity struct {
	Key              Key
	EntityType       string // bare EntityType name
	Name             string
	Source           string // bare EntitySource name
	FeedID           string
	SourceEntityID   string
	TrackEnvironment string
	CountryOfOrigin  string
	Classification   string // bare Classification name; "" = UNCLASSIFIED
	Exercise         bool   // the first report was an exercise report
	SeenAt           time.Time
}

// Binding attaches an identity to an entity.
type Binding struct {
	Key      Key
	EntityID string
	SeenAt   time.Time
}
