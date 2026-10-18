package sentinelobs

import (
	"encoding/json"
	"math"
	"testing"
	"time"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

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
