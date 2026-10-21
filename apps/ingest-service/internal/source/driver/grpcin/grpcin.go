// Package grpcin is the grpc listen driver: senders stream reports to
// sentinel.ingest.v1.IngestService on a port of the listener replica,
// authenticated by the connection's ingest tokens.
package grpcin

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc/credentials"

	"github.com/sentinel/go-common/classification"
	"github.com/sentinel/go-common/terrain"
	"github.com/sentinel/ingest-service/internal/emplacement"
	"github.com/sentinel/ingest-service/internal/source/driver"
	"github.com/sentinel/ingest-service/internal/source/driver/stream"
	"github.com/sentinel/ingest-service/internal/source/transport"
)

// ID is the driver id; Decoder names it on observations and dead letters.
const (
	ID      = "grpc"
	Decoder = "grpc"
)

const (
	defaultMaxMessageBytes = 64 << 10
	minMaxMessageBytes     = 1024
	maxMaxMessageBytes     = 16 << 20
	defaultMessagesPerSec  = 1000
	defaultPublishBatch    = 100
	maxPublishBatch        = 1000
	maxSenders             = 50

	defaultMaxEmplacements = 16
	maxMaxEmplacements     = 256

	defaultOwnshipToleranceMs = 2000
	maxOwnshipToleranceMs     = 60000

	// defaultTerrainWait is how long a detection waits for terrain when the
	// host sets no wait.
	defaultTerrainWait = 2 * time.Second
)

//go:embed schema.json
var schema []byte

func init() {
	driver.Register(driver.Definition{
		ID: ID, Name: "gRPC",
		Description:  "An external system or sensor streams reports to Sentinel over gRPC (sentinel.ingest.v1.IngestService), authenticated by this connection's ingest tokens.",
		Direction:    driver.DirectionIn,
		Capabilities: []string{driver.CapabilityListen},
		SecretFields: []string{"tlsCert", "tlsKey", "clientCa"},
		ConfigSchema: schema,
		Listen:       listen,
		Heartbeats:   true,
		New:          New,
	})
}

// Config is the connection config.
type Config struct {
	Port                       int      `json:"port"`
	Address                    string   `json:"address"`
	Allow                      []string `json:"allow"`
	MaxConnections             *int     `json:"maxConnections"`
	MaxMessageBytes            int      `json:"maxMessageBytes"`
	MaxMessagesPerSec          *int     `json:"maxMessagesPerSec"`
	MaxMessagesPerSecPerSender int      `json:"maxMessagesPerSecPerSender"`
	MaxPublishBatch            int      `json:"maxPublishBatch"`
	SenderManagedEmplacements  bool     `json:"senderManagedEmplacements"`
	// MaxEmplacements is the most emplacements the senders may make (default 16).
	MaxEmplacements int `json:"maxEmplacements"`
	// EnforceCoverage refuses a report whose position lies outside its
	// emplacement's coverage.
	EnforceCoverage bool `json:"enforceCoverage"`
	// OwnshipToleranceMs is how far past a mobile emplacement's newest
	// position report its measurements may be placed (default 2000).
	OwnshipToleranceMs *int `json:"ownshipToleranceMs"`
	// BearingTTLSec is how long a camera's bearing line stays on its site
	// without a newer detection (default 10, 1 to 300).
	BearingTTLSec *int        `json:"bearingTtlSec"`
	TLS           *TLSOptions `json:"tls"`
	// Classification is the common key: the feed's level, the floor every
	// report is raised to.
	Classification string `json:"classification"`
	// Exercise is the common key, reported in GetSession.
	Exercise bool `json:"exercise"`
}

// TLSOptions turns TLS on, and client certificates with it.
type TLSOptions struct {
	Enabled           bool `json:"enabled"`
	RequireClientCert bool `json:"requireClientCert"`
}

// listen reports the port a feed binds: every grpc config listens. A
// missing or non-numeric port reports 0, so the registry's port rules
// refuse it instead of skipping the feed; only the port is read.
func listen(raw json.RawMessage) (string, int, bool) {
	var c struct {
		Port json.RawMessage `json:"port"`
	}
	if json.Unmarshal(raw, &c) != nil {
		return "", 0, false
	}
	port, err := strconv.Atoi(string(c.Port))
	if err != nil {
		port = 0
	}
	return "tcp", port, true
}

