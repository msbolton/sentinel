// Package beast decodes the Mode-S Beast binary format emitted by readsb
// and dump1090 on port 30005. The framer has already split the stream, so
// each frame is: a type byte (0x31 Mode-AC, 0x32 Mode-S short, 0x33 Mode-S
// long, 0x34 status), a 6-byte 12 MHz timestamp, a signal byte and the raw
// Mode S message.
//
// The decoder keeps per-aircraft state: the last even and odd CPR airborne
// position frames for global decoding (both within 10 s), the last fix for
// local decoding and plausibility checks, and the identity fields that
// decorate every observation (callsign, squawk, category). Aircraft not
// heard for five minutes are evicted. Only extended squitters (DF17/18)
// create an aircraft; Mode S replies (DF4/5/20/21) recover their address
// from the parity and are trusted only for an aircraft already known.
package beast

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/twuillemin/modes/pkg/bds/bds05/fields"
	"github.com/twuillemin/modes/pkg/bds/bds08"
	"github.com/twuillemin/modes/pkg/bds/bds09"
	bds09fields "github.com/twuillemin/modes/pkg/bds/bds09/fields"
	"github.com/twuillemin/modes/pkg/bds/bds61"
	bds61fields "github.com/twuillemin/modes/pkg/bds/bds61/fields"
	"github.com/twuillemin/modes/pkg/geo"
	modesfields "github.com/twuillemin/modes/pkg/modes/fields"
	"github.com/twuillemin/modes/pkg/modes/messages"
	modesreader "github.com/twuillemin/modes/pkg/modes/reader"

	"github.com/sentinel/ingest-service/internal/obs"
	"github.com/sentinel/ingest-service/internal/source/decoder"
	"github.com/sentinel/ingest-service/internal/source/transport"
)

// Name is the decoder name.
const Name = "beast"

// Beast frame type bytes.
const (
	typeModeAC     = 0x31
	typeModeSShort = 0x32
	typeModeSLong  = 0x33
	typeStatus     = 0x34
)

const (
	headerLen = 1 + 6 + 1 // type, 12 MHz timestamp, signal level

	// cprPairWindow is how close in time an even and an odd frame must be
	// for a global CPR decode.
	cprPairWindow = 10 * time.Second
	// evictAfter is how long an aircraft may be silent before its state is
	// dropped.
	evictAfter = 5 * time.Minute
	// sweepEvery bounds how often the eviction sweep runs.
	sweepEvery = 30 * time.Second
	// maxJumpNM is the largest distance between consecutive fixes accepted
	// as plausible.
	maxJumpNM = 500.0
	// maxRejects is how many consecutive implausible fixes are tolerated
	// before the stored fix is abandoned (two aircraft sharing an address,
	// or a fix that was itself wrong).
	maxRejects = 5

	feetToMetres    = 0.3048
	fpmToMetresPerS = 0.00508
	knotsToMetresPS = 0.514444
	earthRadiusNM   = 3440.065
)

func init() {
	decoder.Register(decoder.Definition{
		Name:        Name,
		Description: "Mode-S Beast binary from readsb or dump1090 (port 30005): ADS-B positions, velocities, identification and Mode S altitude/squawk.",
		Source:      "ADS_B",
		ContentType: "application/octet-stream",
		New:         New,
	})
}

// New builds the decoder; it takes no options.
func New(json.RawMessage) (decoder.Decoder, error) {
	return &Decoder{aircraft: map[uint32]*aircraft{}}, nil
}

// Decoder holds the per-feed receiver state.
type Decoder struct {
	aircraft  map[uint32]*aircraft
	lastSweep time.Time
}

type cprFrame struct {
	lat, lon uint32
	at       time.Time
}

type aircraft struct {
	lastSeen time.Time
	even     *cprFrame
	odd      *cprFrame

	hasFix   bool
	lat, lon float64
	rejects  int

	hasAltitude bool
	altitudeFt  int32
	callsign    string
	category    string
	squawk      string
	emergency   string
	onGround    bool
}

