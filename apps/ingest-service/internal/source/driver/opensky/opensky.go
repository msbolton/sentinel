// Package opensky polls the OpenSky Network REST API at two speeds: the
// area connected map clients can see at the full interval (viewport mode,
// stretched for large areas and when the daily credit quota runs low), and
// the whole globe every backgroundIntervalSec (2 min) whether or not anyone
// is looking, so every aircraft is in the database for search. Aircraft
// the background poll finds outside the viewed area are marked background:
// they update their entity but not track history. A global poll costs 4
// credits, so 2 minutes uses about 2 900 of an account's 4 000 a day; when
// credits run low the background poll slows to every 10 minutes.
package opensky

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/sentinel/ingest-service/internal/aoi"
	"github.com/sentinel/ingest-service/internal/source/decoder/openskystates"
	"github.com/sentinel/ingest-service/internal/source/driver"
	"github.com/sentinel/ingest-service/internal/source/httppoll"
)

// ID is the driver id.
const ID = "opensky"

const (
	defaultBaseURL  = "https://opensky-network.org/api/states/all"
	defaultTokenURL = "https://auth.opensky-network.org/auth/realms/opensky-network/protocol/openid-connect/token"
	defaultAreaTTL  = 90 * time.Second

	defaultBackground    = 2 * time.Minute
	minBackground        = time.Minute
	lowCreditsBackground = 10 * time.Minute
)

//go:embed schema.json
var schema []byte

func init() {
	driver.Register(driver.Definition{
		ID: ID, Name: "OpenSky Network", Source: "OPENSKY",
		Description:  "Live ADS-B state vectors from the OpenSky Network, polled for the connected clients' viewports.",
		Capabilities: []string{driver.CapabilityPoll, driver.CapabilityArea},
		SecretFields: []string{"clientId", "clientSecret"},
		AreaTTL:      defaultAreaTTL,
		ConfigSchema: schema,
		New:          New,
	})
}

// Config is the feed configuration.
type Config struct {
	IntervalSec       int     `json:"intervalSec"`
	ViewportMode      *bool   `json:"viewportMode"`
	BBox              string  `json:"bbox"`
	ViewportTTLSec    int     `json:"viewportTtlSec"`
	MinPollSpacingSec *int    `json:"minPollSpacingSec"`
	MaxAreaSqDeg      float64 `json:"maxAreaSqDeg"`
	MaxIntervalSec    int     `json:"maxIntervalSec"`
	CreditFloor       *int    `json:"creditFloor"`
	// BackgroundIntervalSec is how often the globe is polled for aircraft
	// outside the viewed area; 0 disables. Only in viewport mode.
	BackgroundIntervalSec *int   `json:"backgroundIntervalSec"`
	TokenURL              string `json:"tokenUrl"`
	BaseURL               string `json:"baseUrl"`
}

// Driver is one OpenSky feed.
type Driver struct {
	spec     driver.Spec
	logger   *zap.Logger
	cfg      Config
	baseURL  string
	client   *httppoll.Client
	sched    *httppoll.Scheduler
	viewport bool
	// background is the global poll interval; 0 when disabled.
	background time.Duration
	now        func() time.Time

	mu      sync.Mutex
	sink    driver.Sink
	lastErr string

	bgPolls    int64
	bgAircraft int
	bgNext     time.Time
	bgErr      string
}

// New validates the config and builds the driver.
func New(spec driver.Spec, deps driver.Deps) (driver.Driver, error) {
	var cfg Config
	if err := driver.DecodeConfig(spec, &cfg); err != nil {
		return nil, err
	}
	d := &Driver{spec: spec, logger: deps.Logger, cfg: cfg, baseURL: defaultBaseURL, viewport: cfg.ViewportMode == nil || *cfg.ViewportMode, now: time.Now}
	if d.logger == nil {
		d.logger = zap.NewNop()
	}
	if d.viewport {
		d.background = defaultBackground
		if cfg.BackgroundIntervalSec != nil {
			d.background = time.Duration(*cfg.BackgroundIntervalSec) * time.Second
			if d.background > 0 && d.background < minBackground {
				d.background = minBackground
			}
		}
	}
	if cfg.BaseURL != "" {
		d.baseURL = cfg.BaseURL
	}
	auth := httppoll.Auth{}
	if id, secret := spec.Secrets["clientId"], spec.Secrets["clientSecret"]; id != "" && secret != "" {
		auth = httppoll.Auth{Kind: httppoll.AuthOAuth2, TokenURL: defaultTokenURL, ClientID: id, ClientSecret: secret}
		if cfg.TokenURL != "" {
			auth.TokenURL = cfg.TokenURL
		}
	}
	d.client = httppoll.NewClient(30*time.Second, auth)

	minSpacing := 3 * time.Second
	if cfg.MinPollSpacingSec != nil {
		minSpacing = time.Duration(*cfg.MinPollSpacingSec) * time.Second
	}
	creditFloor := 200
	if cfg.CreditFloor != nil {
		creditFloor = *cfg.CreditFloor
	}
	maxArea := cfg.MaxAreaSqDeg
	if maxArea <= 0 {
		maxArea = 400
	}
	d.sched = httppoll.NewScheduler(httppoll.Options{
		Interval:     driver.Seconds(cfg.IntervalSec, 10*time.Second),
		MaxInterval:  driver.Seconds(cfg.MaxIntervalSec, 60*time.Second),
		MinSpacing:   minSpacing,
		MaxArea:      maxArea,
		AreaRequired: d.viewport,
		CreditFloor:  creditFloor,
	}, d.poll)

	if !d.viewport {
		area := aoi.Area{Active: true}
		box, err := parseBBox(cfg.BBox)
		if err != nil {
			return nil, fmt.Errorf("bbox: %w", err)
		}
		if box != nil {
			area.Boxes = []aoi.BBox{*box}
		}
		d.sched.SetArea(area)
	}
	return d, nil
}

