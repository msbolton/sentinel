// Package sbs decodes the SBS-1 / BaseStation CSV format emitted by
// dump1090 and readsb on port 30003. MSG types 2 and 3 carry a position;
// MSG 1 (identification), 4 (velocity), 5, 6 and 8 are published as
// positionless partial observations so the track keeps its callsign,
// squawk and kinematics fresh.
package sbs

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
const Name = "sbs"

func init() {
	decoder.Register(decoder.Definition{
		Name: Name, Description: "SBS-1 / BaseStation CSV from dump1090 or readsb (port 30003).",
		Source: "ADS_B", ContentType: "text/plain", New: New,
	})
}

// New builds the decoder; it takes no options.
func New(json.RawMessage) (decoder.Decoder, error) {
	return decoder.Stateless(decode), nil
}

func decode(f transport.Frame) ([]*obs.Observation, error) {
	line := strings.TrimSpace(string(f.Payload))
	if line == "" {
		return nil, errors.New("blank line")
	}
	fields := strings.Split(line, ",")
	if len(fields) < 22 {
		return nil, fmt.Errorf("SBS message too short: %d fields (need 22)", len(fields))
	}
	for i := range fields {
		fields[i] = strings.TrimSpace(fields[i])
	}
	if fields[0] != "MSG" {
		return nil, decoder.Skip("not_msg")
	}
	txType := fields[1]
	icao := strings.ToUpper(fields[4])
	if icao == "" {
		return nil, errors.New("SBS message missing ICAO hex address")
	}
	o := &obs.Observation{
		Decoder: Name, Source: "ADS_B", SourceEntityID: "ICAO-" + icao,
		EntityType: obs.EntityAircraft, TrackEnvironment: obs.EnvAir, DisplayName: "ICAO " + icao,
		SourceTimestamp: timestamp(fields[6], fields[7]),
	}
	o.AddIdentity(obs.IdentityICAO24, icao)
	adsb := &obs.ADSBData{ICAOHex: icao}
	if cs := fields[10]; cs != "" {
		o.DisplayName = cs
		adsb.AircraftID = cs
		o.AddIdentity(obs.IdentityCallsign, cs)
	}
	if alt, err := strconv.ParseFloat(fields[11], 64); err == nil && fields[11] != "" {
		m := alt * 0.3048
		o.Altitude = obs.Float(m)
		adsb.AltitudeBaro = m
	}
	if spd, err := strconv.ParseFloat(fields[12], 64); err == nil && fields[12] != "" {
		o.SpeedKnots = obs.Float(spd)
		adsb.GroundSpeed = spd
	}
	if trk, err := strconv.ParseFloat(fields[13], 64); err == nil && fields[13] != "" {
		o.Heading = obs.Float(trk)
		o.Course = obs.Float(trk)
	}
	if vr, err := strconv.ParseFloat(fields[16], 64); err == nil && fields[16] != "" {
		ms := vr * 0.00508
		adsb.VerticalRate = ms
		o.Velocity = &[3]float64{0, 0, ms}
	}
	if sq := fields[17]; sq != "" {
		adsb.Squawk = sq
		switch sq {
		case "7500":
			adsb.Emergency = "HIJACK"
		case "7600":
			adsb.Emergency = "RADIO_FAILURE"
		case "7700":
			adsb.Emergency = "GENERAL_EMERGENCY"
		}
	}
	adsb.OnGround = fields[21] == "-1"
	o.ADSB = adsb

	switch txType {
	case "2", "3":
		lat, err := strconv.ParseFloat(fields[14], 64)
		if err != nil {
			return nil, fmt.Errorf("SBS latitude: %w", err)
		}
		lon, err := strconv.ParseFloat(fields[15], 64)
		if err != nil {
			return nil, fmt.Errorf("SBS longitude: %w", err)
		}
		o.SetPosition(lat, lon)
	case "1", "4", "5", "6", "8":
		// Partial observation: no fix, but identity, kinematics or squawk.
		if fields[10] == "" && fields[11] == "" && fields[12] == "" && fields[13] == "" && fields[16] == "" && fields[17] == "" {
			return nil, decoder.Skip("msg_" + txType + "_empty")
		}
	default:
		return nil, decoder.Skip("msg_" + txType)
	}
	return []*obs.Observation{o}, nil
}

func timestamp(date, clock string) time.Time {
	if date == "" || clock == "" {
		return time.Time{}
	}
	for _, layout := range []string{"2006/01/02 15:04:05.000", "2006/01/02 15:04:05"} {
		if t, err := time.Parse(layout, date+" "+clock); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}
