package events

import (
	"encoding/json"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/sentinel/go-common/kafka"
	sentinelv1 "github.com/sentinel/proto-gen/sentinel/v1"

	"github.com/sentinel/entity-service/internal/store"
)

func TestParseTimeAcceptsPostgresAndRFC3339(t *testing.T) {
	for _, in := range []string{"2026-10-15 12:00:00.5+00", "2026-10-15 12:00:00.5-05", "2026-10-15T12:00:00.5Z", "2026-10-15 12:00:00+00:00"} {
		if got, ok := ParseTime(in); !ok || got.Year() != 2026 || got.Month() != 10 {
			t.Errorf("%q: %v %v", in, got, ok)
		}
	}
	if _, ok := ParseTime(""); ok {
		t.Error("empty must not parse")
	}
}

func TestEntityProtoMapsRow(t *testing.T) {
	alt, hdg, ce := 10668.0, 270.0, 12.5
	feed, env, last := "opensky", "AIR", "2026-10-15 12:00:02+00"
	rec := &store.EntityRecord{
		ID: "e1", EntityType: "AIRCRAFT", Name: "UAL123", Source: "OPENSKY", Classification: "UNCLASSIFIED", FeedID: &feed,
		Position: &store.GeoPoint{Lat: 41.9, Lon: -87.9}, Altitude: &alt, Heading: &hdg, CircularError: &ce,
		Metadata: map[string]interface{}{"k": "v", "n": 1.0}, Affiliations: nil, TrackEnvironment: &env,
		Kinematics:   map[string]interface{}{"velocity": map[string]interface{}{"north": 1.5, "east": -2.0, "up": 0.0}},
		PlatformData: map[string]interface{}{"adsb": map[string]interface{}{"icao_hex": "A1B2C3", "squawk": "1200"}, "ais": map[string]interface{}{"mmsi": "123", "navStatus": "MOORED"}},
		AgeoutState:  "LIVE", CreatedAt: "2026-10-15 12:00:00+00", UpdatedAt: "2026-10-15 12:00:01+00", LastSeenAt: &last, LastObservedAt: &last,
	}
	e := EntityProto(rec, []*sentinelv1.Identity{{Type: sentinelv1.IdentityType_IDENTITY_TYPE_ICAO24, Value: "A1B2C3"}}, nil)
	if e.EntityType != sentinelv1.EntityType_ENTITY_TYPE_AIRCRAFT || e.Source != sentinelv1.EntitySource_ENTITY_SOURCE_OPENSKY || e.AgeoutState != sentinelv1.AgeoutState_AGEOUT_STATE_LIVE || e.TrackEnvironment != sentinelv1.TrackEnvironment_TRACK_ENVIRONMENT_AIR {
		t.Errorf("enums = %v %v %v %v", e.EntityType, e.Source, e.AgeoutState, e.TrackEnvironment)
	}
	if e.Position == nil || *e.Position.AltitudeMeters != 10668 || *e.Heading != 270 || e.SpeedKnots != nil || *e.CircularError != 12.5 {
		t.Errorf("position/presence = %v %v %v", e.Position, e.Heading, e.SpeedKnots)
	}
	if e.Metadata["k"] != "v" || e.Metadata["n"] != "1" || len(e.Affiliations) != 0 || e.FeedId != "opensky" {
		t.Errorf("metadata/affiliations/feed = %v %v %q", e.Metadata, e.Affiliations, e.FeedId)
	}
	if e.Kinematics == nil || e.Kinematics.Velocity.East != -2 {
		t.Errorf("kinematics = %v", e.Kinematics)
	}
	if e.Platforms["adsb"].GetAdsb().IcaoHex != "A1B2C3" || e.Platforms["ais"].GetAis().NavStatus != sentinelv1.NavigationalStatus_NAVIGATIONAL_STATUS_MOORED {
		t.Errorf("platforms = %v", e.Platforms)
	}
	if e.CreatedAt == nil || e.LastSeenAt == nil || e.LastObservedAt == nil || len(e.Identities) != 1 {
		t.Errorf("timestamps/identities = %v %v %v %v", e.CreatedAt, e.LastSeenAt, e.LastObservedAt, e.Identities)
	}
}

type fakeProducer struct {
	protos []protoCall
	raws   []rawCall
}

type protoCall struct {
	topic, key string
	ev         *sentinelv1.EntityEvent
	headers    map[string]string
}

