// Package aisstream streams AIS from aisstream.io over a WebSocket.
//
// aisstream.io wants a subscription (API key, bounding boxes, optional
// MMSI and message type filters) within three seconds of connecting, and
// replaces it when a new one is sent on the same connection, at most once a
// second. It allows three connections per account, drops consumers that
// fall behind, and forbids browser use, so the key stays here.
//
// The driver keeps up to two connections. The area connection follows the
// map clients' viewports pushed by the API gateway and is closed while
// nobody looks. The watchlist connection, open while the watchlist is not
// empty, covers the globe filtered to the listed MMSIs, so those vessels
// are tracked off-screen too.
//
// Message bodies are go-ais packets as JSON; package aismap maps them,
// exactly as it does for NMEA receivers, so vessels from both fuse on MMSI.
// Positions are thinned per vessel (moving vessels every 15 s, stationary
// ones every 3 min by default); static and voyage reports always pass.
// When the viewed area is larger than maxAreaSqDeg nothing from the area
// connection is stored; the driver counts distinct vessels per grid cell
// instead and publishes the counts (density.events) for the map to draw.
package aisstream

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"regexp"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/sentinel/ingest-service/internal/aoi"
	"github.com/sentinel/ingest-service/internal/obs"
	"github.com/sentinel/ingest-service/internal/source/decoder"
	"github.com/sentinel/ingest-service/internal/source/decoder/aismap"
	"github.com/sentinel/ingest-service/internal/source/driver"
)

// ID is the driver id.
const ID = "aisstream"

const (
	defaultURL           = "wss://stream.aisstream.io/v0/stream"
	defaultMoving        = 15 * time.Second
	defaultStationary    = 3 * time.Minute
	defaultStationaryKn  = 0.5
	defaultMaxAreaSqDeg  = 2500.0
	defaultCellDeg       = 1.0
	defaultDensityWindow = 10 * time.Minute
	defaultDensityEvery  = 15 * time.Second
	maxWatchlist         = 200
	frameBuffer          = 10000
	areaCheckEvery       = 5 * time.Second
	modeIdle             = "idle"
	modeDetail           = "detail"
	modeDensity          = "density"
	connArea             = "area"
	connWatchlist        = "watchlist"
	subscriptionKeyField = "apiKey"
)

// DefaultMessageTypes are the aisstream.io message types the driver asks
// for unless configured otherwise: everything aismap turns into an
// observation.
var DefaultMessageTypes = []string{
	"PositionReport", "StandardClassBPositionReport", "ExtendedClassBPositionReport",
	"LongRangeAisBroadcastMessage", "ShipStaticData", "StaticDataReport", "AidsToNavigationReport",
}

var mmsiRe = regexp.MustCompile(`^[0-9]{9}$`)

//go:embed schema.json
var schema []byte

func init() {
	driver.Register(driver.Definition{
		ID: ID, Name: "aisstream.io AIS", Source: "AIS",
		Description:  "Worldwide AIS from aisstream.io over a WebSocket: vessels over the map clients' viewports plus an MMSI watchlist tracked everywhere. Needs an API key (secretRefs.apiKey).",
		Capabilities: []string{driver.CapabilityArea},
		SecretFields: []string{subscriptionKeyField},
		AreaTTL:      90 * time.Second,
		ConfigSchema: schema,
		New:          New,
	})
}

// Config is the feed configuration. Pointers distinguish "unset" from 0,
// which disables thinning or the area cap.
type Config struct {
	URL                   string   `json:"url"`
	WatchlistMMSI         []string `json:"watchlistMmsi"`
	MessageTypes          []string `json:"messageTypes"`
	MovingIntervalSec     *int     `json:"movingIntervalSec"`
	StationaryIntervalSec *int     `json:"stationaryIntervalSec"`
	StationaryKnots       *float64 `json:"stationaryKnots"`
	MaxAreaSqDeg          *float64 `json:"maxAreaSqDeg"`
	DensityCellDeg        float64  `json:"densityCellDeg"`
	DensityWindowSec      int      `json:"densityWindowSec"`
	DensityIntervalSec    int      `json:"densityIntervalSec"`
}

