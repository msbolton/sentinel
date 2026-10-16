// Package httpjson polls any JSON HTTP API and maps its response to
// observations through the json decoder's field map, so a new REST source
// needs configuration rather than code. The URL, headers and POST body
// carry placeholders filled per request from the area of interest, so one
// feed can follow the connected clients' viewport like OpenSky does.
package httpjson

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/sentinel/ingest-service/internal/aoi"
	"github.com/sentinel/ingest-service/internal/source/decoder"
	"github.com/sentinel/ingest-service/internal/source/decoder/jsonmapped"
	"github.com/sentinel/ingest-service/internal/source/driver"
	"github.com/sentinel/ingest-service/internal/source/httppoll"
	"github.com/sentinel/ingest-service/internal/source/transport"
)

// ID is the driver id.
const ID = "httpjson"

const (
	defaultAreaTTL     = 90 * time.Second
	defaultInterval    = 30 * time.Second
	defaultMaxInterval = 60 * time.Second
	defaultMaxRadiusNm = 250
	defaultCreditFloor = 200
)

//go:embed schema.json
var schema []byte

func init() {
	driver.Register(driver.Definition{
		ID: ID, Name: "HTTP JSON", Source: "",
		Description:  "Any JSON HTTP API polled on an interval or for the clients' viewport, mapped to observations through a field map.",
		Capabilities: []string{driver.CapabilityPoll, driver.CapabilityArea},
		SecretFields: []string{"password", "token", "clientSecret"},
		AreaTTL:      defaultAreaTTL,
		ConfigSchema: schema,
		New:          New,
	})
}

// Config is the feed configuration.
type Config struct {
	URL               string            `json:"url"`
	Method            string            `json:"method"`
	Body              json.RawMessage   `json:"body"`
	Headers           map[string]string `json:"headers"`
	IntervalSec       int               `json:"intervalSec"`
	MinPollSpacingSec *int              `json:"minPollSpacingSec"`
	MaxIntervalSec    int               `json:"maxIntervalSec"`
	MaxAreaSqDeg      float64           `json:"maxAreaSqDeg"`
	RequireArea       bool              `json:"requireArea"`
	MaxRadiusNm       int               `json:"maxRadiusNm"`
	TimeoutSec        int               `json:"timeoutSec"`
	Auth              AuthConfig        `json:"auth"`
	Decoder           DecoderConfig     `json:"decoder"`
	RateLimitHeader   string            `json:"rateLimitHeader"`
	CreditFloor       *int              `json:"creditFloor"`
}

// AuthConfig is the non-secret half of the request authentication; the
// password, token and client secret come from the feed's secret refs.
type AuthConfig struct {
	Kind       string `json:"kind"`
	Username   string `json:"username"`
	HeaderName string `json:"headerName"`
	TokenURL   string `json:"tokenUrl"`
	ClientID   string `json:"clientId"`
	Scopes     string `json:"scopes"`
}

// DecoderConfig carries the json decoder options.
type DecoderConfig struct {
	Options json.RawMessage `json:"options"`
}

