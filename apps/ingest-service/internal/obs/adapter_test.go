package obs

import (
	"testing"
	"time"

	"github.com/sentinel/ingest-service/internal/models"
)

func msgFor(source string, payload string) *models.IngestMessage {
	return &models.IngestMessage{SourceType: source, Payload: []byte(payload), ReceivedAt: time.Date(2026, 10, 15, 12, 0, 2, 0, time.UTC), FeedID: "feed-x"}
}

func TestFromEntityPositionOpenSky(t *testing.T) {
	e := &models.EntityPosition{
		EntityID: "ICAO-a1b2c3", EntityType: models.EntityTypeAircraft, Name: "UAL123", Source: models.SourceOpenSky, FeedID: "opensky",
		Latitude: 41.9, Longitude: -87.9, Altitude: 10668, Heading: 270, SpeedKnots: 450, VelocityNorth: 1.5, VelocityEast: -231.4,
		Timestamp: time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC), TrackEnvironment: "AIR", CountryOfOrigin: "US",
		ADSBData: &models.ADSBData{ICAOHex: "a1b2c3", AircraftID: "UAL123", Registration: "n123ua", Squawk: "1200"},
	}
	o := FromEntityPosition(e, msgFor(models.SourceOpenSky, `["a1b2c3"]`), 4096)
	if o.FeedID != "opensky" || o.Driver != "opensky" || o.Decoder != DecoderOpenSky || o.Source != "OPENSKY" || o.EntityType != "AIRCRAFT" || o.TrackEnvironment != "AIR" {
		t.Errorf("provenance = %+v", o)
	}
	if !o.HasPosition || o.Altitude == nil || *o.Altitude != 10668 || o.Heading == nil || o.Course != nil || o.Velocity == nil || o.Velocity[1] != -231.4 {
		t.Errorf("kinematics = %+v", o)
	}
	want := []Identity{{IdentityICAO24, "A1B2C3"}, {IdentityCallsign, "UAL123"}, {IdentityRegistration, "N123UA"}}
	if len(o.Identities) != len(want) {
		t.Fatalf("identities = %v", o.Identities)
	}
	for i := range want {
		if o.Identities[i] != want[i] {
			t.Errorf("identity %d = %v, want %v", i, o.Identities[i], want[i])
		}
	}
	if id, ok := o.CanonicalIdentity(); !ok || id.Value != "A1B2C3" {
		t.Errorf("canonical = %v %v", id, ok)
	}
	if o.RawContentType != "application/json" || string(o.Raw) != `["a1b2c3"]` || o.RawTruncated {
		t.Errorf("raw = %q %s %v", o.RawContentType, o.Raw, o.RawTruncated)
	}
	if o.ID == "" || o.ID != FromEntityPosition(e, msgFor(models.SourceOpenSky, "x"), 0).ID {
		t.Error("id must be deterministic over feed, source id, timestamp and decoder")
	}
	e2 := *e
	e2.Timestamp = e.Timestamp.Add(time.Second)
	if FromEntityPosition(&e2, msgFor(models.SourceOpenSky, "x"), 0).ID == o.ID {
		t.Error("a new timestamp must give a new id")
	}
}

func TestIdentitiesFromOtherBlocksAndFallbacks(t *testing.T) {
	cases := []struct {
		name string
		e    *models.EntityPosition
		src  string
		want Identity
	}{
		{"ais", &models.EntityPosition{EntityID: "MMSI-367123450", Decoder: DecoderNMEAAIS, AISData: &models.AISData{MMSI: "367123450", IMO: "IMO 9074729"}}, models.SourceTCP, Identity{IdentityMMSI, "367123450"}},
		{"tle", &models.EntityPosition{EntityID: "SAT-25544", TLEData: &models.TLEData{NoradID: 25544}}, models.SourceCelesTrak, Identity{IdentityNoradID, "25544"}},
		{"cot", &models.EntityPosition{EntityID: "u-1", Decoder: DecoderCoT, CoTData: &models.CoTData{UID: "ANDROID-abc"}}, models.SourceMQTT, Identity{IdentityCoTUID, "ANDROID-abc"}},
		{"link16 is not canonical", &models.EntityPosition{EntityID: "TN-7", Decoder: DecoderLink16, Link16Data: &models.Link16Data{TrackNumber: 7}}, models.SourceTCP, Identity{IdentityFeedLocal, "feed-x:TN-7"}},
		{"prefix only", &models.EntityPosition{EntityID: "ICAO-abc123", Decoder: DecoderSBS}, models.SourceTCP, Identity{IdentityICAO24, "ABC123"}},
		{"plain json", &models.EntityPosition{EntityID: "trk-9", Decoder: DecoderJSON}, models.SourceMQTT, Identity{IdentityFeedLocal, "feed-x:trk-9"}},
	}
	for _, tc := range cases {
		o := FromEntityPosition(tc.e, msgFor(tc.src, ""), 0)
		if len(o.Identities) == 0 || o.Identities[0] != tc.want {
			t.Errorf("%s: identities = %v, want first %v", tc.name, o.Identities, tc.want)
		}
	}
	o := FromEntityPosition(&models.EntityPosition{EntityID: "MMSI-1", Decoder: DecoderNMEAAIS, AISData: &models.AISData{MMSI: "1", IMO: "IMO 9074729"}}, msgFor(models.SourceTCP, ""), 0)
	if len(o.Identities) != 2 || o.Identities[1] != (Identity{IdentityIMO, "9074729"}) {
		t.Errorf("IMO attribute = %v", o.Identities)
	}
	if o.Source != "AIS" || o.Driver != "stream" || o.Decoder != DecoderNMEAAIS {
		t.Errorf("stream provenance = %+v", o)
	}
}

func TestNoPositionAndRawTruncation(t *testing.T) {
	e := &models.EntityPosition{EntityID: "MMSI-1", NoPosition: true, Latitude: 0, Longitude: 0, AISData: &models.AISData{MMSI: "1"}}
	o := FromEntityPosition(e, msgFor(models.SourceTCP, "!AIVDM,long payload here"), 8)
	if o.HasPosition {
		t.Error("static report must have no position")
	}
	if !o.RawTruncated || len(o.Raw) != 8 {
		t.Errorf("raw = %q truncated=%v", o.Raw, o.RawTruncated)
	}
	zero := FromEntityPosition(&models.EntityPosition{EntityID: "x"}, msgFor(models.SourceMQTT, ""), 0)
	if zero.HasPosition {
		t.Error("(0,0) must be treated as no position")
	}
	if zero.ReceivedAt.IsZero() {
		t.Error("received time must default to now")
	}
}

func TestEntityTypeFor(t *testing.T) {
	cases := map[string]string{"aircraft": "AIRCRAFT", "VESSEL": "VESSEL", "platform": "UNKNOWN", "": "UNKNOWN", "satellite": "SATELLITE"}
	for in, want := range cases {
		if got := EntityTypeFor(in); got != want {
			t.Errorf("EntityTypeFor(%q) = %q, want %q", in, got, want)
		}
	}
}
