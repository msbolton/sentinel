package nmeaais

import (
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/sentinel/ingest-service/internal/obs"
	"github.com/sentinel/ingest-service/internal/source/decoder"
	"github.com/sentinel/ingest-service/internal/source/transport"
)

// Well-known public sample sentences (gpsd AIVDM documentation, libais and
// pyais test suites); the asserted values are what go-ais decodes.
const (
	type5Part1  = "!AIVDM,2,1,3,B,55?MbV02;H;s<HtKR20EHE:0@T4@Dn2222222216L961O5Gf0NSQEp6ClRp88,0*1C"
	type5Part2  = "!AIVDM,2,2,3,B,88888888880,2*25"
	type18      = "!AIVDM,1,1,,A,B52K>;h00Fc>jpUlNV@ikwpUoP06,0*4C"
	type19Part1 = "!AIVDM,2,1,8,A,C6:b0Kh09b3t1K4ChsS2FK008NL>`2CT@2N000000000S4h5c0,0*0E"
	type19Part2 = "!AIVDM,2,2,8,A,00000000000,0*2B"
	type21      = "!AIVDM,1,1,,B,E>kb9II9S@0`8@:9ah;0TahIW@@;V4=v:nv;h00003vP100,0*20"
	type24A     = "!AIVDM,1,1,,A,H42O55i18tMET00000000000000,2*6D"
	type24B     = "!AIVDM,1,1,,A,H42O55lti4hhhilD3nink000?050,0*40"
	type27      = "!AIVDM,1,1,,B,K815>P8=5EikDd:p,0*18"
	type4       = "!AIVDM,1,1,,A,403OviQuMGCqWrRO9>E6fE700@GO,0*4D"
)

func frame(s string) transport.Frame {
	return transport.Frame{Payload: []byte(s), ReceivedAt: time.Now(), Remote: "r1"}
}

func decodeOne(t *testing.T, d decoder.Decoder, sentences ...string) *obs.Observation {
	t.Helper()
	for _, s := range sentences[:len(sentences)-1] {
		if _, err := d.Decode(frame(s)); !errors.Is(err, decoder.ErrSkip) || decoder.SkipReason(err) != "fragment_buffered" {
			t.Fatalf("%s: fragment should be buffered, got %v", s, err)
		}
	}
	out, err := d.Decode(frame(sentences[len(sentences)-1]))
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 {
		t.Fatalf("got %d observations", len(out))
	}
	return out[0]
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-5 }

func checkVessel(t *testing.T, o *obs.Observation, mmsi string) {
	t.Helper()
	if o.Source != "AIS" || o.Decoder != Name || o.SourceEntityID != "MMSI-"+mmsi || o.TrackEnvironment != obs.EnvSeaSurface {
		t.Errorf("observation = %+v", o)
	}
	if id, ok := o.CanonicalIdentity(); !ok || id.Type != obs.IdentityMMSI || id.Value != mmsi {
		t.Errorf("identities = %v", o.Identities)
	}
	if o.AIS == nil || o.AIS.MMSI != mmsi {
		t.Fatalf("platform = %+v", o.AIS)
	}
}

func TestType1Position(t *testing.T) {
	d, _ := New(nil)
	o := decodeOne(t, d, "!AIVDM,1,1,,B,13u@Dt002s000000000000000000,0*13")
	checkVessel(t, o, "265557232")
	if o.EntityType != obs.EntityVessel || !strings.HasPrefix(o.SourceEntityID, "MMSI-") {
		t.Errorf("observation = %+v", o)
	}
	if o.AIS.MessageType != 1 || o.CircularError == nil || *o.CircularError != 10 || o.AIS.Flag != "SE" {
		t.Errorf("platform = %+v", o.AIS)
	}
	if !o.HasPosition || o.SpeedKnots == nil || !near(*o.SpeedKnots, 18.7) || o.OperationalStatus != "UNDER_WAY_USING_ENGINE" {
		t.Errorf("kinematics = %+v", o)
	}
}