// parseBBox parses "lamin,lomin,lamax,lomax"; empty means global (nil).
func parseBBox(s string) (*aoi.BBox, error) {
	if strings.TrimSpace(s) == "" {
		return nil, nil
	}
	parts := strings.Split(s, ",")
	if len(parts) != 4 {
		return nil, fmt.Errorf("expected 4 comma-separated values, got %d", len(parts))
	}
	vals := make([]float64, 4)
	for i, p := range parts {
		f, err := strconv.ParseFloat(strings.TrimSpace(p), 64)
		if err != nil {
			return nil, fmt.Errorf("value %d: %w", i, err)
		}
		vals[i] = f
	}
	box := aoi.BBox{South: vals[0], West: vals[1], North: vals[2], East: vals[3]}
	if err := box.Validate(); err != nil {
		return nil, err
	}
	return &box, nil
}

// Run polls until ctx ends.
func (d *Driver) Run(ctx context.Context, sink driver.Sink) error {
	d.mu.Lock()
	d.sink = sink
	d.mu.Unlock()
	d.logger.Info("opensky started", zap.Bool("viewport_mode", d.viewport), zap.Bool("authenticated", d.client.Auth.Kind == httppoll.AuthOAuth2), zap.Duration("background", d.background))
	if d.background > 0 {
		done := make(chan struct{})
		defer func() { <-done }()
		bgCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		go func() { defer close(done); d.runBackground(bgCtx, sink) }()
	}
	return d.sched.Run(ctx, func(idle bool) {
		if idle {
			d.logger.Info("opensky idle: waiting for an active viewport")
		} else {
			d.logger.Info("opensky resuming: active viewport")
		}
	})
}

// SetAreaOfInterest applies a pushed area.
func (d *Driver) SetAreaOfInterest(a aoi.Area) aoi.Status {
	if !d.viewport {
		return d.sched.Status() // fixed area from config
	}
	prev := d.sched.Area()
	st := d.sched.SetArea(a)
	if !prev.Equal(a) {
		d.logger.Info("opensky viewport updated", zap.Bool("active", a.Active), zap.Int("clients", a.Clients), zap.Int("boxes", len(a.Boxes)),
			zap.Float64("area_sq_deg", a.SqDeg()), zap.Duration("effective_interval", st.EffectiveInterval))
	}
	return st
}

// AreaStatus reports the area and timing.
func (d *Driver) AreaStatus() aoi.Status { return d.sched.Status() }

// Health merges the scheduler state.
func (d *Driver) Health() driver.Health {
	st := d.sched.Status()
	d.mu.Lock()
	lastErr := d.lastErr
	d.mu.Unlock()
	detail := map[string]any{
		"idle": st.Idle, "active": st.Active, "areaSqDeg": st.AreaSqDeg, "effectiveIntervalSec": int(st.EffectiveInterval / time.Second),
		"lowCredits": st.LowCredits, "polls": d.sched.Polls(),
	}
	d.mu.Lock()
	connected := !st.LastPollAt.IsZero() || d.bgPolls > 0
	if d.background > 0 {
		detail["backgroundIntervalSec"] = int(d.background / time.Second)
		detail["backgroundPolls"] = d.bgPolls
		detail["backgroundAircraft"] = d.bgAircraft
		if !d.bgNext.IsZero() {
			detail["nextBackgroundAt"] = d.bgNext.UTC().Format(time.RFC3339)
		}
		if d.bgErr != "" {
			detail["backgroundError"] = d.bgErr
		}
	}
	d.mu.Unlock()
	return driver.Health{Idle: st.Idle && d.background == 0, Connected: connected, LastError: lastErr, Detail: detail}
}

