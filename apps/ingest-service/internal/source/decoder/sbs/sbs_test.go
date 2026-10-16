package sbs

import (
	"errors"
	"math"
	"testing"
	"time"

	"github.com/sentinel/ingest-service/internal/obs"
	"github.com/sentinel/ingest-service/internal/source/decoder"
	"github.com/sentinel/ingest-service/internal/source/transport"
)

func dec(t *testing.T, line string) (*obs.Observation, error) {
	t.Helper()
	d, _ := New(nil)
	out, err := d.Decode(transport.Frame{Payload: []byte(line)})
	if err != nil {
		return nil, err
	}
	return out[0], nil
}

func TestType3(t *testing.T) {
	o, err := dec(t, "MSG,3,1,1,a1b2c3,1,2025/01/15,12:30:00.000,2025/01/15,12:30:00.000,BAW123,35000,450,180.5,51.5074,-0.1278,,7700,,,,-1")
	if err != nil {
		t.Fatal(err)
	}
	if o.SourceEntityID != "ICAO-A1B2C3" || o.EntityType != obs.EntityAircraft || o.DisplayName != "BAW123" {
		t.Errorf("identity = %+v", o)
	}
	if !o.HasPosition || o.Latitude != 51.5074 || o.Longitude != -0.1278 || math.Abs(*o.Altitude-10668) > 0.1 || *o.SpeedKnots != 450 || *o.Heading != 180.5 {
		t.Errorf("fix = %+v", o)
	}
	if !o.SourceTimestamp.Equal(time.Date(2025, 1, 15, 12, 30, 0, 0, time.UTC)) {
		t.Errorf("timestamp = %v", o.SourceTimestamp)
	}
	if o.ADSB == nil || o.ADSB.ICAOHex != "A1B2C3" || o.ADSB.Squawk != "7700" || o.ADSB.Emergency != "GENERAL_EMERGENCY" || !o.ADSB.OnGround || o.ADSB.AircraftID != "BAW123" {
		t.Errorf("adsb = %+v", o.ADSB)
	}
	if id, ok := o.CanonicalIdentity(); !ok || id.Value != "A1B2C3" {
		t.Errorf("identities = %v", o.Identities)
	}
}

func TestPartialAndSkippedTypes(t *testing.T) {
	o, err := dec(t, "MSG,1,1,1,A1B2C3,1,2025/01/15,12:30:00.000,2025/01/15,12:30:00.000,CALLSIGN,,,,,,,,,,,")
	if err != nil {
		t.Fatalf("MSG 1 with a callsign should be a partial observation: %v", err)
	}
	if o.HasPosition || o.DisplayName != "CALLSIGN" {
		t.Errorf("partial = %+v", o)
	}
	o, err = dec(t, "MSG,4,1,1,A1B2C3,1,2025/01/15,12:30:00.000,2025/01/15,12:30:00.000,,,420,95.2,,,64,,,,,0")
	if err != nil || o.HasPosition || *o.SpeedKnots != 420 || o.Velocity == nil {
		t.Errorf("MSG 4 velocity = %+v err=%v", o, err)
	}
	_, err = dec(t, "MSG,1,1,1,A1B2C3,1,2025/01/15,12:30:00.000,2025/01/15,12:30:00.000,,,,,,,,,,,,")
	if !errors.Is(err, decoder.ErrSkip) {
		t.Errorf("empty MSG 1 should skip, got %v", err)
	}
	_, err = dec(t, "MSG,7,1,1,A1B2C3,1,2025/01/15,12:30:00.000,2025/01/15,12:30:00.000,,35000,,,,,,,,,,0")
	if !errors.Is(err, decoder.ErrSkip) {
		t.Errorf("MSG 7 should skip, got %v", err)
	}
	_, err = dec(t, "STA,,1,1,A1B2C3,1,2025/01/15,12:30:00.000,2025/01/15,12:30:00.000,RM,,,,,,,,,,,")
	if !errors.Is(err, decoder.ErrSkip) {
		t.Errorf("STA should skip, got %v", err)
	}
}

func TestErrors(t *testing.T) {
	for _, bad := range []string{
		"MSG,3,1,1,,1,2025/01/15,12:30:00.000,2025/01/15,12:30:00.000,,35000,450,180.5,51.5074,-0.1278,,,,,,0",
		"MSG,3,1,1,A1B2C3,1,2025/01/15,12:30:00.000",
		"MSG,3,1,1,A1B2C3,1,2025/01/15,12:30:00.000,2025/01/15,12:30:00.000,,35000,450,180.5,,,,,,,,0",
		"   ",
	} {
		if _, err := dec(t, bad); err == nil || errors.Is(err, decoder.ErrSkip) {
			t.Errorf("%q: expected a hard error, got %v", bad, err)
		}
	}
}

func TestMissingTimestampStaysZero(t *testing.T) {
	o, err := dec(t, "MSG,3,1,1,A1B2C3,1,,,2025/01/15,12:30:00.000,,35000,450,180.5,51.5074,-0.1278,,,,,,0")
	if err != nil {
		t.Fatal(err)
	}
	if !o.SourceTimestamp.IsZero() {
		t.Errorf("timestamp should be zero so the runner stamps receive time, got %v", o.SourceTimestamp)
	}
}
