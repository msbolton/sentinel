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
	// EnforceCoverage refuses a report whose position lies outside its
	// emplacement's coverage.
	EnforceCoverage bool        `json:"enforceCoverage"`
	TLS             *TLSOptions `json:"tls"`
	// Classification is the common key: the feed's level, the floor every
	// report is raised to.
	Classification string `json:"classification"`
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

	// touched is when each emplacement's last report was recorded, so Touch
	// runs at most once per touchEvery for each.
	touchMu sync.Mutex
	touched map[string]time.Time

	// idleTimeout is how long a connection may have no RPCs (tests shorten it).
	idleTimeout time.Duration

	gl        atomic.Pointer[transport.GuardListener]
	connected atomic.Bool
	stopping  atomic.Bool

	reportsOK    atomic.Int64
	deadLettered atomic.Int64
	rejectedMu   sync.Mutex
	rejected     map[string]int64
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
		allow:           allow,
		senders:         transport.NewSenders(maxSenders),
		perSender:       transport.NewSenderLimiter(c.MaxMessagesPerSecPerSender, transport.SenderLimiterKeys),
		tokens:          deps.Tokens, emps: deps.Emplacements, writer: deps.EmplacementWriter, logger: deps.Logger,
		touched:  map[string]time.Time{},
		rejected: map[string]int64{}, idleTimeout: idleTimeout,
	}
	if d.logger == nil {
		d.logger = zap.NewNop()
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