// Driver is one grpc connection.
type Driver struct {
	feedID string
	cfg    Config
	addr   string
	host   string
	floor  string

	maxConns, maxMessageBytes, maxPerSec, maxPublishBatch int
	maxEmplacements                                       int

	allow     transport.Allowlist
	limit     *transport.Limiter
	perSender *transport.SenderLimiter
	senders   *transport.Senders
	counts    transport.ListenCounters

	tlsrc *transport.TLSSource
	creds credentials.TransportCredentials

	tokens driver.TokenSource
	emps   driver.EmplacementSource
	writer driver.EmplacementWriter // nil when the host has none
	logger *zap.Logger

	// touched is when each emplacement's last report was recorded, and
	// touchedPos when its position last was, so Touch runs at most once per
	// touchEvery for each of the two (see touch).
	touchMu    sync.Mutex
	touched    map[string]time.Time
	touchedPos map[string]time.Time
	// beatTouched is when each emplacement's last heartbeat was written to
	// its row, with the interval, on a throttle slot of its own (see beat).
	beatTouched map[string]beatTouch

	// tracks are the mobile emplacements' recent fixes, and emittedAt when
	// each one's own observation last went out.
	tracksMu  sync.Mutex
	tracks    map[string]*track
	emittedAt map[string]time.Time
	// emittedState is the heartbeat state each site's last publish carried,
	// so that a change goes out at once (siteDue).
	emittedState map[string]emplacement.HeartbeatState
	tolerance    time.Duration

	// now is the server's clock (tests replace it).
	now func() time.Time
	// live is the run's heartbeat tracker; nil before the first Run.
	live atomic.Pointer[emplacement.Tracker]
	// republishTick is how often bound sites are checked for a publish
	// (tests shorten it).
	republishTick time.Duration
	// heartbeatsOff turns the heartbeat model's refusals and publishes off:
	// the no_heartbeat gate, the re-publish ticker and the offline publish at
	// Stop. Tests of the report path written before heartbeats set it;
	// production never does.
	heartbeatsOff bool

	// terrain answers ground heights and lines of sight (nil when terrain is
	// off); terrainWait is how long one detection may wait for it in all, and
	// maxRangeM how far a line of sight is followed when the emplacement's
	// coverage gives no maximum range.
	terrain     driver.Terrain
	terrainWait time.Duration
	maxRangeM   float64
	// terrainClampSkipped counts radar targets the terrain could not answer
	// for: their measured altitude was kept.
	terrainClampSkipped atomic.Int64

	// cacheWait is how long an upsert waits for the cache to see its write
	// (tests shorten it).
	cacheWait time.Duration

	// idleTimeout is how long a connection may have no RPCs (tests shorten it).
	idleTimeout time.Duration

	gl        atomic.Pointer[transport.GuardListener]
	connected atomic.Bool
	stopping  atomic.Bool

	// bearings are each site's bearing lines (bearings.go), bearingSeq keys
	// those without a track id, and bearingTTL is how long one lasts.
	bearingsMu sync.Mutex
	bearings   map[string]*siteBearings
	bearingSeq uint64
	bearingTTL time.Duration

	reportsOK    atomic.Int64
	deadLettered atomic.Int64
	rejectedMu   sync.Mutex
	rejected     map[string]int64
	// bearingCounts counts the bearings recorded, by reason.
	bearingCountMu sync.Mutex
	bearingCounts  map[string]int64
}

func intOr(p *int, def int) int {
	if p == nil {
		return def
	}
	return *p
}

