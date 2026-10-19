// Package stream is the driver for message streams: a transport delivers
// frames, a decoder turns them into observations. Backpressure between
// the transport and the single decode goroutine follows a per-transport
// default (TCP and WebSocket block, UDP drops, MQTT blocks briefly then
// drops, STOMP nacks) that the feed config can override.
package stream

import (
	"context"
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

	"github.com/sentinel/ingest-service/internal/obs"
	"github.com/sentinel/ingest-service/internal/source/decoder"
	"github.com/sentinel/ingest-service/internal/source/driver"
	"github.com/sentinel/ingest-service/internal/source/transport"
	"github.com/sentinel/ingest-service/internal/source/transport/framing"
)

// ID is the driver id.
const ID = "stream"

//go:embed schema.json
var schema []byte

// FrameBuffer is the number of frames queued between transport and
// decoder; set from configuration before feeds start.
var FrameBuffer = 1024

// FileDirs are the folders the TLS files of a transport (certFile, keyFile,
// caFile, clientCaFile) may be read from, as the secret references' files
// are; set from configuration before feeds start. Empty restricts nothing.
var FileDirs []string

const mqttBlockFor = 2 * time.Second

// DefaultMaxConnections is how many connections a tcpserver transport accepts
// at once when the config sets no maxConnections; an explicit 0 means
// unlimited. Each connection costs a descriptor, a goroutine and a frame
// buffer, which is why a listener is capped unless told otherwise. The tcp
// driver uses the same number, and both schemas state it as the default.
const DefaultMaxConnections = 50

func init() {
	driver.Register(driver.Definition{
		ID: ID, Name: "Message stream",
		Description:  "Reports delivered over TCP, UDP, WebSocket, MQTT or STOMP in a supported wire format.",
		Capabilities: []string{},
		SecretFields: []string{"password", "tlsCert", "tlsKey", "clientCa", "serverCa"},
		ConfigSchema: schema,
		New:          New,
		Listen:       listenOf,
	})
}

// Config is the stream feed configuration.
type Config struct {
	Transport json.RawMessage `json:"transport"`
	Framing   string          `json:"framing"`
	Decoder   decoder.Config  `json:"decoder"`
}

// Driver is one stream feed.
type Driver struct {
	spec      driver.Spec
	logger    *zap.Logger
	tcfg      transport.Config
	tdef      transport.Definition
	transport transport.Transport
	noter     transport.MalformedNoter // the transport, if it tells its senders apart
	decoder   decoder.Decoder
	ddef      decoder.Definition
	policy    string

	malformed atomic.Int64 // frames the decoder dead-lettered

	mu       sync.Mutex
	lastErr  string
	decoding bool
}

// New validates the config and builds the transport and decoder.
func New(spec driver.Spec, deps driver.Deps) (driver.Driver, error) {
	var cfg Config
	if err := driver.DecodeConfig(spec, &cfg); err != nil {
		return nil, err
	}
	tcfg, err := transport.ParseConfig(cfg.Transport)
	if err != nil {
		return nil, err
	}
	if err := checkTLSFiles(tcfg.TLS); err != nil {
		return nil, err
	}
	// A tcpserver is capped unless the config says otherwise. This is decided
	// here, not in NewFromParts, because only the raw config can tell an
	// absent maxConnections from an explicit 0 (unlimited).
	if tcfg.Kind == "tcpserver" && !setsMaxConnections(cfg.Transport) {
		tcfg.MaxConnections = DefaultMaxConnections
	}
	d, err := NewFromParts(spec, deps, tcfg, cfg.Framing, cfg.Decoder)
	if err != nil {
		return nil, err // not d: a nil *Driver would be a non-nil driver.Driver
	}
	return d, nil
}

