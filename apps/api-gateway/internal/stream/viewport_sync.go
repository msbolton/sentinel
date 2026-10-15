package stream

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
)

// ViewportSink receives the aggregated viewport; ingest-service in production.
type ViewportSink interface {
	Push(ctx context.Context, v IngestViewport) error
}

// IngestHTTPSink pushes viewports to ingest-service's feeds API.
type IngestHTTPSink struct {
	URL    string
	Client *http.Client
}

// NewIngestHTTPSink targets the OpenSky feed of the ingest service at baseURL.
func NewIngestHTTPSink(baseURL string) *IngestHTTPSink {
	return &IngestHTTPSink{
		URL:    strings.TrimRight(baseURL, "/") + "/feeds/opensky/viewport",
		Client: &http.Client{Timeout: 5 * time.Second},
	}
}

// Push sends PUT /feeds/opensky/viewport.
func (s *IngestHTTPSink) Push(ctx context.Context, v IngestViewport) error {
	body, err := json.Marshal(v)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, s.URL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return fmt.Errorf("ingest-service returned %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	return nil
}

// ViewportSyncOptions tunes the pusher. Zero values take the defaults.
type ViewportSyncOptions struct {
	Debounce   time.Duration // trailing debounce after a viewport change (1 s)
	MinSpacing time.Duration // floor between two pushes (2 s)
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
// union to the sink: debounced after changes, only when the change matters
// (see ShouldPush), at most once per MinSpacing, and as a heartbeat while
// anyone is connected so the ingest lease stays fresh.
type ViewportSync struct {
	hub    *Hub
	sink   ViewportSink
	opts   ViewportSyncOptions
	logger *zap.Logger

	notify chan struct{}
	stop   chan struct{}
	done   chan struct{}
	once   sync.Once

	mu         sync.Mutex
	lastPushed *IngestViewport
	lastPushAt time.Time
}

// NewViewportSync wires a pusher to hub; call Start to run it.
func NewViewportSync(hub *Hub, sink ViewportSink, opts ViewportSyncOptions, logger *zap.Logger) *ViewportSync {
	return &ViewportSync{
		hub:    hub,
		sink:   sink,
		opts:   opts.withDefaults(),
		logger: logger,
		notify: make(chan struct{}, 1),
		stop:   make(chan struct{}),
		done:   make(chan struct{}),
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

// Stop ends the loop and tells the sink nobody is watching, so ingest can
// idle before the lease expires.
func (s *ViewportSync) Stop() {
	s.once.Do(func() {
		close(s.stop)
		<-s.done
		s.mu.Lock()
		wasActive := s.lastPushed != nil && s.lastPushed.Active
		s.mu.Unlock()
		if wasActive {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			if err := s.sink.Push(ctx, IngestViewport{Active: false, Boxes: []IngestBox{}}); err != nil {
				s.logger.Warn("viewport sync: final inactive push failed", zap.Error(err))
			}
		}
	})
}

// LastPushed returns a copy of the last successfully pushed viewport.
func (s *ViewportSync) LastPushed() *IngestViewport {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lastPushed == nil {
		return nil
	}
	v := *s.lastPushed
	v.Boxes = append([]IngestBox(nil), s.lastPushed.Boxes...)
	return &v
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

// sync aggregates and pushes when warranted. force (heartbeat, retry) skips
// the change check and the spacing. It returns how long to wait before
// trying again when the spacing floor blocked the push.
func (s *ViewportSync) sync(force bool) (time.Duration, error) {
	next := Aggregate(s.hub.Viewports(), s.opts.PadFrac, s.opts.PadMinDeg)
	next.Clients = s.hub.ClientCount()

	s.mu.Lock()
	prev := s.lastPushed
	since := time.Since(s.lastPushAt)
	s.mu.Unlock()

	if force {
		// An idle state needs no heartbeat once it has been delivered.
		if prev != nil && !prev.Active && !next.Active {
			return 0, nil
		}
	} else {
		if !ShouldPush(prev, next) {
			return 0, nil
		}
		if prev != nil && since < s.opts.MinSpacing {
			return s.opts.MinSpacing - since, nil
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.sink.Push(ctx, next); err != nil {
		return 0, err
	}

	s.mu.Lock()
	s.lastPushed = &next
	s.lastPushAt = time.Now()
	s.mu.Unlock()

	s.logger.Info("viewport sync push",
		zap.Bool("active", next.Active),
		zap.Int("clients", next.Clients),
		zap.Int("boxes", len(next.Boxes)),
		zap.Float64("area_sq_deg", next.AreaSqDeg),
		zap.Bool("heartbeat", force),
	)
	return 0, nil
}