// frame is one message off a connection.
type frame struct {
	from string // connArea or connWatchlist
	data []byte
	at   time.Time
}

// counters are the driver's running totals.
type counters struct {
	messages, emitted, thinned, outOfArea, notStored, dropped, errors int64
}

// Driver is one aisstream.io feed.
type Driver struct {
	logger    *zap.Logger
	url       string
	apiKey    string
	watchlist []string
	types     []string
	maxArea   float64
	now       func() time.Time
	dial      dialFunc

	frames   chan frame
	areaWake chan struct{}
	thin     *thinner
	mapper   *aismap.Mapper
	counter  *counter

	densityOut   driver.DensityPublisher
	densityEvery time.Duration

	mu    sync.Mutex
	area  aoi.Area
	conns map[string]*connState
	count counters

	densityErr                   string
	densityCells, densityVessels int
	densityAt                    time.Time
}

// New validates the config and builds the driver.
func New(spec driver.Spec, deps driver.Deps) (driver.Driver, error) {
	var cfg Config
	if err := driver.DecodeConfig(spec, &cfg); err != nil {
		return nil, err
	}
	key := spec.Secrets[subscriptionKeyField]
	if key == "" {
		return nil, fmt.Errorf("feed %s: aisstream needs an API key in secretRefs.%s", spec.FeedID, subscriptionKeyField)
	}
	watchlist, err := normalizeWatchlist(cfg.WatchlistMMSI)
	if err != nil {
		return nil, fmt.Errorf("feed %s: %w", spec.FeedID, err)
	}
	types := cfg.MessageTypes
	if len(types) == 0 {
		types = DefaultMessageTypes
	}
	for _, t := range types {
		if _, ok := packetTypes[t]; !ok {
			return nil, fmt.Errorf("feed %s: unsupported message type %q", spec.FeedID, t)
		}
	}
	cellDeg := cfg.DensityCellDeg
	if cellDeg <= 0 {
		cellDeg = defaultCellDeg
	}
	logger := deps.Logger
	if logger == nil {
		logger = zap.NewNop()
	}
	d := &Driver{
		logger:    logger,
		url:       defaultURL,
		apiKey:    key,
		watchlist: watchlist,
		types:     types,
		maxArea:   floatOr(cfg.MaxAreaSqDeg, defaultMaxAreaSqDeg),
		now:       time.Now,
		dial:      gorillaDial,
		frames:    make(chan frame, frameBuffer),
		areaWake:  make(chan struct{}, 1),
		thin: newThinner(
			secondsOr(cfg.MovingIntervalSec, defaultMoving),
			secondsOr(cfg.StationaryIntervalSec, defaultStationary),
			floatOr(cfg.StationaryKnots, defaultStationaryKn),
		),
		mapper:       aismap.NewMapper(ID),
		counter:      newCounter(cellDeg, driver.Seconds(cfg.DensityWindowSec, defaultDensityWindow)),
		densityOut:   deps.Density,
		densityEvery: driver.Seconds(cfg.DensityIntervalSec, defaultDensityEvery),
		conns:        map[string]*connState{},
	}
	if cfg.URL != "" {
		d.url = cfg.URL
	}
	return d, nil
}

func normalizeWatchlist(in []string) ([]string, error) {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, m := range in {
		if !mmsiRe.MatchString(m) {
			return nil, fmt.Errorf("watchlist MMSI %q is not nine digits", m)
		}
		if !seen[m] {
			seen[m] = true
			out = append(out, m)
		}
	}
	if len(out) > maxWatchlist {
		return nil, fmt.Errorf("watchlist has %d MMSIs; aisstream.io allows %d", len(out), maxWatchlist)
	}
	return out, nil
}

func secondsOr(v *int, def time.Duration) time.Duration {
	if v == nil {
		return def
	}
	return time.Duration(*v) * time.Second
}

func floatOr(v *float64, def float64) float64 {
	if v == nil {
		return def
	}
	return *v
}