// setsMaxConnections reports whether the transport object sets maxConnections
// to a value. Presence is decided the way the decoder finds the field, by
// decoding into a pointer: the key matches ignoring case, the last spelling
// wins, and null counts as unset, as it does for the tcp driver's pointer
// fields.
func setsMaxConnections(raw json.RawMessage) bool {
	var probe struct {
		MaxConnections *int `json:"maxConnections"`
	}
	return json.Unmarshal(raw, &probe) == nil && probe.MaxConnections != nil
}

// checkTLSFiles refuses a TLS file outside FileDirs, after following links, so
// that a link inside an allowed folder cannot lead out of it.
func checkTLSFiles(t *transport.TLS) error {
	if t == nil || len(FileDirs) == 0 {
		return nil
	}
	for _, f := range []struct{ field, path string }{{"certFile", t.CertFile}, {"keyFile", t.KeyFile}, {"caFile", t.CAFile}, {"clientCaFile", t.ClientCAFile}} {
		if f.path == "" {
			continue
		}
		if _, err := transport.ResolveUnder(f.path, FileDirs); err != nil {
			if errors.Is(err, transport.ErrOutsideDirs) {
				return fmt.Errorf("tls.%s: file %s is not under an allowed folder (INGEST_SECRET_FILE_DIRS decides)", f.field, f.path)
			}
			return fmt.Errorf("tls.%s: %w", f.field, err)
		}
	}
	return nil
}

// NewFromParts builds a stream driver from an already parsed transport
// config, a framing name and a decoder section. The tcp and udp drivers
// use it to assemble the same pipeline from a flatter config; it applies
// the same framing checks as New.
func NewFromParts(spec driver.Spec, deps driver.Deps, tcfg transport.Config, framingName string, dec decoder.Config) (*Driver, error) {
	tdef, ok := transport.Lookup(tcfg.Kind)
	if !ok {
		return nil, fmt.Errorf("unknown transport %q (known: %v)", tcfg.Kind, transport.Kinds())
	}
	if !framing.Known(framingName) {
		return nil, fmt.Errorf("unknown framing %q", framingName)
	}
	switch tdef.Type {
	case transport.KindStream:
		if framingName == framing.Message {
			return nil, fmt.Errorf("transport %s is a byte stream and needs a framer (line, delimited, lengthprefix, beast, xmlevent)", tcfg.Kind)
		}
		tcfg.Framing = framingName
	case transport.KindMessage:
		if framingName != "" && framingName != framing.Message && !(tcfg.Kind == "udp" && framingName == framing.Line) {
			return nil, fmt.Errorf("transport %s delivers whole messages; framing must be \"message\"", tcfg.Kind)
		}
		tcfg.Framing = framingName
	}
	ApplySecrets(&tcfg, spec.Secrets)
	tcfg.Logger = deps.Logger
	t, err := tdef.New(tcfg)
	if err != nil {
		return nil, err
	}
	if dec.Name == "" {
		return nil, errors.New("decoder.name is required")
	}
	ddef, ok := decoder.Lookup(dec.Name)
	if !ok {
		return nil, fmt.Errorf("unknown decoder %q (known: %v)", dec.Name, decoder.Names())
	}
	decoded, err := ddef.New(dec.Options)
	if err != nil {
		return nil, err
	}
	policy := spec.Backpressure
	if policy == "" {
		policy = defaultPolicy(tcfg.Kind)
	}
	switch policy {
	case driver.BackpressureBlock, driver.BackpressureDrop, driver.BackpressureNack:
	default:
		return nil, fmt.Errorf("unknown backpressure policy %q", policy)
	}
	noter, _ := t.(transport.MalformedNoter)
	return &Driver{spec: spec, logger: deps.Logger, tcfg: tcfg, tdef: tdef, transport: t, noter: noter, decoder: decoded, ddef: ddef, policy: policy}, nil
}

