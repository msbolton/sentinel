// Package nmeagps decodes NMEA 0183 GGA and RMC sentences from a GPS
// receiver. A GPS sentence names no object, so the feed config must supply
// options.sourceEntityId for the platform the receiver is mounted on.
package nmeagps

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/sentinel/ingest-service/internal/obs"
	"github.com/sentinel/ingest-service/internal/source/decoder"
	"github.com/sentinel/ingest-service/internal/source/transport"
)

// Name is the decoder name.
const Name = "nmea-gps"

func init() {
	decoder.Register(decoder.Definition{
		Name: Name, Description: "NMEA 0183 GGA/RMC sentences from a GPS receiver (one platform per feed).",
		Source: "GPS", ContentType: "text/plain", New: New,
	})
}

// Options name the observed platform.
type Options struct {
	SourceEntityID string `json:"sourceEntityId"`
	Name           string `json:"name"`
	EntityType     string `json:"entityType"`
}

// Decoder is the nmea-gps decoder.
type Decoder struct {
	opts Options
}

// New parses the options; sourceEntityId is required.
func New(raw json.RawMessage) (decoder.Decoder, error) {
	var o Options
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &o); err != nil {
			return nil, fmt.Errorf("nmea-gps options: %w", err)
		}
	}
	o.SourceEntityID = strings.TrimSpace(o.SourceEntityID)
	if o.SourceEntityID == "" {
		return nil, errors.New("nmea-gps: options.sourceEntityId is required (the platform the receiver reports)")
	}
	if o.EntityType == "" {
		o.EntityType = obs.EntityUnknown
	}
	o.EntityType = strings.ToUpper(o.EntityType)
	return &Decoder{opts: o}, nil
}

// Decode parses one sentence.
func (d *Decoder) Decode(f transport.Frame) ([]*obs.Observation, error) {
	return decoder.One(d.parse(f.Payload))
}

// Close is a no-op.
func (d *Decoder) Close() error { return nil }

func (d *Decoder) parse(data []byte) (*obs.Observation, error) {
	line := strings.TrimSpace(string(data))
	if line == "" {
		return nil, errors.New("blank sentence")
	}
	if idx := strings.Index(line, "*"); idx > 0 {
		line = line[:idx]
	}
	fields := strings.Split(line, ",")
	if len(fields) < 2 || !strings.HasPrefix(fields[0], "$") {
		return nil, errors.New("invalid NMEA sentence")
	}
	typ := fields[0]
	if len(typ) >= 3 {
		typ = typ[len(typ)-3:]
	}
	o := &obs.Observation{
		Decoder: Name, Source: "GPS", SourceEntityID: d.opts.SourceEntityID, DisplayName: d.opts.Name,
		EntityType: d.opts.EntityType, TrackEnvironment: obs.EnvUnknown,
	}
	if o.DisplayName == "" {
		o.DisplayName = d.opts.SourceEntityID
	}
	switch typ {
	case "GGA":
		if len(fields) < 10 {
			return nil, fmt.Errorf("GGA sentence too short: %d fields", len(fields))
		}
		if strings.TrimSpace(fields[6]) == "0" {
			return nil, decoder.Skip("no_fix")
		}
		lat, err := coord(fields[2], fields[3])
		if err != nil {
			return nil, fmt.Errorf("GGA latitude: %w", err)
		}
		lon, err := coord(fields[4], fields[5])
		if err != nil {
			return nil, fmt.Errorf("GGA longitude: %w", err)
		}
		o.SetPosition(lat, lon)
		if alt, err := strconv.ParseFloat(fields[9], 64); err == nil {
			o.Altitude = obs.Float(alt)
		}
		o.SourceTimestamp = timeOfDay(fields[1])
	case "RMC":
		if len(fields) < 10 {
			return nil, fmt.Errorf("RMC sentence too short: %d fields", len(fields))
		}
		if fields[2] != "A" {
			return nil, decoder.Skip("fix_void")
		}
		lat, err := coord(fields[3], fields[4])
		if err != nil {
			return nil, fmt.Errorf("RMC latitude: %w", err)
		}
		lon, err := coord(fields[5], fields[6])
		if err != nil {
			return nil, fmt.Errorf("RMC longitude: %w", err)
		}
		o.SetPosition(lat, lon)
		if v, err := strconv.ParseFloat(fields[7], 64); err == nil {
			o.SpeedKnots = obs.Float(v)
		}
		if v, err := strconv.ParseFloat(fields[8], 64); err == nil {
			o.Course = obs.Float(v)
			o.Heading = obs.Float(v)
		}
		o.SourceTimestamp = dateTime(fields[1], fields[9])
	default:
		return nil, decoder.Skip("sentence_" + strings.ToLower(typ))
	}
	return o, nil
}

// coord converts DDDMM.MMMM plus hemisphere to decimal degrees.
func coord(c, hemisphere string) (float64, error) {
	if c == "" {
		return 0, errors.New("empty coordinate")
	}
	val, err := strconv.ParseFloat(c, 64)
	if err != nil {
		return 0, fmt.Errorf("coordinate %q: %w", c, err)
	}
	degrees := float64(int(val / 100))
	minutes := val - degrees*100
	dec := degrees + minutes/60.0
	if hemisphere == "S" || hemisphere == "W" {
		dec = -dec
	}
	return dec, nil
}

func timeOfDay(s string) time.Time {
	if len(s) < 6 {
		return time.Time{}
	}
	h, _ := strconv.Atoi(s[0:2])
	m, _ := strconv.Atoi(s[2:4])
	sec, _ := strconv.Atoi(s[4:6])
	now := time.Now().UTC()
	return time.Date(now.Year(), now.Month(), now.Day(), h, m, sec, 0, time.UTC)
}

func dateTime(t, d string) time.Time {
	if len(t) < 6 || len(d) < 6 {
		return time.Time{}
	}
	h, _ := strconv.Atoi(t[0:2])
	m, _ := strconv.Atoi(t[2:4])
	sec, _ := strconv.Atoi(t[4:6])
	day, _ := strconv.Atoi(d[0:2])
	month, _ := strconv.Atoi(d[2:4])
	year, _ := strconv.Atoi(d[4:6])
	return time.Date(2000+year, time.Month(month), day, h, m, sec, 0, time.UTC)
}
