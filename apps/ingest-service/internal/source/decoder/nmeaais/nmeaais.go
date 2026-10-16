// Package nmeaais decodes AIS messages carried in NMEA VDM/VDO sentences:
// position reports (types 1-3, 18) and the static and voyage report
// (type 5, two fragments). Multi-fragment messages are reassembled per
// sender and sequence id; stale fragments are dropped after 10 seconds.
package nmeaais

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
const Name = "nmea-ais"

const fragmentTTL = 10 * time.Second

func init() {
	decoder.Register(decoder.Definition{
		Name: Name, Description: "AIS position and static reports in NMEA VDM/VDO sentences (AIS-catcher, rtl-ais, receivers).",
		Source: "AIS", ContentType: "text/plain", New: New,
	})
}

type fragments struct {
	parts []string
	at    time.Time
}

// Decoder is the nmea-ais decoder with per-feed fragment state.
type Decoder struct {
	pending map[string]*fragments
}

// New builds a decoder; it takes no options.
func New(json.RawMessage) (decoder.Decoder, error) {
	return &Decoder{pending: map[string]*fragments{}}, nil
}

// Decode parses one sentence.
func (d *Decoder) Decode(f transport.Frame) ([]*obs.Observation, error) {
	return decoder.One(d.parse(f))
}

// Close drops buffered fragments.
func (d *Decoder) Close() error {
	d.pending = map[string]*fragments{}
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
	if fields[1] == "1" {
		return decodePayload(payload)
	}

	total, err := strconv.Atoi(fields[1])
	if err != nil || total < 2 {
		return nil, fmt.Errorf("invalid AIS fragment count: %s", fields[1])
	}
	idx, err := strconv.Atoi(fields[2])
	if err != nil || idx < 1 || idx > total {
		return nil, fmt.Errorf("invalid AIS fragment number: %s", fields[2])
	}
	d.evict(f.ReceivedAt)
	key := f.Remote + "|" + fields[3]
	fr, ok := d.pending[key]
	if !ok || len(fr.parts) != total {
		fr = &fragments{parts: make([]string, total), at: f.ReceivedAt}
		d.pending[key] = fr
	}
	fr.parts[idx-1] = payload
	for _, p := range fr.parts {
		if p == "" {
			return nil, decoder.Skip("fragment_buffered")
		}
	}
	delete(d.pending, key)
	return decodePayload(strings.Join(fr.parts, ""))
}

func (d *Decoder) evict(now time.Time) {
	if now.IsZero() {
		now = time.Now()
	}
	for k, fr := range d.pending {
		if fr.at.IsZero() {
			fr.at = now
		}
		if now.Sub(fr.at) > fragmentTTL {
			delete(d.pending, k)
		}
	}
}

func decodePayload(payload string) (*obs.Observation, error) {
	bits := decodeArmor(payload)
	if len(bits) < 6 {
		return nil, fmt.Errorf("AIS payload too short: %d bits", len(bits))
	}
	msgType := bitsToUint(bits, 0, 6)
	switch {
	case msgType >= 1 && msgType <= 3:
		return positionReport(bits)
	case msgType == 18:
		return classBPosition(bits)
	case msgType == 5:
		return staticReport(bits)
	}
	return nil, decoder.Skip(fmt.Sprintf("ais_type_%d", msgType))
}

func base(mmsi uint32) *obs.Observation {
	mmsiStr := strconv.FormatUint(uint64(mmsi), 10)
	o := &obs.Observation{
		Decoder: Name, Source: "AIS", SourceEntityID: "MMSI-" + mmsiStr,
		EntityType: obs.EntityVessel, TrackEnvironment: obs.EnvSeaSurface, DisplayName: "MMSI " + mmsiStr,
	}
	o.AddIdentity(obs.IdentityMMSI, mmsiStr)
	return o
}

func positionReport(bits []byte) (*obs.Observation, error) {
	if len(bits) < 168 {
		return nil, fmt.Errorf("AIS position report too short: %d bits (need 168)", len(bits))
	}
	msgType := bitsToUint(bits, 0, 6)
	mmsi := bitsToUint(bits, 8, 30)
	o := base(mmsi)
	navStatus := bitsToUint(bits, 38, 4)
	rot := float64(bitsToInt(bits, 42, 8))
	sogRaw := bitsToUint(bits, 50, 10)
	posAcc := bitsToUint(bits, 60, 1)
	lon := float64(bitsToInt(bits, 61, 28)) / 600000.0
	lat := float64(bitsToInt(bits, 89, 27)) / 600000.0
	cogRaw := bitsToUint(bits, 116, 12)
	hdg := bitsToUint(bits, 128, 9)
	manoeuvre := bitsToUint(bits, 143, 2)

	ais := &obs.AISData{
		MMSI: strconv.FormatUint(uint64(mmsi), 10), NavStatus: navStatusName(navStatus), RateOfTurn: rot,
		PositionAccuracyHigh: posAcc == 1, SpecialManoeuvre: manoeuvre == 1,
		MessageType: int(msgType), RepeatIndicator: int(bitsToUint(bits, 6, 2)),
	}
	fillKinematics(o, ais, lat, lon, sogRaw, cogRaw, hdg, posAcc == 1)
	o.AIS = ais
	o.OperationalStatus = ais.NavStatus
	return o, nil
}

