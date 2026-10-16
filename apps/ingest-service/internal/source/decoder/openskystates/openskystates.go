// Package openskystates decodes the OpenSky Network /states/all response
// into one observation per aircraft with a position.
package openskystates

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/sentinel/ingest-service/internal/obs"
)

// Name is the decoder name stamped on observations.
const Name = "opensky-states"

const metersPerSecToKnots = 1.94384

// Response is the top-level JSON document.
type Response struct {
	Time   int64           `json:"time"`
	States [][]interface{} `json:"states"`
}

// StateVector holds the fields used from one OpenSky state vector.
type StateVector struct {
	Icao24        string
	Callsign      string
	OriginCountry string
	Longitude     *float64
	Latitude      *float64
	BaroAltitude  *float64
	GeoAltitude   *float64
	OnGround      bool
	Velocity      *float64
	TrueTrack     *float64
	VerticalRate  *float64
	TimePosition  *int64
	LastContact   *int64
	Squawk        string
}

// Parse unmarshals the body into state vectors. A null states array (no
// aircraft in the box) yields nil, nil.
func Parse(body []byte) ([]StateVector, error) {
	var resp Response
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("unmarshal: %w", err)
	}
	if resp.States == nil {
		return nil, nil
	}
	out := make([]StateVector, 0, len(resp.States))
	for _, raw := range resp.States {
		if len(raw) < 17 {
			continue
		}
		out = append(out, StateVector{
			Icao24: stringAt(raw, 0), Callsign: strings.TrimSpace(stringAt(raw, 1)), OriginCountry: stringAt(raw, 2),
			TimePosition: intAt(raw, 3), LastContact: intAt(raw, 4),
			Longitude: floatAt(raw, 5), Latitude: floatAt(raw, 6), BaroAltitude: floatAt(raw, 7), OnGround: boolAt(raw, 8),
			Velocity: floatAt(raw, 9), TrueTrack: floatAt(raw, 10), VerticalRate: floatAt(raw, 11), GeoAltitude: floatAt(raw, 13),
			Squawk: stringAt(raw, 14),
		})
	}
	return out, nil
}

// Decode parses the body and converts every aircraft with a position.
func Decode(body []byte) ([]*obs.Observation, error) {
	states, err := Parse(body)
	if err != nil {
		return nil, err
	}
	out := make([]*obs.Observation, 0, len(states))
	for _, sv := range states {
		if o := Convert(sv); o != nil {
			out = append(out, o)
		}
	}
	return out, nil
}

// Convert turns a state vector into an observation; nil without a position.
func Convert(sv StateVector) *obs.Observation {
	if sv.Latitude == nil || sv.Longitude == nil {
		return nil
	}
	icao := strings.ToUpper(sv.Icao24)
	o := &obs.Observation{
		Decoder: Name, Source: "OPENSKY", SourceEntityID: "ICAO-" + icao,
		EntityType: obs.EntityAircraft, TrackEnvironment: obs.EnvAir, CountryOfOrigin: sv.OriginCountry,
		DisplayName: sv.Icao24,
	}
	o.SetPosition(*sv.Latitude, *sv.Longitude)
	o.AddIdentity(obs.IdentityICAO24, icao)
	adsb := &obs.ADSBData{ICAOHex: icao, OnGround: sv.OnGround, Squawk: sv.Squawk}
	if sv.Callsign != "" {
		o.DisplayName = sv.Callsign
		adsb.AircraftID = sv.Callsign
		o.AddIdentity(obs.IdentityCallsign, sv.Callsign)
	}
	if sv.BaroAltitude != nil {
		o.Altitude = obs.Float(*sv.BaroAltitude)
		adsb.AltitudeBaro = *sv.BaroAltitude
	} else if sv.GeoAltitude != nil {
		o.Altitude = obs.Float(*sv.GeoAltitude)
	}
	if sv.GeoAltitude != nil {
		adsb.AltitudeGeom = *sv.GeoAltitude
	}
	var vel [3]float64
	hasVel := false
	if sv.Velocity != nil {
		kt := *sv.Velocity * metersPerSecToKnots
		o.SpeedKnots = obs.Float(kt)
		adsb.GroundSpeed = kt
		if sv.TrueTrack != nil {
			rad := *sv.TrueTrack * math.Pi / 180
			vel[0], vel[1] = *sv.Velocity*math.Cos(rad), *sv.Velocity*math.Sin(rad)
			hasVel = true
		}
	}
	if sv.VerticalRate != nil {
		vel[2] = *sv.VerticalRate
		adsb.VerticalRate = *sv.VerticalRate
		hasVel = true
	}
	if hasVel {
		o.Velocity = &vel
	}
	if sv.TrueTrack != nil {
		o.Heading = obs.Float(*sv.TrueTrack)
		o.Course = obs.Float(*sv.TrueTrack)
	}
	if sv.TimePosition != nil {
		o.SourceTimestamp = time.Unix(*sv.TimePosition, 0).UTC()
	} else if sv.LastContact != nil {
		o.SourceTimestamp = time.Unix(*sv.LastContact, 0).UTC()
	}
	o.ADSB = adsb
	return o
}

func stringAt(arr []interface{}, i int) string {
	if i >= len(arr) || arr[i] == nil {
		return ""
	}
	s, _ := arr[i].(string)
	return s
}

func floatAt(arr []interface{}, i int) *float64 {
	if i >= len(arr) || arr[i] == nil {
		return nil
	}
	switch v := arr[i].(type) {
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return nil
		}
		return &v
	case json.Number:
		f, err := v.Float64()
		if err != nil {
			return nil
		}
		return &f
	}
	return nil
}

func boolAt(arr []interface{}, i int) bool {
	if i >= len(arr) || arr[i] == nil {
		return false
	}
	b, _ := arr[i].(bool)
	return b
}

func intAt(arr []interface{}, i int) *int64 {
	if i >= len(arr) || arr[i] == nil {
		return nil
	}
	switch v := arr[i].(type) {
	case float64:
		n := int64(v)
		return &n
	case json.Number:
		n, err := v.Int64()
		if err != nil {
			return nil
		}
		return &n
	}
	return nil
}
