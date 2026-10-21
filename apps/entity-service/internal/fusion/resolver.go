package fusion

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/sentinel/entity-service/internal/identity"
)

// IdentityStore is what the resolver needs from the database.
type IdentityStore interface {
	// Lookup returns the entity id for each key that exists.
	Lookup(ctx context.Context, keys []Key) (map[Key]string, error)
	// CreateEntities inserts one entity per key, registering the key as
	// its identity. When another writer registered the key first, the
	// existing entity id is returned and nothing is created. created
	// reports which keys produced a new entity.
	CreateEntities(ctx context.Context, specs []identity.NewEntity) (ids map[Key]string, created map[Key]bool, err error)
	// AttachIdentities registers secondary identities for resolved
	// entities, skipping any already bound to a different entity.
	AttachIdentities(ctx context.Context, bindings []identity.Binding) (conflicts int, err error)
}

// Resolver maps canonical keys to entity ids with an in-memory cache in
// front of the identity store.
type Resolver struct {
	store IdentityStore
	ttl   time.Duration
	max   int

	mu    sync.Mutex
	cache map[Key]cacheEntry
}

type cacheEntry struct {
	id      string
	expires time.Time
}

// NewResolver creates a resolver caching up to max keys for ttl.
func NewResolver(store IdentityStore, max int, ttl time.Duration) *Resolver {
	if max <= 0 {
		max = 200_000
	}
	if ttl <= 0 {
		ttl = time.Hour
	}
	return &Resolver{store: store, ttl: ttl, max: max, cache: map[Key]cacheEntry{}}
}

// Resolution is the outcome for a batch of keys.
type Resolution struct {
	IDs     map[Key]string
	Created map[Key]bool
}

// Resolve returns an entity id for every key, creating entities for the
// unknown ones from their first report.
func (r *Resolver) Resolve(ctx context.Context, reports []*Report) (*Resolution, error) {
	res := &Resolution{IDs: map[Key]string{}, Created: map[Key]bool{}}
	first := map[Key]*Report{}
	var misses []Key
	now := time.Now()

	r.mu.Lock()
	for _, rep := range reports {
		if _, done := res.IDs[rep.Key]; done {
			continue
		}
		if e, ok := r.cache[rep.Key]; ok && now.Before(e.expires) {
			res.IDs[rep.Key] = e.id
			continue
		}
		if _, queued := first[rep.Key]; !queued {
			first[rep.Key] = rep
			misses = append(misses, rep.Key)
		}
	}
	r.mu.Unlock()

	if len(misses) > 0 {
		found, err := r.store.Lookup(ctx, misses)
		if err != nil {
			return nil, fmt.Errorf("looking up identities: %w", err)
		}
		var create []identity.NewEntity
		for _, k := range misses {
			if id, ok := found[k]; ok {
				res.IDs[k] = id
				continue
			}
			create = append(create, SpecFor(first[k]))
		}
		if len(create) > 0 {
			ids, created, err := r.store.CreateEntities(ctx, create)
			if err != nil {
				return nil, fmt.Errorf("creating entities: %w", err)
			}
			for k, id := range ids {
				res.IDs[k] = id
				if created[k] {
					res.Created[k] = true
				}
			}
		}
		r.remember(res.IDs, misses)
	}

	// Secondary identities (callsign, IMO, registration) point at the
	// resolved entity; conflicts are counted, never merged.
	var bindings []identity.Binding
	for _, rep := range reports {
		id, ok := res.IDs[rep.Key]
		if !ok {
			continue
		}
		for _, k := range rep.Identities {
			if k == rep.Key || k.Type == "FEED_LOCAL" {
				continue
			}
			bindings = append(bindings, identity.Binding{Key: k, EntityID: id, SeenAt: rep.ReceivedAt})
		}
	}
	if len(bindings) > 0 {
		if _, err := r.store.AttachIdentities(ctx, bindings); err != nil {
			return nil, fmt.Errorf("attaching identities: %w", err)
		}
	}
	return res, nil
}

// Invalidate drops every cached key, so the next report of any key reads
// the store again. Called after deletes: a cached id may now name a deleted
// entity whose identities are free to bind to a new one.
func (r *Resolver) Invalidate() {
	r.mu.Lock()
	r.cache = map[Key]cacheEntry{}
	r.mu.Unlock()
}

// Forget drops a key from the cache (after a delete).
func (r *Resolver) Forget(keys ...Key) {
	r.mu.Lock()
	for _, k := range keys {
		delete(r.cache, k)
	}
	r.mu.Unlock()
}

func (r *Resolver) remember(ids map[Key]string, keys []Key) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.cache) >= r.max {
		r.cache = map[Key]cacheEntry{}
	}
	exp := time.Now().Add(r.ttl)
	for _, k := range keys {
		if id, ok := ids[k]; ok {
			r.cache[k] = cacheEntry{id: id, expires: exp}
		}
	}
}