func TestType5TwoFragmentsHasNoPosition(t *testing.T) {
	d, _ := New(nil)
	o := decodeOne(t, d, type5Part1, type5Part2)
	checkVessel(t, o, "351759000")
	if o.HasPosition || o.AIS.MessageType != 5 || o.Dimensions == nil {
		t.Errorf("type 5 = %+v", o)
	}
	a := o.AIS
	if a.VesselName != "EVER DIADEM" || o.DisplayName != "EVER DIADEM" || a.Callsign != "3FOF8" || a.IMO != "9134270" {
		t.Errorf("identity = %+v", a)
	}
	if a.ShipType != 70 || a.ShipTypeName != "Cargo" || a.Destination != "NEW YORK" || a.ETA != "05-15T14:00" || !near(a.Draught, 12.2) {
		t.Errorf("voyage = %+v", a)
	}
	if a.LengthOverall != 295 || a.Beam != 32 || *o.Dimensions != [3]float64{295, 32, 0} {
		t.Errorf("dimensions = %+v %v", a, o.Dimensions)
	}
	var types []string
	for _, id := range o.Identities {
		types = append(types, id.Type+"="+id.Value)
	}
	if strings.Join(types, ",") != "MMSI=351759000,IMO=9134270,CALLSIGN=3FOF8" {
		t.Errorf("identities = %v", types)
	}
	if len(d.(*Decoder).pending) != 0 {
		t.Error("fragments must be released after reassembly")
	}
}

func TestFragmentsKeyedBySender(t *testing.T) {
	d, _ := New(nil)
	a := transport.Frame{Payload: []byte(type5Part1), Remote: "a"}
	b := transport.Frame{Payload: []byte(type5Part2), Remote: "b"}
	_, _ = d.Decode(a)
	if _, err := d.Decode(b); !errors.Is(err, decoder.ErrSkip) {
		t.Errorf("fragment from another sender must not complete the message, got %v", err)
	}
}

func TestStaleFragmentsEvicted(t *testing.T) {
	d, _ := New(nil)
	old := transport.Frame{Payload: []byte(type5Part1), Remote: "r", ReceivedAt: time.Now().Add(-time.Minute)}
	_, _ = d.Decode(old)
	later := transport.Frame{Payload: []byte(type5Part2), Remote: "r", ReceivedAt: time.Now()}
	if _, err := d.Decode(later); !errors.Is(err, decoder.ErrSkip) {
		t.Errorf("stale first fragment must have been evicted, got %v", err)
	}
}

func TestType18ClassBPosition(t *testing.T) {
	d, _ := New(nil)
	o := decodeOne(t, d, type18)
	checkVessel(t, o, "338087471")
	if o.EntityType != obs.EntityVessel || o.AIS.MessageType != 18 || o.AIS.Flag != "US" {
		t.Errorf("observation = %+v", o)
	}
	if !o.HasPosition || !near(o.Latitude, 40.68454) || !near(o.Longitude, -74.072132) {
		t.Errorf("position = %v %v", o.Latitude, o.Longitude)
	}
	if o.SpeedKnots == nil || !near(*o.SpeedKnots, 0.1) || o.Course == nil || !near(*o.Course, 79.6) {
		t.Errorf("kinematics = %+v", o)
	}
	// Heading 511 is not available: it falls back to the course.
	if o.Heading == nil || !near(*o.Heading, 79.6) || o.AIS.TrueHeading != 0 {
		t.Errorf("heading = %v %v", o.Heading, o.AIS.TrueHeading)
	}
	if o.CircularError == nil || *o.CircularError != 10 || o.OperationalStatus != "" {
		t.Errorf("accuracy = %v status = %q", o.CircularError, o.OperationalStatus)
	}
}

