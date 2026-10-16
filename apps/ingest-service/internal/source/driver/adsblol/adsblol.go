// Package adsblol polls the adsb.lol community API: every military
// aircraft (/v2/mil) or, in area scope, all aircraft within the clients'
// viewport (/v2/point/{lat}/{lon}/{radius}).
package adsblol

import (
	"context"
	_ "embed"
	"fmt"
	"math"
	"strconv"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/sentinel/ingest-service/internal/aoi"
	adsbdecoder "github.com/sentinel/ingest-service/internal/source/decoder/adsblol"
	"github.com/sentinel/ingest-service/internal/source/driver"
	"github.com/sentinel/ingest-service/internal/source/httppoll"
)

// ID is the driver id.
const ID = "adsblol"

const (
	defaultBaseURL = "https://api.adsb.lol"
	defaultAreaTTL = 90 * time.Second
	maxRadiusNm    = 250
)

//go:embed schema.json
var schema []byte

func init() {
	driver.Register(driver.Definition{
		ID: ID, Name: "adsb.lol", Source: "ADSB_LOL",
		Description:  "Aircraft from the adsb.lol community ADS-B network: military worldwide, or everything within the clients' viewport.",
		Capabilities: []string{driver.CapabilityPoll, driver.CapabilityArea},
		AreaTTL:      defaultAreaTTL,
		ConfigSchema: schema,
		New:          New,
	})
}

// Config is the feed configuration.
type Config struct {
	IntervalSec       int    `json:"intervalSec"`
	Scope             string `json:"scope"`
	MaxRadiusNm       int    `json:"maxRadiusNm"`
	MinPollSpacingSec *int   `json:"minPollSpacingSec"`
	BaseURL           string `json:"baseUrl"`
}

// Driver is one adsb.lol feed.
type Driver struct {
	logger  *zap.Logger
	cfg     Config
	baseURL string
	client  *httppoll.Client
	sched   *httppoll.Scheduler

	mu      sync.Mutex
	sink    driver.Sink
	lastErr string
}

// New validates the config and builds the driver.
func New(spec driver.Spec, deps driver.Deps) (driver.Driver, error) {
	var cfg Config
	if err := driver.DecodeConfig(spec, &cfg); err != nil {
		return nil, err
	}
	switch cfg.Scope {
	case "", "mil":
		cfg.Scope = "mil"
	case "area":
	default:
		return nil, fmt.Errorf("unknown scope %q", cfg.Scope)
	}
	if cfg.MaxRadiusNm <= 0 || cfg.MaxRadiusNm > maxRadiusNm {
		cfg.MaxRadiusNm = maxRadiusNm
	}
	d := &Driver{logger: deps.Logger, cfg: cfg, baseURL: defaultBaseURL, client: httppoll.NewClient(30*time.Second, httppoll.Auth{})}
	if cfg.BaseURL != "" {
		d.baseURL = cfg.BaseURL
	}
	minSpacing := 3 * time.Second
	if cfg.MinPollSpacingSec != nil {
		minSpacing = time.Duration(*cfg.MinPollSpacingSec) * time.Second
	}
	d.sched = httppoll.NewScheduler(httppoll.Options{
		Interval:     driver.Seconds(cfg.IntervalSec, 10*time.Second),
		MaxInterval:  driver.Seconds(cfg.IntervalSec, 10*time.Second),
		MinSpacing:   minSpacing,
		AreaRequired: cfg.Scope == "area",
	}, d.poll)
	return d, nil
}

// Run polls until ctx ends.
func (d *Driver) Run(ctx context.Context, sink driver.Sink) error {
	d.mu.Lock()
	d.sink = sink
	d.mu.Unlock()
	d.logger.Info("adsb.lol started", zap.String("scope", d.cfg.Scope))
	return d.sched.Run(ctx, func(idle bool) {
		if idle {
			d.logger.Info("adsb.lol idle: waiting for an active viewport")
		}
	})
}

