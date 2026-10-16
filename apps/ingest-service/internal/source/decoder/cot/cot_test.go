package cot

import (
	"errors"
	"math"
	"testing"

	"github.com/sentinel/ingest-service/internal/obs"
	"github.com/sentinel/ingest-service/internal/source/decoder"
	"github.com/sentinel/ingest-service/internal/source/transport"
)

const sample = `<event uid="test-uid" type="a-f-A" time="2025-01-15T12:00:00Z" start="2025-01-15T12:00:00Z" stale="2025-01-15T12:05:00Z" how="m-g">
	<point lat="38.9" lon="-77.0" hae="100" ce="10" le="5"/>
	<detail><track speed="50" course="90"/><contact callsign="ALPHA1"/></detail>
</event>`

func TestDecode(t *testing.T) {
	d, _ := New(nil)
	out, err := d.Decode(transport.Frame{Payload: []byte(sample)})
	if err != nil {
		t.Fatal(err)
	}
	o := out[0]
	if o.SourceEntityID != "test-uid" || o.EntityType != obs.EntityAircraft || o.TrackEnvironment != obs.EnvAir || o.Affiliation != "FRIENDLY" || o.DisplayName != "ALPHA1" {
		t.Errorf("classification = %+v", o)
	}
	if !o.HasPosition || o.Latitude != 38.9 || *o.Altitude != 100 || *o.CircularError != 10 {
		t.Errorf("fix = %+v", o)
	}
	if o.Velocity == nil || math.Abs(o.Velocity[1]-50) > 0.1 || math.Abs(o.Velocity[0]) > 0.1 || math.Abs(*o.SpeedKnots-97.19) > 0.01 {
		t.Errorf("kinematics = %+v", o.Velocity)
	}
	if o.CoT == nil || o.CoT.CoTType != "a-f-A" || o.CoT.How != "m-g" || o.CoT.CE != 10 {
		t.Errorf("cot detail = %+v", o.CoT)
	}
	if id, ok := o.CanonicalIdentity(); !ok || id != (obs.Identity{Type: obs.IdentityCoTUID, Value: "test-uid"}) {
		t.Errorf("identities = %v", o.Identities)
	}
	if o.SourceTimestamp.IsZero() {
		t.Error("timestamp not parsed")
	}
}

func TestClassification(t *testing.T) {
	cases := map[string][2]string{
		"a-f-A-M-F": {obs.EntityAircraft, obs.EnvAir},
		"a-h-S-X":   {obs.EntityVessel, obs.EnvSeaSurface},
		"a-n-G-E-V": {obs.EntityVehicle, obs.EnvGround},
		"a-f-G-U-C": {obs.EntityPerson, obs.EnvGround},
		"a-f-U-S":   {obs.EntityVessel, obs.EnvSubsurface},
		"a-f-X":     {obs.EntityUnknown, obs.EnvUnknown},
		"a":         {obs.EntityUnknown, obs.EnvUnknown},
	}
	for typ, want := range cases {
		if got := entityType(typ); got != want[0] {
			t.Errorf("entityType(%q) = %q, want %q", typ, got, want[0])
		}
		if got := environment(typ); got != want[1] {
			t.Errorf("environment(%q) = %q, want %q", typ, got, want[1])
		}
	}
}

func TestSkipsAndErrors(t *testing.T) {
	d, _ := New(nil)
	if _, err := d.Decode(transport.Frame{Payload: []byte(`<event uid="x" type="t-x-c" time="" start="" stale=""><point lat="0" lon="0"/></event>`)}); !errors.Is(err, decoder.ErrSkip) {
		t.Errorf("tasking event should skip, got %v", err)
	}
	for _, bad := range []string{"", "<event>", "not xml", `<event type="a-f-A"><point lat="1" lon="2"/></event>`} {
		if _, err := d.Decode(transport.Frame{Payload: []byte(bad)}); err == nil || errors.Is(err, decoder.ErrSkip) {
			t.Errorf("%q: expected an error, got %v", bad, err)
		}
	}
}