// Decode handles one Beast frame.
func (d *Decoder) Decode(f transport.Frame) ([]*obs.Observation, error) {
	now := f.ReceivedAt
	if now.IsZero() {
		now = time.Now()
	}
	d.sweep(now)

	if len(f.Payload) < headerLen+1 {
		return nil, fmt.Errorf("beast frame too short: %d bytes", len(f.Payload))
	}
	msg := f.Payload[headerLen:]
	switch f.Payload[0] {
	case typeModeAC:
		return nil, decoder.Skip("mode_ac")
	case typeStatus:
		return nil, decoder.Skip("status")
	case typeModeSShort:
		if len(msg) != 7 {
			return nil, fmt.Errorf("beast short frame carries %d message bytes, want 7", len(msg))
		}
	case typeModeSLong:
		if len(msg) != 14 {
			return nil, fmt.Errorf("beast long frame carries %d message bytes, want 14", len(msg))
		}
	default:
		return nil, fmt.Errorf("unknown beast frame type 0x%02x", f.Payload[0])
	}

	m, err := modesreader.ReadMessage(msg)
	if err != nil {
		return nil, fmt.Errorf("mode S: %w", err)
	}
	switch mm := m.(type) {
	case *messages.MessageDF17:
		if _, err := modesreader.CheckCRC(m, msg, nil, nil); err != nil {
			return nil, fmt.Errorf("DF17: %w", err)
		}
		return d.extendedSquitter(mm.AddressAnnounced.Address, mm.MessageExtendedSquitter, now)
	case *messages.MessageDF18:
		switch mm.ControlField {
		case modesfields.ControlFieldADSB, modesfields.ControlFieldADSBRebroadcast:
		case modesfields.ControlFieldTISBFineFormat, modesfields.ControlFieldTISBCoarseFormat, modesfields.ControlFieldTISBRelayADSB:
			return nil, decoder.Skip("tisb")
		default:
			return nil, decoder.Skip(fmt.Sprintf("df18_cf_%d", mm.ControlField))
		}
		if _, err := modesreader.CheckCRC(m, msg, nil, nil); err != nil {
			return nil, fmt.Errorf("DF18: %w", err)
		}
		return d.extendedSquitter(mm.AddressAnnounced.Address, mm.MessageExtendedSquitter, now)
	case *messages.MessageDF11:
		return nil, decoder.Skip("all_call")
	case *messages.MessageDF4:
		return d.altitudeReply(m, msg, mm.FlightStatus, mm.Altitude, mm.AltitudeReportMethod, now)
	case *messages.MessageDF20:
		return d.altitudeReply(m, msg, mm.FlightStatus, mm.Altitude, mm.AltitudeReportMethod, now)
	case *messages.MessageDF5:
		return d.identityReply(m, msg, mm.FlightStatus, mm.Identity.Identity, now)
	case *messages.MessageDF21:
		return d.identityReply(m, msg, mm.FlightStatus, mm.Identity.Identity, now)
	}
	return nil, decoder.Skip(fmt.Sprintf("df_%d", m.GetDownLinkFormat()))
}

// Close drops the state.
func (d *Decoder) Close() error {
	d.aircraft = map[uint32]*aircraft{}
	return nil
}

func (d *Decoder) sweep(now time.Time) {
	if now.Sub(d.lastSweep) < sweepEvery && !d.lastSweep.IsZero() {
		return
	}
	d.lastSweep = now
	for icao, a := range d.aircraft {
		if now.Sub(a.lastSeen) > evictAfter {
			delete(d.aircraft, icao)
		}
	}
}

// known returns the aircraft whose address the parity of a Mode S reply
// resolved to, if an extended squitter has introduced it.
func (d *Decoder) known(m messages.ModeSMessage, msg []byte) (uint32, *aircraft, error) {
	addr, err := modesreader.CheckCRC(m, msg, nil, nil)
	if err != nil {
		return 0, nil, err
	}
	a, ok := d.aircraft[uint32(addr)]
	if !ok {
		return 0, nil, decoder.Skip("unknown_icao")
	}
	return uint32(addr), a, nil
}

