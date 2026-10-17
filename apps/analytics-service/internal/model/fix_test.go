package model

import (
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	sentinelv1 "github.com/sentinel/proto-gen/sentinel/v1"
)

func entity(id string) *sentinelv1.Entity {
	speed := 12.5
	return &sentinelv1.Entity{
		Id:             id,
		EntityType:     sentinelv1.EntityType_ENTITY_TYPE_VESSEL,
		Classification: sentinelv1.Classification_CLASSIFICATION_SECRET,
		Position:       &sentinelv1.Coordinate{Latitude: 1.5, Longitude: 2.5},
		SpeedKnots:     &speed,
		FeedId:         "ais-east",
		LastFeedId:     "ais-west",
	}
}

func TestFixFromPositionEventPrefersObservationTimeAndFeed(t *testing.T) {
	src := time.Date(2026, 10, 17, 10, 0, 0, 0, time.UTC)
	ev := &sentinelv1.EntityEvent{
		EventTime: timestamppb.New(src.Add(time.Minute)),
		Event: &sentinelv1.EntityEvent_Position{Position: &sentinelv1.EntityPosition{
			Entity:      entity("e1"),
			Observation: &sentinelv1.ObservationRef{FeedId: "ais-north", SourceTimestamp: timestamppb.New(src)},
		}},
	}
	f, ok := FixFromEvent(ev)
	if !ok {
		t.Fatal("position event rejected")
	}
	if f.EntityID != "e1" || f.EntityType != "VESSEL" || f.Classification != "SECRET" {
		t.Errorf("identity fields = %+v", f)
	}
	if !f.At.Equal(src) {
		t.Errorf("At = %v, want observation source time %v", f.At, src)
	}
	if f.FeedID != "ais-north" {
		t.Errorf("FeedID = %q, want the observation's feed", f.FeedID)
	}
	if f.SpeedKnots == nil || *f.SpeedKnots != 12.5 || f.Lat != 1.5 || f.Lon != 2.5 {
		t.Errorf("kinematics = %+v", f)
	}
	if f.Created {
		t.Error("a position event is not a creation")
	}
}

func TestFixFromEventFallbacks(t *testing.T) {
	last := time.Date(2026, 10, 17, 9, 0, 0, 0, time.UTC)
	e := entity("e2")
	e.LastObservedAt = timestamppb.New(last)
	e.Classification = sentinelv1.Classification_CLASSIFICATION_UNSPECIFIED
	ev := &sentinelv1.EntityEvent{
		EventTime: timestamppb.New(last.Add(time.Hour)),
		Event:     &sentinelv1.EntityEvent_Created{Created: &sentinelv1.EntityCreated{Entity: e}},
	}
	f, ok := FixFromEvent(ev)
	if !ok {
		t.Fatal("created event rejected")
	}
	if !f.Created {
		t.Error("created flag not set")
	}
	if !f.At.Equal(last) {
		t.Errorf("At = %v, want last observed %v", f.At, last)
	}
	if f.FeedID != "ais-west" {
		t.Errorf("FeedID = %q, want lastFeedId", f.FeedID)
	}
	if f.Classification != "UNCLASSIFIED" {
		t.Errorf("Classification = %q, want UNCLASSIFIED for unspecified", f.Classification)
	}

	e.LastObservedAt = nil
	f, _ = FixFromEvent(ev)
	if !f.At.Equal(last.Add(time.Hour)) {
		t.Errorf("At = %v, want event time", f.At)
	}
}

func TestFixFromEventRejectsOtherKinds(t *testing.T) {
	cases := map[string]*sentinelv1.EntityEvent{
		"nil":        nil,
		"deleted":    {Event: &sentinelv1.EntityEvent_Deleted{Deleted: &sentinelv1.EntityDeleted{}}},
		"no entity":  {Event: &sentinelv1.EntityEvent_Position{Position: &sentinelv1.EntityPosition{}}},
		"no positon": {Event: &sentinelv1.EntityEvent_Position{Position: &sentinelv1.EntityPosition{Entity: &sentinelv1.Entity{Id: "x"}}}},
	}
	for name, ev := range cases {
		if _, ok := FixFromEvent(ev); ok {
			t.Errorf("%s: accepted", name)
		}
	}
}
