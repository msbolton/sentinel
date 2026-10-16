// Package nmeaais decodes AIS messages carried in NMEA VDM/VDO sentences.
// The sentence layer (fields, checksum, multi-fragment reassembly per
// sender and sequence id, stale fragments dropped after 10 seconds) is
// ours; the message bodies are decoded by github.com/BertoldVdb/go-ais.
//
// Observations are produced for class A position reports (1-3), class B
// position reports (18, 19), the long-range broadcast (27), the static and
// voyage report (5), the class B static report (24, parts A and B; a part B
// takes the name of a part A seen for the same MMSI within 10 minutes) and
// aids to navigation (21). Base station reports (4, 11) and every other
// type are skipped.
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
	"github.com/sentinel/ingest-service/internal/source/transport"
)

// Name is the decoder name.
const Name = "nmea-ais"

const (
	fragmentTTL = 10 * time.Second
	// staticTTL bounds how long a type 24 part A name waits for its part B.
	staticTTL = 10 * time.Minute

	// "Not available" sentinels of ITU-R M.1371.
	lonNotAvailable     = 181.0
	latNotAvailable     = 91.0
	sogNotAvailable     = 102.3
	cogNotAvailable     = 360.0
	headingNotAvailable = 511
	// Long-range (type 27) speed and course are coarse integers.
	longRangeSogNotAvailable = 63
	longRangeCogNotAvailable = 511

	circularErrorHigh      = 1.0
	circularErrorLow       = 10.0
	circularErrorLongRange = 1000.0
)

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

type cachedName struct {
	name string
	at   time.Time
}

// Decoder is the nmea-ais decoder with per-feed fragment and static-name
// state.
type Decoder struct {
	codec     *ais.Codec
	pending   map[string]*fragments
	names     map[uint32]cachedName
	lastSweep time.Time
}

// New builds a decoder; it takes no options.
func New(json.RawMessage) (decoder.Decoder, error) {
	// Some class A transponders send a 420-bit type 5; accept it.
	codec := ais.CodecNew(false, true)
	codec.DropSpace = true
	return &Decoder{codec: codec, pending: map[string]*fragments{}, names: map[uint32]cachedName{}}, nil
}

// Decode parses one sentence.
func (d *Decoder) Decode(f transport.Frame) ([]*obs.Observation, error) {
	return decoder.One(d.parse(f))
}

// Close drops buffered fragments and cached names.
func (d *Decoder) Close() error {
	d.pending = map[string]*fragments{}
	d.names = map[uint32]cachedName{}
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
func (d *Decoder) sweepNames(now time.Time) {
	if now.Sub(d.lastSweep) < time.Minute {
		return
	}
	d.lastSweep = now
	for mmsi, c := range d.names {
		if now.Sub(c.at) > staticTTL {
			delete(d.names, mmsi)
		}
	}
}

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
	switch m := pkt.(type) {
	case ais.PositionReport:
		return classAPosition(m)
	case ais.StandardClassBPositionReport:
		return classBPosition(m)
	case ais.ExtendedClassBPositionReport:
		return extendedClassBPosition(m)
	case ais.LongRangeAisBroadcastMessage:
		return longRangePosition(m)
	case ais.ShipStaticData:
		return staticAndVoyage(m)
	case ais.StaticDataReport:
		return d.classBStatic(m, now)
	case ais.AidsToNavigationReport:
		return aidToNavigation(m)
	case ais.BaseStationReport:
		return nil, decoder.Skip("ais_base_station")
	}
	return nil, decoder.Skip(fmt.Sprintf("ais_type_%d", msgType))
}

func invalid(h ais.Header) error {
	return fmt.Errorf("invalid AIS type %d message from %d", h.MessageID, h.UserID)
}

// base starts an observation for the vessel identified by the header.
func base(h ais.Header) (*obs.Observation, *obs.AISData) {
	mmsi := strconv.FormatUint(uint64(h.UserID), 10)
	o := &obs.Observation{
		Decoder: Name, Source: "AIS", SourceEntityID: "MMSI-" + mmsi,
		EntityType: obs.EntityVessel, TrackEnvironment: obs.EnvSeaSurface, DisplayName: "MMSI " + mmsi,
	}
	o.AddIdentity(obs.IdentityMMSI, mmsi)
	a := &obs.AISData{MMSI: mmsi, MessageType: int(h.MessageID), RepeatIndicator: int(h.RepeatIndicator)}
	if len(mmsi) == 9 {
		a.Flag = midToCountry(mmsi[:3])
	}
	o.AIS = a
	return o, a
}

