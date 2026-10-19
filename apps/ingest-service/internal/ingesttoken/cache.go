package ingesttoken

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/sentinel/go-common/secrettoken"
)

// feedsChannel is registry.ChannelFeeds, the channel every feed write
// announces itself on with the feed id as payload. Deleting a feed deletes
// its tokens by cascade without a token notice, so the cache re-reads a
// feed's tokens on every feed notice. (Spelled here to avoid importing the
// registry; a test keeps the two equal.)
const feedsChannel = "sentinel_feeds"

// flushEvery is how often recorded uses are written to lastUsedAt.
const flushEvery = 60 * time.Second

// Loader is what the cache reads tokens through (the Store).
type Loader interface {
	All(ctx context.Context) ([]Stored, error)
	ForFeed(ctx context.Context, feedID string) ([]Stored, error)
	MarkUsed(ctx context.Context, used map[string]time.Time) error
}

// Cache holds every token by hash, kept current by notifications, and tells
// subscribers when a token stops being usable (revoked, or gone with its
// feed) so they can close what it opened. Safe for concurrent use.
type Cache struct {
	loader Loader
	pool   *pgxpool.Pool
	logger *zap.Logger
	now    func() time.Time

	mu     sync.RWMutex
	byHash map[string]Stored // key: string(hash)

	usedMu sync.Mutex
	used   map[string]time.Time

	subMu   sync.Mutex
	subs    map[int]func(Token)
	nextSub int
}

// NewCache returns an empty cache; pool is the database Run listens on and
// logger may be nil.
func NewCache(loader Loader, pool *pgxpool.Pool, logger *zap.Logger) *Cache {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Cache{loader: loader, pool: pool, logger: logger, now: time.Now,
		byHash: map[string]Stored{}, used: map[string]time.Time{}, subs: map[int]func(Token){}}
}

// Load replaces every token with what the loader lists now.
func (c *Cache) Load(ctx context.Context) error {
	all, err := c.loader.All(ctx)
	if err != nil {
		return err
	}
	c.replace(func(Stored) bool { return true }, all)
	return nil
}

// RefreshFeed replaces one feed's tokens with what the loader lists now.
func (c *Cache) RefreshFeed(ctx context.Context, feedID string) error {
	rows, err := c.loader.ForFeed(ctx, feedID)
	if err != nil {
		return err
	}
	c.replace(func(s Stored) bool { return s.FeedID == feedID }, rows)
	return nil
}

// replace swaps the cached tokens selected by in for rows, and tells the
// subscribers of every token that was usable and is now revoked or gone.
func (c *Cache) replace(in func(Stored) bool, rows []Stored) {
	next := make(map[string]Stored, len(rows))
	for _, r := range rows {
		next[string(r.Hash)] = r
	}
	var lost []Token
	c.mu.Lock()
	for key, old := range c.byHash {
		if !in(old) {
			continue
		}
		cur, still := next[key]
		if old.RevokedAt == nil && (!still || cur.RevokedAt != nil) {
			lost = append(lost, old.Token)
		}
		delete(c.byHash, key)
	}
	for key, r := range next {
		c.byHash[key] = r
	}
	c.mu.Unlock()
	c.notify(lost)
}

// Authenticate returns the token a presented secret is, when it is usable,
// and records the use.
func (c *Cache) Authenticate(secret string) (Token, bool) {
	if !secrettoken.Valid(secret, Prefix) {
		return Token{}, false
	}
	key := string(secrettoken.Hash(secret))
	c.mu.RLock()
	s, ok := c.byHash[key]
	c.mu.RUnlock()
	now := c.now()
	if !ok || !s.Usable(now) {
		return Token{}, false
	}
	c.usedMu.Lock()
	c.used[s.ID] = now
	c.usedMu.Unlock()
	return s.Token, true
}

// OnRevoke registers fn, called once for each token that stops being usable
// by revocation or deletion (not by expiry, which callers check with
// Usable). fn runs on the refreshing goroutine and must not block.
func (c *Cache) OnRevoke(fn func(Token)) (cancel func()) {
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

func (c *Cache) notify(lost []Token) {
	if len(lost) == 0 {
		return
	}
	c.subMu.Lock()
	fns := make([]func(Token), 0, len(c.subs))
	for _, fn := range c.subs {
		fns = append(fns, fn)
	}
	c.subMu.Unlock()
	for _, t := range lost {
		for _, fn := range fns {
			fn(t)
		}
	}
}

// FlushUsed writes the uses recorded since the last flush.
func (c *Cache) FlushUsed(ctx context.Context) error {
	c.usedMu.Lock()
	used := c.used
	c.used = map[string]time.Time{}
	c.usedMu.Unlock()
	return c.loader.MarkUsed(ctx, used)
}

// Run follows token and feed notifications until ctx ends, reloading every
// token after each (re)connect and flushing recorded uses every minute.
func (c *Cache) Run(ctx context.Context) {
	go func() {
		t := time.NewTicker(flushEvery)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				_ = c.FlushUsed(context.Background())
				return
			case <-t.C:
				if err := c.FlushUsed(ctx); err != nil {
					c.logger.Warn("recording ingest token uses failed", zap.Error(err))
				}
			}
		}
	}()
	for ctx.Err() == nil {
		if err := c.listen(ctx); err != nil && ctx.Err() == nil {
			c.logger.Warn("ingest token watch disconnected; reconnecting", zap.Error(err))
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
	conn := pooled.Hijack()
	defer conn.Close(context.Background())
	for _, ch := range []string{Channel, feedsChannel} {
		if _, err := conn.Exec(ctx, "LISTEN "+ch); err != nil {
			return err
		}
	}
	if err := c.Load(ctx); err != nil {
		return err
	}
	for {
		n, err := conn.WaitForNotification(ctx)
		if err != nil {
			return err
		}
		feed := n.Payload
		if n.Channel == Channel {
			var m struct {
				FeedID string `json:"feedId"`
			}
			if json.Unmarshal([]byte(n.Payload), &m) != nil || m.FeedID == "" {
				continue
			}
			feed = m.FeedID
		}
		if feed == "" {
			continue
		}
		if err := c.RefreshFeed(ctx, feed); err != nil {
			// A failed refresh could leave a revoked token usable: reconnect,
			// and the reload after LISTEN repairs every token.
			c.logger.Warn("refreshing a feed's ingest tokens failed; reconnecting to reload", zap.String("feed", feed), zap.Error(err))
			return err
		}
	}
}