func classBPosition(bits []byte) (*obs.Observation, error) {
	if len(bits) < 168 {
		return nil, fmt.Errorf("AIS type 18 too short: %d bits (need 168)", len(bits))
	}
	mmsi := bitsToUint(bits, 8, 30)
	o := base(mmsi)
	sogRaw := bitsToUint(bits, 46, 10)
	posAcc := bitsToUint(bits, 56, 1)
	lon := float64(bitsToInt(bits, 57, 28)) / 600000.0
	lat := float64(bitsToInt(bits, 85, 27)) / 600000.0
	cogRaw := bitsToUint(bits, 112, 12)
	hdg := bitsToUint(bits, 124, 9)
	ais := &obs.AISData{MMSI: strconv.FormatUint(uint64(mmsi), 10), PositionAccuracyHigh: posAcc == 1, MessageType: 18, RepeatIndicator: int(bitsToUint(bits, 6, 2))}
	fillKinematics(o, ais, lat, lon, sogRaw, cogRaw, hdg, posAcc == 1)
	o.AIS = ais
	return o, nil
}

func fillKinematics(o *obs.Observation, ais *obs.AISData, lat, lon float64, sogRaw, cogRaw, hdg uint32, accurate bool) {
	if len(ais.MMSI) == 9 {
		ais.Flag = midToCountry(ais.MMSI[:3])
	}
	// 181/91 are the "not available" codes for longitude/latitude.
	if lon < -180 || lon > 180 || lat < -90 || lat > 90 || lon == 181 || lat == 91 {
		// no fix
	} else {
		o.SetPosition(lat, lon)
	}
	if sogRaw != 1023 {
		sog := float64(sogRaw) / 10.0
		ais.SpeedOverGround = sog
		o.SpeedKnots = obs.Float(sog)
	}
	if cogRaw != 3600 {
		cog := float64(cogRaw) / 10.0
		ais.CourseOverGround = cog
		o.Course = obs.Float(cog)
	}
	if hdg != 511 {
		ais.TrueHeading = float64(hdg)
		o.Heading = obs.Float(float64(hdg))
	} else if o.Course != nil {
		o.Heading = obs.Float(*o.Course)
	}
	ce := 10.0
	if accurate {
		ce = 1.0
	}
	o.CircularError = obs.Float(ce)
}

func staticReport(bits []byte) (*obs.Observation, error) {
	if len(bits) < 424 {
		return nil, fmt.Errorf("AIS type 5 too short: %d bits (need 424)", len(bits))
	}
	mmsi := bitsToUint(bits, 8, 30)
	o := base(mmsi)
	imo := bitsToUint(bits, 40, 30)
	callsign := sixBitText(bits, 70, 42)
	name := sixBitText(bits, 112, 120)
	shipType := bitsToUint(bits, 232, 8)
	dimA := float64(bitsToUint(bits, 240, 9))
	dimB := float64(bitsToUint(bits, 249, 9))
	dimC := float64(bitsToUint(bits, 258, 6))
	dimD := float64(bitsToUint(bits, 264, 6))
	draught := float64(bitsToUint(bits, 294, 8)) / 10.0
	dest := sixBitText(bits, 302, 120)

	ais := &obs.AISData{
		MMSI: strconv.FormatUint(uint64(mmsi), 10), Callsign: callsign, VesselName: name,
		ShipType: int(shipType), ShipTypeName: shipTypeName(shipType),
		DimensionA: dimA, DimensionB: dimB, DimensionC: dimC, DimensionD: dimD,
		LengthOverall: dimA + dimB, Beam: dimC + dimD, Draught: draught, Destination: dest, MessageType: 5,
	}
	if imo > 0 {
		ais.IMO = strconv.FormatUint(uint64(imo), 10)
		o.AddIdentity(obs.IdentityIMO, ais.IMO)
	}
	if callsign != "" {
		o.AddIdentity(obs.IdentityCallsign, callsign)
	}
	if len(ais.MMSI) == 9 {
		ais.Flag = midToCountry(ais.MMSI[:3])
	}
	switch {
	case name != "":
		o.DisplayName = name
	case callsign != "":
		o.DisplayName = callsign
	}
	// A static and voyage report carries no fix.
	o.Dimensions = &[3]float64{ais.LengthOverall, ais.Beam, 0}
	o.AIS = ais
	return o, nil
}

