// Package aismap turns decoded AIS messages (github.com/BertoldVdb/go-ais
// packets) into observations. It is shared by the nmea-ais decoder, which
// gets packets from NMEA sentences, and the aisstream driver, which gets
// the same packets as JSON.
//
// Observations are produced for class A position reports (1-3), class B
// position reports (18, 19), the long-range broadcast (27), the static and
// voyage report (5), the class B static report (24, parts A and B) and aids
// to navigation (21). Base station reports (4, 11) and every other type are
// skipped.
//
// A Mapper remembers each vessel's static and voyage detail (name, IMO,
// callsign, ship type, dimensions, destination, ETA, draught) for a day
// after its last message and fills it into every later observation of that
// vessel. Entities keep one platform detail block per kind and each
// observation replaces it, so without this a position report would wipe
// the ship type and destination the last static report set.
package aismap

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	ais "github.com/BertoldVdb/go-ais"

	"github.com/sentinel/ingest-service/internal/obs"
	"github.com/sentinel/ingest-service/internal/source/decoder"
)

const (
	// staticTTL is how long a vessel's static detail is kept after its
	// last message of any kind.
	staticTTL = 24 * time.Hour
	// sweepEvery bounds how often expired static detail is swept.
	sweepEvery = 10 * time.Minute

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

// static is what a Mapper remembers about one vessel.
type static struct {
	at                     time.Time
	name, imo, callsign    string
	shipType               int
	shipTypeName           string
	destination, eta       string
	draught                float64
	dimA, dimB, dimC, dimD float64
}

// Mapper maps packets to observations and keeps per-vessel static detail.
// It is not safe for concurrent use.
type Mapper struct {
	decoder   string
	statics   map[string]*static
	lastSweep time.Time
}

// NewMapper builds a mapper whose observations name decoderName as their
// decoder.
func NewMapper(decoderName string) *Mapper {
	return &Mapper{decoder: decoderName, statics: map[string]*static{}}
}

// Reset forgets every vessel's static detail.
func (m *Mapper) Reset() { m.statics = map[string]*static{} }

// Known reports how many vessels' static detail is held.
func (m *Mapper) Known() int { return len(m.statics) }

// FromPacket maps one packet received at now. Packets that describe no
// vessel are skipped (decoder.Skip); invalid ones are errors.
func (m *Mapper) FromPacket(pkt ais.Packet, now time.Time) (*obs.Observation, error) {
	var (
		o   *obs.Observation
		err error
	)
	switch p := pkt.(type) {
	case ais.PositionReport:
		o, err = classAPosition(p)
	case ais.StandardClassBPositionReport:
		o, err = classBPosition(p)
	case ais.ExtendedClassBPositionReport:
		o, err = extendedClassBPosition(p)
	case ais.LongRangeAisBroadcastMessage:
		o, err = longRangePosition(p)
	case ais.ShipStaticData:
		o, err = staticAndVoyage(p)
	case ais.StaticDataReport:
		o, err = classBStatic(p)
	case ais.AidsToNavigationReport:
		o, err = aidToNavigation(p)
	case ais.BaseStationReport:
		return nil, decoder.Skip("ais_base_station")
	case nil:
		return nil, fmt.Errorf("no AIS packet")
	default:
		return nil, decoder.Skip(fmt.Sprintf("ais_type_%d", pkt.GetHeader().MessageID))
	}
	if err != nil {
		return nil, err
	}
	o.Decoder = m.decoder
	m.enrich(o, now)
	return o, nil
}

// enrich learns the static detail an observation carries and fills in what
// it lacks from what earlier messages of the same vessel carried. Position
// and kinematic fields are never touched.
func (m *Mapper) enrich(o *obs.Observation, now time.Time) {
	a := o.AIS
	if a == nil || o.EntityType != obs.EntityVessel {
		return
	}
	m.sweep(now)
	s := m.statics[a.MMSI]
	if s == nil {
		s = &static{}
	}
	learned := learn(s, a)
	if learned || m.statics[a.MMSI] != nil {
		s.at = now
		m.statics[a.MMSI] = s
	}
	fill(o, a, s)
}

// learn copies the static fields a message carries into s, and reports
// whether there were any.
func learn(s *static, a *obs.AISData) bool {
	got := false
	if a.VesselName != "" {
		s.name, got = a.VesselName, true
	}
	if a.IMO != "" {
		s.imo, got = a.IMO, true
	}
	if a.Callsign != "" {
		s.callsign, got = a.Callsign, true
	}
	// Type 0 is "not available"; it never replaces a known type.
	if a.ShipTypeName != "" && (a.ShipType != 0 || s.shipTypeName == "") {
		s.shipType, s.shipTypeName, got = a.ShipType, a.ShipTypeName, true
	}
	if a.LengthOverall > 0 || a.Beam > 0 {
		s.dimA, s.dimB, s.dimC, s.dimD, got = a.DimensionA, a.DimensionB, a.DimensionC, a.DimensionD, true
	}
	// The voyage report is authoritative for its fields, empty or not: a
	// blank destination means the crew cleared it.
	if a.MessageType == 5 {
		s.destination, s.eta, s.draught, got = a.Destination, a.ETA, a.Draught, true
	}
	return got
}

// fill copies remembered static detail into the fields a message left empty.
func fill(o *obs.Observation, a *obs.AISData, s *static) {
	if a.IMO == "" && s.imo != "" {
		a.IMO = s.imo
		o.AddIdentity(obs.IdentityIMO, a.IMO)
	}
	if a.Callsign == "" && s.callsign != "" {
		a.Callsign = s.callsign
		o.AddIdentity(obs.IdentityCallsign, a.Callsign)
		if a.VesselName == "" {
			o.DisplayName = a.Callsign
		}
	}
	if a.VesselName == "" && s.name != "" {
		setName(o, a, s.name)
	}
	// Type 0 is "not available", so a remembered type replaces it.
	if (a.ShipTypeName == "" || a.ShipType == 0) && s.shipType != 0 {
		a.ShipType, a.ShipTypeName = s.shipType, s.shipTypeName
	}
	if a.LengthOverall == 0 && a.Beam == 0 && (s.dimA+s.dimB > 0 || s.dimC+s.dimD > 0) {
		setDimensions(o, a, ais.FieldDimension{A: uint16(s.dimA), B: uint16(s.dimB), C: uint8(s.dimC), D: uint8(s.dimD)})
	}
	if a.MessageType != 5 {
		if a.Destination == "" {
			a.Destination = s.destination
		}
		if a.ETA == "" {
			a.ETA = s.eta
		}
		if a.Draught == 0 {
			a.Draught = s.draught
		}
	}
}

// sweep drops vessels not heard from for staticTTL, at most every sweepEvery.
func (m *Mapper) sweep(now time.Time) {
	if now.Sub(m.lastSweep) < sweepEvery {
		return
	}
	m.lastSweep = now
	for mmsi, s := range m.statics {
		if now.Sub(s.at) > staticTTL {
			delete(m.statics, mmsi)
		}
	}
}

func invalid(h ais.Header) error {
	return fmt.Errorf("invalid AIS type %d message from %d", h.MessageID, h.UserID)
}

// base starts an observation for the vessel identified by the header.
func base(h ais.Header) (*obs.Observation, *obs.AISData) {
	mmsi := strconv.FormatUint(uint64(h.UserID), 10)
	o := &obs.Observation{
		Source: "AIS", SourceEntityID: "MMSI-" + mmsi,
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

// classBStatic maps a type 24. Part A carries only the name and part B the
// rest; the Mapper's static detail pairs them.
func classBStatic(m ais.StaticDataReport) (*obs.Observation, error) {
	if !m.Valid {
		return nil, invalid(m.Header)
	}
	o, a := base(m.Header)
	if !m.PartNumber {
		if !m.ReportA.Valid {
			return nil, invalid(m.Header)
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
