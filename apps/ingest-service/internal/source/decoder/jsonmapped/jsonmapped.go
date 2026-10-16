// Package jsonmapped decodes a JSON object into an observation through a
// field map, so a new JSON API needs configuration rather than code. The
// default map matches the legacy Sentinel position shape (entity_id,
// latitude, longitude, ...), so feeds that sent that shape keep working.
package jsonmapped

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/sentinel/ingest-service/internal/obs"
	"github.com/sentinel/ingest-service/internal/source/decoder"
	"github.com/sentinel/ingest-service/internal/source/transport"
)

// Name is the decoder name.
const Name = "json"

func init() {
	decoder.Register(decoder.Definition{
		Name: Name, Description: "JSON object mapped to an observation through a configurable field map.",
		Source: "UNSPECIFIED", ContentType: "application/json", New: New,
	})
}

// Options configure the field map. Every value is a dotted path into the
// object; an empty value disables the field. Latitude and longitude are
// required for a position; a report without them is published without a
// fix. records names an array to iterate (a batch document); timeFormat
// is a Go layout, "unix" or "unixms" (default RFC 3339 or numeric unix).
type Options struct {
	Records string `json:"records"`
	Fields  Fields `json:"fields"`
	// IdentityType names the identity the id field carries (ICAO24, MMSI,
	// NORAD_ID, COT_UID, ...); empty means feed-local.
	IdentityType string `json:"identityType"`
	TimeFormat   string `json:"timeFormat"`
	// AltitudeUnit is "m" (default) or "ft"; SpeedUnit "kt" (default), "m/s", "km/h", "mph".
	AltitudeUnit string `json:"altitudeUnit"`
	SpeedUnit    string `json:"speedUnit"`
	// EntityType fixes the type when the document has none.
	EntityType string `json:"entityType"`
	// Source fixes the EntitySource name (default UNSPECIFIED).
	Source string `json:"source"`
}

// Fields maps observation fields to JSON paths.
type Fields struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	EntityType       string `json:"entityType"`
	Latitude         string `json:"latitude"`
	Longitude        string `json:"longitude"`
	Altitude         string `json:"altitude"`
	Heading          string `json:"heading"`
	Speed            string `json:"speed"`
	Course           string `json:"course"`
	Timestamp        string `json:"timestamp"`
	TrackEnvironment string `json:"trackEnvironment"`
	Affiliation      string `json:"affiliation"`
	Country          string `json:"country"`
	Callsign         string `json:"callsign"`
	VelocityNorth    string `json:"velocityNorth"`
	VelocityEast     string `json:"velocityEast"`
	VelocityUp       string `json:"velocityUp"`
	CircularError    string `json:"circularError"`
}

// DefaultFields is the legacy Sentinel position shape.
var DefaultFields = Fields{
	ID: "entity_id", Name: "name", EntityType: "entity_type", Latitude: "latitude", Longitude: "longitude",
	Altitude: "altitude", Heading: "heading", Speed: "speed_knots", Course: "course", Timestamp: "timestamp",
	TrackEnvironment: "track_environment", Affiliation: "affiliation", Country: "country_of_origin",
	VelocityNorth: "velocity_north", VelocityEast: "velocity_east", VelocityUp: "velocity_up", CircularError: "circular_error",
}

// Decoder is the json decoder.
type Decoder struct {
	opts Options
}

// New parses the options.
func New(raw json.RawMessage) (decoder.Decoder, error) {
	o := Options{Fields: DefaultFields}
	if len(raw) > 0 {
		var given struct {
			Options
			Fields *Fields `json:"fields"`
		}
		if err := json.Unmarshal(raw, &given); err != nil {
			return nil, fmt.Errorf("json decoder options: %w", err)
		}
		o = given.Options
		o.Fields = DefaultFields
		if given.Fields != nil {
			o.Fields = *given.Fields
		}
	}
	if o.Fields.Latitude == "" || o.Fields.Longitude == "" {
		return nil, errors.New("json decoder: fields.latitude and fields.longitude are required")
	}
	if o.Source == "" {
		o.Source = "UNSPECIFIED"
	}
	return &Decoder{opts: o}, nil
}