func (d *Decoder) altitudeReply(m messages.ModeSMessage, msg []byte, fs modesfields.FlightStatus, altFt int32, method modesfields.AltitudeReportMethod, now time.Time) ([]*obs.Observation, error) {
	icao, a, err := d.known(m, msg)
	if err != nil {
		return nil, err
	}
	a.lastSeen = now
	a.onGround = onGround(fs)
	if method != modesfields.AltitudeCodeReport25FootIncrements && method != modesfields.AltitudeCodeReport100FootIncrements {
		return nil, decoder.Skip("altitude_unavailable")
	}
	a.hasAltitude, a.altitudeFt = true, altFt
	o := d.observation(icao, a)
	setAltitude(o, altFt, false)
	return decoder.One(o, nil)
}

func (d *Decoder) identityReply(m messages.ModeSMessage, msg []byte, fs modesfields.FlightStatus, squawk string, now time.Time) ([]*obs.Observation, error) {
	icao, a, err := d.known(m, msg)
	if err != nil {
		return nil, err
	}
	a.lastSeen = now
	a.onGround = onGround(fs)
	if squawk == "" || squawk == "0000" {
		return nil, decoder.Skip("squawk_unavailable")
	}
	a.squawk = squawk
	a.emergency = squawkEmergency(squawk)
	return decoder.One(d.observation(icao, a), nil)
}

// extendedSquitter decodes the 7-byte ADS-B message of a DF17/18.
func (d *Decoder) extendedSquitter(icao uint32, me []byte, now time.Time) ([]*obs.Observation, error) {
	if len(me) != 7 {
		return nil, fmt.Errorf("extended squitter carries %d bytes, want 7", len(me))
	}
	a, ok := d.aircraft[icao]
	if !ok {
		a = &aircraft{}
		d.aircraft[icao] = a
	}
	a.lastSeen = now

	tc := me[0] >> 3
	switch {
	case tc == 0:
		return nil, decoder.Skip("no_position")
	case tc >= 1 && tc <= 4:
		return d.identification(icao, a, me)
	case tc >= 5 && tc <= 8:
		a.onGround = true
		return nil, decoder.Skip("surface_position")
	case (tc >= 9 && tc <= 18) || (tc >= 20 && tc <= 22):
		a.onGround = false
		return d.airbornePosition(icao, a, me, now)
	case tc == 19:
		return d.airborneVelocity(icao, a, me)
	case tc == 28:
		return d.aircraftStatus(icao, a, me)
	}
	return nil, decoder.Skip(fmt.Sprintf("tc_%d", tc))
}

func (d *Decoder) identification(icao uint32, a *aircraft, me []byte) ([]*obs.Observation, error) {
	id, err := bds08.ReadAircraftIdentificationAndCategory(me)
	if err != nil {
		return nil, fmt.Errorf("identification: %w", err)
	}
	callsign := strings.TrimSpace(id.Identification)
	if callsign == "" || strings.Contains(callsign, "#") {
		return nil, decoder.Skip("identification_invalid")
	}
	a.callsign = callsign
	a.category = category(id)
	return decoder.One(d.observation(icao, a), nil)
}

