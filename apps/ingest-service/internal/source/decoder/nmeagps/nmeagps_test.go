package nmeagps

import (
	"encoding/json"
	"errors"
	"math"
	"testing"

	"github.com/sentinel/ingest-service/internal/source/decoder"
	"github.com/sentinel/ingest-service/internal/source/transport"
)

func newDecoder(t *testing.T) decoder.Decoder {
	t.Helper()
	d, err := New(json.RawMessage(`{"sourceEntityId":"truck-1","entityType":"vehicle"}`))
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestRequiresSourceEntityID(t *testing.T) {
	if _, err := New(nil); err == nil {
		t.Error("expected an error without sourceEntityId")
	}
}

func TestGGA(t *testing.T) {
	out, err := newDecoder(t).Decode(transport.Frame{Payload: []byte("$GPGGA,123519,4807.038,N,01131.000,E,1,08,0.9,545.4,M,47.0,M,,*47")})
	if err != nil {
		t.Fatal(err)
	}
	o := out[0]
	if o.SourceEntityID != "truck-1" || o.EntityType != "VEHICLE" || o.Source != "GPS" || o.Decoder != Name {
		t.Errorf("identity/provenance = %+v", o)
	}
	if !o.HasPosition || math.Abs(o.Latitude-48.1173) > 0.001 || math.Abs(o.Longitude-11.5167) > 0.001 || o.Altitude == nil || *o.Altitude != 545.4 {
		t.Errorf("fix = %+v", o)
	}
}

func TestRMCAndVoid(t *testing.T) {
	d := newDecoder(t)
	out, err := d.Decode(transport.Frame{Payload: []byte("$GPRMC,123519,A,4807.038,N,01131.000,E,022.4,084.4,230394,003.1,W*6A")})
	if err != nil {
		t.Fatal(err)
	}
	o := out[0]
	if o.SpeedKnots == nil || *o.SpeedKnots != 22.4 || o.Course == nil || *o.Course != 84.4 || o.SourceTimestamp.Year() != 1994 && o.SourceTimestamp.Year() != 2094 {
		t.Errorf("rmc = %+v ts=%v", o, o.SourceTimestamp)
	}
	_, err = d.Decode(transport.Frame{Payload: []byte("$GPRMC,123519,V,,,,,,,230394,,*00")})
	if !errors.Is(err, decoder.ErrSkip) {
		t.Errorf("void fix should skip, got %v", err)
	}
	_, err = d.Decode(transport.Frame{Payload: []byte("$GPVTG,054.7,T,034.4,M,005.5,N,010.2,K*48")})
	if !errors.Is(err, decoder.ErrSkip) {
		t.Errorf("unsupported sentence should skip, got %v", err)
	}
	if _, err = d.Decode(transport.Frame{Payload: []byte("  ")}); err == nil || errors.Is(err, decoder.ErrSkip) {
		t.Errorf("blank line must be an error, got %v", err)
	}
}
