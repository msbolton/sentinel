// Package driver defines what a feed driver is: something that, given a
// feed's configuration, produces observations into a sink until stopped.
// Stream feeds compose a transport, a framer and a decoder; polled sources
// are drivers in their own right because the area of interest shapes
// their requests and response headers shape their schedule.
package driver

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/sentinel/ingest-service/internal/obs"
)

// Capability names a runtime feature a driver supports.
const (
	// CapabilityArea means the driver polls an area of interest pushed by
	// the API gateway (PUT /feeds/{id}/area) and implements aoi.AreaAware.
	CapabilityArea = "area"
	// CapabilityPoll means the driver polls a remote API on an interval.
	CapabilityPoll = "poll"
	// CapabilityListen means the driver binds a port and must be pinned to
	// one replica.
	CapabilityListen = "listen"
)

// Backpressure policies for stream feeds.
const (
	BackpressureBlock = "block"
	BackpressureDrop  = "drop"
	BackpressureNack  = "nack"
)

// Spec is everything a driver gets about its feed.
type Spec struct {
	FeedID  string
	Driver  string
	Config  json.RawMessage
	Secrets map[string]string
	// Backpressure overrides the transport default (block|drop|nack).
	Backpressure string
}

// Deps are shared services handed to a driver.
type Deps struct {
	Logger *zap.Logger
}

// Sink receives what a driver produces. Implementations are safe for
// concurrent use from the goroutines a driver starts.
type Sink interface {
	// Emit publishes one observation. The sink fills provenance (feed id,
	// driver, receive time, raw payload bounds, id). It blocks while the
	// publisher is saturated and returns ctx.Err() when the run ends.
	Emit(ctx context.Context, o *obs.Observation) error
	// DeadLetter records a payload nothing could decode.
	DeadLetter(raw []byte, reason string, cause error)
	// Skipped counts a frame that produced no observation, by reason.
	Skipped(reason string)
	// Succeeded marks a completed poll or connection round with the number
	// of observations it produced, for health and last-success tracking.
	Succeeded(count int)
	// Blocked accounts time spent waiting on backpressure.
	Blocked(d time.Duration)
}

// Driver produces observations until the context ends.
type Driver interface {
	// Run blocks until ctx is done or an unrecoverable error occurs. A nil
	// return after ctx is done is the normal stop; an error marks the feed
	// as failed and the supervisor retries with backoff.
	Run(ctx context.Context, sink Sink) error
	// Health reports driver-level state merged into feed_status.detail.
	Health() Health
}

// Health is a driver's own view of its condition.
type Health struct {
	Idle      bool   // not polling or not connected, by design (no area, no clients)
	Connected bool   // transport or remote reachable per the last attempt
	LastError string // last transport or poll error; "" when fine
	Detail    map[string]any
}

// Factory builds a driver for a feed.
type Factory func(spec Spec, deps Deps) (Driver, error)

// Direction says which way a driver moves data: in (a source producing
// observations), out (a sink consuming entity and alert events), or both
// over one connection.
type Direction string

const (
	DirectionIn   Direction = "in"
	DirectionOut  Direction = "out"
	DirectionBoth Direction = "both"
)

// Normalize treats an unset direction as a source.
func (d Direction) Normalize() Direction {
	if d == "" {
		return DirectionIn
	}
	return d
}

// Inbound reports whether the direction produces observations.
func (d Direction) Inbound() bool { n := d.Normalize(); return n == DirectionIn || n == DirectionBoth }

// Outbound reports whether the direction consumes events.
func (d Direction) Outbound() bool { n := d.Normalize(); return n == DirectionOut || n == DirectionBoth }

// Valid reports whether d is one of the three directions (or unset).
func (d Direction) Valid() bool {
	switch d {
	case "", DirectionIn, DirectionOut, DirectionBoth:
		return true
	}
	return false
}

// Definition registers one driver with its catalog information.
type Definition struct {
	ID          string
	Name        string
	Description string
	// Source is the EntitySource name written on observations, or "" when
	// the decoder decides (stream).
	Source string
	// Direction is what the driver can do; "" means in. A feed may narrow a
	// "both" driver with config.direction.
	Direction    Direction
	Capabilities []string
	// SecretFields are secret_refs keys the driver expects; they must never
	// appear in config.
	SecretFields []string
	// AreaTTL is how long a pushed area stays valid without a refresh.
	AreaTTL time.Duration
	// ConfigSchema is the JSON Schema the feed config must satisfy.
	ConfigSchema json.RawMessage
	New          Factory
}

// HasCapability reports whether the definition advertises cap.
func (d Definition) HasCapability(cap string) bool {
	for _, c := range d.Capabilities {
		if c == cap {
			return true
		}
	}
	return false
}

var (
	mu   sync.RWMutex
	defs = map[string]Definition{}
)

// Register adds a driver; a duplicate id panics (programming error).
func Register(d Definition) {
	mu.Lock()
	defer mu.Unlock()
	if d.ID == "" || d.New == nil {
		panic("driver: registration needs an id and a factory")
	}
	if _, dup := defs[d.ID]; dup {
		panic("driver: duplicate registration of " + d.ID)
	}
	defs[d.ID] = d
}

// Lookup returns the definition for an id.
func Lookup(id string) (Definition, bool) {
	mu.RLock()
	defer mu.RUnlock()
	d, ok := defs[id]
	return d, ok
}

// All returns every registered definition sorted by id.
func All() []Definition {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]Definition, 0, len(defs))
	for _, d := range defs {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// New builds the driver named by spec.Driver.
func New(spec Spec, deps Deps) (Driver, error) {
	d, ok := Lookup(spec.Driver)
	if !ok {
		return nil, fmt.Errorf("unknown driver %q", spec.Driver)
	}
	if deps.Logger == nil {
		deps.Logger = zap.NewNop()
	}
	return d.New(spec, deps)
}

// DecodeConfig unmarshals spec.Config into v; an empty config is fine.
func DecodeConfig(spec Spec, v any) error {
	if len(spec.Config) == 0 {
		return nil
	}
	if err := json.Unmarshal(spec.Config, v); err != nil {
		return fmt.Errorf("feed %s: decoding config: %w", spec.FeedID, err)
	}
	return nil
}

// Seconds converts a config seconds value with a default.
func Seconds(v int, def time.Duration) time.Duration {
	if v > 0 {
		return time.Duration(v) * time.Second
	}
	return def
}