// ApplySecrets copies resolved secret references into the transport
// config: the password, and TLS material as PEM.
func ApplySecrets(tcfg *transport.Config, secrets map[string]string) {
	tcfg.Password = secrets["password"]
	pemOf := func(k string) []byte {
		if v := secrets[k]; v != "" {
			return []byte(v)
		}
		return nil
	}
	tcfg.CertPEM, tcfg.KeyPEM = pemOf("tlsCert"), pemOf("tlsKey")
	tcfg.ClientCAPEM, tcfg.CAPEM = pemOf("clientCa"), pemOf("serverCa")
}

func defaultPolicy(kind string) string {
	switch kind {
	case "udp":
		return driver.BackpressureDrop
	case "stomp":
		return driver.BackpressureNack
	}
	return driver.BackpressureBlock
}

// listenOf reports the network and port a listening stream transport binds.
// A tcpserver or udp transport always listens: an address with no numeric
// port (no port at all, or a service name) reports port 0, so the registry's
// port rules refuse it instead of it surfacing at bind time. Only
// non-listening kinds and config that does not decode report ok=false. Only
// the kind and address are read, so a mistyped unrelated field (allow, say)
// cannot turn a listener into a non-listener and skip the checks on it.
func listenOf(raw json.RawMessage) (string, int, bool) {
	var cfg struct {
		Transport struct {
			Kind    string `json:"kind"`
			Address string `json:"address"`
		} `json:"transport"`
	}
	if json.Unmarshal(raw, &cfg) != nil {
		return "", 0, false
	}
	var network string
	switch cfg.Transport.Kind {
	case "tcpserver":
		network = "tcp"
	case "udp":
		network = "udp"
	default:
		return "", 0, false
	}
	_, p, err := net.SplitHostPort(cfg.Transport.Address)
	if err != nil {
		return network, 0, true
	}
	// Digits only, as the transports read a port: Atoi would take "+8080" for
	// 8080 and let the uniqueness check count a port the transport refuses.
	port, err := strconv.ParseUint(p, 10, 16)
	if err != nil {
		return network, 0, true
	}
	return network, int(port), true
}

// Run pumps frames from the transport through the decoder into the sink.
func (d *Driver) Run(ctx context.Context, sink driver.Sink) error {
	frames := make(chan transport.Frame, FrameBuffer)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	decodeDone := make(chan struct{})
	go func() {
		defer close(decodeDone)
		d.decodeLoop(ctx, frames, sink)
	}()

	emit := d.emitter(frames, sink)
	err := d.transport.Run(ctx, emit)
	cancel()
	<-decodeDone
	_ = d.decoder.Close()
	if err != nil && ctx.Err() == nil {
		return fmt.Errorf("transport %s: %w", d.tcfg.Kind, err)
	}
	return err
}