// runBackground polls the globe every background interval (slower while
// credits are low) until ctx ends.
func (d *Driver) runBackground(ctx context.Context, sink driver.Sink) {
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		d.pollGlobe(ctx, sink)
		wait := d.background
		if d.sched.Status().LowCredits && wait < lowCreditsBackground {
			wait = lowCreditsBackground
		}
		d.mu.Lock()
		d.bgNext = d.now().Add(wait)
		d.mu.Unlock()
		timer.Reset(wait)
	}
}

// pollGlobe fetches every aircraft and emits those outside the live area
// as background fixes; the area poll covers the rest at full rate.
func (d *Driver) pollGlobe(ctx context.Context, sink driver.Sink) {
	area := d.sched.Area()
	live := area.Active && len(area.Boxes) > 0 && !area.Expired(d.now())
	resp, err := d.client.GetOK(ctx, d.baseURL)
	if err != nil {
		var se *httppoll.StatusError
		if errors.As(err, &se) {
			d.quota(se.Header.Get("X-Rate-Limit-Remaining"))
		}
		d.mu.Lock()
		d.bgErr = err.Error()
		d.mu.Unlock()
		d.logger.Warn("opensky background poll failed", zap.Error(err))
		return
	}
	d.quota(resp.Header.Get("X-Rate-Limit-Remaining"))
	out, err := openskystates.Decode(resp.Body)
	if err != nil {
		sink.DeadLetter(resp.Body, "parse_error", err)
		d.mu.Lock()
		d.bgErr = err.Error()
		d.mu.Unlock()
		return
	}
	sent := 0
	for _, o := range out {
		if live && o.HasPosition && inAnyBox(area.Boxes, o.Latitude, o.Longitude) {
			continue // the area poll reports it at full rate
		}
		o.Background = true
		if err := sink.Emit(ctx, o); err != nil {
			return
		}
		sent++
	}
	sink.Succeeded(sent)
	d.mu.Lock()
	d.bgPolls++
	d.bgAircraft = sent
	d.bgErr = ""
	d.mu.Unlock()
	d.logger.Info("opensky background poll complete", zap.Int("aircraft", sent), zap.Int("total", len(out)))
}

func inAnyBox(boxes []aoi.BBox, lat, lon float64) bool {
	for _, b := range boxes {
		if b.Contains(lat, lon) {
			return true
		}
	}
	return false
}

// poll fetches every box of the area (or the globe) and emits each
// aircraft once.
func (d *Driver) poll(ctx context.Context, area aoi.Area) error {
	d.mu.Lock()
	sink := d.sink
	d.mu.Unlock()
	boxes := area.Boxes
	urls := []string{d.url(nil)}
	if len(boxes) > 0 {
		urls = urls[:0]
		for i := range boxes {
			urls = append(urls, d.url(&boxes[i]))
		}
	}
	seen := map[string]struct{}{}
	sent := 0
	for _, u := range urls {
		resp, err := d.client.GetOK(ctx, u)
		if err != nil {
			var se *httppoll.StatusError
			if errors.As(err, &se) {
				d.quota(se.Header.Get("X-Rate-Limit-Remaining"))
			}
			d.fail(err)
			return err
		}
		d.quota(resp.Header.Get("X-Rate-Limit-Remaining"))
		out, err := openskystates.Decode(resp.Body)
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
	d.logger.Info("opensky poll complete", zap.Int("aircraft", sent), zap.Int("boxes", len(boxes)))
	return nil
}

func (d *Driver) fail(err error) {
	d.mu.Lock()
	d.lastErr = err.Error()
	d.mu.Unlock()
	d.logger.Warn("opensky poll failed", zap.Error(err))
}

func (d *Driver) quota(remaining string) {
	if remaining == "" {
		return
	}
	n, err := strconv.Atoi(strings.TrimSpace(remaining))
	if err != nil {
		return
	}
	if low, changed := d.sched.ReportQuota(n); low && changed {
		d.logger.Warn("opensky credits running low, slowing to the maximum interval", zap.Int("remaining", n))
	}
}

// url builds the request for a box; nil or a global box means no bounds.
func (d *Driver) url(box *aoi.BBox) string {
	if box == nil || box.IsGlobal() {
		return d.baseURL
	}
	f := func(v float64) string { return strconv.FormatFloat(v, 'f', 4, 64) }
	return fmt.Sprintf("%s?lamin=%s&lomin=%s&lamax=%s&lomax=%s", d.baseURL, f(box.South), f(box.West), f(box.North), f(box.East))
}
