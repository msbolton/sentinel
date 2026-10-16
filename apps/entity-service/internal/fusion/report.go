// Package fusion turns observations from any feed into one entity each:
// it resolves natural identities to entity ids, decides which report wins
// when several arrive for the same object, and applies the winner so the
// newest source timestamp always ends up on the entity row.
package fusion

import (
	"math"
	"strings"
	"time"

	"github.com/sentinel/proto-gen/pbjson"
	sentinelv1 "github.com/sentinel/proto-gen/sentinel/v1"

	"github.com/sentinel/entity-service/internal/identity"
)

// Reject reasons stored on observations that did not move an entity.
const (
	RejectDuplicate       = "DUPLICATE"        // an in-batch sibling was newer
	RejectStaleFix        = "STALE_FIX"        // the entity already had a newer fix
	RejectInvalidPosition = "INVALID_POSITION" // NaN or out of range
	RejectNoPosition      = "NO_POSITION"      // a static report; attributes applied, nothing moved
)

// maxFuture caps how far ahead of receipt a source timestamp may claim.
const maxFuture = 60 * time.Second

// Key is a resolved identity (see package identity).
type Key = identity.Key

// Report is one observation prepared for fusion.
type Report struct {
	Obs        *sentinelv1.Observation
	Key        Key    // canonical identity (FEED_LOCAL when nothing canonical was reported)
	Identities []Key  // every identity on the observation, canonical first
	ObservedAt time.Time
	ReceivedAt time.Time
	Valid      bool   // position present and in range
	Reject     string // set when the report cannot move the entity
	Priority   int    // source priority for tie-breaks
}

// Prepare normalises an observation: timestamps, canonical key and
// position validity.
func Prepare(o *sentinelv1.Observation, priority Priority) *Report {
	r := &Report{Obs: o}
	if o.ReceivedAt != nil {
		r.ReceivedAt = o.ReceivedAt.AsTime()
	} else {
		r.ReceivedAt = time.Now().UTC()
	}
	if o.SourceTimestamp != nil {
		r.ObservedAt = o.SourceTimestamp.AsTime()
		if limit := r.ReceivedAt.Add(maxFuture); r.ObservedAt.After(limit) {
			r.ObservedAt = limit
		}
	} else {
		r.ObservedAt = r.ReceivedAt
	}
	r.Identities = identities(o)
	r.Key = canonicalKey(o, r.Identities)
	r.Priority = priority.Of(pbjson.EnumName(o.Source))

	switch {
	case o.Position == nil:
		r.Reject = RejectNoPosition
	case math.IsNaN(o.Position.Latitude) || math.IsNaN(o.Position.Longitude) ||
		o.Position.Latitude < -90 || o.Position.Latitude > 90 ||
		o.Position.Longitude < -180 || o.Position.Longitude > 180:
		r.Reject = RejectInvalidPosition
	default:
		r.Valid = true
	}
	return r
}

func identities(o *sentinelv1.Observation) []Key {
	var canon, attrs []Key
	for _, id := range o.Identities {
		if id == nil || id.Value == "" {
			continue
		}
		k := Key{Type: pbjson.EnumName(id.Type), Value: id.Value}
		if k.Canonical() {
			canon = append(canon, k)
		} else {
			attrs = append(attrs, k)
		}
	}
	return append(canon, attrs...)
}

func canonicalKey(o *sentinelv1.Observation, ids []Key) Key {
	if len(ids) > 0 && ids[0].Canonical() {
		return ids[0]
	}
	for _, k := range ids {
		if k.Type == "FEED_LOCAL" {
			return k
		}
	}
	return Key{Type: "FEED_LOCAL", Value: o.FeedId + ":" + o.SourceEntityId}
}

// Priority ranks sources for tie-breaks between reports with the same
// source timestamp. Higher wins.
type Priority map[string]int

// DefaultPriority: direct receivers first, aggregators next, OpenSky last
// among live feeds, manual edits lowest.
var DefaultPriority = Priority{
	"ADS_B": 100, "AIS": 90, "GPS": 85, "RADAR": 85, "ADSB_LOL": 80, "LINK16": 70, "OPENSKY": 60, "CELESTRAK": 50, "MANUAL": 10,
}

// Of returns the priority of a bare source name (0 when unknown).
func (p Priority) Of(source string) int {
	if p == nil {
		return DefaultPriority[source]
	}
	return p[source]
}

// SpecFor describes the entity to create for an unresolved report.
func SpecFor(r *Report) identity.NewEntity {
	o := r.Obs
	name := strings.TrimSpace(o.DisplayName)
	if name == "" {
		name = o.SourceEntityId
	}
	return identity.NewEntity{
		Key:              r.Key,
		EntityType:       pbjson.EnumName(o.EntityType),
		Name:             name,
		Source:           pbjson.EnumName(o.Source),
		FeedID:           o.FeedId,
		SourceEntityID:   o.SourceEntityId,
		TrackEnvironment: enumOrEmpty(o.TrackEnvironment),
		CountryOfOrigin:  o.CountryOfOrigin,
		Classification:   enumOrEmpty(o.Classification),
		SeenAt:           r.ReceivedAt,
	}
}
