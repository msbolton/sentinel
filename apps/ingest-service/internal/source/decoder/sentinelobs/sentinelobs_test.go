package sentinelobs

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/sentinel/ingest-service/internal/obs"
	"github.com/sentinel/ingest-service/internal/source/decoder"
	"github.com/sentinel/ingest-service/internal/source/transport"
	sentinelv1 "github.com/sentinel/proto-gen/sentinel/v1"
)

func msg() *sentinelv1.Observation {
	alt := 120.0
	return &sentinelv1.Observation{
		SourceEntityId: "truck-7", EntityType: sentinelv1.EntityType_ENTITY_TYPE_VEHICLE, DisplayName: "Truck 7",
		SourceTimestamp: timestamppb.New(time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)),
		Position:        &sentinelv1.Coordinate{Latitude: 51.5, Longitude: -0.12, AltitudeMeters: &alt},
		FeedId:          "spoofed", ReplicaId: "spoofed", FencingToken: 99,
	}
}

func newDecoder(t *testing.T, opts string) decoder.Decoder {
	t.Helper()
	d, err := New(json.RawMessage(opts))
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestBinaryProtobuf(t *testing.T) {
	data, _ := proto.Marshal(msg())
	out, err := newDecoder(t, `{"defaultSource": "OSINT"}`).Decode(transport.Frame{Payload: data})
	if err != nil || len(out) != 1 {
		t.Fatalf("out %v err %v", out, err)
	}
	o := out[0]
	if o.SourceEntityID != "truck-7" || o.EntityType != "VEHICLE" || !o.HasPosition || o.Latitude != 51.5 || *o.Altitude != 120 {
		t.Errorf("observation = %+v", o)
	}
	if o.Decoder != Name || o.Source != "OSINT" || o.FeedID != "" || o.FencingToken != 0 || o.RawContentType != "application/x-protobuf" {
		t.Errorf("provenance = %+v", o)
	}
}

func TestJSONLinesInOneFrame(t *testing.T) {
	a, _ := protojson.Marshal(msg())
	m2 := msg()
	m2.SourceEntityId = "truck-8"
	m2.Source = sentinelv1.EntitySource_ENTITY_SOURCE_GPS
	b, _ := protojson.Marshal(m2)
	payload := append(append(append(a, '\r', '\n'), b...), '\n')
	out, err := newDecoder(t, `{"encoding": "json"}`).Decode(transport.Frame{Payload: payload})
	if err != nil || len(out) != 2 || out[1].SourceEntityID != "truck-8" || out[1].Source != "GPS" || out[0].Source != "" {
		t.Fatalf("out %+v err %v", out, err)
	}
	if out[0].RawContentType != "application/json" {
		t.Errorf("content type = %q", out[0].RawContentType)
	}
	// Unknown fields from a newer sender are ignored.
	if _, err := newDecoder(t, `{"encoding": "json"}`).Decode(transport.Frame{Payload: []byte(`{"sourceEntityId":"x","futureField":1}`)}); err != nil {
		t.Errorf("unknown field: %v", err)
	}
}

func TestRejections(t *testing.T) {
	d := newDecoder(t, `{}`)
	noID := msg()
	noID.SourceEntityId = ""
	data, _ := proto.Marshal(noID)
	if _, err := d.Decode(transport.Frame{Payload: data}); decoder.RejectReason(err) != "missing_identity" {
		t.Errorf("no identity: %v", err)
	}
	bad := msg()
	bad.Position.Latitude = 123
	data, _ = proto.Marshal(bad)
	if _, err := d.Decode(transport.Frame{Payload: data}); decoder.RejectReason(err) != "bad_position" {
		t.Errorf("bad position: %v", err)
	}
	// NaN fails every range comparison, so it must be refused explicitly
	// (a protobuf double can carry it and protojson accepts "NaN").
	for name, set := range map[string]func(*sentinelv1.Coordinate){
		"NaN latitude":  func(c *sentinelv1.Coordinate) { c.Latitude = math.NaN() },
		"NaN longitude": func(c *sentinelv1.Coordinate) { c.Longitude = math.NaN() },
		"infinite":      func(c *sentinelv1.Coordinate) { c.Latitude = math.Inf(1) },
	} {
		m := msg()
		set(m.Position)
		data, _ = proto.Marshal(m)
		if _, err := d.Decode(transport.Frame{Payload: data}); decoder.RejectReason(err) != "bad_position" {
			t.Errorf("%s (protobuf): %v", name, err)
		}
		line, _ := protojson.Marshal(m)
		if _, err := newDecoder(t, `{"encoding": "json"}`).Decode(transport.Frame{Payload: line}); decoder.RejectReason(err) != "bad_position" {
			t.Errorf("%s (json): %v", name, err)
		}
	}
	if _, err := d.Decode(transport.Frame{Payload: []byte{0xff, 0xff, 0xff}}); err == nil || decoder.RejectReason(err) != "" {
		t.Errorf("garbage: %v", err)
	}
	if _, err := New(json.RawMessage(`{"encoding": "xml"}`)); err == nil {
		t.Error("unknown encoding must be refused")
	}
}

// A non-finite double outside the position is dropped and the message kept
// (R18): entity-service cannot serialise NaN or ±Inf.
func TestNonFiniteValuesAreDropped(t *testing.T) {
	m := msg()
	nan, inf := math.NaN(), math.Inf(1)
	m.Heading, m.SpeedKnots = &nan, &inf
	course := 90.0
	m.Course = &course
	m.Position.AltitudeMeters = &nan
	m.Velocity = &sentinelv1.GeodeticVelocity{North: 1, East: nan, Up: 0}
	m.Orientation = &sentinelv1.Orientation{Yaw: 10, Pitch: 0, Roll: 0}
	m.Uncertainty = &sentinelv1.MeasurementUncertainty{CircularError: math.Inf(-1)}
	m.PositionCovariance = &sentinelv1.PositionCovariance{PnPn: 1, PuPu: inf}
	m.Platform = &sentinelv1.PlatformDetail{Detail: &sentinelv1.PlatformDetail_Ais{Ais: &sentinelv1.AisDetail{
		Mmsi: "235009802", SpeedOverGround: nan, CourseOverGround: 45, TrueHeading: inf,
	}}}
	data, _ := proto.Marshal(m)
	out, err := newDecoder(t, `{}`).Decode(transport.Frame{Payload: data})
	if err != nil || len(out) != 1 {
		t.Fatalf("out %v err %v", out, err)
	}
	o := out[0]
	if !o.HasPosition || o.Latitude != 51.5 || o.Longitude != -0.12 {
		t.Errorf("position = %v %v, %v", o.HasPosition, o.Latitude, o.Longitude)
	}
	if o.Heading != nil || o.SpeedKnots != nil || o.Altitude != nil || o.CircularError != nil {
		t.Errorf("non-finite optionals kept: heading %v speed %v altitude %v ce %v", o.Heading, o.SpeedKnots, o.Altitude, o.CircularError)
	}
	if o.Course == nil || *o.Course != 90 {
		t.Errorf("finite course lost: %v", o.Course)
	}
	if o.Velocity != nil || o.PosCovariance != nil {
		t.Errorf("arrays with a non-finite element kept: velocity %v covariance %v", o.Velocity, o.PosCovariance)
	}
	if o.Orientation == nil || o.Orientation[0] != 10 {
		t.Errorf("finite orientation lost: %v", o.Orientation)
	}
	if o.AIS == nil || o.AIS.SpeedOverGround != 0 || o.AIS.TrueHeading != 0 || o.AIS.CourseOverGround != 45 || o.AIS.MMSI != "235009802" {
		t.Errorf("AIS = %+v", o.AIS)
	}

	// protojson spells non-finite doubles as strings.
	line := []byte(`{"sourceEntityId":"truck-7","position":{"latitude":51.5,"longitude":-0.12},"heading":"NaN","speedKnots":"-Infinity","velocity":{"north":"Infinity"}}`)
	out, err = newDecoder(t, `{"encoding": "json"}`).Decode(transport.Frame{Payload: line})
	if err != nil || len(out) != 1 {
		t.Fatalf("json: out %v err %v", out, err)
	}
	if o := out[0]; o.Heading != nil || o.SpeedKnots != nil || o.Velocity != nil || !o.HasPosition || o.Latitude != 51.5 {
		t.Errorf("json: heading %v speed %v velocity %v position %v %v", o.Heading, o.SpeedKnots, o.Velocity, o.HasPosition, o.Latitude)
	}
}

// Every optional float, float array and platform float of an observation
// is covered, so a field added later cannot slip a NaN through.
func TestDropNonFiniteCoversEveryFloat(t *testing.T) {
	o := &obs.Observation{}
	v := reflect.ValueOf(o).Elem()
	for i := 0; i < v.NumField(); i++ {
		f := v.Field(i)
		if f.Kind() != reflect.Pointer {
			continue
		}
		switch e := f.Type().Elem(); {
		case e.Kind() == reflect.Float64:
			f.Set(reflect.New(e))
			f.Elem().SetFloat(math.NaN())
		case e.Kind() == reflect.Array && e.Elem().Kind() == reflect.Float64:
			f.Set(reflect.New(e))
			f.Elem().Index(e.Len() - 1).SetFloat(math.Inf(1))
		case e.Kind() == reflect.Struct:
			f.Set(reflect.New(e))
			for j := 0; j < e.NumField(); j++ {
				if f.Elem().Field(j).Kind() == reflect.Float64 {
					f.Elem().Field(j).SetFloat(math.NaN())
				}
			}
		}
	}
	dropNonFinite(o)
	for i := 0; i < v.NumField(); i++ {
		f, name := v.Field(i), v.Type().Field(i).Name
		if f.Kind() != reflect.Pointer || f.IsNil() {
			continue
		}
		switch e := f.Type().Elem(); e.Kind() {
		case reflect.Float64, reflect.Array:
			t.Errorf("%s kept a non-finite value", name)
		case reflect.Struct:
			for j := 0; j < e.NumField(); j++ {
				if g := f.Elem().Field(j); g.Kind() == reflect.Float64 && g.Float() != 0 {
					t.Errorf("%s.%s = %v", name, e.Field(j).Name, g.Float())
				}
			}
		}
	}
}

// A message with identities but no source_entity_id takes its source id
// from the first identity (R19), so identity-only senders without a
// canonical identity get distinct entities and observation ids.
func TestIdentityOnlyMessagesStayDistinct(t *testing.T) {
	ship := func(imo string) *sentinelv1.Observation {
		m := msg()
		m.SourceEntityId = ""
		m.Identities = []*sentinelv1.Identity{
			{Type: sentinelv1.IdentityType_IDENTITY_TYPE_UNSPECIFIED, Value: "ignored"},
			{Type: sentinelv1.IdentityType_IDENTITY_TYPE_IMO, Value: imo},
			{Type: sentinelv1.IdentityType_IDENTITY_TYPE_CALLSIGN, Value: "CALL"},
		}
		return m
	}
	// Two lines in one datagram, same timestamp.
	a, _ := protojson.Marshal(ship("9321483"))
	b, _ := protojson.Marshal(ship("9074729"))
	out, err := newDecoder(t, `{"encoding": "json"}`).Decode(transport.Frame{Payload: append(append(a, '\n'), b...)})
	if err != nil || len(out) != 2 {
		t.Fatalf("out %v err %v", out, err)
	}
	if out[0].SourceEntityID != "IMO:9321483" || out[1].SourceEntityID != "IMO:9074729" {
		t.Fatalf("source entity ids = %q, %q", out[0].SourceEntityID, out[1].SourceEntityID)
	}
	// What the runner does next: the feed-local key and the id differ.
	for _, o := range out {
		o.FeedID = "feed-1"
		o.EnsureIdentity()
	}
	if out[0].Identities[0] == out[1].Identities[0] {
		t.Errorf("both keyed on %v", out[0].Identities[0])
	}
	if out[0].Identities[0].Type != obs.IdentityFeedLocal || out[0].Identities[0].Value != "feed-1:IMO:9321483" {
		t.Errorf("key = %v", out[0].Identities[0])
	}
	if out[0].ComputeID() == out[1].ComputeID() {
		t.Error("both observations share one id")
	}

	// A sender's own source_entity_id is kept.
	m := ship("9321483")
	m.SourceEntityId = "ship-1"
	data, _ := proto.Marshal(m)
	if out, err := newDecoder(t, `{}`).Decode(transport.Frame{Payload: data}); err != nil || out[0].SourceEntityID != "ship-1" {
		t.Errorf("explicit id: out %v err %v", out, err)
	}

	// Identities that carry nothing (unspecified type, empty value) are no
	// identity at all.
	m = msg()
	m.SourceEntityId = ""
	m.Identities = []*sentinelv1.Identity{
		{Type: sentinelv1.IdentityType_IDENTITY_TYPE_UNSPECIFIED, Value: "x"},
		{Type: sentinelv1.IdentityType_IDENTITY_TYPE_IMO, Value: ""},
	}
	data, _ = proto.Marshal(m)
	if _, err := newDecoder(t, `{}`).Decode(transport.Frame{Payload: data}); decoder.RejectReason(err) != "missing_identity" {
		t.Errorf("empty identities: %v", err)
	}
}

// jsonObservation is one observation in its protobuf JSON form, as a sender
// writes it on a line or as an element of an array.
func jsonObservation(t *testing.T, edit func(*sentinelv1.Observation)) []byte {
	t.Helper()
	m := msg()
	if edit != nil {
		edit(m)
	}
	b, err := protojson.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func named(id string) func(*sentinelv1.Observation) {
	return func(m *sentinelv1.Observation) { m.SourceEntityId = id }
}

// jsonArray joins elements into one JSON array on one line.
func jsonArray(elements ...[]byte) []byte {
	return append(append([]byte("["), bytes.Join(elements, []byte(","))...), ']')
}

func repeat(element []byte, n int) [][]byte {
	out := make([][]byte, n)
	for i := range out {
		out[i] = element
	}
	return out
}

func ids(out []*obs.Observation) []string {
	var got []string
	for _, o := range out {
		got = append(got, o.SourceEntityID)
	}
	return got
}

// A JSON line that starts with [ is a batch: every element is an
// observation like a line of its own.
func TestJSONArrayInOneLine(t *testing.T) {
	a := jsonObservation(t, named("truck-7"))
	b := jsonObservation(t, func(m *sentinelv1.Observation) {
		m.SourceEntityId = "truck-8"
		m.Source = sentinelv1.EntitySource_ENTITY_SOURCE_GPS
	})
	d := newDecoder(t, `{"encoding": "json", "defaultSource": "OSINT"}`)
	for name, payload := range map[string][]byte{
		"compact":         jsonArray(a, b),
		"spaced":          []byte("  [ " + string(a) + " ,\t" + string(b) + " ]\r\n"),
		"newline-ended":   append(jsonArray(a, b), '\n'),
		"no padding":      []byte("[" + string(a) + "," + string(b) + "]"),
		"blank lines too": []byte("\n\r\n" + string(jsonArray(a, b)) + "\n\n"),
	} {
		out, err := d.Decode(transport.Frame{Payload: payload})
		if err != nil || len(out) != 2 {
			t.Fatalf("%s: out %v err %v", name, out, err)
		}
		if got := ids(out); !reflect.DeepEqual(got, []string{"truck-7", "truck-8"}) {
			t.Errorf("%s: ids = %v, want them in order", name, got)
		}
		// The default source fills in only for an element that leaves it unset.
		if out[0].Source != "OSINT" || out[1].Source != "GPS" {
			t.Errorf("%s: sources = %q, %q", name, out[0].Source, out[1].Source)
		}
		o := out[0]
		if o.Decoder != Name || o.RawContentType != "application/json" || !o.HasPosition || o.Latitude != 51.5 || o.FeedID != "" || o.FencingToken != 0 {
			t.Errorf("%s: observation = %+v", name, o)
		}
	}
}

// An empty array has nothing to observe: it is skipped like a blank frame,
// not dead-lettered.
func TestJSONEmptyArrayIsSkipped(t *testing.T) {
	d := newDecoder(t, `{"encoding": "json"}`)
	for _, payload := range []string{"[]", "[ ]", " \t[]\r\n", "[]\n[]\n", "[ \t ]"} {
		out, err := d.Decode(transport.Frame{Payload: []byte(payload)})
		if out != nil || !errors.Is(err, decoder.ErrSkip) || decoder.SkipReason(err) != "empty" {
			t.Errorf("%q: out %v err %v, want Skip(empty)", payload, out, err)
		}
	}
	// Beside other lines an empty array adds nothing and skips nothing.
	payload := append(append([]byte("[]\n"), jsonObservation(t, nil)...), '\n')
	if out, err := d.Decode(transport.Frame{Payload: payload}); err != nil || len(out) != 1 {
		t.Errorf("empty array beside an object: out %v err %v", out, err)
	}
}

// An array holds at most 100 elements. The count comes from the raw
// elements, before any of them is decoded as an observation.
func TestJSONArrayHoldsAtMostOneHundred(t *testing.T) {
	d := newDecoder(t, `{"encoding": "json"}`)
	one := jsonObservation(t, nil)
	out, err := d.Decode(transport.Frame{Payload: jsonArray(repeat(one, 100)...)})
	if err != nil || len(out) != 100 {
		t.Fatalf("100 elements: %d observations, err %v", len(out), err)
	}
	out, err = d.Decode(transport.Frame{Payload: jsonArray(repeat(one, 101)...)})
	if out != nil || decoder.RejectReason(err) != "batch_too_large" {
		t.Fatalf("101 elements: out %v err %v, want Reject(batch_too_large)", out, err)
	}
	if !strings.Contains(err.Error(), "100") {
		t.Errorf("the error does not give the limit: %v", err)
	}
	// 101 elements that are not observations at all are still a batch that
	// is too large, not a bad element: nothing was decoded to find out.
	_, err = d.Decode(transport.Frame{Payload: jsonArray(repeat([]byte("7"), 101)...)})
	if decoder.RejectReason(err) != "batch_too_large" {
		t.Errorf("101 scalars: %v, want Reject(batch_too_large)", err)
	}
}

// The frame is bounded by maxMessageBytes, but an array of tiny elements
// still has thousands of them in it. Reading stops at the 101st, so what an
// array costs does not grow with its element count.
func TestJSONArrayIsCutShortOnceItIsTooLarge(t *testing.T) {
	d := newDecoder(t, `{"encoding": "json"}`)
	frame := func(elements int) transport.Frame {
		return transport.Frame{Payload: []byte("[" + strings.Repeat("{},", elements-1) + "{}]")}
	}
	cost := func(elements int) float64 {
		f := frame(elements)
		if _, err := d.Decode(f); decoder.RejectReason(err) != "batch_too_large" {
			t.Fatalf("%d elements: %v, want Reject(batch_too_large)", elements, err)
		}
		return testing.AllocsPerRun(5, func() { _, _ = d.Decode(f) })
	}
	small, huge := cost(101), cost(20000) // 60 KB, inside the default 64 KiB message
	if huge > 2*small {
		t.Errorf("20000 elements cost %v allocations, 101 cost %v: the whole array is read before the count is checked", huge, small)
	}
}

// A bad element fails the frame, whole, and the error says which element.
// Elements are numbered from 0, as in jq's .[n].
func TestJSONArrayIsAllOrNothingAndNamesTheBadElement(t *testing.T) {
	good := jsonObservation(t, nil)
	noID := jsonObservation(t, named(""))
	badLat := jsonObservation(t, func(m *sentinelv1.Observation) { m.Position.Latitude = 123 })
	d := newDecoder(t, `{"encoding": "json"}`)
	for name, tc := range map[string]struct {
		payload []byte
		reason  string // the Reject reason, "" for a parse error
		text    string // how the error starts
	}{
		"not an observation":     {jsonArray(good, []byte(`{"position": "x"}`)), "", "element 1: not a sentinel.v1.Observation in JSON: "},
		"first element":          {jsonArray([]byte(`{"position": "x"}`), good), "", "element 0: not a sentinel.v1.Observation in JSON: "},
		"a number":               {jsonArray(good, good, []byte(`7`)), "", "element 2: not a sentinel.v1.Observation in JSON: "},
		"null":                   {jsonArray(good, []byte(`null`)), "", "element 1: not a sentinel.v1.Observation in JSON: "},
		"syntax inside element":  {jsonArray(good, []byte(`{"sourceEntityId": }`)), "", "element 1: not a sentinel.v1.Observation in JSON: "},
		"no identity":            {jsonArray(good, noID), "missing_identity", "element 1: missing_identity: "},
		"empty object":           {jsonArray(good, good, good, []byte(`{}`)), "missing_identity", "element 3: missing_identity: "},
		"position out of bounds": {jsonArray(good, good, badLat), "bad_position", "element 2: bad_position: "},
	} {
		out, err := d.Decode(transport.Frame{Payload: tc.payload})
		if out != nil || err == nil {
			t.Errorf("%s: out %v err %v, want no observations and an error", name, out, err)
			continue
		}
		if !strings.HasPrefix(err.Error(), tc.text) {
			t.Errorf("%s: error %q, want it to start %q", name, err, tc.text)
		}
		if got := decoder.RejectReason(err); got != tc.reason {
			t.Errorf("%s: reject reason %q, want %q (the stream driver dead-letters under it)", name, got, tc.reason)
		}
		if errors.Is(err, decoder.ErrSkip) {
			t.Errorf("%s: a bad element is not a skip", name)
		}
	}
}

// A nested array is not flattened: an element must be an observation.
func TestJSONNestedArraysAreBadElements(t *testing.T) {
	good := jsonObservation(t, nil)
	d := newDecoder(t, `{"encoding": "json"}`)
	for name, tc := range map[string]struct {
		payload []byte
		element int // the outer element that is an array
	}{
		"one level":       {jsonArray(jsonArray(good)), 0},
		"empty inside":    {jsonArray(good, []byte("[]")), 1},
		"after good ones": {jsonArray(good, good, jsonArray(good, good)), 2},
		"deeper":          {jsonArray(jsonArray(jsonArray(good))), 0},
	} {
		out, err := d.Decode(transport.Frame{Payload: tc.payload})
		want := fmt.Sprintf("element %d: not a sentinel.v1.Observation in JSON: ", tc.element)
		if out != nil || err == nil || !strings.HasPrefix(err.Error(), want) {
			t.Errorf("%s: out %v err %v, want an error starting %q", name, out, err, want)
		}
		if decoder.RejectReason(err) != "" || errors.Is(err, decoder.ErrSkip) {
			t.Errorf("%s: %v, want a plain parse error", name, err)
		}
	}
}

// Object lines and array lines can share a frame (a datagram, say). Each
// line is read for what it starts with, in order, and the frame is still
// all-or-nothing.
func TestJSONObjectAndArrayLinesMix(t *testing.T) {
	line := func(id string) []byte { return jsonObservation(t, named(id)) }
	d := newDecoder(t, `{"encoding": "json"}`)
	frame := func(lines ...[]byte) transport.Frame {
		return transport.Frame{Payload: bytes.Join(lines, []byte("\n"))}
	}

	out, err := d.Decode(frame(
		line("a"), jsonArray(line("b"), line("c")), nil, []byte("[]"), append(line("d"), '\r'), jsonArray(line("e")),
	))
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(out); !reflect.DeepEqual(got, []string{"a", "b", "c", "d", "e"}) {
		t.Errorf("ids = %v, want a b c d e", got)
	}

	// A bad element in the second array line: the lines before it emit nothing.
	out, err = d.Decode(frame(line("a"), jsonArray(line("b"), []byte(`{}`)), line("c")))
	if out != nil || decoder.RejectReason(err) != "missing_identity" || !strings.HasPrefix(err.Error(), "element 1: ") {
		t.Errorf("bad element after an object line: out %v err %v", out, err)
	}
	// A bad object line after an array line fails the frame as well.
	out, err = d.Decode(frame(jsonArray(line("a"), line("b")), []byte(`{"position": "x"}`)))
	if out != nil || err == nil || decoder.RejectReason(err) != "" {
		t.Errorf("bad object line after an array: out %v err %v", out, err)
	}
	// An array that is too large fails the frame, whatever else is in it.
	out, err = d.Decode(frame(line("a"), jsonArray(repeat(line("b"), 101)...)))
	if out != nil || decoder.RejectReason(err) != "batch_too_large" {
		t.Errorf("array over the cap after an object line: out %v err %v", out, err)
	}
}

// An array that is not one well-formed array on its line is an error, not a
// partial batch. An array is one line like any other, so one spread over
// several lines is cut where the lines are and fails.
func TestJSONArrayThatIsNotWellFormed(t *testing.T) {
	a := string(jsonObservation(t, nil))
	d := newDecoder(t, `{"encoding": "json"}`)
	for name, payload := range map[string]string{
		"unterminated":             "[" + a,
		"unterminated after comma": "[" + a + ",",
		"only an opening bracket":  "[",
		"text after the array":     "[" + a + "] x",
		"a value after the array":  "[" + a + "]" + a,
		"two arrays on a line":     "[" + a + "][" + a + "]",
		"missing comma":            "[" + a + " " + a + "]",
		"trailing comma":           "[" + a + ",]",
		"leading comma":            "[," + a + "]",
		"unterminated string":      `["]`,
		"spread over lines":        "[\n" + a + ",\n" + a + "\n]",
	} {
		out, err := d.Decode(transport.Frame{Payload: []byte(payload)})
		if out != nil || err == nil {
			t.Errorf("%s: out %v err %v, want no observations and an error", name, out, err)
			continue
		}
		if decoder.RejectReason(err) != "" || errors.Is(err, decoder.ErrSkip) {
			t.Errorf("%s: %v, want a plain parse error", name, err)
		}
	}
	// A full array with a trailing comma has no 101st element, so the comma
	// is what is wrong with it, not the size.
	full := "[" + strings.Repeat(a+",", 100) + "]"
	if _, err := d.Decode(transport.Frame{Payload: []byte(full)}); err == nil || decoder.RejectReason(err) != "" || !strings.HasPrefix(err.Error(), "element 100: ") {
		t.Errorf("100 elements and a trailing comma: %v, want a parse error about element 100", err)
	}
	// A line that ends early says so, in the words of the standard library.
	for _, payload := range []string{"[" + a, "[" + a + ",", "["} {
		if _, err := d.Decode(transport.Frame{Payload: []byte(payload)}); err == nil || !strings.HasSuffix(err.Error(), "unexpected EOF") {
			t.Errorf("%.20q: %v, want it to end \"unexpected EOF\"", payload, err)
		}
	}
}

// Binary protobuf is unchanged: a feed that sends protobuf does not unpack
// an array out of its frames.
func TestProtobufFramesAreNotReadAsArrays(t *testing.T) {
	out, err := newDecoder(t, `{}`).Decode(transport.Frame{Payload: jsonArray(jsonObservation(t, nil))})
	if out != nil || err == nil || errors.Is(err, decoder.ErrSkip) {
		t.Errorf("out %v err %v, want an error", out, err)
	}
}
