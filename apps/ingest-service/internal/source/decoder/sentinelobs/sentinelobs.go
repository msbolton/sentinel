// Package sentinelobs decodes Sentinel's own wire contract,
// sentinel.v1.Observation, sent by external systems: binary protobuf (one
// message per frame) or its protobuf JSON form (one message per line, or a
// JSON array of up to 100 messages on a line).
package sentinelobs

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/sentinel/ingest-service/internal/obs"
	"github.com/sentinel/ingest-service/internal/source/decoder"
	"github.com/sentinel/ingest-service/internal/source/transport"
	"github.com/sentinel/ingest-service/internal/wire"
	sentinelv1 "github.com/sentinel/proto-gen/sentinel/v1"
)

// Name is the decoder name.
const Name = "sentinel"

func init() {
	decoder.Register(decoder.Definition{
		Name:        Name,
		Description: fmt.Sprintf("Sentinel's own sentinel.v1.Observation, as binary protobuf or its JSON form. A JSON line may also hold a JSON array of up to %d observations.", maxBatch),
		ContentType: "application/x-protobuf",
		New:         New,
	})
}

// Options are the decoder options.
type Options struct {
	Encoding      string `json:"encoding"`      // protobuf (default) or json
	DefaultSource string `json:"defaultSource"` // EntitySource for messages that leave source unset
}

// Decoder decodes one frame at a time.
type Decoder struct {
	json          bool
	defaultSource string
}

var unmarshalJSON = protojson.UnmarshalOptions{DiscardUnknown: true}

// maxBatch is the most elements one JSON array line may hold. A listener's
// maxMessagesPerSec counts frames, so a batch is one frame to it however many
// observations it carries. What bounds the observations in a frame is this cap,
// which is checked before any element is decoded, and maxMessageBytes, which
// the transport has already applied to the frame.
const maxBatch = 100

// New builds a decoder from its options.
func New(raw json.RawMessage) (decoder.Decoder, error) {
	var opts Options
	if len(bytes.TrimSpace(raw)) > 0 {
		if err := json.Unmarshal(raw, &opts); err != nil {
			return nil, fmt.Errorf("sentinel decoder options: %w", err)
		}
	}
	switch opts.Encoding {
	case "", "protobuf":
		return &Decoder{defaultSource: opts.DefaultSource}, nil
	case "json":
		return &Decoder{json: true, defaultSource: opts.DefaultSource}, nil
	}
	return nil, fmt.Errorf("sentinel decoder: encoding must be protobuf or json, not %q", opts.Encoding)
}

// Decode turns a frame into observations: one for binary; for JSON one per
// non-blank line, or, for a line that starts with [, one per element of the
// array. A bad line or element fails the frame, whole.
func (d *Decoder) Decode(f transport.Frame) ([]*obs.Observation, error) {
	if !d.json {
		var m sentinelv1.Observation
		if err := proto.Unmarshal(f.Payload, &m); err != nil {
			return nil, fmt.Errorf("not a sentinel.v1.Observation: %w", err)
		}
		o, err := d.observation(&m, "application/x-protobuf")
		if err != nil {
			return nil, err
		}
		return []*obs.Observation{o}, nil
	}
	var out []*obs.Observation
	for _, line := range bytes.Split(f.Payload, []byte{'\n'}) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		if line[0] == '[' {
			batch, err := d.decodeArray(line)
			if err != nil {
				return nil, err
			}
			out = append(out, batch...)
			continue
		}
		var m sentinelv1.Observation
		if err := unmarshalJSON.Unmarshal(line, &m); err != nil {
			return nil, fmt.Errorf("not a sentinel.v1.Observation in JSON: %w", err)
		}
		o, err := d.observation(&m, "application/json")
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	if len(out) == 0 {
		return nil, decoder.Skip("empty")
	}
	return out, nil
}

