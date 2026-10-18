package sentinelobs

import (
	"encoding/json"
	"math"
	"reflect"
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
