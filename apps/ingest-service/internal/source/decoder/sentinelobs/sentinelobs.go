// Package sentinelobs decodes Sentinel's own wire contract,
// sentinel.v1.Observation, sent by external systems: binary protobuf (one
// message per frame) or its protobuf JSON form (one message per line).
package sentinelobs

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"

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
		Description: "Sentinel's own sentinel.v1.Observation, as binary protobuf or its JSON form.",
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

// Decode turns a frame into observations: one for binary, one per
// non-blank line for JSON.
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

func (d *Decoder) observation(m *sentinelv1.Observation, contentType string) (*obs.Observation, error) {
	o := wire.FromProto(m)
	if o.SourceEntityID == "" && len(o.Identities) == 0 {
		return nil, decoder.Reject("missing_identity", errors.New("the message needs source_entity_id or at least one identity"))
	}
	if o.HasPosition && (o.Latitude < -90 || o.Latitude > 90 || o.Longitude < -180 || o.Longitude > 180) {
		return nil, decoder.Reject("bad_position", fmt.Errorf("position %v, %v is outside WGS84 bounds", o.Latitude, o.Longitude))
	}
	if o.Source == "" {
		o.Source = d.defaultSource
	}
	o.Decoder = Name
	o.RawContentType = contentType
	return o, nil
}

// Close releases nothing.
func (d *Decoder) Close() error { return nil }
