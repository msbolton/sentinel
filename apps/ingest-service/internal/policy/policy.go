// Package policy decides what an outbound connection may send. It is pure:
// a parsed policy, a little context about the connection, and an entity or
// alert in, a decision out. The checks run cheapest first and the first
// denial wins, so the reason names the rule that stopped the entity.
package policy

import (
	"encoding/json"
	"fmt"

	"github.com/sentinel/go-common/classification"
	"github.com/sentinel/proto-gen/pbjson"
	sentinelv1 "github.com/sentinel/proto-gen/sentinel/v1"
)

// Box is a latitude/longitude rectangle. West greater than east wraps the
// antimeridian.
type Box struct {
	North float64 `json:"north"`
	South float64 `json:"south"`
	East  float64 `json:"east"`
	West  float64 `json:"west"`
}

// Contains reports whether the point lies inside the box.
func (b Box) Contains(lat, lon float64) bool {
	if lat < b.South || lat > b.North {
		return false
	}
	if b.West > b.East {
		return lon >= b.West || lon <= b.East
	}
	return lon >= b.West && lon <= b.East
}

// AlertPolicy restricts which alerts a connection forwards.
type AlertPolicy struct {
	Enabled     *bool    `json:"enabled"`
	MinSeverity string   `json:"minSeverity"`
	Types       []string `json:"types"`
}

// On reports whether alerts are forwarded at all (the default).
func (a AlertPolicy) On() bool { return a.Enabled == nil || *a.Enabled }

// Policy is config.policy of a connection. Empty lists mean no restriction.
type Policy struct {
	EntityTypes           []string    `json:"entityTypes"`
	Feeds                 []string    `json:"feeds"`
	Boxes                 []Box       `json:"boxes"`
	ClassificationCeiling string      `json:"classificationCeiling"`
	Reexport              bool        `json:"reexport"`
	Alerts                AlertPolicy `json:"alerts"`
}

// Context is what the evaluator knows about the connection itself.
type Context struct {
	// SelfFeedID is the connection's own feed id; anything it contributed
	// must never go back out on it.
	SelfFeedID string
	// ConnectionFeedIDs are the ids of every feed that sends (out or both),
	// this one included; their contributions are not re-exported unless the
	// policy allows it.
	ConnectionFeedIDs map[string]bool
}

// Reasons a decision can carry.
const (
	ReasonLoop           = "loop"
	ReasonReexport       = "reexport"
	ReasonClassification = "classification"
	ReasonType           = "type"
	ReasonFeed           = "feed"
	ReasonBox            = "box"
	ReasonNoPosition     = "no_position"
	ReasonAlertsOff      = "alerts_off"
	ReasonSeverity       = "severity"
	ReasonAlertType      = "alert_type"
	ReasonNotTracked     = "not_tracked"
)

// Decision is the outcome of an evaluation.
type Decision struct {
	Allow  bool
	Reason string // "" when allowed
}

func deny(reason string) Decision { return Decision{Reason: reason} }

var allow = Decision{Allow: true}

// Parse reads config.policy; empty or null is the permissive default with
// an UNCLASSIFIED ceiling.
func Parse(raw json.RawMessage) (Policy, error) {
	var p Policy
	if len(raw) == 0 || string(raw) == "null" {
		return p, nil
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return p, fmt.Errorf("policy: %w", err)
	}
	for i, b := range p.Boxes {
		if b.South > b.North {
			return p, fmt.Errorf("policy: box %d has south above north", i)
		}
	}
	return p, nil
}

// Evaluate decides whether the entity may be sent on the connection.
func Evaluate(p Policy, c Context, e *sentinelv1.Entity) Decision {
	if e == nil {
		return deny(ReasonNoPosition)
	}
	// Loop: anything this connection fed in never goes back out on it.
	if contributed(e, func(id string) bool { return id == c.SelfFeedID }) {
		return deny(ReasonLoop)
	}
	// Re-export: another connection's contributions stay put unless asked.
	if !p.Reexport && contributed(e, func(id string) bool { return c.ConnectionFeedIDs[id] }) {
		return deny(ReasonReexport)
	}
	if classification.Rank(pbjson.EnumName(e.Classification)) > classification.Rank(p.ClassificationCeiling) {
		return deny(ReasonClassification)
	}
	if len(p.EntityTypes) > 0 && !contains(p.EntityTypes, pbjson.EnumName(e.EntityType)) {
		return deny(ReasonType)
	}
	if len(p.Feeds) > 0 && !contributed(e, func(id string) bool { return contains(p.Feeds, id) }) {
		return deny(ReasonFeed)
	}
	if len(p.Boxes) > 0 {
		if e.Position == nil {
			return deny(ReasonNoPosition)
		}
		if !inAnyBox(p.Boxes, e.Position.Latitude, e.Position.Longitude) {
			return deny(ReasonBox)
		}
	}
	return allow
}

// AllowAlert decides whether an alert may be sent. tracked says whether the
// alert's entity is currently exported on this connection; an alert about
// an untracked entity still passes when its own position is inside the
// policy's boxes.
func AllowAlert(p Policy, c Context, a *sentinelv1.Alert, tracked bool) Decision {
	if a == nil || !p.Alerts.On() {
		return deny(ReasonAlertsOff)
	}
	if classification.Rank(pbjson.EnumName(a.Classification)) > classification.Rank(p.ClassificationCeiling) {
		return deny(ReasonClassification)
	}
	if p.Alerts.MinSeverity != "" && severityRank(pbjson.EnumName(a.Severity)) < severityRank(p.Alerts.MinSeverity) {
		return deny(ReasonSeverity)
	}
	if len(p.Alerts.Types) > 0 && !contains(p.Alerts.Types, pbjson.EnumName(a.AlertType)) {
		return deny(ReasonAlertType)
	}
	if !tracked {
		if len(p.Boxes) == 0 || a.Position == nil || !inAnyBox(p.Boxes, a.Position.Latitude, a.Position.Longitude) {
			return deny(ReasonNotTracked)
		}
	}
	return allow
}

// contributed reports whether any feed that touched the entity matches.
func contributed(e *sentinelv1.Entity, match func(string) bool) bool {
	if e.FeedId != "" && match(e.FeedId) {
		return true
	}
	if e.LastFeedId != "" && match(e.LastFeedId) {
		return true
	}
	for _, s := range e.Sources {
		if s != nil && s.FeedId != "" && match(s.FeedId) {
			return true
		}
	}
	return false
}

func inAnyBox(boxes []Box, lat, lon float64) bool {
	for _, b := range boxes {
		if b.Contains(lat, lon) {
			return true
		}
	}
	return false
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

var severityOrder = map[string]int{"INFO": 1, "LOW": 2, "MEDIUM": 3, "HIGH": 4, "CRITICAL": 5}

func severityRank(s string) int { return severityOrder[s] }