type rawCall struct {
	topic, key string
	payload    map[string]interface{}
}

func (f *fakeProducer) ProduceProto(topic, key string, msg proto.Message, _ string, extra map[string]string) error {
	f.protos = append(f.protos, protoCall{topic: topic, key: key, ev: msg.(*sentinelv1.EntityEvent), headers: extra})
	return nil
}

func (f *fakeProducer) ProduceRaw(topic string, key, value []byte, _ map[string]string) error {
	var m map[string]interface{}
	_ = json.Unmarshal(value, &m)
	f.raws = append(f.raws, rawCall{topic: topic, key: string(key), payload: m})
	return nil
}

func TestPublisherEmitsTypedAndLegacy(t *testing.T) {
	fp := &fakeProducer{}
	pub := NewPublisher(fp, true, nil)
	alt := 100.0
	e := &sentinelv1.Entity{Id: "e1", EntityType: sentinelv1.EntityType_ENTITY_TYPE_VESSEL, Source: sentinelv1.EntitySource_ENTITY_SOURCE_AIS, Name: "Boat",
		Position: &sentinelv1.Coordinate{Latitude: 1, Longitude: 2, AltitudeMeters: &alt}, Kinematics: &sentinelv1.KinematicState{Velocity: &sentinelv1.GeodeticVelocity{North: 1}}}
	ref := &sentinelv1.ObservationRef{ObservationId: "o1", FeedId: "ais"}

	pub.Restored(e, "STALE")
	pub.Position(e, ref)
	pub.Created(e, ref)
	pub.Updated(e, []string{"name"})
	pub.Deleted("e1", false, 0)
	pub.Deleted("", true, 42)
	pub.Stale("e1", Ageout{EntityType: "VESSEL", Source: "AIS", FeedID: "ais", PreviousState: "LIVE", LastSeenAt: time.Now(), ThresholdMs: 60000})
	pub.AgedOut("e1", Ageout{PreviousState: "STALE"})

	wantTypes := []string{"restored", "position", "created", "updated", "deleted", "deleted", "stale", "aged_out"}
	if len(fp.protos) != len(wantTypes) {
		t.Fatalf("typed events = %d", len(fp.protos))
	}
	for i, c := range fp.protos {
		if c.topic != kafka.TopicEntityEvents || c.headers[kafka.HeaderEventType] != wantTypes[i] || c.ev.EventId == "" || c.ev.Producer != ProducerName {
			t.Errorf("event %d = %+v", i, c)
		}
	}
	if fp.protos[5].key != "all" || fp.protos[5].ev.EntityId != "" || !fp.protos[5].ev.GetDeleted().Bulk || fp.protos[5].ev.GetDeleted().Count != 42 {
		t.Errorf("bulk delete = %+v", fp.protos[5])
	}
	if fp.protos[0].ev.GetRestored().PreviousState != sentinelv1.AgeoutState_AGEOUT_STATE_STALE {
		t.Errorf("restored previous = %v", fp.protos[0].ev.GetRestored())
	}

	wantLegacy := []string{kafka.TopicEntityRestored, kafka.TopicEntityPosition, kafka.TopicEntityCreated, kafka.TopicEntityUpdated, kafka.TopicEntityDeleted, kafka.TopicEntityStale, kafka.TopicEntityAgedOut}
	if len(fp.raws) != len(wantLegacy) {
		t.Fatalf("legacy events = %d (bulk delete has no legacy form)", len(fp.raws))
	}
	for i, c := range fp.raws {
		if c.topic != wantLegacy[i] {
			t.Errorf("legacy %d topic = %s", i, c.topic)
		}
	}
	pos := fp.raws[1].payload
	if pos["entity_id"] != "e1" || pos["entity_type"] != "VESSEL" || pos["source"] != "AIS" || pos["latitude"] != 1.0 || pos["altitude"] != 100.0 || pos["velocity_north"] != 1.0 {
		t.Errorf("legacy position = %v", pos)
	}
	created := fp.raws[2].payload
	if created["id"] != "e1" || created["name"] != "Boat" || created["classification"] != "UNSPECIFIED" {
		t.Errorf("legacy created = %v", created)
	}
	if fp.raws[4].payload["id"] != "e1" {
		t.Errorf("legacy deleted = %v", fp.raws[4].payload)
	}

	quiet := NewPublisher(&fakeProducer{}, false, nil)
	quiet.Position(e, ref)
}
