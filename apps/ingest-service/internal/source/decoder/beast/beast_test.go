package beast

import (
	"encoding/hex"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/twuillemin/modes/pkg/modes/messages"
	modesreader "github.com/twuillemin/modes/pkg/modes/reader"

	"github.com/sentinel/ingest-service/internal/obs"
	"github.com/sentinel/ingest-service/internal/source/decoder"
	"github.com/sentinel/ingest-service/internal/source/transport"
)

// Real messages from the "1090MHz Riddle" (ICAO 40621D at 38000 ft, about
// 52.2572 N 3.9194 E when the even frame is the latest).
const (
	posEven  = "8D40621D58C382D690C8AC2863A7"
	posOdd   = "8D40621D58C386435CC412692AD6"
	ident    = "8D4840D6202CC371C32CE0576098" // KLM1023
	velocity = "8D485020994409940838175B284F" // 159 kt, 182.9 deg, -832 ft/min
	allCall  = "5D4840D6F0D36B"
)

var t0 = time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// frame wraps a Mode S message as the framer would hand it over: type,
// 6-byte timestamp, signal level, message.
func frame(msg []byte, at time.Time) transport.Frame {
	typ := byte(typeModeSLong)
	if len(msg) == 7 {
		typ = typeModeSShort
	}
	p := append([]byte{typ, 0, 0, 0, 0, 0, 1, 0x7f}, msg...)
	return transport.Frame{Payload: p, ReceivedAt: at, Remote: "127.0.0.1:30005"}
}

func rawFrame(typ byte, body []byte, at time.Time) transport.Frame {
	p := append([]byte{typ, 0, 0, 0, 0, 0, 1, 0x7f}, body...)
	return transport.Frame{Payload: p, ReceivedAt: at}
}

