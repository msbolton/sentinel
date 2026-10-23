// Package celestrak fetches orbital elements from CelesTrak as OMM JSON
// and emits SGP4-propagated satellite positions on an interval. Elements
// are fetched inside Run so a slow CelesTrak never delays feed start.
//
// CelesTrak has no API keys; it has a usage policy instead: GP data
// updates every two hours, the large groups allow one download per update
// (a repeat gets 403), and repeated error responses lead to a block. The
// driver asks for each selection once per refresh with a conditional
// request, never refreshes more often than every two hours, and after a
// 403 waits for the next refresh.
package celestrak

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/sentinel/ingest-service/internal/aoi"
	"github.com/sentinel/ingest-service/internal/source/decoder/tlesgp4"
	"github.com/sentinel/ingest-service/internal/source/driver"
	"github.com/sentinel/ingest-service/internal/source/httppoll"
)

// ID is the driver id.
const ID = "celestrak"

const (
	defaultBaseURL         = "https://celestrak.org/NORAD/elements/gp.php"
	defaultSupplementalURL = "https://celestrak.org/NORAD/elements/supplemental/sup-gp.php"
	minRefresh             = 2 * time.Hour
	maxJitter              = 5 * time.Minute
	maxBackoff             = time.Minute
	maxRetryAfter          = 6 * time.Hour
	// deadLetterSample is how much of a response that cannot be parsed is
	// dead-lettered: the start of it says what came back, and the whole of it
	// can be megabytes.
	deadLetterSample = 64 << 10
)

//go:embed schema.json
var schema []byte

func init() {
	driver.Register(driver.Definition{
		ID: ID, Name: "CelesTrak satellites", Source: "CELESTRAK",
		Description:  "Satellite positions propagated with SGP4 from CelesTrak orbital elements (OMM).",
		Capabilities: []string{driver.CapabilityPoll, driver.CapabilityArea},
		AreaTTL:      90 * time.Second,
		ConfigSchema: schema,
		New:          New,
	})
}

// Config is the feed configuration.
type Config struct {
	Groups         []string `json:"groups"`
	CatalogNumbers []int    `json:"catalogNumbers"`
	Designators    []string `json:"designators"`
	Names          []string `json:"names"`
	Special        []string `json:"special"`
	Supplemental   []string `json:"supplemental"`

	RefreshHours           int    `json:"refreshHours"`
	TLERefreshHours        int    `json:"tleRefreshHours"` // former name of refreshHours
	PropagationIntervalSec int    `json:"propagationIntervalSec"`
	BaseURL                string `json:"baseUrl"`
	SupplementalURL        string `json:"supplementalUrl"`
}

type cached struct {
	tle    tlesgp4.TLE
	pinned bool
}

type condState struct {
	etag         string
	lastModified string
}

// Driver is one CelesTrak feed.
type Driver struct {
	logger  *zap.Logger
	cfg     Config
	sels    []selection
	baseURL string
	supURL  string
	client  *httppoll.Client
	refresh time.Duration
	step    time.Duration
	jitter  func() time.Duration
	now     func() time.Time

	wake chan struct{} // an area change asks for a tick now

	mu         sync.Mutex
	area       aoi.Area
	objects    map[int]cached   // by real catalog number
	members    map[string][]int // selection key -> catalog numbers from its last good response
	cond       map[string]condState
	selErr     map[string]string
	lastErr    string
	lastStatus int
	lastFetch  time.Time
	nextFetch  time.Time
	polls      int64
	emitted    int
}

