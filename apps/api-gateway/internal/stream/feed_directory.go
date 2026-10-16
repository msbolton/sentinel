package stream

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/sentinel/go-common/middleware"
)

// FeedDirectory answers which ingest feeds currently take an area of
// interest, so the viewport sync pushes to each of them.
type FeedDirectory interface {
	AreaFeeds(ctx context.Context) ([]string, error)
	// Invalidate drops any cached answer; called when a push learns a feed
	// is gone.
	Invalidate()
}

// StaticDirectory is a fixed list, for tests and single-feed setups.
type StaticDirectory []string

// AreaFeeds returns the list.
func (s StaticDirectory) AreaFeeds(context.Context) ([]string, error) { return []string(s), nil }

// Invalidate does nothing.
func (StaticDirectory) Invalidate() {}

// HTTPFeedDirectory asks ingest-service for the enabled feeds whose driver
// has the area capability and caches the answer for TTL.
type HTTPFeedDirectory struct {
	url    string
	token  string
	client *http.Client
	ttl    time.Duration
	logger *zap.Logger

	mu        sync.Mutex
	ids       []string
	fetchedAt time.Time
}

// NewHTTPFeedDirectory targets the ingest service at baseURL.
func NewHTTPFeedDirectory(baseURL, token string, ttl time.Duration, logger *zap.Logger) *HTTPFeedDirectory {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &HTTPFeedDirectory{
		url:    strings.TrimRight(baseURL, "/") + "/feeds?capability=area&enabled=true",
		token:  token,
		client: &http.Client{Timeout: 5 * time.Second},
		ttl:    ttl,
		logger: logger,
	}
}

// AreaFeeds returns the cached ids while fresh, otherwise refreshes. A
// failed refresh returns the last known ids when there are any, so a
// transient ingest outage does not stop heartbeats.
func (d *HTTPFeedDirectory) AreaFeeds(ctx context.Context) ([]string, error) {
	d.mu.Lock()
	if d.ids != nil && time.Since(d.fetchedAt) < d.ttl {
		ids := append([]string(nil), d.ids...)
		d.mu.Unlock()
		return ids, nil
	}
	d.mu.Unlock()

	ids, err := d.fetch(ctx)
	d.mu.Lock()
	defer d.mu.Unlock()
	if err != nil {
		if d.ids != nil {
			d.logger.Warn("feed directory refresh failed; using last known feeds", zap.Error(err))
			return append([]string(nil), d.ids...), nil
		}
		return nil, err
	}
	d.ids = ids
	d.fetchedAt = time.Now()
	return append([]string(nil), ids...), nil
}

// Invalidate forces the next AreaFeeds to refresh.
func (d *HTTPFeedDirectory) Invalidate() {
	d.mu.Lock()
	d.fetchedAt = time.Time{}
	d.mu.Unlock()
}

func (d *HTTPFeedDirectory) fetch(ctx context.Context) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, d.url, nil)
	if err != nil {
		return nil, err
	}
	if d.token != "" {
		req.Header.Set(middleware.HeaderServiceToken, d.token)
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return nil, fmt.Errorf("ingest-service returned %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	var feeds []struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&feeds); err != nil {
		return nil, fmt.Errorf("decoding feed list: %w", err)
	}
	ids := make([]string, 0, len(feeds))
	for _, f := range feeds {
		if f.ID != "" {
			ids = append(ids, f.ID)
		}
	}
	return ids, nil
}