// signed fills the parity field of msg: the plain CRC for an extended
// squitter (xor 0) or the CRC overlaid with the address for a Mode S reply.
// CheckCRC on a non-DF11/17/18 message returns parity ^ trailer, so with a
// zero trailer it yields the parity itself.
func signed(t *testing.T, msg []byte, xor uint32) []byte {
	t.Helper()
	out := append([]byte(nil), msg...)
	out[len(out)-3], out[len(out)-2], out[len(out)-1] = 0, 0, 0
	p, err := modesreader.CheckCRC(&messages.MessageDF4{}, out, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	v := uint32(p) ^ xor
	out[len(out)-3], out[len(out)-2], out[len(out)-1] = byte(v>>16), byte(v>>8), byte(v)
	return out
}

func modf(x, y float64) float64 { return x - y*math.Floor(x/y) }

func nl(lat float64) float64 {
	switch {
	case lat == 0:
		return 59
	case math.Abs(lat) == 87:
		return 2
	case math.Abs(lat) > 87:
		return 1
	}
	a := 1 - math.Cos(math.Pi/30)
	b := math.Pow(math.Cos(math.Pi/180*math.Abs(lat)), 2)
	return math.Floor(2 * math.Pi / math.Acos(1-a/b))
}

// cprEncode is the airborne CPR encoder (DO-260B A.2.6).
func cprEncode(lat, lon float64, odd bool) (uint32, uint32) {
	i := 0.0
	if odd {
		i = 1
	}
	dLat := 360.0 / (60 - i)
	yz := math.Floor(131072*modf(lat, dLat)/dLat + 0.5)
	rlat := dLat * (yz/131072 + math.Floor(lat/dLat))
	dLon := 360.0 / math.Max(nl(rlat)-i, 1)
	xz := math.Floor(131072*modf(lon, dLon)/dLon + 0.5)
	return uint32(yz) & 0x1FFFF, uint32(xz) & 0x1FFFF
}

// positionMsg builds a DF17 airborne position (TC 11) with a valid CRC.
func positionMsg(t *testing.T, icao uint32, lat, lon float64, altFt int, odd bool) []byte {
	t.Helper()
	latc, lonc := cprEncode(lat, lon, odd)
	n := (altFt + 1000) / 25
	f := byte(0)
	if odd {
		f = 1
	}
	me := []byte{
		11 << 3,
		byte(n>>4)<<1 | 1,
		byte(n&0xF)<<4 | f<<2 | byte(latc>>15),
		byte(latc >> 7),
		byte(latc<<1) | byte(lonc>>16),
		byte(lonc >> 8),
		byte(lonc),
	}
	msg := append([]byte{0x8D, byte(icao >> 16), byte(icao >> 8), byte(icao)}, me...)
	msg = append(msg, 0, 0, 0)
	return signed(t, msg, 0)
}

func newDecoder(t *testing.T) decoder.Decoder {
	t.Helper()
	d, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func decodeOne(t *testing.T, d decoder.Decoder, f transport.Frame) *obs.Observation {
	t.Helper()
	out, err := d.Decode(f)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("got %d observations, want 1", len(out))
	}
	return out[0]
}

func expectSkip(t *testing.T, d decoder.Decoder, f transport.Frame, reason string) {
	t.Helper()
	out, err := d.Decode(f)
	if !errors.Is(err, decoder.ErrSkip) || decoder.SkipReason(err) != reason {
		t.Fatalf("want skip %q, got out=%v err=%v", reason, out, err)
	}
}

func near(a, b, tol float64) bool { return math.Abs(a-b) <= tol }

func TestRegistered(t *testing.T) {
	def, ok := decoder.Lookup(Name)
	if !ok || def.Source != "ADS_B" || def.ContentType != "application/octet-stream" {
		t.Fatalf("definition = %+v, %v", def, ok)
	}
}

func TestPositionPendingUntilPairThenFix(t *testing.T) {
	d := newDecoder(t)
	expectSkip(t, d, frame(mustHex(t, posOdd), t0), "cpr_pending")
	o := decodeOne(t, d, frame(mustHex(t, posEven), t0.Add(time.Second)))
	if !o.HasPosition || !near(o.Latitude, 52.2572, 0.01) || !near(o.Longitude, 3.9194, 0.01) {
		t.Fatalf("position = %v %v %v", o.HasPosition, o.Latitude, o.Longitude)
	}
	if o.Altitude == nil || !near(*o.Altitude, 38000*0.3048, 0.01) || !near(o.ADSB.AltitudeBaro, 38000*0.3048, 0.01) {
		t.Fatalf("altitude = %v adsb=%+v", o.Altitude, o.ADSB)
	}
	if o.SourceEntityID != "ICAO-40621D" || o.EntityType != obs.EntityAircraft || o.TrackEnvironment != obs.EnvAir || o.Decoder != Name || o.Source != "ADS_B" {
		t.Fatalf("observation = %+v", o)
	}
	if len(o.Identities) != 1 || o.Identities[0] != (obs.Identity{Type: obs.IdentityICAO24, Value: "40621D"}) {
		t.Fatalf("identities = %v", o.Identities)
	}
	if o.ADSB.ICAOHex != "40621D" || o.ADSB.OnGround || !o.SourceTimestamp.IsZero() {
		t.Fatalf("adsb = %+v ts=%v", o.ADSB, o.SourceTimestamp)
	}
}

func TestPositionOddLastAndLocalDecode(t *testing.T) {
	d := newDecoder(t)
	expectSkip(t, d, frame(mustHex(t, posEven), t0), "cpr_pending")
	o := decodeOne(t, d, frame(mustHex(t, posOdd), t0.Add(time.Second)))
	if !near(o.Latitude, 52.2658, 0.01) || !near(o.Longitude, 3.9389, 0.01) {
		t.Fatalf("odd-last fix = %v %v", o.Latitude, o.Longitude)
	}
	// With the even frame now stale, a lone even frame resolves locally
	// against the last fix.
	o = decodeOne(t, d, frame(mustHex(t, posEven), t0.Add(20*time.Second)))
	if !near(o.Latitude, 52.2572, 0.01) || !near(o.Longitude, 3.9194, 0.01) {
		t.Fatalf("local fix = %v %v", o.Latitude, o.Longitude)
	}
}

func TestPairOutsideWindowStaysPending(t *testing.T) {
	d := newDecoder(t)
	expectSkip(t, d, frame(mustHex(t, posOdd), t0), "cpr_pending")
	expectSkip(t, d, frame(mustHex(t, posEven), t0.Add(11*time.Second)), "cpr_pending")
}

func TestSyntheticPairDecodes(t *testing.T) {
	d := newDecoder(t)
	const lat, lon = 40.6413, -73.7781
	expectSkip(t, d, frame(positionMsg(t, 0xABCDEF, lat, lon, 10000, false), t0), "cpr_pending")
	o := decodeOne(t, d, frame(positionMsg(t, 0xABCDEF, lat, lon, 10000, true), t0.Add(time.Second)))
	if !near(o.Latitude, lat, 0.01) || !near(o.Longitude, lon, 0.01) || o.Altitude == nil || !near(*o.Altitude, 3048, 0.01) {
		t.Fatalf("fix = %v %v alt=%v", o.Latitude, o.Longitude, o.Altitude)
	}
}

func TestImplausibleJumpRejected(t *testing.T) {
	d := newDecoder(t)
	expectSkip(t, d, frame(mustHex(t, posOdd), t0), "cpr_pending")
	decodeOne(t, d, frame(mustHex(t, posEven), t0.Add(time.Second)))
	// A fresh pair that solves globally to New York while the fix is over
	// the North Sea. The first frame of the pair can only be decoded
	// locally (it lands in the fix's zone, so it is not asserted here); the
	// second completes a global solution far away.
	const lat, lon = 40.6413, -73.7781
	if _, err := d.Decode(frame(positionMsg(t, 0x40621D, lat, lon, 38000, false), t0.Add(20*time.Second))); err != nil && !errors.Is(err, decoder.ErrSkip) {
		t.Fatal(err)
	}
	expectSkip(t, d, frame(positionMsg(t, 0x40621D, lat, lon, 38000, true), t0.Add(21*time.Second)), "cpr_implausible")
	// The aircraft is still known: a Mode S altitude reply is accepted.
	o := decodeOne(t, d, frame(signed(t, mustHex(t, "20001838000000"), 0x40621D), t0.Add(22*time.Second)))
	if o.HasPosition || o.Altitude == nil || !near(*o.Altitude, 38000*0.3048, 0.01) {
		t.Fatalf("df4 after rejection = %+v", o)
	}
}

func TestIdentification(t *testing.T) {
	d := newDecoder(t)
	o := decodeOne(t, d, frame(mustHex(t, ident), t0))
	if o.HasPosition || o.DisplayName != "KLM1023" || o.ADSB.AircraftID != "KLM1023" || o.ADSB.Category != "A0" {
		t.Fatalf("identification = %+v adsb=%+v", o, o.ADSB)
	}
	want := []obs.Identity{{Type: obs.IdentityICAO24, Value: "4840D6"}, {Type: obs.IdentityCallsign, Value: "KLM1023"}}
	if len(o.Identities) != 2 || o.Identities[0] != want[0] || o.Identities[1] != want[1] {
		t.Fatalf("identities = %v", o.Identities)
	}
	// The callsign decorates later observations of the same aircraft.
	o = decodeOne(t, d, frame(signed(t, mustHex(t, "20001838000000"), 0x4840D6), t0.Add(time.Second)))
	if o.DisplayName != "KLM1023" || o.ADSB.AircraftID != "KLM1023" {
		t.Fatalf("df4 after ident = %+v", o)
	}
}

func TestVelocity(t *testing.T) {
	d := newDecoder(t)
	o := decodeOne(t, d, frame(mustHex(t, velocity), t0))
	if o.HasPosition || o.SpeedKnots == nil || !near(*o.SpeedKnots, 159, 0.5) || !near(o.ADSB.GroundSpeed, 159, 0.5) {
		t.Fatalf("speed = %v adsb=%+v", o.SpeedKnots, o.ADSB)
	}
	if o.Heading == nil || !near(*o.Heading, 182.9, 0.1) || o.Course == nil || !near(*o.Course, 182.9, 0.1) {
		t.Fatalf("track = %v %v", o.Heading, o.Course)
	}
	wantVR := -832 * 0.00508
	if !near(o.ADSB.VerticalRate, wantVR, 0.001) || o.Velocity == nil || !near(o.Velocity[2], wantVR, 0.001) {
		t.Fatalf("vertical rate = %v velocity=%v", o.ADSB.VerticalRate, o.Velocity)
	}
	if !near(math.Hypot(o.Velocity[0], o.Velocity[1]), 159*0.514444, 0.5) || o.Velocity[0] >= 0 {
		t.Fatalf("velocity vector = %v", o.Velocity)
	}
}

func TestModeACAndStatusSkipped(t *testing.T) {
	d := newDecoder(t)
	expectSkip(t, d, rawFrame(typeModeAC, []byte{0x0a, 0xaa}, t0), "mode_ac")
	expectSkip(t, d, rawFrame(typeStatus, make([]byte, 14), t0), "status")
	expectSkip(t, d, frame(mustHex(t, allCall), t0), "all_call")
}

func TestModeSReplyNeedsKnownAircraft(t *testing.T) {
	d := newDecoder(t)
	df4 := signed(t, mustHex(t, "20001838000000"), 0x40621D)
	expectSkip(t, d, frame(df4, t0), "unknown_icao")
	expectSkip(t, d, frame(mustHex(t, posEven), t0), "cpr_pending") // the squitter introduces 40621D
	o := decodeOne(t, d, frame(df4, t0.Add(time.Second)))
	if o.HasPosition || o.Altitude == nil || !near(*o.Altitude, 38000*0.3048, 0.01) || o.ADSB.OnGround {
		t.Fatalf("df4 = %+v", o)
	}
	// DF5 identity reply: squawk 7700 (A=7, B=7).
	df5 := signed(t, []byte{0x28, 0x00, 0x0A, 0xAA, 0, 0, 0}, 0x40621D)
	o = decodeOne(t, d, frame(df5, t0.Add(2*time.Second)))
	if o.ADSB.Squawk != "7700" || o.ADSB.Emergency != "GENERAL_EMERGENCY" || o.Altitude != nil {
		t.Fatalf("df5 = %+v", o.ADSB)
	}
}

func TestAircraftStatusEmergency(t *testing.T) {
	d := newDecoder(t)
	// TC 28 subtype 1, EPS 5 (unlawful interference), Mode A 7500.
	// 7500: A=7 (a1 a2 a4), B=5 (b1 b4) -> 0x0A80 | 0x0020 | 0x0002 = 0x0AA2.
	me := []byte{28<<3 | 1, 5<<5 | 0x0A, 0xA2, 0, 0, 0, 0}
	msg := append([]byte{0x8D, 0x40, 0x62, 0x1D}, me...)
	msg = append(msg, 0, 0, 0)
	o := decodeOne(t, d, frame(signed(t, msg, 0), t0))
	if o.ADSB.Squawk != "7500" || o.ADSB.Emergency != "HIJACK" {
		t.Fatalf("status = %+v", o.ADSB)
	}
}

func TestOtherFormatsSkipped(t *testing.T) {
	d := newDecoder(t)
	// Surface position (TC 5) marks the aircraft on the ground and is skipped.
	me := []byte{5 << 3, 0, 0, 0, 0, 0, 0}
	msg := append([]byte{0x8D, 0x48, 0x40, 0xD6}, me...)
	expectSkip(t, d, frame(signed(t, append(msg, 0, 0, 0), 0), t0), "surface_position")
	if o := decodeOne(t, d, frame(mustHex(t, ident), t0)); !o.ADSB.OnGround {
		t.Fatalf("on ground not carried: %+v", o.ADSB)
	}
	// DF18 TIS-B (CF 2) and an unsupported DF0 are skipped.
	expectSkip(t, d, frame(append([]byte{18<<3 | 2}, make([]byte, 13)...), t0), "tisb")
	expectSkip(t, d, frame(make([]byte, 7), t0), "df_0")
	// Operational status (TC 31) is not decoded.
	msg = append([]byte{0x8D, 0x48, 0x40, 0xD6}, []byte{31 << 3, 0, 0, 0, 0, 0, 0}...)
	expectSkip(t, d, frame(signed(t, append(msg, 0, 0, 0), 0), t0), "tc_31")
}

func TestEviction(t *testing.T) {
	d := newDecoder(t)
	decodeOne(t, d, frame(mustHex(t, ident), t0))
	df4 := signed(t, mustHex(t, "20001838000000"), 0x4840D6)
	decodeOne(t, d, frame(df4, t0.Add(time.Minute)))
	// Silent for more than five minutes: the state is swept and the reply
	// is no longer trusted.
	expectSkip(t, d, frame(df4, t0.Add(7*time.Minute)), "unknown_icao")
}

func TestCorruptFrameIsAnError(t *testing.T) {
	d := newDecoder(t)
	bad := mustHex(t, posEven)
	bad[len(bad)-1] ^= 0x01
	if _, err := d.Decode(frame(bad, t0)); err == nil || errors.Is(err, decoder.ErrSkip) {
		t.Fatalf("corrupt CRC: err = %v", err)
	}
	if _, err := d.Decode(rawFrame(typeModeSLong, make([]byte, 5), t0)); err == nil || errors.Is(err, decoder.ErrSkip) {
		t.Fatalf("short long frame: err = %v", err)
	}
	if _, err := d.Decode(rawFrame(0x35, make([]byte, 14), t0)); err == nil || errors.Is(err, decoder.ErrSkip) {
		t.Fatalf("unknown type: err = %v", err)
	}
	if _, err := d.Decode(transport.Frame{Payload: []byte{typeModeSShort}}); err == nil || errors.Is(err, decoder.ErrSkip) {
		t.Fatalf("truncated: err = %v", err)
	}
}
