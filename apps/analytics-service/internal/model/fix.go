// Package model holds the one value the service passes around: a Fix, the
// position report an entity event carries, reduced to what the detectors
// and the coverage grid need.
package model

import (
	"time"

	"github.com/sentinel/proto-gen/pbjson"
	sentinelv1 "github.com/sentinel/proto-gen/sentinel/v1"
)

// Fix is one accepted position of an entity.
type Fix struct {
	EntityID       string
	EntityType     string // bare EntityType name, e.g. AIRCRAFT
	Classification string // bare Classification name, e.g. UNCLASSIFIED
	FeedID         string
	Lat, Lon       float64
	AltitudeM      *float64
	SpeedKnots     *float64
	Heading        *float64
	At             time.Time // source time of the observation behind the event
	Created        bool      // the event announced a new entity
}

// FixFromEvent reduces a created or position event to a Fix. Other event
// kinds, and events without an entity or a position, yield false. The
// timestamp is the observation's source time, else the entity's last
// observed time, else the event time; the feed is the observation's, else
// the entity's last feed.
func FixFromEvent(ev *sentinelv1.EntityEvent) (Fix, bool) {
	if ev == nil {
		return Fix{}, false
	}
	var entity *sentinelv1.Entity
	var ref *sentinelv1.ObservationRef
	created := false
	switch e := ev.Event.(type) {
	case *sentinelv1.EntityEvent_Position:
		entity, ref = e.Position.GetEntity(), e.Position.GetObservation()
	case *sentinelv1.EntityEvent_Created:
		entity, ref = e.Created.GetEntity(), e.Created.GetObservation()
		created = true
	default:
		return Fix{}, false
	}
	if entity == nil || entity.Position == nil || entity.Id == "" {
		return Fix{}, false
	}
	f := Fix{
		EntityID:       entity.Id,
		EntityType:     pbjson.EnumName(entity.EntityType),
		Classification: pbjson.EnumName(entity.Classification),
		Lat:            entity.Position.Latitude,
		Lon:            entity.Position.Longitude,
		AltitudeM:      entity.Position.AltitudeMeters,
		SpeedKnots:     entity.SpeedKnots,
		Heading:        entity.Heading,
		At:             time.Now().UTC(),
		Created:        created,
	}
	switch {
	case ref != nil && ref.SourceTimestamp != nil:
		f.At = ref.SourceTimestamp.AsTime()
	case entity.LastObservedAt != nil:
		f.At = entity.LastObservedAt.AsTime()
	case ev.EventTime != nil:
		f.At = ev.EventTime.AsTime()
	}
	switch {
	case ref != nil && ref.FeedId != "":
		f.FeedID = ref.FeedId
	case entity.LastFeedId != "":
		f.FeedID = entity.LastFeedId
	default:
		f.FeedID = entity.FeedId
	}
	if f.Classification == "" || f.Classification == "UNSPECIFIED" {
		f.Classification = "UNCLASSIFIED"
	}
	return f, true
}