// Run keeps the connections the area and watchlist call for and turns
// their messages into observations until ctx ends.
func (d *Driver) Run(ctx context.Context, sink driver.Sink) error {
	d.logger.Info("aisstream started", zap.Int("watchlist", len(d.watchlist)), zap.Strings("types", d.types), zap.Float64("max_area_sq_deg", d.maxArea))
	var wg sync.WaitGroup
	defer wg.Wait()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	if len(d.watchlist) > 0 {
		c := d.newConn(connWatchlist)
		c.update(d.subscription(globalBoxes(), d.watchlist))
		wg.Add(1)
		go func() { defer wg.Done(); c.run(ctx) }()
	}
	wg.Add(1)
	go func() { defer wg.Done(); d.followArea(ctx) }()

	density := time.NewTicker(d.densityEvery)
	defer density.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case f := <-d.frames:
			d.handle(ctx, sink, f)
		case <-density.C:
			d.publishDensity()
		}
	}
}

// followArea opens, updates and closes the area connection as the pushed
// area changes or its lease lapses.
func (d *Driver) followArea(ctx context.Context) {
	var (
		c      *conn
		stop   func()
		pushed string
	)
	open := func() (*conn, func()) {
		c := d.newConn(connArea)
		cctx, cancel := context.WithCancel(ctx)
		done := make(chan struct{})
		go func() { defer close(done); c.run(cctx) }()
		return c, func() { cancel(); <-done }
	}
	closeConn := func() {
		if stop != nil {
			stop()
			stop, c, pushed = nil, nil, ""
			d.mu.Lock()
			delete(d.conns, connArea)
			d.mu.Unlock()
		}
	}
	defer closeConn()
	check := time.NewTicker(areaCheckEvery)
	defer check.Stop()
	for {
		boxes := d.wantedBoxes()
		switch {
		case len(boxes) == 0:
			closeConn()
		default:
			sub := d.subscription(boxes, nil)
			if c == nil {
				c, stop = open()
				d.logger.Info("aisstream area connection opened", zap.Int("boxes", len(boxes)))
			}
			if key := sub.key(); key != pushed {
				c.update(sub)
				pushed = key
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-d.areaWake:
		case <-check.C:
		}
	}
}

// wantedBoxes is what the area connection should subscribe to: the pushed
// boxes while the area is live, rounded outward to 0.1° so small pans do
// not resubscribe; nothing otherwise.
func (d *Driver) wantedBoxes() [][2][2]float64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.liveLocked(d.now()) {
		return nil
	}
	out := make([][2][2]float64, 0, len(d.area.Boxes))
	for _, b := range d.area.Boxes {
		if b.IsGlobal() {
			return globalBoxes()
		}
		out = append(out, roundBox(b))
	}
	return out
}

// SetAreaOfInterest applies the viewport the gateway pushed.
func (d *Driver) SetAreaOfInterest(a aoi.Area) aoi.Status {
	d.mu.Lock()
	changed := !d.area.Equal(a)
	d.area = a
	d.mu.Unlock()
	if changed {
		d.logger.Info("aisstream viewport updated", zap.Bool("active", a.Active), zap.Int("clients", a.Clients), zap.Int("boxes", len(a.Boxes)), zap.Float64("area_sq_deg", a.SqDeg()))
		select {
		case d.areaWake <- struct{}{}:
		default:
		}
	}
	return d.AreaStatus()
}

// AreaStatus reports the area. The feed is idle when nobody is looking and
// the watchlist is empty.
func (d *Driver) AreaStatus() aoi.Status {
	d.mu.Lock()
	defer d.mu.Unlock()
	return aoi.Status{
		Active:           d.area.Active,
		Clients:          d.area.Clients,
		Boxes:            d.area.Boxes,
		AreaSqDeg:        d.area.SqDeg(),
		ExpiresAt:        d.area.ExpiresAt,
		Idle:             !d.liveLocked(d.now()) && len(d.watchlist) == 0,
		CreditsRemaining: -1,
	}
}

// liveLocked reports whether clients are looking at a non-expired area.
func (d *Driver) liveLocked(now time.Time) bool {
	return d.area.Active && len(d.area.Boxes) > 0 && !d.area.Expired(now)
}

