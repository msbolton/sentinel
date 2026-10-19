package emplacement

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

// Loader is what the cache reads emplacements through (the Store).
type Loader interface {
	List(ctx context.Context, f Filter) ([]Emplacement, error)
	Get(ctx context.Context, id string) (Emplacement, error)
}

// Cache is every live emplacement in memory, kept current by the
// sentinel_emplacements notifications. It is safe for concurrent use.
type Cache struct {
	loader Loader
	pool   *pgxpool.Pool
	logger *zap.Logger

	mu   sync.RWMutex
	byID map[string]Emplacement

	subMu   sync.Mutex
	subs    map[int]func(id string)
	nextSub int

	readyOnce sync.Once
	ready     chan struct{}
}

// NewCache returns an empty cache. pool is the database Run listens on;
// logger may be nil.
func NewCache(loader Loader, pool *pgxpool.Pool, logger *zap.Logger) *Cache {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Cache{loader: loader, pool: pool, logger: logger, byID: map[string]Emplacement{}, subs: map[int]func(string){}, ready: make(chan struct{})}
}

// Ready returns a channel closed, once, after the first successful Load,
// whether called directly or by Run. Until then the cache knows no
// emplacements, so callers that serve senders answer Unavailable while it is
// open.
func (c *Cache) Ready() <-chan struct{} { return c.ready }

// Load replaces the contents with what the loader lists now, and tells the
// subscribers every id that was added, changed (by version) or removed.
func (c *Cache) Load(ctx context.Context) error {
	list, err := c.loader.List(ctx, Filter{})
	if err != nil {
		return err
	}
	next := make(map[string]Emplacement, len(list))
	for _, e := range list {
		next[e.ID] = e
	}
	var changed []string
	c.mu.Lock()
	for id, e := range next {
		if old, ok := c.byID[id]; !ok || old.Version != e.Version {
			changed = append(changed, id)
		}
	}
	for id := range c.byID {
		if _, ok := next[id]; !ok {
			changed = append(changed, id)
		}
	}
	c.byID = next
	c.mu.Unlock()
	c.readyOnce.Do(func() { close(c.ready) })
	c.notify(changed)
	return nil
}

// Refresh reads one emplacement again after a notification about it.
func (c *Cache) Refresh(ctx context.Context, id string) error {
	e, err := c.loader.Get(ctx, id)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	c.mu.Lock()
	if err != nil {
		delete(c.byID, id)
	} else {
		c.byID[id] = e
	}
	c.mu.Unlock()
	c.notify([]string{id})
	return nil
}

// Get returns the emplacement with id.
func (c *Cache) Get(id string) (Emplacement, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	e, ok := c.byID[id]
	return e, ok
}

// All returns every emplacement, by name.
func (c *Cache) All() []Emplacement {
	c.mu.RLock()
	out := make([]Emplacement, 0, len(c.byID))
	for _, e := range c.byID {
		out = append(out, e)
	}
	c.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool {
		a, b := strings.ToLower(out[i].Name), strings.ToLower(out[j].Name)
		if a != b {
			return a < b
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// ByFeed returns the emplacements bound to feedID, by name.
func (c *Cache) ByFeed(feedID string) []Emplacement {
	var out []Emplacement
	for _, e := range c.All() {
		if e.FeedID != nil && *e.FeedID == feedID {
			out = append(out, e)
		}
	}
	return out
}

// OnChange registers fn, called with the id of every emplacement that was
// added, changed or removed. fn runs on the caller of Load or Refresh and
// must not block. The returned func unregisters it.
func (c *Cache) OnChange(fn func(id string)) (cancel func()) {
	c.subMu.Lock()
	id := c.nextSub
	c.nextSub++
	c.subs[id] = fn
	c.subMu.Unlock()
	return func() {
		c.subMu.Lock()
		delete(c.subs, id)
		c.subMu.Unlock()
	}
}

func (c *Cache) notify(ids []string) {
	if len(ids) == 0 {
		return
	}
	c.subMu.Lock()
	fns := make([]func(string), 0, len(c.subs))
	for _, fn := range c.subs {
		fns = append(fns, fn)
	}
	c.subMu.Unlock()
	for _, id := range ids {
		for _, fn := range fns {
			fn(id)
		}
	}
}

// Run follows notifications until ctx ends. Every connection reloads the
// whole table after it starts listening, so what changed while it was down
// is not lost. A notification whose refresh fails drops the connection, so
// the reload on the next one repairs the entry instead of leaving it stale.
func (c *Cache) Run(ctx context.Context) {
	for ctx.Err() == nil {
		if err := c.listen(ctx); err != nil && ctx.Err() == nil {
			c.logger.Warn("emplacement watch disconnected; reconnecting", zap.Error(err))
			select {
			case <-ctx.Done():
			case <-time.After(time.Second):
			}
		}
	}
}

func (c *Cache) listen(ctx context.Context) error {
	pooled, err := c.pool.Acquire(ctx)
	if err != nil {
		return err
	}
	conn := pooled.Hijack() // a LISTENing connection must not go back to the pool
	defer conn.Close(context.Background())
	if _, err := conn.Exec(ctx, "LISTEN "+Channel); err != nil {
		return err
	}
	if err := c.Load(ctx); err != nil {
		return err
	}
	for {
		n, err := conn.WaitForNotification(ctx)
		if err != nil {
			return err
		}
		if err := c.handle(ctx, n.Payload); err != nil {
			c.logger.Warn("emplacement refresh failed; reconnecting to reload", zap.Error(err))
			return err
		}
	}
}

// handle applies one notification payload. A payload without an id is
// ignored. A refresh error is returned, so the caller can reconnect.
func (c *Cache) handle(ctx context.Context, payload string) error {
	var msg struct {
		ID string `json:"id"`
	}
	if json.Unmarshal([]byte(payload), &msg) != nil || msg.ID == "" {
		return nil
	}
	if err := c.Refresh(ctx, msg.ID); err != nil {
		return fmt.Errorf("refreshing %s: %w", msg.ID, err)
	}
	return nil
}