func classAPosition(m ais.PositionReport) (*obs.Observation, error) {
	if !m.Valid {
		return nil, invalid(m.Header)
	}
	o, a := base(m.Header)
	a.NavStatus = navStatusName(m.NavigationalStatus)
	a.RateOfTurn = float64(m.RateOfTurn)
	a.SpecialManoeuvre = m.SpecialManoeuvreIndicator == 1
	o.OperationalStatus = a.NavStatus
	setPosition(o, a, float64(m.Latitude), float64(m.Longitude), m.PositionAccuracy, accuracyError(m.PositionAccuracy))
	setSpeedCourse(o, a, float64(m.Sog), float64(m.Cog))
	setHeading(o, a, m.TrueHeading)
	return o, nil
}

func classBPosition(m ais.StandardClassBPositionReport) (*obs.Observation, error) {
	if !m.Valid {
		return nil, invalid(m.Header)
	}
	o, a := base(m.Header)
	setPosition(o, a, float64(m.Latitude), float64(m.Longitude), m.PositionAccuracy, accuracyError(m.PositionAccuracy))
	setSpeedCourse(o, a, float64(m.Sog), float64(m.Cog))
	setHeading(o, a, m.TrueHeading)
	return o, nil
}

func extendedClassBPosition(m ais.ExtendedClassBPositionReport) (*obs.Observation, error) {
	if !m.Valid {
		return nil, invalid(m.Header)
	}
	o, a := base(m.Header)
	setPosition(o, a, float64(m.Latitude), float64(m.Longitude), m.PositionAccuracy, accuracyError(m.PositionAccuracy))
	setSpeedCourse(o, a, float64(m.Sog), float64(m.Cog))
	setHeading(o, a, m.TrueHeading)
	setShipType(a, m.Type)
	setDimensions(o, a, m.Dimension)
	setName(o, a, m.Name)
	return o, nil
}

func longRangePosition(m ais.LongRangeAisBroadcastMessage) (*obs.Observation, error) {
	if !m.Valid {
		return nil, invalid(m.Header)
	}
	o, a := base(m.Header)
	a.NavStatus = navStatusName(m.NavigationalStatus)
	o.OperationalStatus = a.NavStatus
	setPosition(o, a, float64(m.Latitude), float64(m.Longitude), m.PositionAccuracy, circularErrorLongRange)
	sog, cog := sogNotAvailable, cogNotAvailable
	if m.Sog != longRangeSogNotAvailable {
		sog = float64(m.Sog)
	}
	if m.Cog != longRangeCogNotAvailable {
		cog = float64(m.Cog)
	}
	setSpeedCourse(o, a, sog, cog)
	setHeading(o, a, headingNotAvailable)
	return o, nil
}

func staticAndVoyage(m ais.ShipStaticData) (*obs.Observation, error) {
	if !m.Valid {
		return nil, invalid(m.Header)
	}
	o, a := base(m.Header)
	if m.ImoNumber > 0 {
		a.IMO = strconv.FormatUint(uint64(m.ImoNumber), 10)
		o.AddIdentity(obs.IdentityIMO, a.IMO)
	}
	setCallsign(o, a, m.CallSign)
	setShipType(a, m.Type)
	setDimensions(o, a, m.Dimension)
	a.Draught = float64(m.MaximumStaticDraught)
	a.Destination = cleanText(m.Destination)
	a.ETA = formatETA(m.Eta)
	setName(o, a, m.Name)
	return o, nil
}

func (d *Decoder) classBStatic(m ais.StaticDataReport, now time.Time) (*obs.Observation, error) {
	if !m.Valid {
		return nil, invalid(m.Header)
	}
	o, a := base(m.Header)
	d.sweepNames(now)
	if !m.PartNumber {
		if !m.ReportA.Valid {
			return nil, invalid(m.Header)
		}
		if name := cleanText(m.ReportA.Name); name != "" {
			d.names[m.UserID] = cachedName{name: name, at: now}
		}
		setName(o, a, m.ReportA.Name)
		return o, nil
	}
	b := m.ReportB
	if !b.Valid {
		return nil, invalid(m.Header)
	}
	setCallsign(o, a, b.CallSign)
	setShipType(a, b.ShipType)
	setDimensions(o, a, b.Dimension)
	if c, ok := d.names[m.UserID]; ok && now.Sub(c.at) <= staticTTL {
		setName(o, a, c.name)
	}
	return o, nil
}