// sixBitText decodes AIS 6-bit ASCII and trims trailing @ and spaces.
func sixBitText(bits []byte, start, bitLength int) string {
	n := bitLength / 6
	out := make([]byte, 0, n)
	for i := 0; i < n; i++ {
		off := start + i*6
		if off+6 > len(bits) {
			break
		}
		v := bitsToUint(bits, off, 6)
		if v < 32 {
			out = append(out, byte(v)+'@')
		} else {
			out = append(out, byte(v))
		}
	}
	return strings.TrimRight(string(out), "@ ")
}

func decodeArmor(payload string) []byte {
	bits := make([]byte, 0, len(payload)*6)
	for _, c := range payload {
		v := int(c) - 48
		if v > 40 {
			v -= 8
		}
		for i := 5; i >= 0; i-- {
			if v&(1<<i) != 0 {
				bits = append(bits, 1)
			} else {
				bits = append(bits, 0)
			}
		}
	}
	return bits
}

func bitsToUint(bits []byte, start, length int) uint32 {
	var r uint32
	for i := 0; i < length && start+i < len(bits); i++ {
		r = (r << 1) | uint32(bits[start+i])
	}
	return r
}

func bitsToInt(bits []byte, start, length int) int32 {
	v := bitsToUint(bits, start, length)
	if start < len(bits) && bits[start] == 1 {
		v |= ^((1 << length) - 1)
	}
	return int32(v)
}

func navStatusName(s uint32) string {
	switch s {
	case 0:
		return "UNDER_WAY_USING_ENGINE"
	case 1:
		return "AT_ANCHOR"
	case 2:
		return "NOT_UNDER_COMMAND"
	case 3:
		return "RESTRICTED_MANOEUVRABILITY"
	case 4:
		return "CONSTRAINED_BY_DRAUGHT"
	case 5:
		return "MOORED"
	case 6:
		return "AGROUND"
	case 7:
		return "ENGAGED_IN_FISHING"
	case 8:
		return "UNDER_WAY_SAILING"
	case 14:
		return "AIS_SART"
	}
	return "UNKNOWN"
}

func shipTypeName(code uint32) string {
	switch {
	case code >= 20 && code <= 29:
		return "Wing in Ground"
	case code == 30:
		return "Fishing"
	case code == 31 || code == 32:
		return "Towing"
	case code == 33:
		return "Dredging"
	case code == 34:
		return "Diving Operations"
	case code == 35:
		return "Military Operations"
	case code == 36:
		return "Sailing"
	case code == 37:
		return "Pleasure Craft"
	case code >= 40 && code <= 49:
		return "High Speed Craft"
	case code == 50:
		return "Pilot Vessel"
	case code == 51:
		return "Search and Rescue"
	case code == 52:
		return "Tug"
	case code == 53:
		return "Port Tender"
	case code == 55:
		return "Law Enforcement"
	case code >= 60 && code <= 69:
		return "Passenger"
	case code >= 70 && code <= 79:
		return "Cargo"
	case code >= 80 && code <= 89:
		return "Tanker"
	}
	return "Other"
}

var midMap = map[string]string{
	"201": "GR", "211": "DE", "219": "DK", "220": "DK", "224": "ES", "225": "ES", "226": "FR", "227": "FR",
	"228": "FR", "229": "MT", "230": "FI", "231": "FO", "232": "GB", "233": "GB", "234": "GB", "235": "GB",
	"236": "GI", "237": "GR", "238": "HR", "239": "GR", "240": "GR", "241": "GR", "242": "MA", "243": "HU",
	"244": "NL", "245": "NL", "246": "NL", "247": "IT", "248": "MT", "249": "MT", "250": "IE", "255": "PT",
	"256": "MT", "257": "NO", "258": "NO", "259": "NO", "261": "PL", "263": "PT", "265": "SE", "266": "SE",
	"269": "CH", "270": "CZ", "271": "TR", "272": "UA", "273": "RU", "274": "MK", "275": "LV", "276": "EE",
	"277": "LT", "278": "SI", "279": "RS", "303": "US", "338": "US", "366": "US", "367": "US", "368": "US",
	"369": "US", "316": "CA", "401": "AF", "412": "CN", "413": "CN", "414": "CN", "431": "JP", "432": "JP",
	"440": "KR", "441": "KR", "501": "FR", "503": "AU", "506": "MM", "508": "MV", "510": "NZ", "512": "NZ",
	"525": "ID", "533": "MY", "548": "PH", "563": "SG", "564": "SG", "565": "SG", "566": "SG", "567": "TH",
	"601": "ZA", "603": "AO", "605": "DZ", "636": "LR", "637": "LR", "710": "BR", "720": "BO", "725": "CL",
	"730": "CO", "735": "EC", "740": "FK", "760": "MX",
}

func midToCountry(mid string) string { return midMap[mid] }
