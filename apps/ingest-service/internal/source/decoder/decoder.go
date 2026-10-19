// Package decoder turns frames into observations. A decoder is created per
// feed (it may keep state: AIS fragment buffers, receiver CPR state) and
// fills the object fields of an observation; the feed runner adds
// provenance.
package decoder

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"

	"github.com/sentinel/ingest-service/internal/obs"
	"github.com/sentinel/ingest-service/internal/source/transport"
)

// ErrSkip is returned for a frame that is valid but carries nothing to
// observe (a heartbeat, a partial multi-fragment message, an SBS line
// without a position). It is counted as skipped, not dead-lettered.
var ErrSkip = errors.New("frame carries no observation")

// Skip wraps ErrSkip with a reason for the skipped counter.
func Skip(reason string) error { return &skipError{reason: reason} }

type skipError struct{ reason string }

func (e *skipError) Error() string        { return "skipped: " + e.reason }
func (e *skipError) Is(target error) bool { return target == ErrSkip }

// SkipReason extracts the reason from a Skip error ("" for other errors).
func SkipReason(err error) string {
	var se *skipError
	if errors.As(err, &se) {
		return se.reason
	}
	if errors.Is(err, ErrSkip) {
		return "no_observation"
	}
	return ""
}

// Reject marks a frame that decoded but cannot become an observation; the
// stream driver dead-letters it under reason instead of parse_error.
func Reject(reason string, err error) error { return &rejectError{reason: reason, err: err} }

type rejectError struct {
	reason string
	err    error
}

func (e *rejectError) Error() string { return e.reason + ": " + e.err.Error() }
func (e *rejectError) Unwrap() error { return e.err }

// RejectReason extracts the reason from a Reject error ("" for other errors).
func RejectReason(err error) string {
	var re *rejectError
	if errors.As(err, &re) {
		return re.reason
	}
	return ""
}

// Decoder decodes one frame at a time. Decode is called from one goroutine
// per feed; implementations need no locking of their own state.
type Decoder interface {
	Decode(f transport.Frame) ([]*obs.Observation, error)
	Close() error
}

// Factory builds a decoder from its options (the decoder.options object of
// the feed config, which may be empty).
type Factory func(opts json.RawMessage) (Decoder, error)

// Definition registers one decoder.
type Definition struct {
	Name        string
	Description string
	// Source is the bare EntitySource name this decoder's observations carry.
	Source string
	// ContentType describes the raw payload kept on observations.
	ContentType string
	New         Factory
}

var (
	mu   sync.RWMutex
	defs = map[string]Definition{}
)

// Register adds a decoder; a duplicate name panics (programming error).
func Register(d Definition) {
	mu.Lock()
	defer mu.Unlock()
	if _, dup := defs[d.Name]; dup {
		panic("decoder: duplicate registration of " + d.Name)
	}
	defs[d.Name] = d
}

// Lookup returns the definition for a name.
func Lookup(name string) (Definition, bool) {
	mu.RLock()
	defer mu.RUnlock()
	d, ok := defs[name]
	return d, ok
}

// Names lists the registered decoders in sorted order.
func Names() []string {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]string, 0, len(defs))
	for k := range defs {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// New builds the named decoder.
func New(name string, opts json.RawMessage) (Decoder, error) {
	d, ok := Lookup(name)
	if !ok {
		return nil, fmt.Errorf("unknown decoder %q (known: %v)", name, Names())
	}
	return d.New(opts)
}

// Config is the decoder section of a stream feed config.
type Config struct {
	Name    string          `json:"name"`
	Options json.RawMessage `json:"options"`
}

// Stateless adapts a pure function to the Decoder interface.
type Stateless func(f transport.Frame) ([]*obs.Observation, error)

// Decode calls the function.
func (s Stateless) Decode(f transport.Frame) ([]*obs.Observation, error) { return s(f) }

// Close is a no-op.
func (Stateless) Close() error { return nil }

// One is a helper for decoders that produce a single observation.
func One(o *obs.Observation, err error) ([]*obs.Observation, error) {
	if err != nil {
		return nil, err
	}
	return []*obs.Observation{o}, nil
}