// New validates the config and builds the driver.
func New(spec driver.Spec, deps driver.Deps) (driver.Driver, error) {
	var cfg Config
	if err := driver.DecodeConfig(spec, &cfg); err != nil {
		return nil, err
	}
	hours := cfg.RefreshHours
	if hours == 0 {
		hours = cfg.TLERefreshHours
	}
	refresh := time.Duration(hours) * time.Hour
	if refresh < minRefresh {
		refresh = minRefresh
	}
	d := &Driver{
		logger:  deps.Logger,
		cfg:     cfg,
		sels:    selectionsOf(cfg),
		baseURL: defaultBaseURL,
		supURL:  defaultSupplementalURL,
		client:  httppoll.NewClient(60*time.Second, httppoll.Auth{}),
		refresh: refresh,
		step:    driver.Seconds(cfg.PropagationIntervalSec, 60*time.Second),
		jitter:  func() time.Duration { return time.Duration(rand.Int64N(int64(maxJitter))) },
		now:     time.Now,
		wake:    make(chan struct{}, 1),
		objects: map[int]cached{},
		members: map[string][]int{},
		cond:    map[string]condState{},
		selErr:  map[string]string{},
	}
	if cfg.BaseURL != "" {
		d.baseURL = cfg.BaseURL
	}
	if cfg.SupplementalURL != "" {
		d.supURL = cfg.SupplementalURL
	}
	return d, nil
}

// Run fetches elements (retrying with backoff), then propagates on the
// interval and refreshes the elements periodically, until ctx ends.
func (d *Driver) Run(ctx context.Context, sink driver.Sink) error {
	keys := make([]string, 0, len(d.sels))
	for _, s := range d.sels {
		keys = append(keys, s.Key())
	}
	d.logger.Info("celestrak started", zap.Strings("selections", keys), zap.Duration("refresh", d.refresh), zap.Duration("step", d.step))
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
			ok, retryAfter := d.fetchAll(ctx, sink)
			if ctx.Err() != nil {
				return nil
			}
			var wait time.Duration
			switch {
			case ok:
				backoff = 0
				wait = d.refresh + d.jitter()
			case retryAfter > 0:
				wait = retryAfter
			default:
				if backoff == 0 {
					backoff = time.Second
				} else if backoff *= 2; backoff > maxBackoff {
					backoff = maxBackoff
				}
				wait = backoff
			}
			d.mu.Lock()
			d.nextFetch = d.now().Add(wait)
			d.mu.Unlock()
			refreshTimer.Reset(wait)
			if ok {
				d.propagate(ctx, sink) // first positions as soon as elements exist
			}
		case <-propagate.C:
			d.propagate(ctx, sink)
		case <-d.wake:
			d.propagate(ctx, sink)
		}
	}
}

// SetAreaOfInterest applies the viewport the gateway pushed. A change
// asks for a propagation now, so a client that pans sees satellites
// without waiting for the next tick.
func (d *Driver) SetAreaOfInterest(a aoi.Area) aoi.Status {
	d.mu.Lock()
	changed := !d.area.Equal(a)
	d.area = a
	d.mu.Unlock()
	if changed {
		d.logger.Info("celestrak viewport updated", zap.Bool("active", a.Active), zap.Int("clients", a.Clients), zap.Int("boxes", len(a.Boxes)), zap.Float64("area_sq_deg", a.SqDeg()))
		select {
		case d.wake <- struct{}{}:
		default:
		}
	}
	return d.AreaStatus()
}

// AreaStatus reports the area and the tick timing. The feed is idle when
// nobody is looking and nothing is pinned.
func (d *Driver) AreaStatus() aoi.Status {
	d.mu.Lock()
	defer d.mu.Unlock()
	return aoi.Status{
		Active:            d.area.Active,
		Clients:           d.area.Clients,
		Boxes:             d.area.Boxes,
		AreaSqDeg:         d.area.SqDeg(),
		EffectiveInterval: d.step,
		LastPollAt:        d.lastFetch,
		NextPollIn:        d.step,
		ExpiresAt:         d.area.ExpiresAt,
		Idle:              !d.liveLocked(d.now()) && d.pinnedLocked() == 0,
		CreditsRemaining:  -1,
	}
}

// liveLocked reports whether clients are looking at a non-expired area.
func (d *Driver) liveLocked(now time.Time) bool {
	return d.area.Active && len(d.area.Boxes) > 0 && !d.area.Expired(now)
}

func (d *Driver) pinnedLocked() int {
	n := 0
	for _, c := range d.objects {
		if c.pinned {
			n++
		}
	}
	return n
}