func TestType19ExtendedClassBTwoFragments(t *testing.T) {
	d, _ := New(nil)
	o := decodeOne(t, d, type19Part1, type19Part2)
	checkVessel(t, o, "413827183")
	a := o.AIS
	if a.MessageType != 19 || a.Flag != "CN" || o.DisplayName != "DONGTAI2HAO" || a.VesselName != "DONGTAI2HAO" {
		t.Errorf("identity = %+v", o)
	}
	if !o.HasPosition || !near(o.Latitude, 30.12084) || !near(o.Longitude, 115.28905) {
		t.Errorf("position = %v %v", o.Latitude, o.Longitude)
	}
	if o.SpeedKnots == nil || !near(*o.SpeedKnots, 3.8) || o.Course == nil || !near(*o.Course, 310.9) || o.Heading == nil || *o.Heading != 310 {
		t.Errorf("kinematics = %+v", o)
	}
	if a.ShipType != 70 || a.ShipTypeName != "Cargo" || a.DimensionA != 76 || a.DimensionB != 11 || a.DimensionC != 22 || a.DimensionD != 0 {
		t.Errorf("static = %+v", a)
	}
	if o.Dimensions == nil || *o.Dimensions != [3]float64{87, 22, 0} || *o.CircularError != 1 || !a.PositionAccuracyHigh {
		t.Errorf("dimensions = %v accuracy = %v", o.Dimensions, o.CircularError)
	}
}

func TestType21AidToNavigation(t *testing.T) {
	d, _ := New(nil)
	o := decodeOne(t, d, type21)
	checkVessel(t, o, "993692005")
	if o.EntityType != obs.EntityUnknown || o.TrackEnvironment != obs.EnvSeaSurface {
		t.Errorf("entity = %s/%s", o.EntityType, o.TrackEnvironment)
	}
	if o.DisplayName != "SF APP TSS VAIS 3N" || o.AIS.MessageType != 21 {
		t.Errorf("name = %q type = %d", o.DisplayName, o.AIS.MessageType)
	}
	if o.AIS.ShipType != 18 || o.AIS.ShipTypeName != "Aid to navigation" {
		t.Errorf("aton type = %d %q", o.AIS.ShipType, o.AIS.ShipTypeName)
	}
	// A virtual AtoN is still reported at its charted position.
	if !o.HasPosition || !near(o.Latitude, 37.954717) || !near(o.Longitude, -123.178883) || *o.CircularError != 10 {
		t.Errorf("position = %v %v %v", o.Latitude, o.Longitude, o.CircularError)
	}
	if o.Dimensions != nil || o.SpeedKnots != nil || o.Course != nil || o.Heading != nil {
		t.Errorf("an AtoN carries no kinematics: %+v", o)
	}
}

func TestType24PartsMergeName(t *testing.T) {
	d, _ := New(nil)
	a := decodeOne(t, d, type24A)
	checkVessel(t, a, "271041815")
	if a.HasPosition || a.AIS.MessageType != 24 || a.DisplayName != "PROGUY" || a.AIS.VesselName != "PROGUY" || a.AIS.Flag != "TR" {
		t.Errorf("part A = %+v %+v", a, a.AIS)
	}
	if a.AIS.Callsign != "" || a.Dimensions != nil {
		t.Errorf("part A carries no part B fields: %+v", a.AIS)
	}

	b := decodeOne(t, d, type24B)
	checkVessel(t, b, "271041815")
	if b.HasPosition || b.AIS.MessageType != 24 {
		t.Errorf("part B = %+v", b)
	}
	if b.DisplayName != "PROGUY" || b.AIS.VesselName != "PROGUY" {
		t.Errorf("part B must carry the cached part A name, got %q", b.DisplayName)
	}
	if b.AIS.Callsign != "TC6163" || b.AIS.ShipType != 60 || b.AIS.ShipTypeName != "Passenger" {
		t.Errorf("part B static = %+v", b.AIS)
	}
	if b.AIS.DimensionA != 0 || b.AIS.DimensionB != 15 || b.AIS.DimensionC != 0 || b.AIS.DimensionD != 5 || b.Dimensions == nil || *b.Dimensions != [3]float64{15, 5, 0} {
		t.Errorf("part B dimensions = %+v %v", b.AIS, b.Dimensions)
	}
	found := false
	for _, id := range b.Identities {
		if id.Type == obs.IdentityCallsign && id.Value == "TC6163" {
			found = true
		}
	}
	if !found {
		t.Errorf("callsign identity missing: %v", b.Identities)
	}
}