// Decode maps one object, or every record of a batch document.
func (d *Decoder) Decode(f transport.Frame) ([]*obs.Observation, error) {
	trimmed := strings.TrimSpace(string(f.Payload))
	if trimmed == "" {
		return nil, errors.New("blank payload")
	}
	var doc any
	dec := json.NewDecoder(strings.NewReader(trimmed))
	dec.UseNumber()
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("parsing JSON: %w", err)
	}
	var records []any
	if d.opts.Records != "" {
		v, ok := lookup(doc, d.opts.Records)
		if !ok {
			return nil, decoder.Skip("no_records")
		}
		arr, ok := v.([]any)
		if !ok {
			return nil, fmt.Errorf("%s is not an array", d.opts.Records)
		}
		records = arr
	} else if arr, ok := doc.([]any); ok {
		records = arr
	} else {
		records = []any{doc}
	}
	out := make([]*obs.Observation, 0, len(records))
	var firstErr error
	for _, rec := range records {
		o, err := d.mapRecord(rec, f.ReceivedAt)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		out = append(out, o)
	}
	if len(out) == 0 {
		if firstErr != nil {
			return nil, firstErr
		}
		return nil, decoder.Skip("no_records")
	}
	return out, nil
}

// Close is a no-op.
func (d *Decoder) Close() error { return nil }

func (d *Decoder) mapRecord(rec any, received time.Time) (*obs.Observation, error) {
	fl := d.opts.Fields
	o := &obs.Observation{Decoder: Name, Source: d.opts.Source, EntityType: obs.EntityUnknown, TrackEnvironment: obs.EnvUnknown}
	o.SourceEntityID = str(rec, fl.ID)
	if o.SourceEntityID == "" {
		o.SourceEntityID = uuid.New().String()
	}
	o.DisplayName = str(rec, fl.Name)
	if t := str(rec, fl.EntityType); t != "" {
		o.EntityType = entityType(t)
	} else if d.opts.EntityType != "" {
		o.EntityType = entityType(d.opts.EntityType)
	}
	if env := str(rec, fl.TrackEnvironment); env != "" {
		o.TrackEnvironment = strings.ToUpper(env)
	}
	o.Affiliation = strings.ToUpper(str(rec, fl.Affiliation))
	o.CountryOfOrigin = str(rec, fl.Country)

	lat, okLat := num(rec, fl.Latitude)
	lon, okLon := num(rec, fl.Longitude)
	if okLat && okLon {
		if lat < -90 || lat > 90 || lon < -180 || lon > 180 {
			return nil, fmt.Errorf("position out of range: %v, %v", lat, lon)
		}
		if lat != 0 || lon != 0 {
			o.SetPosition(lat, lon)
		}
	}
	if v, ok := num(rec, fl.Altitude); ok {
		if d.opts.AltitudeUnit == "ft" {
			v *= 0.3048
		}
		o.Altitude = obs.Float(v)
	}
	if v, ok := num(rec, fl.Heading); ok {
		o.Heading = obs.Float(v)
	}
	if v, ok := num(rec, fl.Course); ok {
		o.Course = obs.Float(v)
	}
	if v, ok := num(rec, fl.Speed); ok {
		switch d.opts.SpeedUnit {
		case "m/s":
			v *= 1.94384
		case "km/h":
			v /= 1.852
		case "mph":
			v *= 0.868976
		}
		o.SpeedKnots = obs.Float(v)
	}
	vn, okN := num(rec, fl.VelocityNorth)
	ve, okE := num(rec, fl.VelocityEast)
	vu, okU := num(rec, fl.VelocityUp)
	if okN || okE || okU {
		o.Velocity = &[3]float64{vn, ve, vu}
	}
	if v, ok := num(rec, fl.CircularError); ok {
		o.CircularError = obs.Float(v)
	}
	if ts, ok := d.timestamp(rec); ok {
		o.SourceTimestamp = ts
	}
	if cs := str(rec, fl.Callsign); cs != "" {
		o.AddIdentity(obs.IdentityCallsign, cs)
	}
	if d.opts.IdentityType != "" {
		o.AddIdentity(strings.ToUpper(d.opts.IdentityType), normaliseIdentity(strings.ToUpper(d.opts.IdentityType), o.SourceEntityID))
	}
	return o, nil
}