// SetAreaOfInterest applies a pushed area; in mil scope it is recorded
// but does not change what is polled.
func (d *Driver) SetAreaOfInterest(a aoi.Area) aoi.Status { return d.sched.SetArea(a) }

// AreaStatus reports the area and timing.
func (d *Driver) AreaStatus() aoi.Status { return d.sched.Status() }

// Health merges the scheduler state.
func (d *Driver) Health() driver.Health {
	st := d.sched.Status()
	d.mu.Lock()
	lastErr := d.lastErr
	d.mu.Unlock()
	return driver.Health{Idle: st.Idle, Connected: !st.LastPollAt.IsZero(), LastError: lastErr, Detail: map[string]any{
		"scope": d.cfg.Scope, "idle": st.Idle, "active": st.Active, "areaSqDeg": st.AreaSqDeg, "effectiveIntervalSec": int(st.EffectiveInterval / time.Second), "polls": d.sched.Polls(),
	}}
}

func (d *Driver) poll(ctx context.Context, area aoi.Area) error {
	d.mu.Lock()
	sink := d.sink
	d.mu.Unlock()
	urls := []string{d.baseURL + "/v2/mil"}
	if d.cfg.Scope == "area" {
		urls = urls[:0]
		for _, b := range area.Boxes {
			urls = append(urls, d.pointURL(b))
		}
	}
	seen := map[string]struct{}{}
	sent := 0
	for _, u := range urls {
		resp, err := d.client.GetOK(ctx, u)
		if err != nil {
			d.fail(err)
			return err
		}
		out, err := adsbdecoder.Decode(resp.Body, time.Now().UTC())
		if err != nil {
			d.fail(err)
			sink.DeadLetter(resp.Body, "parse_error", err)
			return fmt.Errorf("parse response: %w", err)
		}
		for _, o := range out {
			if _, dup := seen[o.SourceEntityID]; dup {
				continue
			}
			seen[o.SourceEntityID] = struct{}{}
			if err := sink.Emit(ctx, o); err != nil {
				return err
			}
			sent++
		}
	}
	d.mu.Lock()
	d.lastErr = ""
	d.mu.Unlock()
	sink.Succeeded(sent)
	d.logger.Info("adsb.lol poll complete", zap.Int("aircraft", sent), zap.Int("requests", len(urls)))
	return nil
}

func (d *Driver) fail(err error) {
	d.mu.Lock()
	d.lastErr = err.Error()
	d.mu.Unlock()
	d.logger.Warn("adsb.lol poll failed", zap.Error(err))
}

// pointURL covers a box with a circle: the centre plus the half-diagonal
// in nautical miles, capped at the API maximum.
func (d *Driver) pointURL(b aoi.BBox) string {
	lat, lon := b.Center()
	radius := int(math.Ceil(RadiusNm(b)))
	if radius < 1 {
		radius = 1
	}
	if radius > d.cfg.MaxRadiusNm {
		radius = d.cfg.MaxRadiusNm
	}
	f := func(v float64) string { return strconv.FormatFloat(v, 'f', 4, 64) }
	return fmt.Sprintf("%s/v2/point/%s/%s/%d", d.baseURL, f(lat), f(lon), radius)
}

// RadiusNm is the great-circle distance from a box's centre to its corner.
func RadiusNm(b aoi.BBox) float64 {
	lat, lon := b.Center()
	return haversineNm(lat, lon, b.North, b.East)
}

func haversineNm(lat1, lon1, lat2, lon2 float64) float64 {
	const earthNm = 3440.065
	dLat := (lat2 - lat1) * math.Pi / 180
	dLon := (lon2 - lon1) * math.Pi / 180
	a := math.Sin(dLat/2)*math.Sin(dLat/2) + math.Cos(lat1*math.Pi/180)*math.Cos(lat2*math.Pi/180)*math.Sin(dLon/2)*math.Sin(dLon/2)
	return earthNm * 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
}
