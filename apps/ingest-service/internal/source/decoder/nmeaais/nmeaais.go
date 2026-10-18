// Package nmeaais decodes AIS messages carried in NMEA VDM/VDO sentences.
// The sentence layer (fields, checksum, multi-fragment reassembly per
// sender and sequence id, stale fragments dropped after 10 seconds) is
// ours; the message bodies are decoded by github.com/BertoldVdb/go-ais and
// mapped to observations by package aismap, which also carries each
// vessel's static detail (name, type, destination) into its later
// position reports.
package nmeaais

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	ais "github.com/BertoldVdb/go-ais"

	"github.com/sentinel/ingest-service/internal/obs"
	"github.com/sentinel/ingest-service/internal/source/decoder"
	"github.com/sentinel/ingest-service/internal/source/decoder/aismap"
	"github.com/sentinel/ingest-service/internal/source/transport"
)

// Name is the decoder name.
const Name = "nmea-ais"

const fragmentTTL = 10 * time.Second

func init() {
	decoder.Register(decoder.Definition{
		Name: Name, Description: "AIS position, static and aid-to-navigation reports in NMEA VDM/VDO sentences (AIS-catcher, rtl-ais, receivers).",
		Source: "AIS", ContentType: "text/plain", New: New,
	})
}

type fragments struct {
	parts []string
	at    time.Time
}

// Decoder is the nmea-ais decoder with per-feed fragment and vessel state.
type Decoder struct {
	codec   *ais.Codec
	pending map[string]*fragments
	mapper  *aismap.Mapper
}

// New builds a decoder; it takes no options.
func New(json.RawMessage) (decoder.Decoder, error) {
	// Some class A transponders send a 420-bit type 5; accept it.
	codec := ais.CodecNew(false, true)
	codec.DropSpace = true
	return &Decoder{codec: codec, pending: map[string]*fragments{}, mapper: aismap.NewMapper(Name)}, nil
}

// Decode parses one sentence.
func (d *Decoder) Decode(f transport.Frame) ([]*obs.Observation, error) {
	return decoder.One(d.parse(f))
}

// Close drops buffered fragments and remembered vessels.
func (d *Decoder) Close() error {
	d.pending = map[string]*fragments{}
	d.mapper.Reset()
	return nil
}

func (d *Decoder) parse(f transport.Frame) (*obs.Observation, error) {
	line := strings.TrimSpace(string(f.Payload))
	if line == "" {
		return nil, errors.New("blank sentence")
	}
	if idx := strings.Index(line, "*"); idx > 0 {
		line = line[:idx]
	}
	fields := strings.Split(line, ",")
	if len(fields) < 7 {
		return nil, fmt.Errorf("AIS sentence too short: %d fields", len(fields))
	}
	if !strings.HasSuffix(fields[0], "VDM") && !strings.HasSuffix(fields[0], "VDO") {
		return nil, fmt.Errorf("not an AIS VDM/VDO sentence: %s", fields[0])
	}
	payload := fields[5]
	if payload == "" {
		return nil, errors.New("empty AIS payload")
	}
	now := f.ReceivedAt
	if now.IsZero() {
		now = time.Now()
	}
	if fields[1] == "1" {
		return d.decodePayload(payload, now)
	}

	total, err := strconv.Atoi(fields[1])
	if err != nil || total < 2 {
		return nil, fmt.Errorf("invalid AIS fragment count: %s", fields[1])
	}
	idx, err := strconv.Atoi(fields[2])
	if err != nil || idx < 1 || idx > total {
		return nil, fmt.Errorf("invalid AIS fragment number: %s", fields[2])
	}
	d.evictFragments(now)
	key := f.Remote + "|" + fields[3]
	fr, ok := d.pending[key]
	if !ok || len(fr.parts) != total {
		fr = &fragments{parts: make([]string, total), at: now}
		d.pending[key] = fr
	}
	fr.parts[idx-1] = payload
	for _, p := range fr.parts {
		if p == "" {
			return nil, decoder.Skip("fragment_buffered")
		}
	}
	delete(d.pending, key)
	return d.decodePayload(strings.Join(fr.parts, ""), now)
}

func (d *Decoder) evictFragments(now time.Time) {
	for k, fr := range d.pending {
		if now.Sub(fr.at) > fragmentTTL {
			delete(d.pending, k)
		}
	}
}

// sweepNames drops stale part A names, at most once a minute.
func (d *Decoder) decodePayload(payload string, now time.Time) (*obs.Observation, error) {
	bits := decodeArmor(payload)
	if len(bits) < 6 {
		return nil, fmt.Errorf("AIS payload too short: %d bits", len(bits))
	}
	msgType := 0
	for _, b := range bits[:6] {
		msgType = msgType<<1 | int(b)
	}
	pkt := d.codec.DecodePacket(bits)
	if pkt == nil {
		return nil, fmt.Errorf("invalid AIS type %d payload: %d bits", msgType, len(bits))
	}
	return d.mapper.FromPacket(pkt, now)
}

// decodeArmor unpacks the 6-bit ASCII armor into one bit per byte, the
// form go-ais decodes.
func decodeArmor(payload string) []byte {
	bits := make([]byte, 0, len(payload)*6)
	for _, c := range payload {
		v := int(c) - 48
		if v > 40 {
			v -= 8
		}
		for i := 5; i >= 0; i-- {
			bits = append(bits, byte((v>>i)&1))
		}
	}
	return bits
}