// Driver is one HTTP JSON feed.
type Driver struct {
	logger  *zap.Logger
	cfg     Config
	host    string
	body    string // POST body template, "" for none
	client  *httppoll.Client
	sched   *httppoll.Scheduler
	decoder decoder.Decoder
	now     func() time.Time

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
	if strings.TrimSpace(cfg.URL) == "" {
		return nil, errors.New("url is required")
	}
	parsed, err := url.Parse(strings.NewReplacer("{", "", "}", "").Replace(cfg.URL))
	if err != nil || parsed.Host == "" || parsed.Scheme == "" {
		return nil, fmt.Errorf("url %q is not an absolute http(s) URL", cfg.URL)
	}
	switch strings.ToUpper(cfg.Method) {
	case "", http.MethodGet:
		cfg.Method = http.MethodGet
	case http.MethodPost:
		cfg.Method = http.MethodPost
	default:
		return nil, fmt.Errorf("unsupported method %q", cfg.Method)
	}
	if cfg.MaxRadiusNm <= 0 {
		cfg.MaxRadiusNm = defaultMaxRadiusNm
	}
	if len(cfg.Decoder.Options) == 0 {
		return nil, errors.New("decoder.options with fields.latitude and fields.longitude is required")
	}
	var fields struct {
		Fields *jsonmapped.Fields `json:"fields"`
	}
	if err := json.Unmarshal(cfg.Decoder.Options, &fields); err != nil {
		return nil, fmt.Errorf("decoder.options: %w", err)
	}
	if fields.Fields == nil || fields.Fields.Latitude == "" || fields.Fields.Longitude == "" {
		return nil, errors.New("decoder.options.fields.latitude and fields.longitude are required")
	}
	dec, err := jsonmapped.New(cfg.Decoder.Options)
	if err != nil {
		return nil, err
	}

	auth := httppoll.Auth{
		Kind: cfg.Auth.Kind, Username: cfg.Auth.Username, HeaderName: cfg.Auth.HeaderName, TokenURL: cfg.Auth.TokenURL, Scopes: cfg.Auth.Scopes,
		Password: spec.Secrets["password"], Token: spec.Secrets["token"], ClientID: cfg.Auth.ClientID, ClientSecret: spec.Secrets["clientSecret"],
	}
	if auth.ClientID == "" {
		auth.ClientID = spec.Secrets["clientId"]
	}
	if err := auth.Validate(); err != nil {
		return nil, fmt.Errorf("auth: %w", err)
	}

	d := &Driver{logger: deps.Logger, cfg: cfg, host: parsed.Host, decoder: dec, now: time.Now,
		client: httppoll.NewClient(driver.Seconds(cfg.TimeoutSec, 30*time.Second), auth)}
	if d.logger == nil {
		d.logger = zap.NewNop()
	}
	if cfg.Method == http.MethodPost && len(bytes.TrimSpace(cfg.Body)) > 0 {
		var s string
		if json.Unmarshal(cfg.Body, &s) == nil {
			d.body = s
		} else {
			var compact bytes.Buffer
			if err := json.Compact(&compact, cfg.Body); err != nil {
				return nil, fmt.Errorf("body: %w", err)
			}
			d.body = compact.String()
		}
	}

	minSpacing := 3 * time.Second
	if cfg.MinPollSpacingSec != nil {
		minSpacing = time.Duration(*cfg.MinPollSpacingSec) * time.Second
	}
	creditFloor := defaultCreditFloor
	if cfg.CreditFloor != nil {
		creditFloor = *cfg.CreditFloor
	}
	if cfg.RateLimitHeader == "" {
		creditFloor = 0
	}
	d.sched = httppoll.NewScheduler(httppoll.Options{
		Interval:     driver.Seconds(cfg.IntervalSec, defaultInterval),
		MaxInterval:  driver.Seconds(cfg.MaxIntervalSec, defaultMaxInterval),
		MinSpacing:   minSpacing,
		MaxArea:      cfg.MaxAreaSqDeg,
		AreaRequired: cfg.RequireArea,
		CreditFloor:  creditFloor,
	}, d.poll)
	return d, nil
}

// Run polls until ctx ends.
func (d *Driver) Run(ctx context.Context, sink driver.Sink) error {
	d.mu.Lock()
	d.sink = sink
	d.mu.Unlock()
	d.logger.Info("httpjson started", zap.String("host", d.host), zap.String("method", d.cfg.Method), zap.Bool("require_area", d.cfg.RequireArea), zap.String("auth", d.client.Auth.Kind))
	return d.sched.Run(ctx, func(idle bool) {
		if idle {
			d.logger.Info("httpjson idle: waiting for an active area", zap.String("host", d.host))
		} else {
			d.logger.Info("httpjson resuming: active area", zap.String("host", d.host))
		}
	})
}

