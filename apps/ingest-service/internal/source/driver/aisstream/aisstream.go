// Package aisstream streams AIS from aisstream.io over a WebSocket.
//
// aisstream.io wants a subscription (API key, bounding boxes, optional
// MMSI and message type filters) within three seconds of connecting. It
// allows three connections per account, drops consumers that fall behind,
// and forbids browser use, so the key stays here.
//
// The driver keeps one connection subscribed to the whole world (about 115
// messages a second in October 2026), so every vessel aisstream.io hears
// is in the database for search. What the map clients look at decides the
// rate: vessels inside a viewport pushed by the API gateway, and those on
// the watchlist, are stored at full rate (moving every 15 s, stationary
// every 3 min); every other vessel at most every 5 minutes as a background
// fix, which updates its entity but stays out of track history.
//
// Message bodies are go-ais packets as JSON; package aismap maps them,
// exactly as it does for NMEA receivers, so vessels from both fuse on MMSI.
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
	defaultBackground    = 5 * time.Minute
	frameBuffer          = 10000
	connGlobal           = "global"
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
		Description:  "Worldwide AIS from aisstream.io over a WebSocket: every vessel, at full rate over the map clients' viewports and the watchlist and slowly elsewhere. Needs an API key (secretRefs.apiKey).",
		Capabilities: []string{driver.CapabilityArea},
		SecretFields: []string{subscriptionKeyField},
		AreaTTL:      90 * time.Second,
		ConfigSchema: schema,
		New:          New,
	})
}

// Config is the feed configuration. Pointers distinguish "unset" from 0,
// which disables that thinning.
type Config struct {
	URL                   string   `json:"url"`
	WatchlistMMSI         []string `json:"watchlistMmsi"`
	MessageTypes          []string `json:"messageTypes"`
	MovingIntervalSec     *int     `json:"movingIntervalSec"`
	StationaryIntervalSec *int     `json:"stationaryIntervalSec"`
	StationaryKnots       *float64 `json:"stationaryKnots"`
	BackgroundIntervalSec *int     `json:"backgroundIntervalSec"`
}

// frame is one message off the connection.
type frame struct {
	from string
	data []byte
	at   time.Time
}

// counters are the driver's running totals.
type counters struct {
	messages, emitted, detail, background, thinned, dropped, errors int64
}

// Driver is one aisstream.io feed.
type Driver struct {
	logger    *zap.Logger
	url       string
	apiKey    string
	watchlist map[string]bool
	types     []string
	now       func() time.Time
	dial      dialFunc

	frames chan frame
	thin   *thinner
	mapper *aismap.Mapper

	mu    sync.Mutex
	area  aoi.Area
	conns map[string]*connState
	count counters
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
	watchlist := map[string]bool{}
	for _, m := range cfg.WatchlistMMSI {
		if !mmsiRe.MatchString(m) {
			return nil, fmt.Errorf("feed %s: watchlist MMSI %q is not nine digits", spec.FeedID, m)
		}
		watchlist[m] = true
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
		now:       time.Now,
		dial:      gorillaDial,
		frames:    make(chan frame, frameBuffer),
		thin: newThinner(
			secondsOr(cfg.MovingIntervalSec, defaultMoving),
			secondsOr(cfg.StationaryIntervalSec, defaultStationary),
			secondsOr(cfg.BackgroundIntervalSec, defaultBackground),
			floatOr(cfg.StationaryKnots, defaultStationaryKn),
		),
		mapper: aismap.NewMapper(ID),
		conns:  map[string]*connState{},
	}
	if cfg.URL != "" {
		d.url = cfg.URL
	}
	return d, nil
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

// Run keeps the world subscription open and turns its messages into
// observations until ctx ends.
func (d *Driver) Run(ctx context.Context, sink driver.Sink) error {
	d.logger.Info("aisstream started", zap.Int("watchlist", len(d.watchlist)), zap.Strings("types", d.types), zap.Duration("background", d.thin.background))
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	c := d.newConn(connGlobal)
	c.update(d.subscription(globalBoxes(), nil))
	done := make(chan struct{})
	go func() { defer close(done); c.run(ctx) }()
	defer func() { cancel(); <-done }()
	for {
		select {
		case <-ctx.Done():
			return nil
		case f := <-d.frames:
			d.handle(ctx, sink, f)
		}
	}
}

// SetAreaOfInterest applies the viewport the gateway pushed: vessels inside
// it are stored at full rate.
func (d *Driver) SetAreaOfInterest(a aoi.Area) aoi.Status {
	d.mu.Lock()
	changed := !d.area.Equal(a)
	d.area = a
	d.mu.Unlock()
	if changed {
		d.logger.Info("aisstream viewport updated", zap.Bool("active", a.Active), zap.Int("clients", a.Clients), zap.Int("boxes", len(a.Boxes)), zap.Float64("area_sq_deg", a.SqDeg()))
	}
	return d.AreaStatus()
}

// AreaStatus reports the area. The feed is never idle: it streams the world.
func (d *Driver) AreaStatus() aoi.Status {
	d.mu.Lock()
	defer d.mu.Unlock()
	return aoi.Status{
		Active:           d.area.Active,
		Clients:          d.area.Clients,
		Boxes:            d.area.Boxes,
		AreaSqDeg:        d.area.SqDeg(),
		ExpiresAt:        d.area.ExpiresAt,
		CreditsRemaining: -1,
	}
}

// liveLocked reports whether clients are looking at a non-expired area.
func (d *Driver) liveLocked(now time.Time) bool {
	return d.area.Active && len(d.area.Boxes) > 0 && !d.area.Expired(now)
}

// Health reports the connection and counters.
func (d *Driver) Health() driver.Health {
	d.mu.Lock()
	defer d.mu.Unlock()
	connected, lastErr := false, ""
	var conn map[string]any
	if c := d.conns[connGlobal]; c != nil {
		conn = c.detail()
		connected, lastErr = c.connected, c.lastError
	}
	detail := map[string]any{
		"connection":            conn,
		"watchlist":             len(d.watchlist),
		"viewing":               d.liveLocked(d.now()),
		"backgroundIntervalSec": int(d.thin.background / time.Second),
		"messages":              d.count.messages,
		"emitted":               d.count.emitted,
		"detail":                d.count.detail,
		"background":            d.count.background,
		"thinned":               d.count.thinned,
		"dropped":               d.count.dropped,
		"errors":                d.count.errors,
		"vesselsKnown":          d.mapper.Known(),
	}
	return driver.Health{Connected: connected, LastError: lastErr, Detail: detail}
}

// handle turns one message into at most one observation. A vessel on the
// watchlist or inside the viewed area is a full-rate fix; anything else is
// a background fix.
func (d *Driver) handle(ctx context.Context, sink driver.Sink, f frame) {
	o, err := d.parse(f)
	d.mu.Lock()
	d.count.messages++
	live := d.liveLocked(f.at)
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
	watched := o.AIS != nil && d.watchlist[o.AIS.MMSI]
	inView := live && o.HasPosition && inAnyBox(boxes, o.Latitude, o.Longitude)
	o.Background = !watched && !inView
	if !d.thin.allow(o, f.at) {
		d.bump(func(c *counters) { c.thinned++ })
		sink.Skipped("thinned")
		return
	}
	if err := sink.Emit(ctx, o); err != nil {
		return
	}
	d.bump(func(c *counters) {
		c.emitted++
		if o.Background {
			c.background++
		} else {
			c.detail++
		}
	})
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