func TestType24PartBWithoutPartA(t *testing.T) {
	d, _ := New(nil)
	b := decodeOne(t, d, type24B)
	if b.DisplayName != "TC6163" || b.AIS.VesselName != "" {
		t.Errorf("an unpaired part B is named by its callsign, got %q / %q", b.DisplayName, b.AIS.VesselName)
	}
}

func TestType24NameKeptForADay(t *testing.T) {
	for _, tc := range []struct {
		age  time.Duration
		want string
	}{
		{11 * time.Minute, "PROGUY"},
		{25 * time.Hour, "TC6163"},
	} {
		d, _ := New(nil)
		old := transport.Frame{Payload: []byte(type24A), Remote: "r", ReceivedAt: time.Now().Add(-tc.age)}
		if _, err := d.Decode(old); err != nil {
			t.Fatal(err)
		}
		out, err := d.Decode(frame(type24B))
		if err != nil {
			t.Fatal(err)
		}
		if out[0].DisplayName != tc.want {
			t.Errorf("part A %v before part B: name %q, want %q", tc.age, out[0].DisplayName, tc.want)
		}
	}
}

func TestType27LongRange(t *testing.T) {
	d, _ := New(nil)
	o := decodeOne(t, d, type27)
	checkVessel(t, o, "538005120")
	if o.AIS.MessageType != 27 || o.EntityType != obs.EntityVessel {
		t.Errorf("observation = %+v", o)
	}
	if !o.HasPosition || !near(o.Latitude, 24.601667) || !near(o.Longitude, -79.641667) {
		t.Errorf("position = %v %v", o.Latitude, o.Longitude)
	}
	if o.SpeedKnots == nil || *o.SpeedKnots != 24 || o.Course == nil || *o.Course != 174 || o.Heading == nil || *o.Heading != 174 {
		t.Errorf("kinematics = %+v", o)
	}
	// Long-range positions are 1/10 minute: a kilometre of uncertainty.
	if o.CircularError == nil || *o.CircularError != 1000 || !o.AIS.PositionAccuracyHigh {
		t.Errorf("accuracy = %v", o.CircularError)
	}
	if o.OperationalStatus != "UNDER_WAY_USING_ENGINE" || o.AIS.NavStatus != "UNDER_WAY_USING_ENGINE" {
		t.Errorf("status = %q", o.OperationalStatus)
	}
}

func TestErrorsAndSkips(t *testing.T) {
	d, _ := New(nil)
	for _, bad := range []string{
		"", "!AIVDM,1,1", "$GPGGA,1,2,3,4,5,6,7", "!AIVDM,1,1,,B,,0*13",
		// A type 1 payload cut short of its 168 bits is dead-lettered, not skipped.
		"!AIVDM,1,1,,B,13u@Dt002s0,0*13",
		// A type 24 with neither part decodable.
		"!AIVDM,1,1,,B,H3pro:4W4GtTV1?QE@00000000,0*1F",
	} {
		if _, err := d.Decode(frame(bad)); err == nil || errors.Is(err, decoder.ErrSkip) {
			t.Errorf("%q: expected a hard error, got %v", bad, err)
		}
	}
	// Type 4 (base station report) is valid but describes no vessel: skip.
	if _, err := d.Decode(frame(type4)); !errors.Is(err, decoder.ErrSkip) || decoder.SkipReason(err) != "ais_base_station" {
		t.Errorf("base station should skip, got %v", err)
	}
	// Type 8 (binary broadcast) is skipped by type number.
	if _, err := d.Decode(frame("!AIVDM,1,1,,A,85Mwp`1Kf3aCnsNvBWLi=wQuNhA5t43N`5nCuI=p<IBfVqnMgPGs,0*47")); !errors.Is(err, decoder.ErrSkip) || decoder.SkipReason(err) != "ais_type_8" {
		t.Errorf("unsupported type should skip, got %v", err)
	}
}
