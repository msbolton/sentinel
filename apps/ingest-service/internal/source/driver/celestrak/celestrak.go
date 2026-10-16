// Package celestrak fetches two-line elements from CelesTrak and emits
// SGP4-propagated satellite positions on an interval. TLEs are fetched
// inside Run so a slow CelesTrak never delays feed start.
package celestrak

import (
	"context"
	_ "embed"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/sentinel/ingest-service/internal/source/decoder/tlesgp4"
	"github.com/sentinel/ingest-service/internal/source/driver"
	"github.com/sentinel/ingest-service/internal/source/httppoll"
)

// ID is the driver id.
const ID = "celestrak"

const defaultBaseURL = "https://celestrak.org/NORAD/elements/gp.php"

//go:embed schema.json
var schema []byte

func init() {
	driver.Register(driver.Definition{
		ID: ID, Name: "CelesTrak satellites", Source: "CELESTRAK",
		Description:  "Satellite positions propagated with SGP4 from CelesTrak two-line elements.",
		Capabilities: []string{driver.CapabilityPoll},
		ConfigSchema: schema,
		New:          New,
	})
}

// Config is the feed configuration.
type Config struct {
	Groups                 []string `json:"groups"`
	TLERefreshHours        int      `json:"tleRefreshHours"`
	PropagationIntervalSec int      `json:"propagationIntervalSec"`
	BaseURL                string   `json:"baseUrl"`
}

// Driver is one CelesTrak feed.
type Driver struct {
	logger  *zap.Logger
	cfg     Config
	baseURL string
	client  *httppoll.Client
	refresh time.Duration
	step    time.Duration

	mu        sync.Mutex
	tles      map[int]tlesgp4.TLE
	lastFetch time.Time
	lastErr   string
	polls     int64
}

// New validates the config and builds the driver.
func New(spec driver.Spec, deps driver.Deps) (driver.Driver, error) {
	var cfg Config
	if err := driver.DecodeConfig(spec, &cfg); err != nil {
		return nil, err
	}
	if len(cfg.Groups) == 0 {
		cfg.Groups = []string{"active"}
	}
	d := &Driver{
		logger: deps.Logger, cfg: cfg, baseURL: defaultBaseURL, client: httppoll.NewClient(60*time.Second, httppoll.Auth{}),
		refresh: time.Duration(max(cfg.TLERefreshHours, 1)) * time.Hour, step: driver.Seconds(cfg.PropagationIntervalSec, 60*time.Second),
		tles: map[int]tlesgp4.TLE{},
	}
	if cfg.BaseURL != "" {
		d.baseURL = cfg.BaseURL
	}
	return d, nil
}

// Run fetches TLEs (retrying with backoff), then propagates on the
// interval and refreshes the elements periodically, until ctx ends.
func (d *Driver) Run(ctx context.Context, sink driver.Sink) error {
	d.logger.Info("celestrak started", zap.Strings("groups", d.cfg.Groups), zap.Duration("refresh", d.refresh), zap.Duration("step", d.step))
	refreshTimer := time.NewTimer(0) // fetch immediately
	defer refreshTimer.Stop()
	propagate := time.NewTicker(d.step)
	defer propagate.Stop()
	backoff := time.Duration(0)
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-refreshTimer.C:
			if err := d.fetchAll(ctx); err != nil {
				if ctx.Err() != nil {
					return nil
				}
				if backoff == 0 {
					backoff = time.Second
				} else if backoff *= 2; backoff > time.Minute {
					backoff = time.Minute
				}
				refreshTimer.Reset(backoff)
				continue
			}
			backoff = 0
			refreshTimer.Reset(d.refresh)
			d.propagate(ctx, sink) // first positions as soon as elements exist
		case <-propagate.C:
			d.propagate(ctx, sink)
		}
	}
}

// Health reports the element cache state.
func (d *Driver) Health() driver.Health {
	d.mu.Lock()
	defer d.mu.Unlock()
	h := driver.Health{Connected: !d.lastFetch.IsZero(), LastError: d.lastErr, Detail: map[string]any{"satellites": len(d.tles), "polls": d.polls}}
	if !d.lastFetch.IsZero() {
		h.Detail["tleFetchedAt"] = d.lastFetch.UTC().Format(time.RFC3339)
	}
	return h
}

func (d *Driver) fetchAll(ctx context.Context) error {
	var firstErr error
	fetched := 0
	for _, group := range d.cfg.Groups {
		group = strings.TrimSpace(group)
		if group == "" {
			continue
		}
		set, err := d.fetch(ctx, group)
		if err != nil {
			d.logger.Warn("celestrak TLE fetch failed", zap.String("group", group), zap.Error(err))
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		d.mu.Lock()
		for _, t := range set {
			d.tles[t.NoradID] = t
		}
		d.mu.Unlock()
		fetched += len(set)
		d.logger.Info("celestrak TLE refresh complete", zap.String("group", group), zap.Int("satellites", len(set)))
	}
	d.mu.Lock()
	if firstErr != nil {
		d.lastErr = firstErr.Error()
	} else {
		d.lastErr = ""
	}
	if fetched > 0 {
		d.lastFetch = time.Now()
	}
	d.mu.Unlock()
	if fetched == 0 && firstErr != nil {
		return firstErr
	}
	return nil
}

func (d *Driver) fetch(ctx context.Context, group string) ([]tlesgp4.TLE, error) {
	u := fmt.Sprintf("%s?GROUP=%s&FORMAT=tle", d.baseURL, url.QueryEscape(group))
	resp, err := d.client.GetOK(ctx, u)
	if err != nil {
		return nil, err
	}
	return tlesgp4.Parse(string(resp.Body))
}

func (d *Driver) propagate(ctx context.Context, sink driver.Sink) {
	d.mu.Lock()
	snapshot := make([]tlesgp4.TLE, 0, len(d.tles))
	for _, t := range d.tles {
		snapshot = append(snapshot, t)
	}
	d.mu.Unlock()
	if len(snapshot) == 0 {
		return
	}
	now := time.Now().UTC()
	sent := 0
	for _, t := range snapshot {
		o := tlesgp4.Propagate(t, now)
		if o == nil {
			sink.Skipped("decayed_orbit")
			continue
		}
		if err := sink.Emit(ctx, o); err != nil {
			return
		}
		sent++
	}
	d.mu.Lock()
	d.polls++
	d.mu.Unlock()
	sink.Succeeded(sent)
	d.logger.Info("celestrak propagation complete", zap.Int("satellites", sent), zap.Int("cached", len(snapshot)))
}
