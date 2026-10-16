package jsonmapped

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/sentinel/ingest-service/internal/obs"
	"github.com/sentinel/ingest-service/internal/source/transport"
)

func TestLegacyShape(t *testing.T) {
	d, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	out, err := d.Decode(transport.Frame{Payload: []byte(`{"entity_id":"test-1","entity_type":"vessel","name":"Boat","latitude":38.9,"longitude":-77.0,"speed_knots":12.5,"timestamp":"2025-01-15T12:00:00Z"}`)})
	if err != nil {
		t.Fatal(err)
	}
	o := out[0]
	if o.SourceEntityID != "test-1" || o.EntityType != obs.EntityVessel || o.DisplayName != "Boat" || !o.HasPosition || o.Latitude != 38.9 || o.SpeedKnots == nil || *o.SpeedKnots != 12.5 {
		t.Errorf("observation = %+v", o)
	}
	if !o.SourceTimestamp.Equal(time.Date(2025, 1, 15, 12, 0, 0, 0, time.UTC)) {
		t.Errorf("timestamp = %v", o.SourceTimestamp)
	}
	if o.Heading != nil || o.Altitude != nil {
		t.Error("absent fields must stay nil")
	}
}

func TestMissingIDGetsGenerated(t *testing.T) {
	d, _ := New(nil)
	out, err := d.Decode(transport.Frame{Payload: []byte(`{"latitude":1,"longitude":2}`)})
	if err != nil || out[0].SourceEntityID == "" {
		t.Fatalf("out=%v err=%v", out, err)
	}
}

func TestCustomMapWithRecordsAndUnits(t *testing.T) {
	opts := `{"records":"data.items","identityType":"ICAO24","altitudeUnit":"ft","speedUnit":"m/s","timeFormat":"unix","entityType":"aircraft","source":"ADS_B",
	  "fields":{"id":"hex","latitude":"pos.lat","longitude":"pos.lon","altitude":"alt","speed":"spd","timestamp":"seen","callsign":"flight"}}`
	d, err := New(json.RawMessage(opts))
	if err != nil {
		t.Fatal(err)
	}
	payload := `{"data":{"items":[{"hex":"abc123","pos":{"lat":51.5,"lon":-0.1},"alt":1000,"spd":100,"seen":1700000000,"flight":"BAW1"},{"hex":"nolatlon"}]}}`
	out, err := d.Decode(transport.Frame{Payload: []byte(payload)})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 {
		t.Fatalf("records = %d", len(out))
	}
	o := out[0]
	if o.SourceEntityID != "abc123" || o.EntityType != obs.EntityAircraft || o.Source != "ADS_B" {
		t.Errorf("record = %+v", o)
	}
	if *o.Altitude != 304.8 || *o.SpeedKnots < 194.3 || *o.SpeedKnots > 194.4 {
		t.Errorf("units not converted: alt=%v spd=%v", *o.Altitude, *o.SpeedKnots)
	}
	if o.SourceTimestamp.Unix() != 1700000000 {
		t.Errorf("timestamp = %v", o.SourceTimestamp)
	}
	if id, ok := o.CanonicalIdentity(); !ok || id != (obs.Identity{Type: "ICAO24", Value: "abc123"}) {
		t.Errorf("identities = %v", o.Identities)
	}
	if out[1].HasPosition {
		t.Error("record without lat/lon must have no position")
	}
}

func TestErrors(t *testing.T) {
	d, _ := New(nil)
	for _, bad := range []string{"", "  ", "not json", `{"latitude":200,"longitude":0}`} {
		if _, err := d.Decode(transport.Frame{Payload: []byte(bad)}); err == nil {
			t.Errorf("%q: expected an error", bad)
		}
	}
	if _, err := New(json.RawMessage(`{"fields":{"latitude":"a"}}`)); err == nil {
		t.Error("missing longitude path must be rejected")
	}
}