// modeLocked says what happens to messages from the area connection.
func (d *Driver) modeLocked(now time.Time) string {
	switch {
	case !d.liveLocked(now):
		return modeIdle
	case d.maxArea > 0 && d.area.SqDeg() > d.maxArea:
		return modeDensity
	}
	return modeDetail
}

// Health reports the connections and counters.
func (d *Driver) Health() driver.Health {
	d.mu.Lock()
	defer d.mu.Unlock()
	now := d.now()
	conns := map[string]any{}
	connected := false
	lastErr := ""
	for name, c := range d.conns {
		conns[name] = c.detail()
		connected = connected || c.connected
		if c.lastError != "" {
			lastErr = name + ": " + c.lastError
		}
	}
	detail := map[string]any{
		"mode":         d.modeLocked(now),
		"connections":  conns,
		"watchlist":    len(d.watchlist),
		"areaSqDeg":    d.area.SqDeg(),
		"maxAreaSqDeg": d.maxArea,
		"messages":     d.count.messages,
		"emitted":      d.count.emitted,
		"thinned":      d.count.thinned,
		"outOfArea":    d.count.outOfArea,
		"notStored":    d.count.notStored,
		"dropped":      d.count.dropped,
		"errors":       d.count.errors,
		"vesselsKnown": d.mapper.Known(),
	}
	if !d.densityAt.IsZero() {
		detail["density"] = map[string]any{"cells": d.densityCells, "vessels": d.densityVessels, "publishedAt": d.densityAt.UTC().Format(time.RFC3339)}
	}
	if d.densityErr != "" {
		detail["densityError"] = d.densityErr
	}
	idle := !d.liveLocked(now) && len(d.watchlist) == 0
	return driver.Health{Idle: idle, Connected: connected, LastError: lastErr, Detail: detail}
}

// handle turns one message into at most one observation.
func (d *Driver) handle(ctx context.Context, sink driver.Sink, f frame) {
	o, err := d.parse(f)
	d.mu.Lock()
	d.count.messages++
	mode := d.modeLocked(f.at)
	boxes := d.area.Boxes
	d.mu.Unlock()
	if err != nil {
		if errors.Is(err, decoder.ErrSkip) {
			sink.Skipped(decoder.SkipReason(err))
			return
		}
		d.bump(func(c *counters) { c.errors++ })
		sink.DeadLetter(f.data, "bad_message", err)
		return
	}
	// Every vessel the area connection reports is counted, whatever the
	// mode, so the grid is current the moment a view grows past the cap.
	if f.from == connArea && o.HasPosition && o.EntityType == obs.EntityVessel {
		d.counter.observe(o.SourceEntityID, o.Latitude, o.Longitude, f.at)
	}
	if f.from == connArea {
		switch mode {
		case modeDetail:
			if o.HasPosition && !inAnyBox(boxes, o.Latitude, o.Longitude) {
				d.bump(func(c *counters) { c.outOfArea++ })
				sink.Skipped("out_of_area")
				return
			}
		default:
			d.bump(func(c *counters) { c.notStored++ })
			sink.Skipped("area_" + mode)
			return
		}
	}
	if !d.thin.allow(o, f.at) {
		d.bump(func(c *counters) { c.thinned++ })
		sink.Skipped("thinned")
		return
	}
	if err := sink.Emit(ctx, o); err != nil {
		return
	}
	d.bump(func(c *counters) { c.emitted++ })
	sink.Succeeded(1)
}

func (d *Driver) bump(fn func(*counters)) {
	d.mu.Lock()
	fn(&d.count)
	d.mu.Unlock()
}

// enqueue hands a frame to the processing loop without ever blocking the
// reader: aisstream.io disconnects consumers that fall behind, so a full
// buffer drops the frame here instead.
func (d *Driver) enqueue(f frame) {
	select {
	case d.frames <- f:
	default:
		d.bump(func(c *counters) { c.dropped++ })
	}
}

func inAnyBox(boxes []aoi.BBox, lat, lon float64) bool {
	for _, b := range boxes {
		if b.Contains(lat, lon) {
			return true
		}
	}
	return false
}
