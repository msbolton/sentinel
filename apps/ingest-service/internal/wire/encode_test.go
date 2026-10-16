package wire

import (
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	commonkafka "github.com/sentinel/go-common/kafka"
	sentinelv1 "github.com/sentinel/proto-gen/sentinel/v1"

	"github.com/sentinel/ingest-service/internal/obs"
)

func sample() *obs.Observation {
	alt, hdg := 10668.0, 270.5
	return &obs.Observation{
		ID: "obs-1", FeedID: "opensky", Driver: "opensky", Decoder: "opensky-states", SourceEntityID: "ICAO-A1B2C3", Source: "OPENSKY",
		SourceTimestamp: time.Date(2026, 10, 15, 12, 0, 0, 500_000_000, time.UTC), ReceivedAt: time.Date(2026, 10, 15, 12, 0, 2, 0, time.UTC),
		EntityType: "AIRCRAFT", TrackEnvironment: "AIR", DisplayName: "UAL123",
		Identities:  []obs.Identity{{Type: obs.IdentityICAO24, Value: "A1B2C3"}, {Type: obs.IdentityCallsign, Value: "UAL123"}},
		HasPosition: true, Latitude: 41.9742, Longitude: -87.9073, Altitude: &alt, Heading: &hdg,
		Velocity: &[3]float64{1.5, -231.4, 0},
		ADSB:     &obs.ADSBData{ICAOHex: "A1B2C3", Squawk: "1200", NacP: 9},
		Raw:      []byte(`["a1b2c3"]`), RawContentType: "application/json",
		ReplicaID: "r1", FencingToken: 3,
	}
}

func TestMarshalRoundTrip(t *testing.T) {
	data, err := Marshal(sample())
	if err != nil {
		t.Fatal(err)
	}
	var m sentinelv1.Observation
	if err := proto.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	if m.ObservationId != "obs-1" || m.FeedId != "opensky" || m.Source != sentinelv1.EntitySource_ENTITY_SOURCE_OPENSKY || m.EntityType != sentinelv1.EntityType_ENTITY_TYPE_AIRCRAFT || m.TrackEnvironment != sentinelv1.TrackEnvironment_TRACK_ENVIRONMENT_AIR {
		t.Errorf("provenance/enums = %+v", &m)
	}
	if m.SourceTimestamp.AsTime() != sample().SourceTimestamp || m.ReceivedAt == nil {
		t.Errorf("timestamps = %v %v", m.SourceTimestamp, m.ReceivedAt)
	}
	if len(m.Identities) != 2 || m.Identities[0].Type != sentinelv1.IdentityType_IDENTITY_TYPE_ICAO24 || m.Identities[1].Type != sentinelv1.IdentityType_IDENTITY_TYPE_CALLSIGN {
		t.Errorf("identities = %v", m.Identities)
	}
	if m.Position == nil || m.Position.AltitudeMeters == nil || *m.Position.AltitudeMeters != 10668 || m.Heading == nil || *m.Heading != 270.5 || m.Course != nil || m.SpeedKnots != nil {
		t.Errorf("position/presence = %v heading=%v course=%v", m.Position, m.Heading, m.Course)
	}
	if m.Velocity == nil || m.Velocity.East != -231.4 {
		t.Errorf("velocity = %v", m.Velocity)
	}
	if m.Platform.GetAdsb() == nil || m.Platform.GetAdsb().Squawk != "1200" || m.Platform.GetAdsb().NacP != 9 {
		t.Errorf("platform = %v", m.Platform)
	}
	if m.Raw == nil || string(m.Raw.Data) != `["a1b2c3"]` || m.ReplicaId != "r1" || m.FencingToken != 3 {
		t.Errorf("raw/replica = %v %s %d", m.Raw, m.ReplicaId, m.FencingToken)
	}
}

func TestClassificationOnWire(t *testing.T) {
	o := sample()
	o.Classification = obs.ClassSecret
	if got := ToProto(o).Classification; got != sentinelv1.Classification_CLASSIFICATION_SECRET {
		t.Errorf("classification = %v, want SECRET", got)
	}
	o.Classification = ""
	if got := ToProto(o).Classification; got != sentinelv1.Classification_CLASSIFICATION_UNSPECIFIED {
		t.Errorf("empty classification = %v, want UNSPECIFIED", got)
	}
}

func TestNoPositionAndUnknownEnums(t *testing.T) {
	o := &obs.Observation{ID: "x", FeedID: "f", Source: "NOPE", EntityType: "", Identities: []obs.Identity{{Type: obs.IdentityFeedLocal, Value: "f:x"}}}
	m := ToProto(o)
	if m.Position != nil || m.Source != sentinelv1.EntitySource_ENTITY_SOURCE_UNSPECIFIED || m.EntityType != sentinelv1.EntityType_ENTITY_TYPE_UNKNOWN || m.Platform != nil || m.Raw != nil {
		t.Errorf("unexpected defaults: %+v", m)
	}
}

func TestKeyAndHeaders(t *testing.T) {
	if got := Key(sample()); got != "ICAO24:A1B2C3" {
		t.Errorf("key = %q", got)
	}
	o := sample()
	o.Identities = []obs.Identity{{Type: obs.IdentityFeedLocal, Value: "opensky:ICAO-A1B2C3"}}
	if got := Key(o); got != "feed:opensky:ICAO-A1B2C3" {
		t.Errorf("feed-local key = %q", got)
	}
	h := Headers(sample())
	if h[commonkafka.HeaderSchema] != "sentinel.v1.Observation" || h[commonkafka.HeaderFeedID] != "opensky" || h[commonkafka.HeaderProducer] != Producer || h[commonkafka.HeaderContentType] != commonkafka.ContentTypeProtobuf || h[commonkafka.HeaderFence] != "3" {
		t.Errorf("headers = %v", h)
	}
	o.FencingToken = 0
	if _, has := Headers(o)[commonkafka.HeaderFence]; has {
		t.Error("no fence header without a lease")
	}
}
