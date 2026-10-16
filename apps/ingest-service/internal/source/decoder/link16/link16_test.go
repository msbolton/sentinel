package link16

import (
	"encoding/binary"
	"math"
	"testing"

	"github.com/sentinel/ingest-service/internal/obs"
	"github.com/sentinel/ingest-service/internal/source/transport"
)

func build(version, msgType byte, label, trackNum uint16, lat, lon int32, alt int16, speed, heading uint16) []byte {
	buf := make([]byte, 36)
	buf[0], buf[1] = version, msgType
	binary.BigEndian.PutUint16(buf[2:4], 36)
	td := buf[16:]
	binary.BigEndian.PutUint16(td[0:2], label)
	binary.BigEndian.PutUint16(td[4:6], trackNum)
	binary.BigEndian.PutUint32(td[6:10], uint32(lat))
	binary.BigEndian.PutUint32(td[10:14], uint32(lon))
	binary.BigEndian.PutUint16(td[14:16], uint16(alt))
	binary.BigEndian.PutUint16(td[16:18], speed)
	binary.BigEndian.PutUint16(td[18:20], heading)
	return buf
}

func TestAircraftTrack(t *testing.T) {
	data := build(1, 1, 0x0200, 42,
		int32(math.Round(51.5*math.Pow(2, 23)/90)), int32(math.Round(-0.1*math.Pow(2, 23)/180)),
		int16(math.Round(3000/3.048)), 2500, uint16(math.Round(90*65536/360)))
	d, _ := New(nil)
	out, err := d.Decode(transport.Frame{Payload: data})
	if err != nil {
		t.Fatal(err)
	}
	o := out[0]
	if o.SourceEntityID != "JTN-42" || o.EntityType != obs.EntityAircraft || o.TrackEnvironment != obs.EnvAir {
		t.Errorf("identity = %+v", o)
	}
	if math.Abs(o.Latitude-51.5) > 0.001 || math.Abs(o.Longitude+0.1) > 0.001 || math.Abs(*o.Altitude-3000) > 1 || math.Abs(*o.SpeedKnots-250) > 0.1 || math.Abs(*o.Heading-90) > 0.1 {
		t.Errorf("kinematics = %+v", o)
	}
	if o.Link16 == nil || o.Link16.TrackNumber != 42 || o.Link16.JSeriesLabel != "0200" {
		t.Errorf("link16 = %+v", o.Link16)
	}
	if _, ok := o.CanonicalIdentity(); ok {
		t.Error("a track number is not a canonical identity")
	}
}

func TestLabels(t *testing.T) {
	d, _ := New(nil)
	for label, want := range map[uint16]string{0x0302: obs.EntityVessel, 0x0305: obs.EntityVehicle, 0x0700: obs.EntityUnknown} {
		out, err := d.Decode(transport.Frame{Payload: build(1, 1, label, 1, 0, 0, 0, 0, 0)})
		if err != nil || out[0].EntityType != want {
			t.Errorf("label %04x: type=%v err=%v", label, out, err)
		}
	}
}

func TestHeaderValidation(t *testing.T) {
	d, _ := New(nil)
	data := build(1, 1, 0x0200, 42, 0, 0, 0, 0, 0)
	binary.BigEndian.PutUint16(data[2:4], 100)
	if _, err := d.Decode(transport.Frame{Payload: data}); err == nil {
		t.Error("length mismatch must fail")
	}
	short := make([]byte, 20)
	short[0], short[1] = 1, 1
	binary.BigEndian.PutUint16(short[2:4], 20)
	if _, err := d.Decode(transport.Frame{Payload: short}); err == nil {
		t.Error("payload too short must fail")
	}
	hdr := make([]byte, 16)
	hdr[1] = 1
	binary.BigEndian.PutUint16(hdr[2:4], 16)
	if IsHeader(hdr) {
		t.Error("version 0 must be rejected")
	}
	hdr[0], hdr[1] = 2, 16
	if !IsHeader(hdr) {
		t.Error("valid header rejected")
	}
	hdr[1] = 17
	if IsHeader(hdr) {
		t.Error("msg type 17 must be rejected")
	}
}