// emitter applies the backpressure policy between transport and decoder.
func (d *Driver) emitter(frames chan<- transport.Frame, sink driver.Sink) transport.Emit {
	switch d.policy {
	case driver.BackpressureDrop:
		return func(ctx context.Context, f transport.Frame) error {
			select {
			case frames <- f:
				return nil
			default:
				sink.Skipped("dropped_backpressure")
				return nil
			}
		}
	case driver.BackpressureNack:
		return func(ctx context.Context, f transport.Frame) error {
			select {
			case frames <- f:
				return nil
			default:
				sink.Skipped("nacked_backpressure")
				return transport.ErrBackpressure
			}
		}
	}
	// block: MQTT waits a bounded time then drops (the broker would
	// otherwise buffer unboundedly); stream transports block for real so
	// TCP flow control reaches the sender.
	if d.tcfg.Kind == "mqtt" {
		return func(ctx context.Context, f transport.Frame) error {
			select {
			case frames <- f:
				return nil
			default:
			}
			start := time.Now()
			timer := time.NewTimer(mqttBlockFor)
			defer timer.Stop()
			select {
			case frames <- f:
				sink.Blocked(time.Since(start))
				return nil
			case <-timer.C:
				sink.Blocked(time.Since(start))
				sink.Skipped("dropped_backpressure")
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
	return func(ctx context.Context, f transport.Frame) error {
		select {
		case frames <- f:
			return nil
		default:
		}
		start := time.Now()
		select {
		case frames <- f:
			sink.Blocked(time.Since(start))
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (d *Driver) decodeLoop(ctx context.Context, frames <-chan transport.Frame, sink driver.Sink) {
	for {
		select {
		case <-ctx.Done():
			return
		case f := <-frames:
			d.decodeOne(ctx, f, sink)
		}
	}
}

func (d *Driver) decodeOne(ctx context.Context, f transport.Frame, sink driver.Sink) {
	out, err := d.safeDecode(f)
	// A WebSocket feed's remote is its URL, which can carry an API key, so
	// wherever the remote is recorded (the sender of a dead letter, the sensor
	// an observation defaults to) it is in its redacted form.
	remote := transport.RedactRemote(f.Remote)
	if err != nil {
		if reason := decoder.SkipReason(err); reason != "" {
			sink.Skipped(reason)
			return
		}
		msg := err.Error()
		d.mu.Lock()
		d.lastErr = msg
		d.mu.Unlock()
		reason := "parse_error"
		if r := decoder.RejectReason(err); r != "" {
			reason = r
		}
		if errors.Is(err, errPanic) {
			reason = "panic"
		}
		d.malformed.Add(1)
		if d.noter != nil {
			d.noter.NoteMalformed(remote, reason, msg)
		}
		if ps, ok := sink.(driver.ProvenanceSink); ok {
			ps.DeadLetterFrom(f.Payload, reason, err, driver.Provenance{Decoder: d.ddef.Name, Sender: remote, ReceivedAt: f.ReceivedAt})
		} else {
			sink.DeadLetter(f.Payload, reason, err)
		}
		return
	}
	for _, o := range out {
		if o.Decoder == "" {
			o.Decoder = d.ddef.Name
		}
		if o.Source == "" {
			o.Source = d.ddef.Source
		}
		if o.ReceivedAt.IsZero() {
			o.ReceivedAt = f.ReceivedAt
		}
		if o.SensorID == "" {
			o.SensorID = remote
		}
		if o.RawContentType == "" {
			o.RawContentType = d.ddef.ContentType
		}
		// An observation is the whole frame's unless its decoder kept the part
		// it came from (an element of a JSON array).
		if len(o.Raw) == 0 {
			o.Raw = f.Payload
		}
		if err := sink.Emit(ctx, o); err != nil {
			return
		}
	}
	sink.Succeeded(len(out))
}

var errPanic = errors.New("decoder panic")

type panicError struct {
	value any
}

func (e *panicError) Error() string   { return fmt.Sprintf("decoder panic: %v", e.value) }
func (e *panicError) Is(t error) bool { return t == errPanic }

func (d *Driver) safeDecode(f transport.Frame) (out []*obs.Observation, err error) {
	defer func() {
		if r := recover(); r != nil {
			out, err = nil, &panicError{value: r}
			d.logger.Error("decoder panic recovered", zap.String("feed", d.spec.FeedID), zap.String("decoder", d.ddef.Name), zap.Any("panic", r))
		}
	}()
	return d.decoder.Decode(f)
}

// Health reports the transport state.
func (d *Driver) Health() driver.Health {
	st := d.transport.State()
	d.mu.Lock()
	lastDecode := d.lastErr
	d.mu.Unlock()
	h := driver.Health{Connected: st.Connected, LastError: st.LastError, Detail: map[string]any{
		"transport": d.tcfg.Kind, "decoder": d.ddef.Name, "framing": d.tcfg.Framing, "backpressure": d.policy,
		"connected": st.Connected, "connections": st.Connections,
	}}
	h.Detail["malformed"] = d.malformed.Load()
	if lastDecode != "" {
		h.Detail["lastDecodeError"] = lastDecode
	}
	for k, v := range st.Detail {
		h.Detail[k] = v
	}
	return h
}
