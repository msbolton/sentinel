// Package link16 decodes JREAP-C encapsulated Link 16 J-series track
// messages (binary frames; use lengthprefix framing over a stream).
package link16

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"

	"github.com/sentinel/ingest-service/internal/obs"
	"github.com/sentinel/ingest-service/internal/source/decoder"
	"github.com/sentinel/ingest-service/internal/source/transport"
)

// Name is the decoder name.
const Name = "link16"

func init() {
	decoder.Register(decoder.Definition{
		Name: Name, Description: "JREAP-C encapsulated Link 16 J-series track messages (binary).",
		Source: "LINK16", ContentType: "application/octet-stream", New: New,
	})
}

// New builds the decoder; it takes no options.
func New(json.RawMessage) (decoder.Decoder, error) {
	return decoder.Stateless(decode), nil
}

func decode(f transport.Frame) ([]*obs.Observation, error) {
	data := f.Payload
	if len(data) < 16 {
		return nil, fmt.Errorf("JREAP-C message too short for header: %d bytes", len(data))
	}
	if !IsHeader(data) {
		return nil, errors.New("invalid JREAP-C header")
	}
	totalLen := binary.BigEndian.Uint16(data[2:4])
	const trackOffset = 16
	if int(totalLen) < trackOffset+20 || len(data) < trackOffset+20 {
		return nil, fmt.Errorf("JREAP-C payload too short for track data: need %d bytes at offset %d", 20, trackOffset)
	}
	td := data[trackOffset:]
	label := binary.BigEndian.Uint16(td[0:2])
	trackNumber := binary.BigEndian.Uint16(td[4:6])
	lat := float64(int32(binary.BigEndian.Uint32(td[6:10]))) * 90.0 / math.Pow(2, 23)
	lon := float64(int32(binary.BigEndian.Uint32(td[10:14]))) * 180.0 / math.Pow(2, 23)
	alt := float64(int16(binary.BigEndian.Uint16(td[14:16]))) * 3.048
	speed := float64(binary.BigEndian.Uint16(td[16:18])) * 0.1
	heading := float64(binary.BigEndian.Uint16(td[18:20])) * 360.0 / 65536.0

	tn := strconv.Itoa(int(trackNumber))
	o := &obs.Observation{
		Decoder: Name, Source: "LINK16", SourceEntityID: "JTN-" + tn, DisplayName: "JTN " + tn,
		EntityType: entityType(label), TrackEnvironment: environment(label),
		Altitude: obs.Float(alt), Heading: obs.Float(heading), Course: obs.Float(heading), SpeedKnots: obs.Float(speed),
		Link16: &obs.Link16Data{TrackNumber: int(trackNumber), JSeriesLabel: fmt.Sprintf("%04X", label)},
	}
	o.AddIdentity(obs.IdentityLink16TN, tn)
	if lat >= -90 && lat <= 90 && lon >= -180 && lon <= 180 {
		o.SetPosition(lat, lon)
	}
	return []*obs.Observation{o}, nil
}

// IsHeader validates a byte slice as a plausible JREAP-C header: version
// 1-2, message type 1-16, and a length field consistent with the data.
func IsHeader(data []byte) bool {
	if len(data) < 16 {
		return false
	}
	if v := data[0]; v < 1 || v > 2 {
		return false
	}
	if t := data[1]; t < 1 || t > 16 {
		return false
	}
	n := binary.BigEndian.Uint16(data[2:4])
	return n >= 16 && int(n) <= len(data)
}

func entityType(label uint16) string {
	switch {
	case label>>8 == 0x02:
		return obs.EntityAircraft
	case label == 0x0302:
		return obs.EntityVessel
	case label == 0x0305:
		return obs.EntityVehicle
	}
	return obs.EntityUnknown
}

func environment(label uint16) string {
	switch {
	case label>>8 == 0x02:
		return obs.EnvAir
	case label == 0x0302:
		return obs.EnvSeaSurface
	case label == 0x0305:
		return obs.EnvGround
	}
	return obs.EnvUnknown
}
