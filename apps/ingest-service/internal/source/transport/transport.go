// Package transport moves bytes from a remote endpoint into frames. A
// transport knows nothing about the wire format; a stream transport splits
// its byte stream with a framer, a message transport (MQTT, STOMP, UDP)
// delivers each message as one frame.
package transport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

// Frame is one unit of input handed to a decoder.
type Frame struct {
	Payload    []byte
	ReceivedAt time.Time
	Remote     string            // peer address, topic or queue the frame came from
	Attrs      map[string]string // transport-specific metadata (MQTT topic, STOMP headers)
}

// ErrBackpressure is returned by an Emit when the feed cannot take the frame
// right now and the transport should push back on the remote (STOMP nack).
var ErrBackpressure = errors.New("frame rejected: backpressure")

// Emit hands a frame to the feed. It may block while the feed is saturated
// and returns ctx.Err() when the context ends, or ErrBackpressure when the
// policy is to refuse.
type Emit func(ctx context.Context, f Frame) error

// Transport produces frames until the context ends.
type Transport interface {
	// Run connects and emits frames. It returns when ctx is done or on an
	// error the transport cannot recover from itself (a bad address, a bind
	// failure). Reconnectable failures are retried inside Run.
	Run(ctx context.Context, emit Emit) error
	// State reports connectivity for feed status.
	State() State
}

// State is a transport's observable condition.
type State struct {
	Connected   bool
	Connections int    // live peer connections (servers), 1 or 0 for clients
	LastError   string // last connect or read error, empty when healthy
	Detail      map[string]any
}

// Kind classifies a transport for backpressure defaults and framing rules.
type Kind int

const (
	// KindStream transports deliver a byte stream and need a framer.
	KindStream Kind = iota
	// KindMessage transports deliver whole messages; framing must be "message".
	KindMessage
)

// Config is the transport section of a stream feed's config. Fields not
// relevant to a kind are ignored by it.
type Config struct {
	Kind string `json:"kind"`

	// tcpclient, websocket, stomp, mqtt
	Address   string `json:"address"`   // host:port (tcp*, udp)
	URL       string `json:"url"`       // websocket
	BrokerURL string `json:"brokerUrl"` // mqtt: tcp://host:1883; stomp: host:61613

	// mqtt
	Topics   []string `json:"topics"`
	QoS      int      `json:"qos"`
	ClientID string   `json:"clientId"`

	// stomp
	Queue string `json:"queue"`

	// Shared
	Username string `json:"username"`
	Password string `json:"-"` // from secret refs
	TLS      *TLS   `json:"tls"`

	// Listener protection (tcpserver, udp).
	Allow             []string `json:"allow"`             // IPs or CIDRs allowed to send; empty = anyone who can reach the port
	MaxConnections    int      `json:"maxConnections"`    // tcpserver: concurrent connections; 0 = unlimited
	MaxMessagesPerSec int      `json:"maxMessagesPerSec"` // frames per second for the listener; 0 = unlimited

	// TLS material from secret references; never part of the config JSON.
	CertPEM, KeyPEM, ClientCAPEM, CAPEM []byte `json:"-"`

	// MaxFrameBytes bounds one frame for stream transports (default 1 MiB).
	MaxFrameBytes int `json:"maxFrameBytes"`
	// ReadTimeoutSec closes an idle stream connection (0 = none).
	ReadTimeoutSec int `json:"readTimeoutSec"`
	// Framing names the framer for stream transports (default line).
	Framing string `json:"-"`
}

// TLS options for stream transports.
type TLS struct {
	Enabled            bool   `json:"enabled"`
	InsecureSkipVerify bool   `json:"insecureSkipVerify"`
	ServerName         string `json:"serverName"`
	CAFile             string `json:"caFile"`
	CertFile           string `json:"certFile"`
	KeyFile            string `json:"keyFile"`
	ClientCAFile       string `json:"clientCaFile"`      // tcpserver: CA that signs accepted client certificates
	RequireClientCert  bool   `json:"requireClientCert"` // tcpserver: mutual TLS
}

// MaxFrame returns the configured frame bound or the default.
func (c Config) MaxFrame() int {
	if c.MaxFrameBytes > 0 {
		return c.MaxFrameBytes
	}
	return 1 << 20
}

// Factory builds a transport from its config.
type Factory func(cfg Config) (Transport, error)

// Definition registers one transport kind.
type Definition struct {
	Kind        string
	Type        Kind
	Description string
	// Listens is true for transports that bind a local port and so run on a
	// listener replica (or on the replica the feed is pinned to).
	Listens bool
	New     Factory
}

var (
	mu   sync.RWMutex
	defs = map[string]Definition{}
)

// Register adds a transport kind; a duplicate kind panics (programming error).
func Register(d Definition) {
	mu.Lock()
	defer mu.Unlock()
	if _, dup := defs[d.Kind]; dup {
		panic("transport: duplicate registration of " + d.Kind)
	}
	defs[d.Kind] = d
}

// Lookup returns the definition for a kind.
func Lookup(kind string) (Definition, bool) {
	mu.RLock()
	defer mu.RUnlock()
	d, ok := defs[kind]
	return d, ok
}

// Kinds lists the registered kinds in sorted order.
func Kinds() []string {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]string, 0, len(defs))
	for k := range defs {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// New builds a transport for cfg.Kind.
func New(cfg Config) (Transport, error) {
	d, ok := Lookup(cfg.Kind)
	if !ok {
		return nil, fmt.Errorf("unknown transport %q (known: %v)", cfg.Kind, Kinds())
	}
	return d.New(cfg)
}

// ParseConfig decodes the transport section of a stream feed config.
func ParseConfig(raw json.RawMessage) (Config, error) {
	var c Config
	if len(raw) == 0 {
		return c, errors.New("transport config is required")
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		return c, fmt.Errorf("transport config: %w", err)
	}
	if c.Kind == "" {
		return c, errors.New("transport.kind is required")
	}
	return c, nil
}
