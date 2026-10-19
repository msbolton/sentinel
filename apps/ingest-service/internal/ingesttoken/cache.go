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

const (
	// flushEvery is how often recorded uses are written to lastUsedAt.
	flushEvery = 60 * time.Second
	// finalFlushTimeout bounds the flush Run makes as it stops.
	finalFlushTimeout = 5 * time.Second
	// minBackoff and maxBackoff bound the wait before reconnecting.
	minBackoff = time.Second
	maxBackoff = 30 * time.Second
)

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

	// refreshMu serializes Load and RefreshFeed across both the read and the
	// swap, so a read that went stale while waiting cannot overwrite a newer one.
	refreshMu sync.Mutex

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
	c.refreshMu.Lock()
	defer c.refreshMu.Unlock()
	all, err := c.loader.All(ctx)
	if err != nil {
		return err
	}
	c.replace(func(Stored) bool { return true }, all)
	return nil
}

// RefreshFeed replaces one feed's tokens with what the loader lists now.
func (c *Cache) RefreshFeed(ctx context.Context, feedID string) error {
	c.refreshMu.Lock()
	defer c.refreshMu.Unlock()
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
	now := c.now()
	var lost []Token
	c.mu.Lock()
	for key, old := range c.byHash {
		if !in(old) {
			continue
		}
		cur, still := next[key]
		if old.Usable(now) && (!still || cur.RevokedAt != nil) {
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
// Usable). fn runs on the refreshing goroutine and must not block. A call
// already under way may still reach fn once after cancel returns.
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
	if len(used) == 0 {
		return nil
	}
	err := c.loader.MarkUsed(ctx, used)
	if err != nil {
		// Keep the uses for the next flush, the later time winning per token.
		c.usedMu.Lock()
		for id, at := range used {
			if cur, ok := c.used[id]; !ok || at.After(cur) {
				c.used[id] = at
			}
		}
		c.usedMu.Unlock()
	}
	return err
}

// Run follows token and feed notifications until ctx ends, reloading every
// token after each (re)connect and flushing recorded uses every minute. It
// returns once the final flush is done.
func (c *Cache) Run(ctx context.Context) {
	flushed := make(chan struct{})
	go func() {
		defer close(flushed)
		t := time.NewTicker(flushEvery)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				fctx, cancel := context.WithTimeout(context.Background(), finalFlushTimeout)
				defer cancel()
				if err := c.FlushUsed(fctx); err != nil {
					c.logger.Warn("recording ingest token uses at shutdown failed", zap.Error(err))
				}
				return
			case <-t.C:
				if err := c.FlushUsed(ctx); err != nil {
					c.logger.Warn("recording ingest token uses failed", zap.Error(err))
				}
			}
		}
	}()
	defer func() { <-flushed }()

	backoff := minBackoff
	for ctx.Err() == nil {
		err := c.listen(ctx, func() { backoff = minBackoff })
		if err == nil || ctx.Err() != nil {
			continue
		}
		c.logger.Warn("ingest token watch disconnected; reconnecting", zap.Error(err), zap.Duration("in", backoff))
		select {
		case <-ctx.Done():
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, maxBackoff)
	}
}

// listen holds one connection: LISTEN, reload every token, then refresh on
// each notice. ready is called once the reload has succeeded.
func (c *Cache) listen(ctx context.Context, ready func()) error {
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
	ready()
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
				// Which token changed is unknown, so reload them all rather
				// than risk a revoked one staying usable.
				c.logger.Warn("unreadable ingest token notice; reloading every token", zap.String("payload", n.Payload))
				if err := c.Load(ctx); err != nil {
					return err
				}
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
