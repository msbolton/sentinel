// Package adsblol decodes the adsb.lol v2 aircraft response (the readsb
// aircraft.json shape) into observations.
package adsblol

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/sentinel/ingest-service/internal/obs"
)

// Name is the decoder name stamped on observations.
const Name = "adsblol"

const knotsPerMeterPerSec = 1.94384

// Response is the top-level document.
type Response struct {
	AC  []Aircraft `json:"ac"`
	Now float64    `json:"now"`
}

// Aircraft is one aircraft record.
type Aircraft struct {
	Hex              string    `json:"hex"`
	Flight           *string   `json:"flight"`
	Reg              *string   `json:"r"`
	AircraftType     *string   `json:"t"`
	AircraftTypeName *string   `json:"desc"`
	OperatorName     *string   `json:"ownOp"`
	Lat              *float64  `json:"lat"`
	Lon              *float64  `json:"lon"`
	AltBaro          *Altitude `json:"alt_baro"`
	AltGeom          *float64  `json:"alt_geom"`
	GroundSpeed      *float64  `json:"gs"`
	Track            *float64  `json:"track"`
	BaroRate         *float64  `json:"baro_rate"`
	Squawk           *string   `json:"squawk"`
	Emergency        *string   `json:"emergency"`
	Category         *string   `json:"category"`
	NacP             *int      `json:"nac_p"`
	NacV             *int      `json:"nac_v"`
	SIL              *int      `json:"sil"`
	SILType          *string   `json:"sil_type"`
	NIC              *int      `json:"nic"`
	RC               *float64  `json:"rc"`
	GVA              *int      `json:"gva"`
	SDA              *int      `json:"sda"`
	Seen             *float64  `json:"seen"`
	SeenPos          *float64  `json:"seen_pos"`
}

// Altitude is alt_baro: a number in feet or the string "ground".
type Altitude struct {
	Feet     float64
	OnGround bool
}

// UnmarshalJSON accepts both forms.
func (a *Altitude) UnmarshalJSON(data []byte) error {
	var n float64
	if err := json.Unmarshal(data, &n); err == nil {
		a.Feet, a.OnGround = n, false
		return nil
	}
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		if s == "ground" {
			a.OnGround = true
			return nil
		}
		return fmt.Errorf("unknown alt_baro string %q", s)
	}
	return fmt.Errorf("cannot unmarshal alt_baro %s", string(data))
}

// Parse unmarshals the body.
func Parse(body []byte) (Response, error) {
	var resp Response
	if err := json.Unmarshal(body, &resp); err != nil {
		return resp, fmt.Errorf("unmarshal: %w", err)
	}
	return resp, nil
}

// Decode parses the body and converts every aircraft with a position. now
// is the receive time; the response's own clock is used when present.
func Decode(body []byte, now time.Time) ([]*obs.Observation, error) {
	resp, err := Parse(body)
	if err != nil {
		return nil, err
	}
	at := now
	if resp.Now > 0 {
		at = time.UnixMilli(int64(resp.Now * 1000)).UTC()
	}
	out := make([]*obs.Observation, 0, len(resp.AC))
	for _, ac := range resp.AC {
		if o := Convert(ac, at); o != nil {
			out = append(out, o)
		}
	}
	return out, nil
}

// Convert turns an aircraft into an observation; nil without a position.
func Convert(ac Aircraft, at time.Time) *obs.Observation {
	if ac.Lat == nil || ac.Lon == nil {
		return nil
	}
	icao := strings.ToUpper(ac.Hex)
	o := &obs.Observation{
		Decoder: Name, Source: "ADSB_LOL", SourceEntityID: "ICAO-" + icao,
		EntityType: obs.EntityAircraft, TrackEnvironment: obs.EnvAir, DisplayName: ac.Hex, SourceTimestamp: at,
	}
	if ac.SeenPos != nil {
		o.SourceTimestamp = at.Add(-time.Duration(*ac.SeenPos * float64(time.Second)))
	}
	o.SetPosition(*ac.Lat, *ac.Lon)
	o.AddIdentity(obs.IdentityICAO24, icao)
	adsb := &obs.ADSBData{ICAOHex: icao}
	if ac.Flight != nil {
		if cs := strings.TrimSpace(*ac.Flight); cs != "" {
			o.DisplayName = cs
			adsb.AircraftID = cs
			o.AddIdentity(obs.IdentityCallsign, cs)
		}
	}
	if ac.Reg != nil && *ac.Reg != "" {
		adsb.Registration = *ac.Reg
		o.AddIdentity(obs.IdentityRegistration, strings.ToUpper(*ac.Reg))
	}
	if ac.AircraftType != nil {
		adsb.AircraftType = *ac.AircraftType
	}
	if ac.AircraftTypeName != nil {
		adsb.AircraftTypeName = *ac.AircraftTypeName
	}
	if ac.OperatorName != nil {
		adsb.OperatorName = *ac.OperatorName
	}
	if ac.Squawk != nil {
		adsb.Squawk = *ac.Squawk
	}
	if ac.Emergency != nil && *ac.Emergency != "none" && *ac.Emergency != "" {
		adsb.Emergency = strings.ToUpper(*ac.Emergency)
	}
	if ac.AltBaro != nil {
		adsb.OnGround = ac.AltBaro.OnGround
		if !ac.AltBaro.OnGround {
			m := ac.AltBaro.Feet * 0.3048
			o.Altitude = obs.Float(m)
			adsb.AltitudeBaro = m
		}
	} else if ac.AltGeom != nil {
		o.Altitude = obs.Float(*ac.AltGeom * 0.3048)
	}
	if ac.AltGeom != nil {
		adsb.AltitudeGeom = *ac.AltGeom * 0.3048
	}
	var vel [3]float64
	hasVel := false
	if ac.GroundSpeed != nil {
		o.SpeedKnots = obs.Float(*ac.GroundSpeed)
		adsb.GroundSpeed = *ac.GroundSpeed
		if ac.Track != nil {
			ms := *ac.GroundSpeed / knotsPerMeterPerSec
			rad := *ac.Track * math.Pi / 180
			vel[0], vel[1] = ms*math.Cos(rad), ms*math.Sin(rad)
			hasVel = true
		}
	}
	if ac.BaroRate != nil {
		vel[2] = *ac.BaroRate * 0.00508
		adsb.VerticalRate = vel[2]
		hasVel = true
	}
	if hasVel {
		o.Velocity = &vel
	}
	if ac.Track != nil {
		o.Heading = obs.Float(*ac.Track)
		o.Course = obs.Float(*ac.Track)
	}
	if ac.Category != nil {
		adsb.Category = *ac.Category
	}
	if ac.NacP != nil {
		adsb.NacP = *ac.NacP
	}
	if ac.NacV != nil {
		adsb.NacV = *ac.NacV
	}
	if ac.SIL != nil {
		adsb.SIL = *ac.SIL
	}
	if ac.SILType != nil {
		adsb.SILType = *ac.SILType
	}
	if ac.NIC != nil {
		adsb.NIC = *ac.NIC
	}
	if ac.RC != nil {
		adsb.RC = *ac.RC
	}
	if ac.GVA != nil {
		adsb.GVA = *ac.GVA
	}
	if ac.SDA != nil {
		adsb.SDA = *ac.SDA
	}
	o.ADSB = adsb
	return o
}
