package nmeaais

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/sentinel/ingest-service/internal/obs"
	"github.com/sentinel/ingest-service/internal/source/decoder"
	"github.com/sentinel/ingest-service/internal/source/transport"
)

func frame(s string) transport.Frame {
	return transport.Frame{Payload: []byte(s), ReceivedAt: time.Now(), Remote: "r1"}
}

func TestType1Position(t *testing.T) {
	d, _ := New(nil)
	out, err := d.Decode(frame("!AIVDM,1,1,,B,13u@Dt002s000000000000000000,0*13"))
	if err != nil {
		t.Fatal(err)
	}
	o := out[0]
	if o.EntityType != obs.EntityVessel || !strings.HasPrefix(o.SourceEntityID, "MMSI-") || o.TrackEnvironment != obs.EnvSeaSurface || o.Source != "AIS" {
		t.Errorf("observation = %+v", o)
	}
	if o.AIS == nil || o.AIS.MMSI == "" || o.AIS.MessageType < 1 || o.AIS.MessageType > 3 || o.CircularError == nil {
		t.Errorf("platform = %+v", o.AIS)
	}
	if id, ok := o.CanonicalIdentity(); !ok || id.Type != obs.IdentityMMSI {
		t.Errorf("identities = %v", o.Identities)
	}
}

func TestType5TwoFragmentsHasNoPosition(t *testing.T) {
	d, _ := New(nil)
	_, err := d.Decode(frame("!AIVDM,2,1,3,B,55?MbV02>H97ac<H4eEK6WSF220l4hB222222222220l1@E846RLAT0Eq,0*2C"))
	if !errors.Is(err, decoder.ErrSkip) || decoder.SkipReason(err) != "fragment_buffered" {
		t.Fatalf("first fragment should be buffered, got %v", err)
	}
	out, err := d.Decode(frame("!AIVDM,2,2,3,B,888888888888880,2*21"))
	if err != nil {
		t.Fatal(err)
	}
	o := out[0]
	if o.HasPosition || o.AIS == nil || o.AIS.MessageType != 5 || o.Dimensions == nil {
		t.Errorf("type 5 = %+v", o)
	}
	if len(d.(*Decoder).pending) != 0 {
		t.Error("fragments must be released after reassembly")
	}
}

func TestFragmentsKeyedBySender(t *testing.T) {
	d, _ := New(nil)
	a := transport.Frame{Payload: []byte("!AIVDM,2,1,3,B,55?MbV02>H97ac<H4eEK6WSF220l4hB222222222220l1@E846RLAT0Eq,0*2C"), Remote: "a"}
	b := transport.Frame{Payload: []byte("!AIVDM,2,2,3,B,888888888888880,2*21"), Remote: "b"}
	_, _ = d.Decode(a)
	if _, err := d.Decode(b); !errors.Is(err, decoder.ErrSkip) {
		t.Errorf("fragment from another sender must not complete the message, got %v", err)
	}
}

func TestStaleFragmentsEvicted(t *testing.T) {
	d, _ := New(nil)
	old := transport.Frame{Payload: []byte("!AIVDM,2,1,3,B,55?MbV02>H97ac<H4eEK6WSF220l4hB222222222220l1@E846RLAT0Eq,0*2C"), Remote: "r", ReceivedAt: time.Now().Add(-time.Minute)}
	_, _ = d.Decode(old)
	later := transport.Frame{Payload: []byte("!AIVDM,2,2,3,B,888888888888880,2*21"), Remote: "r", ReceivedAt: time.Now()}
	if _, err := d.Decode(later); !errors.Is(err, decoder.ErrSkip) {
		t.Errorf("stale first fragment must have been evicted, got %v", err)
	}
}

func TestErrorsAndSkips(t *testing.T) {
	d, _ := New(nil)
	for _, bad := range []string{"", "!AIVDM,1,1", "$GPGGA,1,2,3,4,5,6,7", "!AIVDM,1,1,,B,,0*13"} {
		if _, err := d.Decode(frame(bad)); err == nil || errors.Is(err, decoder.ErrSkip) {
			t.Errorf("%q: expected a hard error, got %v", bad, err)
		}
	}
	// Type 4 (base station report) is valid but unsupported: skip.
	if _, err := d.Decode(frame("!AIVDM,1,1,,A,403OviQuMGCqWrRO9>E6fE700@GO,0*4D")); !errors.Is(err, decoder.ErrSkip) {
		t.Errorf("unsupported type should skip, got %v", err)
	}
}

func TestSixBitText(t *testing.T) {
	bits := make([]byte, 30)
	enc := func(off int, v byte) {
		for i := 5; i >= 0; i-- {
			if v&(1<<uint(i)) != 0 {
				bits[off+(5-i)] = 1
			}
		}
	}
	enc(0, 8)
	enc(6, 5)
	enc(12, 12)
	enc(18, 12)
	enc(24, 15)
	if got := sixBitText(bits, 0, 30); got != "HELLO" {
		t.Errorf("got %q", got)
	}
	bits = make([]byte, 18)
	enc(0, 48)
	enc(6, 49)
	enc(12, 50)
	if got := sixBitText(bits, 0, 18); got != "012" {
		t.Errorf("digits = %q", got)
	}
}