// Health reports the element cache state.
func (d *Driver) Health() driver.Health {
	d.mu.Lock()
	defer d.mu.Unlock()
	pinned := d.pinnedLocked()
	live := d.liveLocked(d.now())
	detail := map[string]any{
		"satellites": len(d.objects),
		"pinned":     pinned,
		"emitted":    d.emitted,
		"polls":      d.polls,
		"selections": len(d.sels),
	}
	if !d.lastFetch.IsZero() {
		detail["fetchedAt"] = d.lastFetch.UTC().Format(time.RFC3339)
	}
	if !d.nextFetch.IsZero() {
		detail["nextFetchAt"] = d.nextFetch.UTC().Format(time.RFC3339)
	}
	if d.lastStatus != 0 {
		detail["lastStatus"] = d.lastStatus
	}
	if len(d.selErr) > 0 {
		errs := make(map[string]string, len(d.selErr))
		for k, v := range d.selErr {
			errs[k] = v
		}
		detail["errors"] = errs
	}
	return driver.Health{Idle: !live && pinned == 0, Connected: !d.lastFetch.IsZero(), LastError: d.lastErr, Detail: detail}
}

// fetchAll requests every selection once and rebuilds the cache from the
// union of their members. ok is true when at least one selection
// succeeded (or was unchanged); retryAfter is the server's Retry-After
// when every selection failed and one was given.
func (d *Driver) fetchAll(ctx context.Context, sink driver.Sink) (ok bool, retryAfter time.Duration) {
	fresh := map[int]tlesgp4.TLE{}
	results := map[string][]int{}
	errs := map[string]string{}
	var firstErr string
	lastStatus := 0
	succeeded := 0
	for _, sel := range d.sels {
		if ctx.Err() != nil {
			return false, 0
		}
		ids, status, ra, err := d.fetchSelection(ctx, sink, sel, fresh)
		if status != 0 {
			lastStatus = status
		}
		if err != nil {
			msg := err.Error()
			d.logger.Warn("celestrak fetch failed", zap.String("selection", sel.Key()), zap.Int("status", status), zap.Error(err))
			errs[sel.Key()] = msg
			if firstErr == "" {
				firstErr = sel.Key() + ": " + msg
			}
			if ra > retryAfter {
				retryAfter = ra
			}
			continue
		}
		succeeded++
		if ids != nil {
			results[sel.Key()] = ids
			d.logger.Info("celestrak refresh complete", zap.String("selection", sel.Key()), zap.Int("objects", len(ids)))
		}
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	for key, ids := range results {
		d.members[key] = ids
	}
	for key := range errs {
		// A selection that failed keeps its last good members: a transient
		// error must not drop objects.
		_ = key
	}
	rebuilt := make(map[int]cached, len(d.objects))
	for _, sel := range d.sels {
		for _, id := range d.members[sel.Key()] {
			c := rebuilt[id]
			if t, ok := fresh[id]; ok {
				c.tle = t
			} else if prev, ok := d.objects[id]; ok && c.tle.Line1 == "" {
				c.tle = prev.tle
			}
			if c.tle.Line1 == "" {
				continue
			}
			c.pinned = c.pinned || sel.Pinned
			rebuilt[id] = c
		}
	}
	d.objects = rebuilt
	d.selErr = errs
	d.lastErr = firstErr
	d.lastStatus = lastStatus
	if succeeded > 0 {
		d.lastFetch = d.now()
	}
	if succeeded == 0 {
		return false, retryAfter
	}
	return true, 0
}

// fetchSelection makes one conditional request. ids is nil on a 304 (the
// previous members stand); parsed elements are added to fresh.
func (d *Driver) fetchSelection(ctx context.Context, sink driver.Sink, sel selection, fresh map[int]tlesgp4.TLE) (ids []int, status int, retryAfter time.Duration, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sel.URL(d.baseURL, d.supURL), nil)
	if err != nil {
		return nil, 0, 0, err
	}
	d.mu.Lock()
	cond := d.cond[sel.Key()]
	d.mu.Unlock()
	if cond.etag != "" {
		req.Header.Set("If-None-Match", cond.etag)
	}
	if cond.lastModified != "" {
		req.Header.Set("If-Modified-Since", cond.lastModified)
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return nil, 0, 0, err
	}
	switch {
	case resp.Status == http.StatusNotModified:
		return nil, resp.Status, 0, nil
	case resp.Status < 200 || resp.Status > 299:
		body := strings.TrimSpace(string(resp.Body))
		if len(body) > 200 {
			body = body[:200]
		}
		wait := parseRetryAfter(resp.Header.Get("Retry-After"), d.now())
		if wait == 0 && resp.Status == http.StatusForbidden {
			// CelesTrak's answer to a repeat download, and to an address it
			// is about to block: ask again at the next refresh, not in a minute.
			wait = d.refresh + d.jitter()
		}
		return nil, resp.Status, wait, fmt.Errorf("remote returned %d: %s", resp.Status, body)
	}
	records, err := tlesgp4.ParseOMM(resp.Body)
	if err != nil {
		// Dead-lettered like the other pollers' unparseable responses, so that its
		// error text reaches the status as a decoder's does, through the runner,
		// and is hidden from callers not cleared for the feed. The error returned
		// stays what it was, and so does the driver's health.
		sink.DeadLetter(sample(resp.Body), "parse_error", fmt.Errorf("%s: %w", sel.Key(), err))
		return nil, resp.Status, 0, err
	}
	ids = make([]int, 0, len(records))
	for _, rec := range records {
		t, err := tlesgp4.FromOMM(rec)
		if err != nil {
			raw, _ := json.Marshal(rec)
			sink.DeadLetter(raw, "bad_elements", err)
			continue
		}
		fresh[t.NoradID] = t
		ids = append(ids, t.NoradID)
	}
	d.mu.Lock()
	d.cond[sel.Key()] = condState{etag: resp.Header.Get("ETag"), lastModified: resp.Header.Get("Last-Modified")}
	d.mu.Unlock()
	return ids, resp.Status, 0, nil
}

