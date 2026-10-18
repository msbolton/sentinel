package aisstream

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	ais "github.com/BertoldVdb/go-ais"

	"github.com/sentinel/ingest-service/internal/obs"
	"github.com/sentinel/ingest-service/internal/source/decoder"
)

// envelope is an aisstream.io message: Message holds the go-ais packet as
// JSON under the name of its type.
type envelope struct {
	MessageType string `json:"MessageType"`
	MetaData    struct {
		ShipName string `json:"ShipName"`
		TimeUTC  string `json:"time_utc"`
	} `json:"MetaData"`
	Message map[string]json.RawMessage `json:"Message"`
}

// timeLayout is Go's time.String layout, which aisstream.io uses for
// time_utc ("2026-10-18 10:35:00.210811898 +0000 UTC").
const timeLayout = "2006-01-02 15:04:05.999999999 -0700 MST"

func decodeAs[T ais.Packet](raw []byte) (ais.Packet, error) {
	var p T
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, err
	}
	return p, nil
}

// packetTypes are the aisstream.io message types the driver understands.
var packetTypes = map[string]func([]byte) (ais.Packet, error){
	"PositionReport":               decodeAs[ais.PositionReport],
	"StandardClassBPositionReport": decodeAs[ais.StandardClassBPositionReport],
	"ExtendedClassBPositionReport": decodeAs[ais.ExtendedClassBPositionReport],
	"LongRangeAisBroadcastMessage": decodeAs[ais.LongRangeAisBroadcastMessage],
	"ShipStaticData":               decodeAs[ais.ShipStaticData],
	"StaticDataReport":             decodeAs[ais.StaticDataReport],
	"AidsToNavigationReport":       decodeAs[ais.AidsToNavigationReport],
	"BaseStationReport":            decodeAs[ais.BaseStationReport],
}

// parse maps one message. The observation is timed by the message's
// time_utc; the ship name aisstream.io adds names a vessel no static
// report has named yet.
func (d *Driver) parse(f frame) (*obs.Observation, error) {
	var env envelope
	if err := json.Unmarshal(f.data, &env); err != nil {
		return nil, fmt.Errorf("envelope: %w", err)
	}
	dec, ok := packetTypes[env.MessageType]
	if !ok {
		return nil, decoder.Skip("type_" + env.MessageType)
	}
	raw, ok := env.Message[env.MessageType]
	if !ok {
		return nil, fmt.Errorf("%s message without a %s body", env.MessageType, env.MessageType)
	}
	pkt, err := dec(raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", env.MessageType, err)
	}
	o, err := d.mapper.FromPacket(pkt, f.at)
	if err != nil {
		return nil, err
	}
	if at, err := time.Parse(timeLayout, env.MetaData.TimeUTC); err == nil {
		o.SourceTimestamp = at.UTC()
	}
	if a := o.AIS; a != nil && a.VesselName == "" {
		if name := strings.Trim(env.MetaData.ShipName, "@ "); name != "" {
			a.VesselName = name
			if strings.HasPrefix(o.DisplayName, "MMSI ") || o.DisplayName == a.Callsign {
				o.DisplayName = name
			}
		}
	}
	return o, nil
}