func (d *Decoder) airbornePosition(icao uint32, a *aircraft, me []byte, now time.Time) ([]*obs.Observation, error) {
	tc := me[0] >> 3
	frame := &cprFrame{lat: fields.ReadEncodedLatitude(me), lon: fields.ReadEncodedLongitude(me), at: now}
	odd := fields.ReadCompactPositionReportingFormat(me) == fields.CPRFormatOdd
	if odd {
		a.odd = frame
	} else {
		a.even = frame
	}
	// A zero altitude code means "not available"; the library reports it as
	// malformed, which must not fail the position.
	altFt, hasAlt := int32(0), false
	if alt, _, _, err := fields.ReadAltitude(me); err == nil {
		altFt, hasAlt = alt, true
	}

	lat, lon, err := a.resolve(odd)
	if err != nil {
		return nil, err
	}
	if a.hasFix && distanceNM(a.lat, a.lon, lat, lon) > maxJumpNM {
		a.rejects++
		if a.rejects >= maxRejects {
			a.hasFix, a.rejects = false, 0
			a.even, a.odd = nil, nil
		}
		return nil, decoder.Skip("cpr_implausible")
	}
	a.hasFix, a.lat, a.lon, a.rejects = true, lat, lon, 0
	if hasAlt {
		a.hasAltitude, a.altitudeFt = true, altFt
	}

	o := d.observation(icao, a)
	o.SetPosition(lat, lon)
	if hasAlt {
		setAltitude(o, altFt, tc >= 20)
	}
	return decoder.One(o, nil)
}

// resolve turns the stored CPR frames into a fix: globally when an even and
// an odd frame arrived within the pair window, locally against the last
// fix otherwise. odd says which frame just arrived.
func (a *aircraft) resolve(odd bool) (float64, float64, error) {
	if a.even != nil && a.odd != nil {
		gap := a.even.at.Sub(a.odd.at)
		if gap < 0 {
			gap = -gap
		}
		if gap <= cprPairWindow {
			lat, lon, err := geo.GetCPRAirborneGlobalPosition(a.even.lat, a.even.lon, a.odd.lat, a.odd.lon, !odd)
			if err == nil {
				return lat, lon, nil
			}
			// A pair straddling a latitude zone boundary cannot be solved
			// globally; fall through to a local decode when possible.
		}
	}
	if a.hasFix {
		f := a.even
		if odd {
			f = a.odd
		}
		lat, lon := geo.GetCPRAirborneLocalPosition(f.lat, f.lon, a.lat, a.lon, odd)
		return lat, lon, nil
	}
	return 0, 0, decoder.Skip("cpr_pending")
}

func (d *Decoder) airborneVelocity(icao uint32, a *aircraft, me []byte) ([]*obs.Observation, error) {
	v, err := bds09.ReadAirborneVelocity(me)
	if err != nil {
		return nil, fmt.Errorf("velocity: %w", err)
	}
	o := d.observation(icao, a)
	switch vv := v.(type) {
	case *bds09.AirborneVelocityGroundSpeedNormal:
		groundVector(o, vv.VelocityEW, vv.VelocityEWStatus, vv.DirectionEastWest, vv.VelocityNS, vv.VelocityNSStatus, vv.DirectionNorthSouth, vv.VerticalRate, vv.VerticalRateStatus)
	case *bds09.AirborneVelocityGroundSpeedSupersonic:
		groundVector(o, vv.VelocityEW, vv.VelocityEWStatus, vv.DirectionEastWest, vv.VelocityNS, vv.VelocityNSStatus, vv.DirectionNorthSouth, vv.VerticalRate, vv.VerticalRateStatus)
	case *bds09.AirborneVelocityAirSpeedNormal:
		airVector(o, me, vv.MagneticHeading, vv.MagneticHeadingStatus, vv.Airspeed, vv.AirspeedStatus, vv.VerticalRate, vv.VerticalRateStatus)
	case *bds09.AirborneVelocityAirSpeedSupersonic:
		airVector(o, me, vv.MagneticHeading, vv.MagneticHeadingStatus, vv.Airspeed, vv.AirspeedStatus, vv.VerticalRate, vv.VerticalRateStatus)
	default:
		return nil, decoder.Skip("velocity_subtype")
	}
	if o.SpeedKnots == nil && o.Heading == nil && o.Velocity == nil && o.ADSB.VerticalRate == 0 && o.ADSB.MagneticHeading == 0 && o.ADSB.TrueAirSpeed == 0 && o.ADSB.IndicatedAirSpd == 0 {
		return nil, decoder.Skip("velocity_empty")
	}
	return decoder.One(o, nil)
}

