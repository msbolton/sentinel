package obs

import "testing"

func TestEmplacementIdentityIsCanonical(t *testing.T) {
	o := Observation{FeedID: "emplacements", SourceEntityID: "e-1"}
	o.AddIdentity(IdentityCallsign, "RADAR 1")
	o.AddIdentity(IdentityEmplacement, "e-1")
	o.EnsureIdentity()
	id, ok := o.CanonicalIdentity()
	if !ok || id != (Identity{Type: IdentityEmplacement, Value: "e-1"}) {
		t.Fatalf("canonical identity = %v %v, identities %v", id, ok, o.Identities)
	}
}