func aidToNavigation(m ais.AidsToNavigationReport) (*obs.Observation, error) {
	if !m.Valid {
		return nil, invalid(m.Header)
	}
	o, a := base(m.Header)
	o.EntityType = obs.EntityUnknown
	a.ShipType = int(m.Type)
	a.ShipTypeName = "Aid to navigation"
	setPosition(o, a, float64(m.Latitude), float64(m.Longitude), m.PositionAccuracy, accuracyError(m.PositionAccuracy))
	setDimensions(o, a, m.Dimension)
	setName(o, a, cleanText(m.Name)+cleanText(m.NameExtension))
	return o, nil
}

func accuracyError(high bool) float64 {
	if high {
		return circularErrorHigh
	}
	return circularErrorLow
}

// setPosition records the fix unless it carries the "not available"
// sentinels or is out of range; the circular error is kept either way, as
// the hand decoder did.
func setPosition(o *obs.Observation, a *obs.AISData, lat, lon float64, accurate bool, circularError float64) {
	a.PositionAccuracyHigh = accurate
	o.CircularError = obs.Float(circularError)
	if lon == lonNotAvailable || lat == latNotAvailable || lon < -180 || lon > 180 || lat < -90 || lat > 90 {
		return
	}
	o.SetPosition(lat, lon)
}

func setSpeedCourse(o *obs.Observation, a *obs.AISData, sog, cog float64) {
	if sog < sogNotAvailable {
		a.SpeedOverGround = sog
		o.SpeedKnots = obs.Float(sog)
	}
	if cog < cogNotAvailable {
		a.CourseOverGround = cog
		o.Course = obs.Float(cog)
	}
}

// setHeading records the true heading, falling back to the course when the
// heading is not available.
func setHeading(o *obs.Observation, a *obs.AISData, heading uint16) {
	if heading != headingNotAvailable {
		a.TrueHeading = float64(heading)
		o.Heading = obs.Float(float64(heading))
	} else if o.Course != nil {
		o.Heading = obs.Float(*o.Course)
	}
}

func setShipType(a *obs.AISData, code uint8) {
	a.ShipType = int(code)
	a.ShipTypeName = shipTypeName(code)
}

func setDimensions(o *obs.Observation, a *obs.AISData, dim ais.FieldDimension) {
	a.DimensionA, a.DimensionB = float64(dim.A), float64(dim.B)
	a.DimensionC, a.DimensionD = float64(dim.C), float64(dim.D)
	a.LengthOverall = a.DimensionA + a.DimensionB
	a.Beam = a.DimensionC + a.DimensionD
	if a.LengthOverall > 0 || a.Beam > 0 {
		o.Dimensions = &[3]float64{a.LengthOverall, a.Beam, 0}
	}
}

func setCallsign(o *obs.Observation, a *obs.AISData, callsign string) {
	a.Callsign = cleanText(callsign)
	if a.Callsign != "" {
		o.AddIdentity(obs.IdentityCallsign, a.Callsign)
		o.DisplayName = a.Callsign
	}
}

// setName takes the vessel name as the display name; it must run after
// setCallsign so a name wins over a callsign.
func setName(o *obs.Observation, a *obs.AISData, name string) {
	if name = cleanText(name); name != "" {
		a.VesselName = name
		o.DisplayName = name
	}
}

// cleanText trims the @ padding and spaces of an AIS 6-bit string.
func cleanText(s string) string { return strings.Trim(s, "@ ") }

// formatETA renders the voyage ETA as "MM-DDTHH:MM", or "" when month or
// day is not available.
func formatETA(e ais.FieldETA) string {
	if e.Month < 1 || e.Month > 12 || e.Day < 1 || e.Day > 31 {
		return ""
	}
	hour, minute := int(e.Hour), int(e.Minute)
	if hour > 23 {
		hour = 0
	}
	if minute > 59 {
		minute = 0
	}
	return fmt.Sprintf("%02d-%02dT%02d:%02d", e.Month, e.Day, hour, minute)
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

func navStatusName(s uint8) string {
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

func shipTypeName(code uint8) string {
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
