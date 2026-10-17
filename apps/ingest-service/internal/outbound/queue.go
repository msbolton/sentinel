// Package outbound runs the sending half of a connection: a Kafka consumer
// group of its own, the sharing policy, a per-entity coalescing queue and
// a rate-limited delivery loop into the driver's Consumer.
package outbound

import (
	"sync"
	"time"

	"github.com/sentinel/ingest-service/internal/source/driver"
)

// Queue holds at most one pending update per entity in arrival order. A
// newer upsert replaces a pending one (the remote only wants the latest
// snapshot); a removal replaces any pending upsert and is never dropped.
// Beyond Max pending entries the oldest pending upsert is discarded.
type Queue struct {
	mu      sync.Mutex
	max     int
	pending map[string]*driver.EntityUpdate
	order   []string // entity ids in arrival order; may hold ids already delivered
	// lastSent is when each entity was last delivered, for per-entity spacing.
	lastSent map[string]time.Time
	minGap   time.Duration

	Coalesced    int64
	DroppedOldest int64
}

// NewQueue sizes the queue; minGap is the minimum spacing between two
// upserts of the same entity (created and removal updates bypass it).
func NewQueue(max int, minGap time.Duration) *Queue {
	if max <= 0 {
		max = 10000
	}
	return &Queue{max: max, pending: map[string]*driver.EntityUpdate{}, lastSent: map[string]time.Time{}, minGap: minGap}
}

// Push queues an update, coalescing with anything pending for the entity.
func (q *Queue) Push(u driver.EntityUpdate) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if prev, ok := q.pending[u.EntityID]; ok {
		if prev.Kind == driver.RemoveEntity && u.Kind == driver.UpsertEntity {
			// A removal already pending wins over a late snapshot.
			q.Coalesced++
			return
		}
		q.Coalesced++
		q.pending[u.EntityID] = &u
		return
	}
	q.pending[u.EntityID] = &u
	q.order = append(q.order, u.EntityID)
	if len(q.pending) > q.max {
		q.dropOldestUpsert()
	}
}

// dropOldestUpsert discards the oldest pending upsert; removals stay.
func (q *Queue) dropOldestUpsert() {
	for i, id := range q.order {
		u, ok := q.pending[id]
		if !ok {
			continue
		}
		if u.Kind == driver.UpsertEntity {
			delete(q.pending, id)
			q.order = append(q.order[:i], q.order[i+1:]...)
			q.DroppedOldest++
			return
		}
	}
}

// Pop returns the next deliverable update, honouring per-entity spacing:
// an upsert of an entity sent less than minGap ago is skipped for now. The
// second result is false when nothing is deliverable.
func (q *Queue) Pop(now time.Time) (driver.EntityUpdate, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	kept := q.order[:0]
	var found *driver.EntityUpdate
	for i, id := range q.order {
		u, ok := q.pending[id]
		if !ok {
			continue // delivered earlier; compact
		}
		if found == nil && q.deliverable(u, now) {
			found = u
			delete(q.pending, id)
			continue
		}
		kept = append(kept, id)
		if found != nil {
			kept = append(kept, q.order[i+1:]...)
			break
		}
	}
	// Clear the tail so dropped ids do not pin memory.
	for i := len(kept); i < len(q.order); i++ {
		q.order[i] = ""
	}
	q.order = kept
	if found == nil {
		return driver.EntityUpdate{}, false
	}
	q.lastSent[found.EntityID] = now
	if found.Kind == driver.RemoveEntity {
		delete(q.lastSent, found.EntityID)
	}
	return *found, true
}

func (q *Queue) deliverable(u *driver.EntityUpdate, now time.Time) bool {
	if u.Kind == driver.RemoveEntity || q.minGap <= 0 {
		return true
	}
	last, ok := q.lastSent[u.EntityID]
	return !ok || now.Sub(last) >= q.minGap
}

// Len is the number of pending updates.
func (q *Queue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.pending)
}

// NextDue is how long until a pending upsert becomes deliverable, or zero
// when one is deliverable now or nothing is pending.
func (q *Queue) NextDue(now time.Time) time.Duration {
	q.mu.Lock()
	defer q.mu.Unlock()
	var best time.Duration
	for _, u := range q.pending {
		if q.deliverable(u, now) {
			return 0
		}
		wait := q.minGap - now.Sub(q.lastSent[u.EntityID])
		if best == 0 || wait < best {
			best = wait
		}
	}
	return best
}