func groundVector(o *obs.Observation, ew uint16, ewStatus bds09fields.NumericValueStatus, ewDir bds09fields.DirectionEastWest, ns uint16, nsStatus bds09fields.NumericValueStatus, nsDir bds09fields.DirectionNorthSouth, vr int16, vrStatus bds09fields.NumericValueStatus) {
	up, hasVR := verticalRate(o, vr, vrStatus)
	if ewStatus == bds09fields.NVSNoInformation || nsStatus == bds09fields.NVSNoInformation {
		return
	}
	east, north := float64(ew), float64(ns)
	if ewDir == bds09fields.DEWWest {
		east = -east
	}
	if nsDir == bds09fields.DNSSouth {
		north = -north
	}
	speed := math.Hypot(east, north)
	o.SpeedKnots = obs.Float(speed)
	o.ADSB.GroundSpeed = speed
	if speed > 0 {
		track := math.Atan2(east, north) * 180 / math.Pi
		if track < 0 {
			track += 360
		}
		o.Heading = obs.Float(track)
		o.Course = obs.Float(track)
	}
	vel := [3]float64{north * knotsToMetresPS, east * knotsToMetresPS, 0}
	if hasVR {
		vel[2] = up
	}
	o.Velocity = &vel
}

func airVector(o *obs.Observation, me []byte, heading float32, headingStatus bds09fields.MagneticHeadingStatus, airspeed uint16, airspeedStatus bds09fields.NumericValueStatus, vr int16, vrStatus bds09fields.NumericValueStatus) {
	verticalRate(o, vr, vrStatus)
	if headingStatus == bds09fields.MHSAvailable {
		o.Heading = obs.Float(float64(heading))
		o.ADSB.MagneticHeading = float64(heading)
	}
	if airspeedStatus != bds09fields.NVSNoInformation {
		// Bit 25 of the ME selects true (1) or indicated (0) airspeed; the
		// library masks it off.
		if me[3]&0x80 != 0 {
			o.ADSB.TrueAirSpeed = float64(airspeed)
		} else {
			o.ADSB.IndicatedAirSpd = float64(airspeed)
		}
	}
}

// verticalRate records the vertical rate (ft/min from the library) in m/s
// and returns it when the message carries one.
func verticalRate(o *obs.Observation, vr int16, status bds09fields.NumericValueStatus) (float64, bool) {
	if status == bds09fields.NVSNoInformation {
		return 0, false
	}
	ms := float64(vr) * fpmToMetresPerS
	o.ADSB.VerticalRate = ms
	return ms, true
}

func (d *Decoder) aircraftStatus(icao uint32, a *aircraft, me []byte) ([]*obs.Observation, error) {
	st, err := bds61.ReadAircraftStatus(me)
	if err != nil {
		return nil, fmt.Errorf("aircraft status: %w", err)
	}
	em, ok := st.(*bds61.AircraftStatusEmergency)
	if !ok {
		return nil, decoder.Skip(fmt.Sprintf("aircraft_status_%d", st.GetSubtype()))
	}
	if sq := squawkFromModeA(em.ModeACode); sq != "0000" {
		a.squawk = sq
	}
	if e := emergencyStatus(em.EmergencyPriorityStatus); e != "" {
		a.emergency = e
	} else {
		a.emergency = squawkEmergency(a.squawk)
	}
	if a.squawk == "" && a.emergency == "" {
		return nil, decoder.Skip("aircraft_status_empty")
	}
	return decoder.One(d.observation(icao, a), nil)
}