// sample is the start of a response, as much of it as is dead-lettered.
func sample(body []byte) []byte { return body[:min(len(body), deadLetterSample)] }

// parseRetryAfter reads a Retry-After header as seconds or an HTTP date,
// capped so a hostile value cannot park the feed for days.
func parseRetryAfter(v string, now time.Time) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	var wait time.Duration
	if secs, err := strconv.Atoi(v); err == nil {
		wait = time.Duration(secs) * time.Second
	} else if t, err := http.ParseTime(v); err == nil {
		wait = t.Sub(now)
	}
	if wait <= 0 {
		return 0
	}
	if wait > maxRetryAfter {
		wait = maxRetryAfter
	}
	return wait
}

// propagate computes every cached object's position and emits the ones
// somebody can see: pinned objects always, the rest only while their
// sub-satellite point is inside a box clients are looking at.
func (d *Driver) propagate(ctx context.Context, sink driver.Sink) {
	now := d.now().UTC()
	d.mu.Lock()
	snapshot := make([]cached, 0, len(d.objects))
	for _, c := range d.objects {
		snapshot = append(snapshot, c)
	}
	live := d.liveLocked(now)
	boxes := append([]aoi.BBox(nil), d.area.Boxes...)
	d.mu.Unlock()
	if len(snapshot) == 0 {
		return
	}
	sent := 0
	for _, c := range snapshot {
		o := tlesgp4.Propagate(c.tle, now)
		if o == nil {
			sink.Skipped("decayed_orbit")
			continue
		}
		if !c.pinned && !(live && inAnyBox(boxes, o.Latitude, o.Longitude)) {
			sink.Skipped("out_of_area")
			continue
		}
		if err := sink.Emit(ctx, o); err != nil {
			return
		}
		sent++
	}
	d.mu.Lock()
	d.polls++
	d.emitted = sent
	d.mu.Unlock()
	sink.Succeeded(sent)
	d.logger.Info("celestrak propagation complete", zap.Int("emitted", sent), zap.Int("cached", len(snapshot)))
}

func inAnyBox(boxes []aoi.BBox, lat, lon float64) bool {
	for _, b := range boxes {
		if b.Contains(lat, lon) {
			return true
		}
	}
	return false
}
