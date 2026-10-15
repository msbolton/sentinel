package ingest

import (
	"testing"
	"time"

	"github.com/sentinel/ingest-service/internal/models"
)

func TestIsDuplicate_SuppressesRepeatedFixOnlyBriefly(t *testing.T) {
	tc := &TrackCorrelator{tracks: map[string]*trackEntry{}}
	fixTime := time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC)
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	track := &trackEntry{EntityID: "ICAO-1", Latitude: 41.9, Longitude: -87.9, LastSeen: fixTime, LastForwarded: now}
	same := &models.EntityPosition{EntityID: "ICAO-1", Latitude: 41.9, Longitude: -87.9, Timestamp: fixTime}

	if !tc.isDuplicateAt(track, same, now.Add(2*time.Second)) {
		t.Error("an identical fix 2 s after the last forward should be suppressed")
	}
	if tc.isDuplicateAt(track, same, now.Add(duplicateForwardInterval)) {
		t.Error("an identical fix must be forwarded again once the forward interval has passed")
	}

	moved := &models.EntityPosition{EntityID: "ICAO-1", Latitude: 41.91, Longitude: -87.9, Timestamp: fixTime}
	if tc.isDuplicateAt(track, moved, now.Add(time.Second)) {
		t.Error("a fix that moved more than 10 m is not a duplicate")
	}
	newer := &models.EntityPosition{EntityID: "ICAO-1", Latitude: 41.9, Longitude: -87.9, Timestamp: fixTime.Add(10 * time.Second)}
	if tc.isDuplicateAt(track, newer, now.Add(time.Second)) {
		t.Error("a fix with a newer feed timestamp is not a duplicate")
	}
}

func TestCorrelate_RecordsForwardTime(t *testing.T) {
	tc := &TrackCorrelator{tracks: map[string]*trackEntry{}}
	fixTime := time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC)
	ep := &models.EntityPosition{EntityID: "ICAO-2", EntityType: "aircraft", Latitude: 1, Longitude: 2, Timestamp: fixTime}

	before := time.Now()
	if _, dup := tc.Correlate(ep); dup {
		t.Fatal("first report must not be a duplicate")
	}
	track := tc.tracks["ICAO-2"]
	if track.LastForwarded.Before(before) {
		t.Errorf("LastForwarded not set on registration: %v", track.LastForwarded)
	}
	// The same report straight away is a duplicate.
	if _, dup := tc.Correlate(ep); !dup {
		t.Error("immediate repeat should be suppressed")
	}
	// Pretend the last forward was long ago: the repeat gets through and refreshes LastForwarded.
	track.LastForwarded = time.Now().Add(-time.Minute)
	if _, dup := tc.Correlate(ep); dup {
		t.Error("repeat after the forward interval should pass")
	}
	if time.Since(track.LastForwarded) > time.Second {
		t.Error("LastForwarded should be refreshed on forward")
	}
}