// observation builds the positionless base observation carrying the
// aircraft's identity fields; callers add position and kinematics.
func (d *Decoder) observation(icao uint32, a *aircraft) *obs.Observation {
	hex := fmt.Sprintf("%06X", icao)
	o := &obs.Observation{
		Decoder: Name, Source: "ADS_B", SourceEntityID: "ICAO-" + hex,
		EntityType: obs.EntityAircraft, TrackEnvironment: obs.EnvAir, DisplayName: "ICAO " + hex,
	}
	o.AddIdentity(obs.IdentityICAO24, hex)
	adsb := &obs.ADSBData{ICAOHex: hex, Squawk: a.squawk, Emergency: a.emergency, Category: a.category, OnGround: a.onGround}
	if a.callsign != "" {
		o.DisplayName = a.callsign
		adsb.AircraftID = a.callsign
		o.AddIdentity(obs.IdentityCallsign, a.callsign)
	}
	o.ADSB = adsb
	return o
}

func setAltitude(o *obs.Observation, altFt int32, geometric bool) {
	m := float64(altFt) * feetToMetres
	o.Altitude = obs.Float(m)
	if geometric {
		o.ADSB.AltitudeGeom = m
	} else {
		o.ADSB.AltitudeBaro = m
	}
}

func onGround(fs modesfields.FlightStatus) bool {
	switch fs {
	case modesfields.FlightStatusNoAlertNoSPIOnTheGround, modesfields.FlightStatusAlertNoSPIOnTheGround, modesfields.FlightStatusAlertSPIOnTheGround:
		return true
	}
	return false
}

// category spells the emitter category the readsb way: set letter and
// value, e.g. A3 (large aircraft).
func category(id *bds08.AircraftIdentificationAndCategory) string {
	set := strings.TrimPrefix(id.Category.GetCategorySetName(), "Set ")
	if len(set) != 1 {
		return ""
	}
	value := id.Category.ToString()
	if len(value) == 0 || value[0] < '0' || value[0] > '7' {
		return ""
	}
	return set + value[:1]
}

// squawkFromModeA converts the 13-bit Mode A field of BDS 6,1 (laid out as
// the Mode S identity field: C1 A1 C2 A2 C4 A4 X B1 D1 B2 D2 B4 D4) to its
// four octal digits.
func squawkFromModeA(code uint16) string {
	bit := func(n uint) uint16 { return (code >> n) & 1 }
	c1, a1, c2, a2, c4, a4 := bit(12), bit(11), bit(10), bit(9), bit(8), bit(7)
	b1, d1, b2, d2, b4, d4 := bit(5), bit(4), bit(3), bit(2), bit(1), bit(0)
	a := a4<<2 | a2<<1 | a1
	b := b4<<2 | b2<<1 | b1
	c := c4<<2 | c2<<1 | c1
	d := d4<<2 | d2<<1 | d1
	return fmt.Sprintf("%d%d%d%d", a, b, c, d)
}

func squawkEmergency(sq string) string {
	switch sq {
	case "7500":
		return "HIJACK"
	case "7600":
		return "RADIO_FAILURE"
	case "7700":
		return "GENERAL_EMERGENCY"
	}
	return ""
}

func emergencyStatus(eps bds61fields.EmergencyPriorityStatus) string {
	switch eps {
	case bds61fields.EPSGeneralEmergency:
		return "GENERAL_EMERGENCY"
	case bds61fields.EPSLifeguardMedical:
		return "LIFEGUARD_MEDICAL"
	case bds61fields.EPSMinimumFuel:
		return "MINIMUM_FUEL"
	case bds61fields.EPSNoCommunication:
		return "RADIO_FAILURE"
	case bds61fields.EPSUnlawfulInterference:
		return "HIJACK"
	case bds61fields.EPSDownedAircraft:
		return "DOWNED_AIRCRAFT"
	}
	return ""
}

// distanceNM is the great-circle distance in nautical miles.
func distanceNM(lat1, lon1, lat2, lon2 float64) float64 {
	const rad = math.Pi / 180
	dLat := (lat2 - lat1) * rad
	dLon := (lon2 - lon1) * rad
	h := math.Sin(dLat/2)*math.Sin(dLat/2) + math.Cos(lat1*rad)*math.Cos(lat2*rad)*math.Sin(dLon/2)*math.Sin(dLon/2)
	return 2 * earthRadiusNM * math.Asin(math.Min(1, math.Sqrt(h)))
}
