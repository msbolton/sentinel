// Package cot decodes Cursor on Target XML events (TAK). One frame is one
// <event> element.
package cot

import (
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/sentinel/ingest-service/internal/obs"
	"github.com/sentinel/ingest-service/internal/source/decoder"
	"github.com/sentinel/ingest-service/internal/source/transport"
)

// Name is the decoder name.
const Name = "cot"

func init() {
	decoder.Register(decoder.Definition{
		Name: Name, Description: "Cursor on Target XML events (TAK servers and clients).",
		Source: "COT", ContentType: "application/xml", New: New,
	})
}

type event struct {
	XMLName xml.Name `xml:"event"`
	UID     string   `xml:"uid,attr"`
	Type    string   `xml:"type,attr"`
	Time    string   `xml:"time,attr"`
	Start   string   `xml:"start,attr"`
	Stale   string   `xml:"stale,attr"`
	How     string   `xml:"how,attr"`
	Access  string   `xml:"access,attr"`
	Opex    string   `xml:"opex,attr"`
	QOS     string   `xml:"qos,attr"`
	Point   struct {
		Lat float64 `xml:"lat,attr"`
		Lon float64 `xml:"lon,attr"`
		Hae float64 `xml:"hae,attr"`
		CE  float64 `xml:"ce,attr"`
		LE  float64 `xml:"le,attr"`
	} `xml:"point"`
	Detail struct {
		Track struct {
			Speed  float64 `xml:"speed,attr"`
			Course float64 `xml:"course,attr"`
		} `xml:"track"`
		Contact struct {
			Callsign string `xml:"callsign,attr"`
		} `xml:"contact"`
	} `xml:"detail"`
}

// New builds the decoder; it takes no options.
func New(json.RawMessage) (decoder.Decoder, error) {
	return decoder.Stateless(decode), nil
}

func decode(f transport.Frame) ([]*obs.Observation, error) {
	data := strings.TrimSpace(string(f.Payload))
	if data == "" {
		return nil, errors.New("blank payload")
	}
	var ev event
	if err := xml.Unmarshal([]byte(data), &ev); err != nil {
		return nil, fmt.Errorf("parsing CoT XML: %w", err)
	}
	if ev.UID == "" {
		return nil, errors.New("CoT event has no uid")
	}
	if strings.HasPrefix(ev.Type, "t-") {
		return nil, decoder.Skip("tasking_event")
	}
	o := &obs.Observation{
		Decoder: Name, Source: "COT", SourceEntityID: ev.UID,
		EntityType: entityType(ev.Type), TrackEnvironment: environment(ev.Type),
		Affiliation: affiliation(ev.Type), DisplayName: ev.Detail.Contact.Callsign,
	}
	if o.DisplayName == "" {
		o.DisplayName = ev.UID
	}
	o.AddIdentity(obs.IdentityCoTUID, ev.UID)
	if ev.Detail.Contact.Callsign != "" {
		o.AddIdentity(obs.IdentityCallsign, ev.Detail.Contact.Callsign)
	}
	if ts, err := time.Parse(time.RFC3339, ev.Time); err == nil {
		o.SourceTimestamp = ts.UTC()
	}
	// CoT uses 9999999 for unknown coordinates.
	if ev.Point.Lat >= -90 && ev.Point.Lat <= 90 && ev.Point.Lon >= -180 && ev.Point.Lon <= 180 && !(ev.Point.Lat == 0 && ev.Point.Lon == 0) {
		o.SetPosition(ev.Point.Lat, ev.Point.Lon)
	}
	if ev.Point.Hae < 9999999 && ev.Point.Hae != 0 {
		o.Altitude = obs.Float(ev.Point.Hae)
	}
	if ev.Point.CE > 0 && ev.Point.CE < 9999999 {
		o.CircularError = obs.Float(ev.Point.CE)
	}
	if ev.Detail.Track.Speed > 0 || ev.Detail.Track.Course > 0 {
		speedMS := ev.Detail.Track.Speed
		course := ev.Detail.Track.Course
		o.SpeedKnots = obs.Float(speedMS * 1.94384)
		o.Course = obs.Float(course)
		o.Heading = obs.Float(course)
		rad := course * math.Pi / 180
		o.Velocity = &[3]float64{speedMS * math.Cos(rad), speedMS * math.Sin(rad), 0}
	}
	o.CoT = &obs.CoTData{UID: ev.UID, CoTType: ev.Type, How: ev.How, CE: ev.Point.CE, LE: ev.Point.LE, StaleTime: ev.Stale, AccessControl: ev.Access, Opex: ev.Opex, QOS: ev.QOS}
	return []*obs.Observation{o}, nil
}

func parts(cotType string) []string { return strings.Split(cotType, "-") }

func entityType(cotType string) string {
	p := parts(cotType)
	if len(p) < 3 || p[0] != "a" {
		return obs.EntityUnknown
	}
	switch strings.ToUpper(p[2]) {
	case "A":
		return obs.EntityAircraft
	case "S", "U":
		return obs.EntityVessel
	case "G":
		if len(p) >= 4 && strings.EqualFold(p[3], "U") {
			return obs.EntityPerson
		}
		return obs.EntityVehicle
	case "P":
		return obs.EntitySatellite
	}
	return obs.EntityUnknown
}

func environment(cotType string) string {
	p := parts(cotType)
	if len(p) < 3 {
		return obs.EnvUnknown
	}
	switch strings.ToUpper(p[2]) {
	case "A":
		return obs.EnvAir
	case "S":
		return obs.EnvSeaSurface
	case "G":
		return obs.EnvGround
	case "U":
		return obs.EnvSubsurface
	case "P":
		return obs.EnvSpace
	}
	return obs.EnvUnknown
}

func affiliation(cotType string) string {
	p := parts(cotType)
	if len(p) < 2 {
		return ""
	}
	switch p[1] {
	case "f", "a":
		return "FRIENDLY"
	case "h", "s", "j", "k":
		return "HOSTILE"
	case "n":
		return "NEUTRAL"
	case "u", "p":
		return "UNKNOWN"
	}
	return ""
}
