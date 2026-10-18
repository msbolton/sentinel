package stream

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/sentinel/go-common/middleware"
)

// ErrFeedGone is returned by a sink when the feed no longer exists or no
// longer takes an area; the sync forgets it and refreshes the directory.
var ErrFeedGone = errors.New("feed does not take an area of interest")

// ViewportSink receives the aggregated viewport for one feed; ingest-service
// in production.
type ViewportSink interface {
	Push(ctx context.Context, feedID string, v IngestViewport) error
}

// IngestHTTPSink pushes areas to ingest-service's management API.
type IngestHTTPSink struct {
	BaseURL string
	Token   string
	Client  *http.Client
}

// NewIngestHTTPSink targets the ingest service at baseURL.
func NewIngestHTTPSink(baseURL, token string) *IngestHTTPSink {
	return &IngestHTTPSink{
		BaseURL: strings.TrimRight(baseURL, "/"),
		Token:   token,
		Client:  &http.Client{Timeout: 5 * time.Second},
	}
}

// Push sends PUT /feeds/{id}/area.
func (s *IngestHTTPSink) Push(ctx context.Context, feedID string, v IngestViewport) error {
	body, err := json.Marshal(v)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, s.BaseURL+"/feeds/"+feedID+"/area", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if s.Token != "" {
		req.Header.Set(middleware.HeaderServiceToken, s.Token)
	}
	resp, err := s.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusConflict {
		return fmt.Errorf("%w: %s (%d)", ErrFeedGone, feedID, resp.StatusCode)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return fmt.Errorf("ingest-service returned %d for %s: %s", resp.StatusCode, feedID, strings.TrimSpace(string(msg)))
	}
	return nil
}

// ViewportSyncOptions tunes the pusher. Zero values take the defaults.
type ViewportSyncOptions struct {
	Debounce   time.Duration // trailing debounce after a viewport change (1 s)
	MinSpacing time.Duration // floor between two pushes to one feed (2 s)
	Heartbeat  time.Duration // re-push while clients are connected (30 s)
	PadFrac    float64       // padding as a fraction of each viewport's span (0.10)
	PadMinDeg  float64       // minimum padding in degrees (0.25)
	MaxBackoff time.Duration // retry cap after a failed push (30 s)
}

func (o ViewportSyncOptions) withDefaults() ViewportSyncOptions {
	if o.Debounce <= 0 {
		o.Debounce = time.Second
	}
	if o.MinSpacing <= 0 {
		o.MinSpacing = 2 * time.Second
	}
	if o.Heartbeat <= 0 {
		o.Heartbeat = 30 * time.Second
	}
	if o.PadFrac <= 0 {
		o.PadFrac = defaultPadFrac
	}
	if o.PadMinDeg <= 0 {
		o.PadMinDeg = defaultPadMinDeg
	}
	if o.MaxBackoff <= 0 {
		o.MaxBackoff = 30 * time.Second
	}
	return o
}

// ViewportSync watches the hub's client viewports and pushes their padded
// union to every feed the directory lists: debounced after changes, only
// when the change matters (see ShouldPush), at most once per MinSpacing per
// feed, and as a heartbeat while anyone is connected so the ingest leases
// stay fresh.
type ViewportSync struct {
	hub    *Hub
	sink   ViewportSink
	dir    FeedDirectory
	opts   ViewportSyncOptions
	logger *zap.Logger

	notify chan struct{}
	stop   chan struct{}
	done   chan struct{}
	once   sync.Once

	mu         sync.Mutex
	lastPushed map[string]*IngestViewport
	lastPushAt map[string]time.Time
}

// NewViewportSync wires a pusher to hub; call Start to run it. A nil
// directory means only the "opensky" feed.
func NewViewportSync(hub *Hub, sink ViewportSink, dir FeedDirectory, opts ViewportSyncOptions, logger *zap.Logger) *ViewportSync {
	if dir == nil {
		dir = StaticDirectory{"opensky"}
	}
	if logger == nil {
		logger = zap.NewNop()
	}
	return &ViewportSync{
		hub:        hub,
		sink:       sink,
		dir:        dir,
		opts:       opts.withDefaults(),
		logger:     logger,
		notify:     make(chan struct{}, 1),
		stop:       make(chan struct{}),
		done:       make(chan struct{}),
		lastPushed: map[string]*IngestViewport{},
		lastPushAt: map[string]time.Time{},
	}
}

// Notify signals that client viewports may have changed. It never blocks.
func (s *ViewportSync) Notify() {
	select {
	case s.notify <- struct{}{}:
	default:
	}
}

// Start runs the sync loop in the background.
func (s *ViewportSync) Start() {
	go s.run()
}