// decodeArray decodes a line that is a JSON array of observations. The array
// is all-or-nothing, and an error says which element it is about, numbered
// from 0 as in jq's .[n]. A nested array is a bad element, not a batch. Each
// observation keeps its own element as its raw payload: the stream driver
// stamps the whole frame only on observations whose decoder left Raw empty, so
// a batch does not repeat its line on each of up to 100 observations.
func (d *Decoder) decodeArray(line []byte) ([]*obs.Observation, error) {
	elements, err := splitArray(line)
	if err != nil {
		return nil, err
	}
	out := make([]*obs.Observation, 0, len(elements))
	for i, raw := range elements {
		var m sentinelv1.Observation
		if err := unmarshalJSON.Unmarshal(raw, &m); err != nil {
			return nil, fmt.Errorf("element %d: not a sentinel.v1.Observation in JSON: %w", i, err)
		}
		o, err := d.observation(&m, "application/json")
		if err != nil {
			// %w keeps a Reject's reason, which the dead letter is filed under.
			return nil, fmt.Errorf("element %d: %w", i, err)
		}
		o.Raw = raw
		out = append(out, o)
	}
	return out, nil
}

// splitArray cuts a JSON array into its raw elements without decoding any as
// an observation. It reads no further than the first element past maxBatch,
// so an array of thousands of tiny elements costs what 101 do and the size
// decision never waits on decoding.
func splitArray(line []byte) ([]json.RawMessage, error) {
	// The decoder says EOF where a value or the closing ] should have been.
	cutShort := func(err error) error {
		if errors.Is(err, io.EOF) {
			return io.ErrUnexpectedEOF
		}
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(line))
	if _, err := dec.Token(); err != nil { // the opening [, which the caller has seen
		return nil, fmt.Errorf("not a JSON array of sentinel.v1.Observation: %w", cutShort(err))
	}
	var elements []json.RawMessage
	for dec.More() {
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return nil, fmt.Errorf("element %d: not a sentinel.v1.Observation in JSON: %w", len(elements), cutShort(err))
		}
		if len(elements) == maxBatch { // a 101st element
			return nil, decoder.Reject("batch_too_large", fmt.Errorf("a JSON array holds at most %d observations", maxBatch))
		}
		elements = append(elements, raw)
	}
	if _, err := dec.Token(); err != nil { // the closing ]
		return nil, fmt.Errorf("not a JSON array of sentinel.v1.Observation: %w", cutShort(err))
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("not a JSON array of sentinel.v1.Observation: unexpected data after the closing ]")
	}
	return elements, nil
}

// Validate turns a received sentinel.v1.Observation into an observation the
// way this decoder does: it refuses one with neither a source entity id nor
// an identity (missing_identity) or with a position outside WGS84
// (bad_position), drops non-finite values, and fills in the source when the
// message has none. Decoder is set to Name; a caller that is not this
// decoder (the grpc driver) sets its own afterwards.
func Validate(m *sentinelv1.Observation, contentType, defaultSource string) (*obs.Observation, error) {
	o := wire.FromProto(m)
	if o.SourceEntityID == "" && len(o.Identities) == 0 {
		return nil, decoder.Reject("missing_identity", errors.New("the message needs source_entity_id or at least one identity"))
	}
	// The source entity id keys the feed-local entity and the observation
	// id, so an identity-only message borrows its first identity: without it
	// every such message would fuse into one entity and share one id.
	if o.SourceEntityID == "" {
		o.SourceEntityID = o.Identities[0].Type + ":" + o.Identities[0].Value
	}
	// Written so only in-range values pass: NaN fails every comparison.
	if o.HasPosition && !(o.Latitude >= -90 && o.Latitude <= 90 && o.Longitude >= -180 && o.Longitude <= 180) {
		return nil, decoder.Reject("bad_position", fmt.Errorf("position %v, %v is outside WGS84 bounds", o.Latitude, o.Longitude))
	}
	dropNonFinite(o)
	if o.Source == "" {
		o.Source = defaultSource
	}
	o.Decoder = Name
	o.RawContentType = contentType
	return o, nil
}

func (d *Decoder) observation(m *sentinelv1.Observation, contentType string) (*obs.Observation, error) {
	return Validate(m, contentType, d.defaultSource)
}

// Close releases nothing.
func (d *Decoder) Close() error { return nil }
