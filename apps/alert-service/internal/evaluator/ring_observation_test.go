package evaluator

import "testing"

// A ring is only observed while its anchor is live; a stale, aged-out or
// missing anchor must not count as "outside" and so must never raise an exit.
func TestRingObservation(t *testing.T) {
	cases := []struct {
		name   string
		found  bool
		state  string
		lng    float64
		inside bool
		ok     bool
	}{
		{"live anchor, track inside", true, "LIVE", 52.0, true, true},
		{"live anchor, track outside", true, "LIVE", 53.0, false, true},
		{"stale anchor is not an observation", true, "STALE", 52.0, false, false},
		{"aged-out anchor is not an observation", true, "AGED_OUT", 52.0, false, false},
		{"missing anchor is not an observation", false, "", 52.0, false, false},
	}
	for _, c := range cases {
		inside, ok := ringObservation(c.found, c.state, 24, 52, 24, c.lng, 8000)
		if ok != c.ok || inside != c.inside {
			t.Errorf("%s: inside=%v ok=%v, want inside=%v ok=%v", c.name, inside, ok, c.inside, c.ok)
		}
	}
}