// Stop ends the loop and tells every active feed nobody is watching, so
// ingest can idle before the leases expire.
func (s *ViewportSync) Stop() {
	s.once.Do(func() {
		close(s.stop)
		<-s.done
		s.mu.Lock()
		var active []string
		for id, v := range s.lastPushed {
			if v != nil && v.Active {
				active = append(active, id)
			}
		}
		s.mu.Unlock()
		for _, id := range active {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			if err := s.sink.Push(ctx, id, IngestViewport{Active: false, Boxes: []IngestBox{}}); err != nil {
				s.logger.Warn("viewport sync: final inactive push failed", zap.String("feed", id), zap.Error(err))
			}
			cancel()
		}
	})
}

// LastPushed returns a copy of the last viewport pushed to feedID.
func (s *ViewportSync) LastPushed(feedID string) *IngestViewport {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.lastPushed[feedID]
	if !ok || v == nil {
		return nil
	}
	c := *v
	c.Boxes = append([]IngestBox(nil), v.Boxes...)
	return &c
}

func (s *ViewportSync) run() {
	defer close(s.done)

	heartbeat := time.NewTicker(s.opts.Heartbeat)
	defer heartbeat.Stop()

	debounce := time.NewTimer(time.Hour)
	debounce.Stop()
	retry := time.NewTimer(time.Hour)
	retry.Stop()
	backoff := time.Duration(0)

	for {
		select {
		case <-s.stop:
			return
		case <-s.notify:
			debounce.Reset(s.opts.Debounce)
		case <-debounce.C:
			if wait, err := s.sync(false); err != nil {
				backoff = s.scheduleRetry(retry, backoff, err)
			} else if wait > 0 {
				debounce.Reset(wait)
			}
		case <-heartbeat.C:
			if _, err := s.sync(true); err != nil {
				backoff = s.scheduleRetry(retry, backoff, err)
			}
		case <-retry.C:
			if _, err := s.sync(true); err != nil {
				backoff = s.scheduleRetry(retry, backoff, err)
			} else {
				backoff = 0
			}
		}
	}
}

func (s *ViewportSync) scheduleRetry(retry *time.Timer, backoff time.Duration, err error) time.Duration {
	if backoff == 0 {
		backoff = time.Second
	} else if backoff *= 2; backoff > s.opts.MaxBackoff {
		backoff = s.opts.MaxBackoff
	}
	s.logger.Warn("viewport sync push failed", zap.Error(err), zap.Duration("retry_in", backoff))
	retry.Reset(backoff)
	return backoff
}

// sync aggregates once and pushes to each listed feed when warranted. force
// (heartbeat, retry) skips the change check and the spacing. It returns how
// long to wait before trying again when a spacing floor blocked a push, and
// the first push error.
func (s *ViewportSync) sync(force bool) (time.Duration, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ids, err := s.dir.AreaFeeds(ctx)
	if err != nil {
		return 0, fmt.Errorf("listing area feeds: %w", err)
	}

	next := Aggregate(s.hub.DetailViewports(), s.opts.PadFrac, s.opts.PadMinDeg)
	next.Clients = s.hub.ClientCount()

	listed := map[string]bool{}
	for _, id := range ids {
		listed[id] = true
	}
	s.mu.Lock()
	for id := range s.lastPushed {
		if !listed[id] {
			delete(s.lastPushed, id)
			delete(s.lastPushAt, id)
		}
	}
	s.mu.Unlock()

	var (
		wait     time.Duration
		firstErr error
	)
	for _, id := range ids {
		s.mu.Lock()
		prev := s.lastPushed[id]
		since := time.Since(s.lastPushAt[id])
		s.mu.Unlock()

		if force {
			if prev != nil && !prev.Active && !next.Active {
				continue // an idle state needs no heartbeat once delivered
			}
		} else {
			if !ShouldPush(prev, next) {
				continue
			}
			if prev != nil && since < s.opts.MinSpacing {
				if w := s.opts.MinSpacing - since; w > wait {
					wait = w
				}
				continue
			}
		}

		if err := s.sink.Push(ctx, id, next); err != nil {
			if errors.Is(err, ErrFeedGone) {
				s.logger.Info("viewport sync: feed no longer takes an area", zap.String("feed", id))
				s.mu.Lock()
				delete(s.lastPushed, id)
				delete(s.lastPushAt, id)
				s.mu.Unlock()
				s.dir.Invalidate()
				continue
			}
			if firstErr == nil {
				firstErr = err
			}
			continue
		}

		pushed := next
		pushed.Boxes = append([]IngestBox(nil), next.Boxes...)
		s.mu.Lock()
		s.lastPushed[id] = &pushed
		s.lastPushAt[id] = time.Now()
		s.mu.Unlock()

		s.logger.Info("viewport sync push",
			zap.String("feed", id),
			zap.Bool("active", next.Active),
			zap.Int("clients", next.Clients),
			zap.Int("boxes", len(next.Boxes)),
			zap.Float64("area_sq_deg", next.AreaSqDeg),
			zap.Bool("heartbeat", force),
		)
	}
	return wait, firstErr
}
