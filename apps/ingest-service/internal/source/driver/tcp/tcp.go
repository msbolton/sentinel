// Package tcp is the driver for external systems that send data over TCP:
// Sentinel listens on a port the operator chooses (mode listen, on a
// listener replica) or connects to the sender (mode connect). It builds
// the stream pipeline (tcpserver or tcpclient, a framer, a decoder) from
// a flat config.
package tcp

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"net"
	"strconv"

	"github.com/sentinel/ingest-service/internal/source/decoder"
	"github.com/sentinel/ingest-service/internal/source/driver"
	"github.com/sentinel/ingest-service/internal/source/driver/stream"
	"github.com/sentinel/ingest-service/internal/source/transport"
	"github.com/sentinel/ingest-service/internal/source/transport/framing"
)

// ID is the driver id.
const ID = "tcp"

//go:embed schema.json
var schema []byte

func init() {
	driver.Register(driver.Definition{
		ID: ID, Name: "TCP",
		Description:  "An external system sends data over TCP: it connects to Sentinel on a port you choose, or Sentinel connects to it. Sentinel's own protobuf format or a device format.",
		Direction:    driver.DirectionIn,
		Capabilities: []string{driver.CapabilityListen},
		SecretFields: []string{"tlsCert", "tlsKey", "clientCa", "serverCa"},
		ConfigSchema: schema,
		Listen:       listen,
		New:          New,
	})
}

// Config is the feed configuration.
type Config struct {
	Mode              string          `json:"mode"`
	Host              string          `json:"host"`
	Port              int             `json:"port"`
	Address           string          `json:"address"`
	Format            string          `json:"format"`
	Encoding          string          `json:"encoding"`
	Framing           string          `json:"framing"`
	DecoderOptions    json.RawMessage `json:"decoderOptions"`
	DefaultSource     string          `json:"defaultSource"`
	Allow             []string        `json:"allow"`
	MaxConnections    *int            `json:"maxConnections"`
	MaxMessageBytes   int             `json:"maxMessageBytes"`
	MaxMessagesPerSec *int            `json:"maxMessagesPerSec"`
	TLS               *transport.TLS  `json:"tls"`
}

// Driver wraps the stream driver to report where it listens or connects.
type Driver struct {
	*stream.Driver
	detailKey string
	target    map[string]any
	tls       bool
	allow     int
}

// listen reports the port a feed in listen mode binds. Every listen-mode
// config listens: a missing or non-numeric port reports port 0, so the
// registry's port rules refuse it instead of skipping the feed. Only the
// mode and port are read, so a mistyped unrelated field (allow, say)
// cannot turn a listener into a non-listener and skip the checks on it.
func listen(raw json.RawMessage) (string, int, bool) {
	var c struct {
		Mode string          `json:"mode"`
		Port json.RawMessage `json:"port"`
	}
	if json.Unmarshal(raw, &c) != nil || c.Mode != "listen" {
		return "", 0, false
	}
	port, err := strconv.Atoi(string(c.Port))
	if err != nil {
		port = 0
	}
	return "tcp", port, true
}

// FramingFor is the framing a format and encoding imply, unless overridden.
func FramingFor(format, encoding, override string) string {
	if override != "" {
		return override
	}
	switch format {
	case "", "sentinel":
		if encoding == "json" {
			return framing.Line
		}
		return framing.Delimited
	case "cot":
		return framing.XMLEvent
	case "beast":
		return framing.Beast
	}
	return framing.Line
}

// DecoderFor builds the decoder section for a format.
func DecoderFor(format, encoding, defaultSource string, options json.RawMessage) (decoder.Config, error) {
	if format == "" || format == "sentinel" {
		opts, _ := json.Marshal(map[string]string{"encoding": encoding, "defaultSource": defaultSource})
		return decoder.Config{Name: "sentinel", Options: opts}, nil
	}
	return decoder.Config{Name: format, Options: options}, nil
}

func intOr(p *int, def int) int {
	if p == nil {
		return def
	}
	return *p
}

// New builds the driver.
func New(spec driver.Spec, deps driver.Deps) (driver.Driver, error) {
	var c Config
	if err := driver.DecodeConfig(spec, &c); err != nil {
		return nil, err
	}
	dec, err := DecoderFor(c.Format, c.Encoding, c.DefaultSource, c.DecoderOptions)
	if err != nil {
		return nil, err
	}
	tcfg := transport.Config{TLS: c.TLS, MaxFrameBytes: c.MaxMessageBytes, MaxMessagesPerSec: intOr(c.MaxMessagesPerSec, 1000)}
	if tcfg.MaxFrameBytes == 0 {
		tcfg.MaxFrameBytes = 65536
	}
	d := &Driver{tls: c.TLS != nil && c.TLS.Enabled, allow: len(c.Allow)}
	switch c.Mode {
	case "listen":
		if c.Port < 1024 || c.Port > 65535 {
			return nil, fmt.Errorf("tcp listen: port must be 1024-65535, not %d", c.Port)
		}
		tcfg.Kind, tcfg.Address = "tcpserver", net.JoinHostPort(c.Address, strconv.Itoa(c.Port))
		tcfg.Allow, tcfg.MaxConnections = c.Allow, intOr(c.MaxConnections, 50)
		d.detailKey, d.target = "listen", map[string]any{"protocol": "tcp", "host": deps.ListenerHost, "port": c.Port}
	case "connect":
		if c.Host == "" || c.Port < 1 || c.Port > 65535 {
			return nil, fmt.Errorf("tcp connect: host and port 1-65535 are required")
		}
		tcfg.Kind, tcfg.Address = "tcpclient", net.JoinHostPort(c.Host, strconv.Itoa(c.Port))
		d.detailKey, d.target = "connect", map[string]any{"protocol": "tcp", "host": c.Host, "port": c.Port}
	default:
		return nil, fmt.Errorf("tcp: mode must be listen or connect, not %q", c.Mode)
	}
	// NewFromParts applies the TLS secrets (tlsCert, tlsKey, clientCa, serverCa).
	s, err := stream.NewFromParts(spec, deps, tcfg, FramingFor(c.Format, c.Encoding, c.Framing), dec)
	if err != nil {
		return nil, err
	}
	d.Driver = s
	return d, nil
}

// Health adds where the connection listens or connects, the format, TLS
// and (when listening) the allowlist to the stream health.
func (d *Driver) Health() driver.Health {
	h := d.Driver.Health()
	h.Detail[d.detailKey] = d.target
	h.Detail["format"] = h.Detail["decoder"]
	h.Detail["tls"] = d.tls
	if d.detailKey == "listen" {
		h.Detail["allowlist"] = allowDetail(d.allow)
	}
	return h
}

// allowDetail is "open" for an empty allowlist, else the entry count.
func allowDetail(n int) any {
	if n == 0 {
		return "open"
	}
	return n
}
