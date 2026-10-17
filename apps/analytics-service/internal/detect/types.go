// Package detect judges fixes against baselines. The detectors are pure
// functions from (baseline, fix, previous fix, thresholds) to a hit or
// nothing; the engine runs them, keeps the little per-entity state they
// need, turns hits into findings and publishes the ones that matter.
package detect

import (
	"time"

	"github.com/sentinel/analytics-service/internal/model"
)

// Finding kinds.
const (
	KindUnusualPlace    = "UNUSUAL_PLACE"
	KindUnusualTime     = "UNUSUAL_TIME"
	KindUnusualMovement = "UNUSUAL_MOVEMENT"
	KindWentDark        = "WENT_DARK"
	KindReappeared      = "REAPPEARED"
)

// Severities, in rank order.
const (
	SeverityInfo     = "INFO"
	SeverityLow      = "LOW"
	SeverityMedium   = "MEDIUM"
	SeverityHigh     = "HIGH"
	SeverityCritical = "CRITICAL"
)

// SeverityRank orders severities; unknown ranks lowest.
func SeverityRank(s string) int {
	switch s {
	case SeverityCritical:
		return 5
	case SeverityHigh:
		return 4
	case SeverityMedium:
		return 3
	case SeverityLow:
		return 2
	case SeverityInfo:
		return 1
	}
	return 0
}

// Hit is what a detector found.
type Hit struct {
	Kind          string
	Severity      string
	Score         float64
	Summary       string
	Expected      map[string]any
	Actual        map[string]any
	EvidenceStart time.Time
	EvidenceEnd   time.Time
	Lat, Lon      float64
	HasPosition   bool
}

// LastFix is what the engine remembers about an entity between fixes.
type LastFix struct {
	Fix       model.Fix
	Dark      bool      // a WENT_DARK finding is open for this silence
	DarkSince time.Time // when the silence was first found
}
