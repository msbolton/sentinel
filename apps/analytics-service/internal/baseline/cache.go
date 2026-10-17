package baseline

import (
	"context"
	"sync"
	"time"
)

// Getter loads a baseline; nil, nil means the entity has none yet.
type Getter interface {
	Get(ctx context.Context, entityID string) (*Baseline, error)
}

// Cache keeps baselines in memory for a while so the detectors do not hit
// Postgres on every fix. A missing baseline is cached too (as nil), so an
// entity without one costs one query per TTL, not one per fix.
type Cache struct {
	source Getter
	ttl    time.Duration
	now    func() time.Time

	mu      sync.RWMutex
	entries map[string]cacheEntry
}

type cacheEntry struct {
	b  *Baseline
	at time.Time
}

// NewCache wraps a Getter with a TTL.
func NewCache(source Getter, ttl time.Duration) *Cache {
	return &Cache{source: source, ttl: ttl, now: time.Now, entries: map[string]cacheEntry{}}
}

// Get returns the cached baseline, loading it when absent or expired.
func (c *Cache) Get(ctx context.Context, entityID string) (*Baseline, error) {
	c.mu.RLock()
	e, ok := c.entries[entityID]
	c.mu.RUnlock()
	if ok && c.now().Sub(e.at) < c.ttl {
		return e.b, nil
	}
	b, err := c.source.Get(ctx, entityID)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.entries[entityID] = cacheEntry{b: b, at: c.now()}
	c.mu.Unlock()
	return b, nil
}

// Put stores a freshly computed baseline.
func (c *Cache) Put(b *Baseline) {
	if b == nil {
		return
	}
	c.mu.Lock()
	c.entries[b.EntityID] = cacheEntry{b: b, at: c.now()}
	c.mu.Unlock()
}

// Invalidate drops an entity's entry.
func (c *Cache) Invalidate(entityID string) {
	c.mu.Lock()
	delete(c.entries, entityID)
	c.mu.Unlock()
}

// Len is the number of entries, present or negative.
func (c *Cache) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.entries)
}
