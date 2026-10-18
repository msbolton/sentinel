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
	"sync"
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

const mqttBlockFor = 2 * time.Second

func init() {
	driver.Register(driver.Definition{
		ID: ID, Name: "Message stream",
		Description:  "Reports delivered over TCP, UDP, WebSocket, MQTT or STOMP in a supported wire format.",
		Capabilities: []string{},
		SecretFields: []string{"password"},
		ConfigSchema: schema,
		New:          New,
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
	decoder   decoder.Decoder
	ddef      decoder.Definition
	policy    string

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
	tdef, ok := transport.Lookup(tcfg.Kind)
	if !ok {
		return nil, fmt.Errorf("unknown transport %q (known: %v)", tcfg.Kind, transport.Kinds())
	}
	if !framing.Known(cfg.Framing) {
		return nil, fmt.Errorf("unknown framing %q", cfg.Framing)
	}
	switch tdef.Type {
	case transport.KindStream:
		if cfg.Framing == framing.Message {
			return nil, fmt.Errorf("transport %s is a byte stream and needs a framer (line, lengthprefix, beast, xmlevent)", tcfg.Kind)
		}
		tcfg.Framing = cfg.Framing
	case transport.KindMessage:
		if cfg.Framing != "" && cfg.Framing != framing.Message && !(tcfg.Kind == "udp" && cfg.Framing == framing.Line) {
			return nil, fmt.Errorf("transport %s delivers whole messages; framing must be \"message\"", tcfg.Kind)
		}
		tcfg.Framing = cfg.Framing
	}
	tcfg.Password = spec.Secrets["password"]
	t, err := tdef.New(tcfg)
	if err != nil {
		return nil, err
	}
	if cfg.Decoder.Name == "" {
		return nil, errors.New("decoder.name is required")
	}
	ddef, ok := decoder.Lookup(cfg.Decoder.Name)
	if !ok {
		return nil, fmt.Errorf("unknown decoder %q (known: %v)", cfg.Decoder.Name, decoder.Names())
	}
	dec, err := ddef.New(cfg.Decoder.Options)
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
	return &Driver{spec: spec, logger: deps.Logger, tcfg: tcfg, tdef: tdef, transport: t, decoder: dec, ddef: ddef, policy: policy}, nil
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
	if err != nil {
		if reason := decoder.SkipReason(err); reason != "" {
			sink.Skipped(reason)
			return
		}
		d.mu.Lock()
		d.lastErr = err.Error()
		d.mu.Unlock()
		reason := "parse_error"
		if r := decoder.RejectReason(err); r != "" {
			reason = r
		}
		if errors.Is(err, errPanic) {
			reason = "panic"
		}
		sink.DeadLetter(f.Payload, reason, err)
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
			o.SensorID = f.Remote
		}
		if o.RawContentType == "" {
			o.RawContentType = d.ddef.ContentType
		}
		o.Raw = f.Payload
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
	if lastDecode != "" {
		h.Detail["lastDecodeError"] = lastDecode
	}
	for k, v := range st.Detail {
		h.Detail[k] = v
	}
	return h
}
