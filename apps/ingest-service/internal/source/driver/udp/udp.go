// Package udp is the driver for external systems that send datagrams to
// Sentinel on a port the operator chooses, on a listener replica. It builds
// the stream pipeline (the udp transport, a framing, a decoder) from a flat
// config.
package udp

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"net"
	"strconv"

	"github.com/sentinel/ingest-service/internal/source/driver"
	"github.com/sentinel/ingest-service/internal/source/driver/stream"
	"github.com/sentinel/ingest-service/internal/source/driver/tcp"
	"github.com/sentinel/ingest-service/internal/source/transport"
	"github.com/sentinel/ingest-service/internal/source/transport/framing"
)

// ID is the driver id.
const ID = "udp"

//go:embed schema.json
var schema []byte

func init() {
	driver.Register(driver.Definition{
		ID: ID, Name: "UDP",
		Description:  "An external system sends datagrams to Sentinel on a port you choose. Sentinel's own protobuf format or a device format.",
		Direction:    driver.DirectionIn,
		Capabilities: []string{driver.CapabilityListen},
		ConfigSchema: schema,
		Listen:       listen,
		New:          New,
	})
}

// Config is the feed configuration.
type Config struct {
	Port              int             `json:"port"`
	Address           string          `json:"address"`
	Format            string          `json:"format"`
	Encoding          string          `json:"encoding"`
	DecoderOptions    json.RawMessage `json:"decoderOptions"`
	DefaultSource     string          `json:"defaultSource"`
	Allow             []string        `json:"allow"`
	MaxMessageBytes   int             `json:"maxMessageBytes"`
	MaxMessagesPerSec *int            `json:"maxMessagesPerSec"`
}

// Driver wraps the stream driver to report where it listens.
type Driver struct {
	*stream.Driver
	target map[string]any
	allow  int
}

// listen reports the port a udp feed binds. A udp feed always listens: a
// missing or non-numeric port reports port 0, so the registry's port rules
// refuse it instead of skipping the feed. Only the port is read, so a
// mistyped unrelated field (allow, say) cannot turn a listener into a
// non-listener and skip the checks on it.
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
	return "udp", port, true
}

// framingFor is the framing a format and encoding imply. The udp transport
// splits a datagram into lines for "line" and keeps it whole for "message":
// binary protobuf, CoT XML and Link 16 are one message per datagram; JSON,
// NMEA and SBS may carry several lines.
func framingFor(format, encoding string) string {
	switch format {
	case "", "sentinel":
		if encoding == "json" {
			return framing.Line
		}
		return framing.Message
	case "cot", "link16":
		return framing.Message
	}
	return framing.Line
}

// New builds the driver.
func New(spec driver.Spec, deps driver.Deps) (driver.Driver, error) {
	var c Config
	if err := driver.DecodeConfig(spec, &c); err != nil {
		return nil, err
	}
	if c.Port < 1024 || c.Port > 65535 {
		return nil, fmt.Errorf("udp: port must be 1024-65535, not %d", c.Port)
	}
	dec, err := tcp.DecoderFor(c.Format, c.Encoding, c.DefaultSource, c.DecoderOptions)
	if err != nil {
		return nil, err
	}
	rate := 1000
	if c.MaxMessagesPerSec != nil {
		rate = *c.MaxMessagesPerSec
	}
	tcfg := transport.Config{Kind: "udp", Address: net.JoinHostPort(c.Address, strconv.Itoa(c.Port)), Allow: c.Allow,
		MaxFrameBytes: c.MaxMessageBytes, MaxMessagesPerSec: rate}
	if tcfg.MaxFrameBytes == 0 {
		tcfg.MaxFrameBytes = 65507
	}
	s, err := stream.NewFromParts(spec, deps, tcfg, framingFor(c.Format, c.Encoding), dec)
	if err != nil {
		return nil, err
	}
	return &Driver{Driver: s, allow: len(c.Allow), target: map[string]any{"protocol": "udp", "host": deps.ListenerHost, "port": c.Port}}, nil
}

// Health adds where the connection listens, the format, TLS (never, for
// datagrams) and the allowlist to the stream health.
func (d *Driver) Health() driver.Health {
	h := d.Driver.Health()
	h.Detail["listen"] = d.target
	h.Detail["format"] = h.Detail["decoder"]
	h.Detail["tls"] = false
	if d.allow == 0 {
		h.Detail["allowlist"] = "open"
	} else {
		h.Detail["allowlist"] = d.allow
	}
	return h
}
