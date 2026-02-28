package ingest

import (
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/sentinel/ingest-service/internal/models"
)

const (
	// correlationDistanceThresholdKm is the maximum distance in kilometers
	// between two positions to consider them the same entity.
	correlationDistanceThresholdKm = 0.5

	// duplicateTimeThresholdSec is the minimum time in seconds between
	// identical position reports before treating them as duplicates.
	duplicateTimeThresholdSec = 1.0

	// trackExpiryDuration is how long an entity track is kept before expiration.
	trackExpiryDuration = 15 * time.Minute

	// earthRadiusKm is the mean radius of the Earth.
	earthRadiusKm = 6371.0
)

// trackEntry represents a tracked entity in the correlator's memory.
type trackEntry struct {
	EntityID   string
	EntityType string
	Name       string
	Latitude   float64
	Longitude  float64
	LastSeen   time.Time
}

// TrackCorrelator maintains an in-memory map of recently observed entities and
// correlates incoming positions to existing tracks based on proximity, entity
// type, and identity. It handles duplicate suppression for repeated reports of
// the same entity at the same location within a time threshold.
type TrackCorrelator struct {
	mu     sync.RWMutex
	tracks map[string]*trackEntry // keyed by entity ID
}

// NewTrackCorrelator creates a new correlator and starts a background goroutine
// that periodically evicts stale tracks.
func NewTrackCorrelator() *TrackCorrelator {
	tc := &TrackCorrelator{
		tracks: make(map[string]*trackEntry),
	}
	go tc.evictionLoop()
	return tc
}

// Correlate attempts to match an incoming entity position to an existing track.
// If the entity already has an ID that matches a known track, it updates that track.
// If the entity has no ID, it searches for a nearby entity of the same type.
// Returns the correlated entity (with stable ID assigned) and whether this
// position should be suppressed as a duplicate.
func (tc *TrackCorrelator) Correlate(entity *models.EntityPosition) (correlated *models.EntityPosition, isDuplicate bool) {
	tc.mu.Lock()
	defer tc.mu.Unlock()

	// If the entity already has an ID, check if we know it.
	if entity.EntityID != "" {
		if existing, ok := tc.tracks[entity.EntityID]; ok {
			// Check for duplicate suppression.
			if tc.isDuplicate(existing, entity) {
				return entity, true
			}
			// Update the existing track.
			existing.Latitude = entity.Latitude
			existing.Longitude = entity.Longitude
			existing.LastSeen = entity.Timestamp
			if entity.Name != "" {
				existing.Name = entity.Name
			}
			return entity, false
		}

		// New entity with an ID; register it.
		tc.tracks[entity.EntityID] = &trackEntry{
			EntityID:   entity.EntityID,
			EntityType: entity.EntityType,
			Name:       entity.Name,
			Latitude:   entity.Latitude,
			Longitude:  entity.Longitude,
			LastSeen:   entity.Timestamp,
		}
		return entity, false
	}

	// Entity has no ID; attempt proximity correlation.
	matched := tc.findNearestMatch(entity)
	if matched != nil {
		entity.EntityID = matched.EntityID
		if entity.Name == "" {
			entity.Name = matched.Name
		}

		if tc.isDuplicate(matched, entity) {
			return entity, true
		}

		matched.Latitude = entity.Latitude
		matched.Longitude = entity.Longitude
		matched.LastSeen = entity.Timestamp
		return entity, false
	}

	// No match found; assign a new entity ID.
	entity.EntityID = fmt.Sprintf("TRK-%s", uuid.New().String()[:8])
	tc.tracks[entity.EntityID] = &trackEntry{
		EntityID:   entity.EntityID,
		EntityType: entity.EntityType,
		Name:       entity.Name,
		Latitude:   entity.Latitude,
		Longitude:  entity.Longitude,
		LastSeen:   entity.Timestamp,
	}
	return entity, false
}

// findNearestMatch searches for the closest tracked entity of the same type
// within the correlation distance threshold.
func (tc *TrackCorrelator) findNearestMatch(entity *models.EntityPosition) *trackEntry {
	var closest *trackEntry
	minDist := correlationDistanceThresholdKm

	for _, track := range tc.tracks {
		// Only match same entity type (or unknown).
		if track.EntityType != entity.EntityType &&
			entity.EntityType != models.EntityTypeUnknown &&
			track.EntityType != models.EntityTypeUnknown {
			continue
		}

		dist := haversineKm(entity.Latitude, entity.Longitude, track.Latitude, track.Longitude)
		if dist < minDist {
			minDist = dist
			closest = track
		}
	}

	return closest
}

// isDuplicate returns true if the incoming position is essentially identical
// to the existing track's last known position within time and distance thresholds.
func (tc *TrackCorrelator) isDuplicate(existing *trackEntry, incoming *models.EntityPosition) bool {
	timeDelta := incoming.Timestamp.Sub(existing.LastSeen).Seconds()
	if timeDelta > duplicateTimeThresholdSec {
		return false
	}

	dist := haversineKm(incoming.Latitude, incoming.Longitude, existing.Latitude, existing.Longitude)
	return dist < 0.01 // 10 meters
}

// evictionLoop runs periodically to remove stale tracks that have not been
// updated within the expiry duration.
func (tc *TrackCorrelator) evictionLoop() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		tc.evictStale()
	}
}

// evictStale removes all tracks that have not been updated within trackExpiryDuration.
func (tc *TrackCorrelator) evictStale() {
	tc.mu.Lock()
	defer tc.mu.Unlock()

	cutoff := time.Now().UTC().Add(-trackExpiryDuration)
	for id, track := range tc.tracks {
		if track.LastSeen.Before(cutoff) {
			delete(tc.tracks, id)
		}
	}
}

// TrackCount returns the number of currently tracked entities.
func (tc *TrackCorrelator) TrackCount() int {
	tc.mu.RLock()
	defer tc.mu.RUnlock()
	return len(tc.tracks)
}

// haversineKm calculates the great-circle distance in kilometers between
// two WGS-84 coordinate pairs using the Haversine formula.
func haversineKm(lat1, lon1, lat2, lon2 float64) float64 {
	dLat := degreesToRadians(lat2 - lat1)
	dLon := degreesToRadians(lon2 - lon1)

	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(degreesToRadians(lat1))*math.Cos(degreesToRadians(lat2))*
			math.Sin(dLon/2)*math.Sin(dLon/2)

	c := 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))

	return earthRadiusKm * c
}

// degreesToRadians converts degrees to radians.
func degreesToRadians(deg float64) float64 {
	return deg * math.Pi / 180.0
}