// New checks the config and prepares TLS; Run binds the port.
func New(spec driver.Spec, deps driver.Deps) (driver.Driver, error) {
	var c Config
	if err := driver.DecodeConfig(spec, &c); err != nil {
		return nil, err
	}
	if c.Port < 1024 || c.Port > 65535 {
		return nil, fmt.Errorf("feed %s: port must be 1024 to 65535, not %d", spec.FeedID, c.Port)
	}
	if spec.Backpressure != "" && spec.Backpressure != driver.BackpressureBlock {
		return nil, fmt.Errorf("feed %s: a grpc connection's backpressure is block, not %q", spec.FeedID, spec.Backpressure)
	}
	size := c.MaxMessageBytes
	if size == 0 {
		size = defaultMaxMessageBytes
	}
	if size < minMaxMessageBytes || size > maxMaxMessageBytes {
		return nil, fmt.Errorf("feed %s: maxMessageBytes must be %d to %d", spec.FeedID, minMaxMessageBytes, maxMaxMessageBytes)
	}
	if intOr(c.MaxConnections, 0) < 0 {
		return nil, fmt.Errorf("feed %s: maxConnections must not be negative", spec.FeedID)
	}
	if intOr(c.MaxMessagesPerSec, 0) < 0 {
		return nil, fmt.Errorf("feed %s: maxMessagesPerSec must not be negative", spec.FeedID)
	}
	if c.MaxMessagesPerSecPerSender < 0 {
		return nil, fmt.Errorf("feed %s: maxMessagesPerSecPerSender must not be negative", spec.FeedID)
	}
	if tol := intOr(c.OwnshipToleranceMs, defaultOwnshipToleranceMs); tol < 0 || tol > maxOwnshipToleranceMs {
		return nil, fmt.Errorf("feed %s: ownshipToleranceMs must be 0 to %d", spec.FeedID, maxOwnshipToleranceMs)
	}
	ttl := intOr(c.BearingTTLSec, defaultBearingTTLSec)
	if ttl < minBearingTTLSec || ttl > maxBearingTTLSec {
		return nil, fmt.Errorf("feed %s: bearingTtlSec must be %d to %d", spec.FeedID, minBearingTTLSec, maxBearingTTLSec)
	}
	maxEmps := c.MaxEmplacements
	if maxEmps == 0 {
		maxEmps = defaultMaxEmplacements
	}
	if maxEmps < 1 || maxEmps > maxMaxEmplacements {
		return nil, fmt.Errorf("feed %s: maxEmplacements must be 1 to %d", spec.FeedID, maxMaxEmplacements)
	}
	batch := c.MaxPublishBatch
	if batch == 0 {
		batch = defaultPublishBatch
	}
	if batch < 1 || batch > maxPublishBatch {
		return nil, fmt.Errorf("feed %s: maxPublishBatch must be 1 to %d", spec.FeedID, maxPublishBatch)
	}
	allow, err := transport.ParseAllowlist(c.Allow)
	if err != nil {
		return nil, fmt.Errorf("feed %s: allow: %w", spec.FeedID, err)
	}
	tlsOn := c.TLS != nil && c.TLS.Enabled
	mutual := c.TLS != nil && c.TLS.RequireClientCert
	if mutual && !tlsOn {
		return nil, fmt.Errorf("feed %s: requiring client certificates needs tls.enabled", spec.FeedID)
	}
	if spec.Secrets["clientCa"] != "" && !mutual {
		return nil, fmt.Errorf("feed %s: a clientCa secret needs tls.requireClientCert", spec.FeedID)
	}
	d := &Driver{
		feedID: spec.FeedID, cfg: c, host: deps.ListenerHost,
		addr:            net.JoinHostPort(c.Address, strconv.Itoa(c.Port)),
		floor:           classification.Normalize(c.Classification),
		maxConns:        intOr(c.MaxConnections, stream.DefaultMaxConnections),
		maxMessageBytes: size,
		maxPerSec:       intOr(c.MaxMessagesPerSec, defaultMessagesPerSec),
		maxPublishBatch: batch,
		maxEmplacements: maxEmps,
		allow:           allow,
		senders:         transport.NewSenders(maxSenders),
		perSender:       transport.NewSenderLimiter(c.MaxMessagesPerSecPerSender, transport.SenderLimiterKeys),
		tokens:          deps.Tokens, emps: deps.Emplacements, writer: deps.EmplacementWriter, logger: deps.Logger,
		touched:       map[string]time.Time{},
		touchedPos:    map[string]time.Time{},
		tracks:        map[string]*track{},
		emittedAt:     map[string]time.Time{},
		beatTouched:   map[string]beatTouch{},
		emittedState:  map[string]emplacement.HeartbeatState{},
		republishTick: republishTick,
		now:           time.Now,
		cacheWait:     cacheWait,
		tolerance:     time.Duration(intOr(c.OwnshipToleranceMs, defaultOwnshipToleranceMs)) * time.Millisecond,
		rejected:      map[string]int64{}, idleTimeout: idleTimeout,
		bearings:      map[string]*siteBearings{},
		bearingTTL:    time.Duration(ttl) * time.Second,
		bearingCounts: map[string]int64{},
	}
	if d.logger == nil {
		d.logger = zap.NewNop()
	}
	d.terrain, d.terrainWait, d.maxRangeM = deps.Terrain, deps.TerrainWait, deps.TerrainMaxRangeM
	if d.terrainWait <= 0 {
		d.terrainWait = defaultTerrainWait
	}
	if d.maxRangeM <= 0 {
		d.maxRangeM = terrain.DefaultMaxRangeM
	}
	d.limit = transport.NewLimiter(d.maxPerSec)
	if tlsOn {
		if err := d.prepareTLS(spec, deps, mutual); err != nil {
			return nil, fmt.Errorf("feed %s: %w", spec.FeedID, err)
		}
	}
	return d, nil
}

var errNoTokens = errors.New("the grpc driver needs the ingest token cache and the emplacements")

// addFix puts a fix on the emplacement's track, making the track if need be.
func (d *Driver) addFix(id string, f fix) {
	d.tracksMu.Lock()
	defer d.tracksMu.Unlock()
	t := d.tracks[id]
	if t == nil {
		t = &track{}
		d.tracks[id] = t
	}
	t.add(f)
}

// originAt is where the emplacement was at ts according to its track. It
// never makes a track.
func (d *Driver) originAt(id string, ts time.Time) (origin, bool) {
	d.tracksMu.Lock()
	defer d.tracksMu.Unlock()
	t := d.tracks[id]
	if t == nil {
		return origin{}, false
	}
	return t.at(ts, d.tolerance)
}

// sensorDue reports whether an emplacement's own observation is due, and
// marks it emitted when it is.
func (d *Driver) sensorDue(id string, now time.Time) bool {
	d.tracksMu.Lock()
	defer d.tracksMu.Unlock()
	if last, ok := d.emittedAt[id]; ok && now.Sub(last) < sensorEvery {
		return false
	}
	d.emittedAt[id], d.emittedState[id] = now, emplacement.HeartbeatOnline
	return true
}
