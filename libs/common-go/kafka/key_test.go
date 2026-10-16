package kafka

import "testing"

func TestObservationKey(t *testing.T) {
	if got := ObservationKey("ICAO24", "A1B2C3", "opensky", "ICAO-A1B2C3"); got != "ICAO24:A1B2C3" {
		t.Errorf("identity key = %q", got)
	}
	if got := ObservationKey("", "", "my-mqtt", "trk-7"); got != "feed:my-mqtt:trk-7" {
		t.Errorf("feed-local key = %q", got)
	}
	if got := ObservationKey("MMSI", "", "ais", "x"); got != "feed:ais:x" {
		t.Errorf("empty identity value must fall back: %q", got)
	}
}
