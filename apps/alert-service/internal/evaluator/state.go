package evaluator

import (
	"sync"
	"time"
)

// Rule evaluation is edge-triggered: an alert fires when an entity crosses
// into the alerting condition, not on every position that satisfies it.
// Position events arrive every poll (about 10 s per aircraft), so a
// level-triggered evaluator produced one GEOFENCE_ENTRY per fix for every
// aircraft inside the zone.
const (
	// refireCooldown bounds repeats of the same alert for the same rule and
	// entity when state is lost (service restart) or an entity flaps on the
	// boundary.
	refireCooldown = 10 * time.Minute
	// stateTTL evicts entities not seen for this long.
	stateTTL = 2 * time.Hour
)

type ruleEntityState struct {
	inside    bool // last known containment / violation
	seenAt    time.Time
	lastFired map[string]time.Time // alert type -> when it last fired
}

// ruleStateTracker remembers, per rule and entity, whether the entity was
// last seen inside the condition, and when each alert type last fired.
type ruleStateTracker struct {
	mu     sync.Mutex
	states map[string]*ruleEntityState
	evals  int
}

func newRuleStateTracker() *ruleStateTracker {
	return &ruleStateTracker{states: map[string]*ruleEntityState{}}
}

// geofenceTransition decides which geofence alert, if any, a change from
// prevInside (nil = first observation) to inside raises under mode.
// A first observation inside the zone counts as an entry; a first
// observation outside raises nothing, because nothing was exited.
func geofenceTransition(prevInside *bool, inside bool, mode string) string {
	if prevInside != nil && *prevInside == inside {
		return ""
	}
	if prevInside == nil && !inside {
		return ""
	}
	return geofenceAlertType(inside, mode)
}

// transition records the observation for rule+entity at now and returns the
// alert to raise: `alertFor(prev, cur)` decides on the edge, and the cooldown
// suppresses repeats of the same alert type.
func (t *ruleStateTracker) transition(ruleID, entityID string, inside bool, now time.Time, alertFor func(prev *bool, cur bool) string) string {
	key := ruleID + "|" + entityID
	t.mu.Lock()
	defer t.mu.Unlock()

	t.evals++
	if t.evals%1000 == 0 {
		t.sweepLocked(now)
	}

	st := t.states[key]
	var prev *bool
	if st != nil {
		p := st.inside
		prev = &p
	} else {
		st = &ruleEntityState{lastFired: map[string]time.Time{}}
		t.states[key] = st
	}
	st.inside = inside
	st.seenAt = now

	alertType := alertFor(prev, inside)
	if alertType == "" {
		return ""
	}
	if last, ok := st.lastFired[alertType]; ok && now.Sub(last) < refireCooldown {
		return ""
	}
	st.lastFired[alertType] = now
	return alertType
}

func (t *ruleStateTracker) sweepLocked(now time.Time) {
	for key, st := range t.states {
		if now.Sub(st.seenAt) > stateTTL {
			delete(t.states, key)
		}
	}
}