// SetAreaOfInterest applies a pushed area. Without requireArea the area
// still shapes the requests while it is active; without one the driver
// polls the globe.
func (d *Driver) SetAreaOfInterest(a aoi.Area) aoi.Status {
	prev := d.sched.Area()
	st := d.sched.SetArea(a)
	if !prev.Equal(a) {
		d.logger.Info("httpjson area updated", zap.String("host", d.host), zap.Bool("active", a.Active), zap.Int("clients", a.Clients), zap.Int("boxes", len(a.Boxes)),
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
	return driver.Health{Idle: st.Idle, Connected: !st.LastPollAt.IsZero(), LastError: lastErr, Detail: map[string]any{
		"host": d.host, "method": d.cfg.Method, "requireArea": d.cfg.RequireArea,
		"idle": st.Idle, "active": st.Active, "areaSqDeg": st.AreaSqDeg, "effectiveIntervalSec": int(st.EffectiveInterval / time.Second),
		"lowCredits": st.LowCredits, "polls": d.sched.Polls(), "lastError": lastErr,
	}}
}

// poll requests every box of the area (or the globe once) and emits each
// record once per poll.
func (d *Driver) poll(ctx context.Context, area aoi.Area) error {
	d.mu.Lock()
	sink := d.sink
	d.mu.Unlock()
	now := d.now()
	boxes := area.Boxes
	if !area.Active || area.Expired(now) {
		boxes = nil
	}
	vars := []map[string]string{d.placeholders(nil, now)}
	if len(boxes) > 0 {
		vars = vars[:0]
		for i := range boxes {
			vars = append(vars, d.placeholders(&boxes[i], now))
		}
	}
	seen := map[string]struct{}{}
	sent := 0
	for _, v := range vars {
		resp, err := d.request(ctx, v)
		if err != nil {
			var se *httppoll.StatusError
			if errors.As(err, &se) {
				d.quota(se.Header)
			}
			d.fail(err)
			return err
		}
		d.quota(resp.Header)
		out, err := d.decoder.Decode(transport.Frame{Payload: resp.Body, ReceivedAt: now.UTC(), Remote: d.host})
		if err != nil {
			if reason := decoder.SkipReason(err); reason != "" {
				sink.Skipped(reason)
				continue
			}
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
	d.logger.Info("httpjson poll complete", zap.String("host", d.host), zap.Int("observations", sent), zap.Int("requests", len(vars)))
	return nil
}

// request issues one call with the placeholders substituted.
func (d *Driver) request(ctx context.Context, vars map[string]string) (*httppoll.Response, error) {
	var body io.Reader
	if d.cfg.Method == http.MethodPost {
		body = strings.NewReader(substitute(d.body, vars))
	}
	req, err := http.NewRequestWithContext(ctx, d.cfg.Method, substitute(d.cfg.URL, vars), body)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range d.cfg.Headers {
		req.Header.Set(k, substitute(v, vars))
	}
	return d.client.DoOK(req)
}

func (d *Driver) fail(err error) {
	d.mu.Lock()
	d.lastErr = err.Error()
	d.mu.Unlock()
	d.logger.Warn("httpjson poll failed", zap.String("host", d.host), zap.Error(err))
}

// quota feeds the remaining-quota header, when configured, to the scheduler.
func (d *Driver) quota(h http.Header) {
	if d.cfg.RateLimitHeader == "" || h == nil {
		return
	}
	n, err := strconv.Atoi(strings.TrimSpace(h.Get(d.cfg.RateLimitHeader)))
	if err != nil {
		return
	}
	if low, changed := d.sched.ReportQuota(n); low && changed {
		d.logger.Warn("httpjson quota running low, slowing to the maximum interval", zap.String("host", d.host), zap.Int("remaining", n))
	}
}

// placeholders computes the substitution values for one box; nil means
// the globe.
func (d *Driver) placeholders(box *aoi.BBox, now time.Time) map[string]string {
	b := aoi.BBox{South: -90, North: 90, West: -180, East: 180}
	radius := float64(d.cfg.MaxRadiusNm)
	if box != nil {
		b = *box
		radius = math.Ceil(radiusNm(b))
		if radius < 1 {
			radius = 1
		}
		if radius > float64(d.cfg.MaxRadiusNm) {
			radius = float64(d.cfg.MaxRadiusNm)
		}
	}
	lat, lon := b.Center()
	f := func(v float64) string { return strconv.FormatFloat(v, 'f', 4, 64) }
	return map[string]string{
		"lat": f(lat), "lon": f(lon),
		"radius_nm": strconv.Itoa(int(radius)), "radius_km": strconv.Itoa(int(math.Ceil(radius * 1.852))),
		"north": f(b.North), "south": f(b.South), "east": f(b.East), "west": f(b.West),
		"bbox":     strings.Join([]string{f(b.South), f(b.West), f(b.North), f(b.East)}, ","),
		"now_unix": strconv.FormatInt(now.Unix(), 10),
	}
}

// substitute replaces every {name} placeholder present in vars.
func substitute(template string, vars map[string]string) string {
	if !strings.Contains(template, "{") {
		return template
	}
	keys := make([]string, 0, len(vars))
	for k := range vars {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	pairs := make([]string, 0, 2*len(keys))
	for _, k := range keys {
		pairs = append(pairs, "{"+k+"}", vars[k])
	}
	return strings.NewReplacer(pairs...).Replace(template)
}

// radiusNm is the great-circle distance from a box's centre to its corner,
// the radius of the circle that covers it.
func radiusNm(b aoi.BBox) float64 {
	const earthNm = 3440.065
	lat1, lon1 := b.Center()
	lat2, lon2 := b.North, b.East
	dLat := (lat2 - lat1) * math.Pi / 180
	dLon := (lon2 - lon1) * math.Pi / 180
	a := math.Sin(dLat/2)*math.Sin(dLat/2) + math.Cos(lat1*math.Pi/180)*math.Cos(lat2*math.Pi/180)*math.Sin(dLon/2)*math.Sin(dLon/2)
	return earthNm * 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
}
