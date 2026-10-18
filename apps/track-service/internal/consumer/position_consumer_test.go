package consumer

import (
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	sentinelv1 "github.com/sentinel/proto-gen/sentinel/v1"
)

func TestPointFromEventUsesObservationProvenance(t *testing.T) {
	alt, hdg := 10668.0, 270.0
	at := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	ev := &sentinelv1.EntityEvent{EntityId: "e1", EventTime: timestamppb.New(at.Add(time.Second)), Event: &sentinelv1.EntityEvent_Position{Position: &sentinelv1.EntityPosition{
		Entity: &sentinelv1.Entity{Id: "e1", EntityType: sentinelv1.EntityType_ENTITY_TYPE_AIRCRAFT, Source: sentinelv1.EntitySource_ENTITY_SOURCE_OPENSKY,
			Position: &sentinelv1.Coordinate{Latitude: 41.9, Longitude: -87.9, AltitudeMeters: &alt}, Heading: &hdg,
			Kinematics: &sentinelv1.KinematicState{Velocity: &sentinelv1.GeodeticVelocity{North: 1, East: 2, Up: 3}}, LastFeedId: "opensky"},
		Observation: &sentinelv1.ObservationRef{ObservationId: "o1", FeedId: "adsb-lol", Source: sentinelv1.EntitySource_ENTITY_SOURCE_ADSB_LOL, SourceEntityId: "ICAO-X", SourceTimestamp: timestamppb.New(at)},
	}}}
	p, ok := PointFromEvent(ev)
	if !ok {
		t.Fatal("expected a point")
	}
	if p.EntityID != "e1" || p.Latitude != 41.9 || *p.Altitude != 10668 || *p.Heading != 270 || p.SpeedKnots != nil || *p.VelocityEast != 2 {
		t.Errorf("point = %+v", p)
	}
	if !p.Timestamp.Equal(at) || *p.FeedID != "adsb-lol" || *p.Source != "ADSB_LOL" || *p.SourceEntityID != "ICAO-X" || *p.ObservationID != "o1" || *p.EntityType != "AIRCRAFT" {
		t.Errorf("provenance = %+v", p)
	}

	// A created event without an observation (REST) falls back to the entity.
	created := &sentinelv1.EntityEvent{EntityId: "e2", EventTime: timestamppb.New(at), Event: &sentinelv1.EntityEvent_Created{Created: &sentinelv1.EntityCreated{
		Entity: &sentinelv1.Entity{Id: "e2", Source: sentinelv1.EntitySource_ENTITY_SOURCE_MANUAL, Position: &sentinelv1.Coordinate{Latitude: 1, Longitude: 2}, LastFeedId: "manual"}}}}
	p, ok = PointFromEvent(created)
	if !ok || *p.Source != "MANUAL" || *p.FeedID != "manual" || !p.Timestamp.Equal(at) || p.Altitude != nil {
		t.Errorf("created point = %+v ok=%v", p, ok)
	}

	// A background refresh moves the entity but adds no track point.
	bg := proto.Clone(ev).(*sentinelv1.EntityEvent)
	bg.GetPosition().Observation.Background = true
	if _, ok := PointFromEvent(bg); ok {
		t.Error("a background refresh must not produce a track point")
	}

	// No position or an unrelated event type yields nothing.
	if _, ok := PointFromEvent(&sentinelv1.EntityEvent{Event: &sentinelv1.EntityEvent_Deleted{Deleted: &sentinelv1.EntityDeleted{}}}); ok {
		t.Error("deleted event must not produce a point")
	}
	raw, _ := proto.Marshal(ev)
	var back sentinelv1.EntityEvent
	if err := proto.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
}