// normaliseIdentity spells canonical identities the way the other decoders
// do, so an aircraft seen through a JSON API fuses with the same aircraft
// from OpenSky or a Beast receiver: ICAO24 upper-case hex, MMSI and
// NORAD_ID digits only.
func normaliseIdentity(typ, value string) string {
	switch typ {
	case obs.IdentityICAO24:
		return strings.ToUpper(strings.TrimSpace(value))
	case obs.IdentityMMSI, obs.IdentityNoradID:
		var b strings.Builder
		for _, r := range value {
			if r >= '0' && r <= '9' {
				b.WriteRune(r)
			}
		}
		return b.String()
	}
	return value
}

func (d *Decoder) timestamp(rec any) (time.Time, bool) {
	v, ok := lookup(rec, d.opts.Fields.Timestamp)
	if !ok || v == nil {
		return time.Time{}, false
	}
	switch d.opts.TimeFormat {
	case "unix":
		if f, ok := toFloat(v); ok {
			return time.Unix(0, int64(f*1e9)).UTC(), true
		}
	case "unixms":
		if f, ok := toFloat(v); ok {
			return time.UnixMilli(int64(f)).UTC(), true
		}
	case "":
		if f, ok := toFloat(v); ok {
			if f > 1e12 {
				return time.UnixMilli(int64(f)).UTC(), true
			}
			return time.Unix(0, int64(f*1e9)).UTC(), true
		}
		if s, ok := v.(string); ok {
			if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
				return t.UTC(), true
			}
		}
	default:
		if s, ok := v.(string); ok {
			if t, err := time.Parse(d.opts.TimeFormat, s); err == nil {
				return t.UTC(), true
			}
		}
	}
	return time.Time{}, false
}

func entityType(s string) string {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "AIRCRAFT":
		return obs.EntityAircraft
	case "VESSEL", "SHIP":
		return obs.EntityVessel
	case "VEHICLE":
		return obs.EntityVehicle
	case "PERSON":
		return obs.EntityPerson
	case "SENSOR":
		return obs.EntitySensor
	case "SATELLITE":
		return obs.EntitySatellite
	}
	return obs.EntityUnknown
}

// lookup resolves a dotted path (a.b.c, with numeric segments indexing
// arrays) in a decoded JSON value.
func lookup(v any, path string) (any, bool) {
	if path == "" {
		return nil, false
	}
	cur := v
	for _, seg := range strings.Split(path, ".") {
		switch node := cur.(type) {
		case map[string]any:
			next, ok := node[seg]
			if !ok {
				return nil, false
			}
			cur = next
		case []any:
			i, err := strconv.Atoi(seg)
			if err != nil || i < 0 || i >= len(node) {
				return nil, false
			}
			cur = node[i]
		default:
			return nil, false
		}
	}
	return cur, true
}

func str(rec any, path string) string {
	v, ok := lookup(rec, path)
	if !ok || v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return strings.TrimSpace(t)
	case json.Number:
		return t.String()
	case bool:
		return strconv.FormatBool(t)
	}
	return ""
}

func num(rec any, path string) (float64, bool) {
	v, ok := lookup(rec, path)
	if !ok || v == nil {
		return 0, false
	}
	return toFloat(v)
}

func toFloat(v any) (float64, bool) {
	switch t := v.(type) {
	case json.Number:
		f, err := t.Float64()
		if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
			return 0, false
		}
		return f, true
	case float64:
		if math.IsNaN(t) || math.IsInf(t, 0) {
			return 0, false
		}
		return t, true
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(t), 64)
		if err != nil {
			return 0, false
		}
		return f, true
	}
	return 0, false
}
