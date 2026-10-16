// Package cot renders Sentinel entities, alerts and shapes as Cursor on
// Target XML events for TAK servers and clients. It is pure: a snapshot and
// a clock in, bytes out. Conventions follow what ATAK and WinTAK render:
// MIL-STD-2525 atoms for tracks, spot markers for alerts, drawing shapes
// for geofences, and the t-x-d-d tasking for deletions.
package cot

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"strings"
	"time"

	"github.com/sentinel/proto-gen/pbjson"
	sentinelv1 "github.com/sentinel/proto-gen/sentinel/v1"
)

// Unknown is the CoT sentinel for an unknown point attribute.
const Unknown = 9999999.0

// Options shape every event an encoder produces.
type Options struct {
	// SiteID names this Sentinel in uids and the <_sentinel> detail so the
	// receiving side (and our own decoder) can tell our events apart.
	SiteID string
	// TrackStale is how long a track stays valid after its last fix.
	TrackStale time.Duration
	// StaleNow is the stale window for an entity Sentinel already marks
	// stale, and for the final event when an entity leaves the policy.
	StaleNow time.Duration
	// AlertStale is how long an alert marker stays valid.
	AlertStale time.Duration
	// ShapeStale is how long a geofence or location stays valid.
	ShapeStale time.Duration
	// How overrides the <event how> for tracks ("" = m-g).
	How string
	// EmergencyMinSeverity is the lowest severity rendered as a 911 alert
	// (which takes over every TAK screen); "" never does.
	EmergencyMinSeverity string
	// StrokeColor and FillColor are ATAK ARGB ints for shapes.
	StrokeColor int32
	FillColor   int32
}

func (o Options) withDefaults() Options {
	if o.SiteID == "" {
		o.SiteID = "sentinel"
	}
	if o.TrackStale <= 0 {
		o.TrackStale = 2 * time.Minute
	}
	if o.StaleNow <= 0 {
		o.StaleNow = 10 * time.Second
	}
	if o.AlertStale <= 0 {
		o.AlertStale = 5 * time.Minute
	}
	if o.ShapeStale <= 0 {
		o.ShapeStale = 2 * time.Minute
	}
	if o.How == "" {
		o.How = "m-g"
	}
	if o.StrokeColor == 0 {
		o.StrokeColor = -65536 // opaque red
	}
	if o.FillColor == 0 {
		o.FillColor = 1090453504 // translucent red
	}
	return o
}

// Encoder renders events.
type Encoder struct{ opts Options }

// New builds an encoder.
func New(o Options) *Encoder { return &Encoder{opts: o.withDefaults()} }

// Event is the CoT XML envelope.
type Event struct {
	XMLName xml.Name `xml:"event"`
	Version string   `xml:"version,attr"`
	UID     string   `xml:"uid,attr"`
	Type    string   `xml:"type,attr"`
	Time    string   `xml:"time,attr"`
	Start   string   `xml:"start,attr"`
	Stale   string   `xml:"stale,attr"`
	How     string   `xml:"how,attr"`
	Point   Point    `xml:"point"`
	Detail  Detail   `xml:"detail"`
}

// Point is the CoT position with unknown values as 9999999.
type Point struct {
	Lat float64 `xml:"lat,attr"`
	Lon float64 `xml:"lon,attr"`
	Hae float64 `xml:"hae,attr"`
	CE  float64 `xml:"ce,attr"`
	LE  float64 `xml:"le,attr"`
}

// Detail holds the optional child elements TAK clients read.
type Detail struct {
	Contact     *Contact     `xml:"contact,omitempty"`
	Track       *Track       `xml:"track,omitempty"`
	Remarks     *Remarks     `xml:"remarks,omitempty"`
	Sentinel    *Sentinel    `xml:"_sentinel,omitempty"`
	Links       []Link       `xml:"link,omitempty"`
	Color       *Color       `xml:"color,omitempty"`
	StrokeColor *ArgbValue   `xml:"strokeColor,omitempty"`
	FillColor   *ArgbValue   `xml:"fillColor,omitempty"`
	StrokeWt    *FloatValue  `xml:"strokeWeight,omitempty"`
	Emergency   *Emergency   `xml:"emergency,omitempty"`
	ForceDelete *struct{}    `xml:"__forcedelete,omitempty"`
	Shape       *Shape       `xml:"shape,omitempty"`
	Archive     *struct{}    `xml:"archive,omitempty"`
}

// Contact carries the callsign shown on the map.
type Contact struct {
	Callsign string `xml:"callsign,attr"`
}

// Track is speed (m/s) and course (degrees true).
type Track struct {
	Speed  float64 `xml:"speed,attr"`
	Course float64 `xml:"course,attr"`
}

// Remarks is free text.
type Remarks struct {
	Text string `xml:",chardata"`
}

// Sentinel is our provenance marker; the decoder skips events carrying our
// own site id so nothing echoes back through a bidirectional connection.
type Sentinel struct {
	Site   string `xml:"site,attr"`
	Entity string `xml:"entity,attr,omitempty"`
	Feed   string `xml:"feed,attr,omitempty"`
	Kind   string `xml:"kind,attr,omitempty"`
}

// Link relates events (alert to entity, deletion to target, shape vertex).
type Link struct {
	UID      string `xml:"uid,attr,omitempty"`
	Type     string `xml:"type,attr,omitempty"`
	Relation string `xml:"relation,attr,omitempty"`
	Point    string `xml:"point,attr,omitempty"`
}

// Color is the ARGB marker colour.
type Color struct {
	Argb int32 `xml:"argb,attr"`
}

// ArgbValue is a colour given as the element's value attribute.
type ArgbValue struct {
	Value int32 `xml:"value,attr"`
}

// FloatValue is a number given as the element's value attribute.
type FloatValue struct {
	Value float64 `xml:"value,attr"`
}

// Emergency is ATAK's 911 detail.
type Emergency struct {
	Type      string `xml:"type,attr"`
	Cancel    bool   `xml:"cancel,attr,omitempty"`
	Text      string `xml:",chardata"`
}

// Shape carries an ellipse for circles.
type Shape struct {
	Ellipse *Ellipse `xml:"ellipse,omitempty"`
}

// Ellipse is a circle when major equals minor.
type Ellipse struct {
	Major float64 `xml:"major,attr"`
	Minor float64 `xml:"minor,attr"`
	Angle float64 `xml:"angle,attr"`
}

// EntityUID is the uid Sentinel uses for an entity on TAK: the entity's own
// CoT uid when it came from TAK (so a re-export updates the same object),
// otherwise a site-scoped id.
func (e *Encoder) EntityUID(en *sentinelv1.Entity) string {
	for _, id := range en.GetIdentities() {
		if id.GetType() == sentinelv1.IdentityType_IDENTITY_TYPE_COT_UID && id.GetValue() != "" {
			return id.GetValue()
		}
	}
	return fmt.Sprintf("SENTINEL-%s-%s", e.opts.SiteID, en.GetId())
}

// AlertUID names an alert marker.
func (e *Encoder) AlertUID(alertID string) string {
	return fmt.Sprintf("SENTINEL-%s-alert-%s", e.opts.SiteID, alertID)
}

// GeofenceUID names a geofence shape.
func (e *Encoder) GeofenceUID(ruleID string) string {
	return fmt.Sprintf("SENTINEL-%s-geofence-%s", e.opts.SiteID, ruleID)
}

// LocationUID names a location marker.
func (e *Encoder) LocationUID(locationID string) string {
	return fmt.Sprintf("SENTINEL-%s-location-%s", e.opts.SiteID, locationID)
}

// Track renders an entity snapshot. final marks the last event for an
// entity leaving the policy: it carries the short stale window so clients
// grey it out and drop it.
func (e *Encoder) Track(en *sentinelv1.Entity, now time.Time, final bool) ([]byte, error) {
	if en == nil || en.Position == nil {
		return nil, fmt.Errorf("entity %s has no position", en.GetId())
	}
	at := now
	if en.LastObservedAt != nil {
		at = en.LastObservedAt.AsTime()
	}
	stale := at.Add(e.opts.TrackStale)
	if final || en.AgeoutState == sentinelv1.AgeoutState_AGEOUT_STATE_STALE {
		stale = now.Add(e.opts.StaleNow)
	}
	how := e.opts.How
	switch {
	case en.Source == sentinelv1.EntitySource_ENTITY_SOURCE_MANUAL:
		how = "h-e"
	case len(en.Sources) > 1:
		how = "m-f"
	}
	ev := Event{
		Version: "2.0", UID: e.EntityUID(en), Type: TypeFor(en),
		Time: ts(at), Start: ts(at), Stale: ts(stale), How: how,
		Point: Point{Lat: en.Position.Latitude, Lon: en.Position.Longitude, Hae: Unknown, CE: Unknown, LE: Unknown},
	}
	if en.Position.AltitudeMeters != nil {
		ev.Point.Hae = *en.Position.AltitudeMeters
	}
	if en.CircularError != nil && *en.CircularError > 0 {
		ev.Point.CE = *en.CircularError
	}
	name := strings.TrimSpace(en.Name)
	if name == "" {
		name = en.SourceEntityId
	}
	ev.Detail.Contact = &Contact{Callsign: name}
	if en.SpeedKnots != nil || en.Course != nil || en.Heading != nil {
		t := &Track{}
		if en.SpeedKnots != nil {
			t.Speed = *en.SpeedKnots * 0.514444
		}
		switch {
		case en.Course != nil:
			t.Course = *en.Course
		case en.Heading != nil:
			t.Course = *en.Heading
		}
		ev.Detail.Track = t
	}
	ev.Detail.Remarks = &Remarks{Text: remarks(en)}
	ev.Detail.Sentinel = &Sentinel{Site: e.opts.SiteID, Entity: en.Id, Feed: en.FeedId, Kind: "track"}
	return marshal(ev)
}

// Delete renders the t-x-d-d tasking that removes an object on every client.
func (e *Encoder) Delete(uid string, now time.Time) ([]byte, error) {
	ev := Event{
		Version: "2.0", UID: fmt.Sprintf("SENTINEL-%s-delete-%d", e.opts.SiteID, now.UnixNano()), Type: "t-x-d-d",
		Time: ts(now), Start: ts(now), Stale: ts(now.Add(e.opts.StaleNow)), How: "h-g-i-g-o",
		Point: Point{Lat: 0, Lon: 0, Hae: Unknown, CE: Unknown, LE: Unknown},
	}
	ev.Detail.Links = []Link{{UID: uid, Type: "none", Relation: "none"}}
	ev.Detail.ForceDelete = &struct{}{}
	ev.Detail.Sentinel = &Sentinel{Site: e.opts.SiteID, Kind: "delete"}
	return marshal(ev)
}

// Alert renders an alert as a spot marker linked to its entity, or as a
// 911 emergency at or above the configured severity. entityUID may be ""
// when the entity is not exported on this connection.
func (e *Encoder) Alert(a *sentinelv1.Alert, entityUID string, now time.Time) ([]byte, error) {
	if a == nil || a.AlertId == "" {
		return nil, fmt.Errorf("alert without id")
	}
	at := now
	if a.CreatedAt != nil {
		at = a.CreatedAt.AsTime()
	}
	severity := pbjson.EnumName(a.Severity)
	ev := Event{
		Version: "2.0", UID: e.AlertUID(a.AlertId), Type: "b-m-p-s-m",
		Time: ts(at), Start: ts(at), Stale: ts(now.Add(e.opts.AlertStale)), How: "m-g",
		Point: Point{Hae: Unknown, CE: Unknown, LE: Unknown},
	}
	if a.Position != nil {
		ev.Point.Lat, ev.Point.Lon = a.Position.Latitude, a.Position.Longitude
	}
	title := strings.TrimSpace(a.Title)
	if title == "" {
		title = pbjson.EnumName(a.AlertType)
	}
	ev.Detail.Contact = &Contact{Callsign: title}
	ev.Detail.Remarks = &Remarks{Text: strings.TrimSpace(fmt.Sprintf("%s %s. %s", severity, pbjson.EnumName(a.AlertType), a.Description))}
	ev.Detail.Color = &Color{Argb: severityColor(severity)}
	if entityUID != "" {
		ev.Detail.Links = []Link{{UID: entityUID, Type: "a-u-G", Relation: "p-p"}}
	}
	if e.opts.EmergencyMinSeverity != "" && severityRank(severity) >= severityRank(e.opts.EmergencyMinSeverity) && severityRank(severity) > 0 {
		ev.Type = "b-a-o-tbl"
		ev.Detail.Emergency = &Emergency{Type: "911 Alert", Text: title}
	}
	ev.Detail.Sentinel = &Sentinel{Site: e.opts.SiteID, Entity: a.EntityId, Kind: "alert"}
	return marshal(ev)
}

// AlertCancel renders the emergency cancellation for a resolved 911 alert.
func (e *Encoder) AlertCancel(a *sentinelv1.Alert, now time.Time) ([]byte, error) {
	ev := Event{
		Version: "2.0", UID: e.AlertUID(a.GetAlertId()), Type: "b-a-o-can",
		Time: ts(now), Start: ts(now), Stale: ts(now.Add(e.opts.StaleNow)), How: "m-g",
		Point: Point{Hae: Unknown, CE: Unknown, LE: Unknown},
	}
	if a.GetPosition() != nil {
		ev.Point.Lat, ev.Point.Lon = a.Position.Latitude, a.Position.Longitude
	}
	ev.Detail.Emergency = &Emergency{Type: "911 Alert", Cancel: true}
	ev.Detail.Sentinel = &Sentinel{Site: e.opts.SiteID, Entity: a.GetEntityId(), Kind: "alert"}
	return marshal(ev)
}

// Polygon renders a closed drawing shape; points are (lat, lon) pairs.
func (e *Encoder) Polygon(uid, name string, points [][2]float64, now time.Time) ([]byte, error) {
	if len(points) < 3 {
		return nil, fmt.Errorf("polygon %s needs at least three points", uid)
	}
	ev := Event{
		Version: "2.0", UID: uid, Type: "u-d-f",
		Time: ts(now), Start: ts(now), Stale: ts(now.Add(e.opts.ShapeStale)), How: "h-e",
		Point: Point{Lat: points[0][0], Lon: points[0][1], Hae: Unknown, CE: Unknown, LE: Unknown},
	}
	for _, p := range points {
		ev.Detail.Links = append(ev.Detail.Links, Link{Point: fmt.Sprintf("%.6f,%.6f", p[0], p[1])})
	}
	ev.Detail.Contact = &Contact{Callsign: name}
	ev.Detail.StrokeColor = &ArgbValue{Value: e.opts.StrokeColor}
	ev.Detail.FillColor = &ArgbValue{Value: e.opts.FillColor}
	ev.Detail.StrokeWt = &FloatValue{Value: 3}
	ev.Detail.Archive = &struct{}{}
	ev.Detail.Sentinel = &Sentinel{Site: e.opts.SiteID, Kind: "geofence"}
	return marshal(ev)
}

// Circle renders a circular drawing shape of the given radius in metres.
func (e *Encoder) Circle(uid, name string, lat, lon, radiusM float64, now time.Time) ([]byte, error) {
	ev := Event{
		Version: "2.0", UID: uid, Type: "u-d-c-c",
		Time: ts(now), Start: ts(now), Stale: ts(now.Add(e.opts.ShapeStale)), How: "h-e",
		Point: Point{Lat: lat, Lon: lon, Hae: Unknown, CE: Unknown, LE: Unknown},
	}
	ev.Detail.Shape = &Shape{Ellipse: &Ellipse{Major: radiusM, Minor: radiusM, Angle: 360}}
	ev.Detail.Contact = &Contact{Callsign: name}
	ev.Detail.StrokeColor = &ArgbValue{Value: e.opts.StrokeColor}
	ev.Detail.FillColor = &ArgbValue{Value: e.opts.FillColor}
	ev.Detail.StrokeWt = &FloatValue{Value: 3}
	ev.Detail.Archive = &struct{}{}
	ev.Detail.Sentinel = &Sentinel{Site: e.opts.SiteID, Kind: "geofence"}
	return marshal(ev)
}

// Marker renders a named point (a Sentinel location).
func (e *Encoder) Marker(uid, name, remarks string, lat, lon float64, now time.Time) ([]byte, error) {
	ev := Event{
		Version: "2.0", UID: uid, Type: "b-m-p-s-m",
		Time: ts(now), Start: ts(now), Stale: ts(now.Add(e.opts.ShapeStale)), How: "h-e",
		Point: Point{Lat: lat, Lon: lon, Hae: Unknown, CE: Unknown, LE: Unknown},
	}
	ev.Detail.Contact = &Contact{Callsign: name}
	if remarks != "" {
		ev.Detail.Remarks = &Remarks{Text: remarks}
	}
	ev.Detail.Archive = &struct{}{}
	ev.Detail.Sentinel = &Sentinel{Site: e.opts.SiteID, Kind: "location"}
	return marshal(ev)
}

// Ping renders the t-x-c-t heartbeat TAK servers expect from a client.
func (e *Encoder) Ping(now time.Time) ([]byte, error) {
	ev := Event{
		Version: "2.0", UID: e.opts.SiteID + "-ping", Type: "t-x-c-t",
		Time: ts(now), Start: ts(now), Stale: ts(now.Add(time.Minute)), How: "m-g",
		Point: Point{Hae: Unknown, CE: Unknown, LE: Unknown},
	}
	return marshal(ev)
}

// TypeFor maps an entity to a CoT type: the entity's own CoT type when it
// came from TAK, otherwise a MIL-STD-2525 atom from its type, affiliation
// and environment.
func TypeFor(en *sentinelv1.Entity) string {
	if p := en.GetPlatforms()["cot"]; p != nil {
		if c := p.GetCot(); c != nil && c.CotType != "" {
			return c.CotType
		}
	}
	aff := affiliationLetter(en.Affiliation)
	env := func(dim string) string { return "a-" + aff + "-" + dim }
	switch en.EntityType {
	case sentinelv1.EntityType_ENTITY_TYPE_AIRCRAFT:
		if md := en.GetMetadata(); md["military"] == "true" {
			return env("A-M-F")
		}
		if p := en.GetPlatforms()["adsb"]; p != nil {
			if a := p.GetAdsb(); a != nil && a.Category == "A7" {
				return env("A-C-H")
			}
		}
		return env("A-C-F")
	case sentinelv1.EntityType_ENTITY_TYPE_DRONE:
		return env("A-M-F-Q")
	case sentinelv1.EntityType_ENTITY_TYPE_VESSEL:
		if en.TrackEnvironment == sentinelv1.TrackEnvironment_TRACK_ENVIRONMENT_SUBSURFACE {
			return env("U")
		}
		return env("S-X-M")
	case sentinelv1.EntityType_ENTITY_TYPE_VEHICLE:
		return env("G-E-V")
	case sentinelv1.EntityType_ENTITY_TYPE_PERSON:
		return env("G-U-C")
	case sentinelv1.EntityType_ENTITY_TYPE_UNIT:
		return env("G-U")
	case sentinelv1.EntityType_ENTITY_TYPE_FACILITY:
		return env("G-I")
	case sentinelv1.EntityType_ENTITY_TYPE_EQUIPMENT:
		return env("G-E")
	case sentinelv1.EntityType_ENTITY_TYPE_SENSOR, sentinelv1.EntityType_ENTITY_TYPE_SIGNAL:
		return env("G-E-S")
	case sentinelv1.EntityType_ENTITY_TYPE_SATELLITE:
		return env("P-S")
	}
	switch en.TrackEnvironment {
	case sentinelv1.TrackEnvironment_TRACK_ENVIRONMENT_AIR:
		return env("A")
	case sentinelv1.TrackEnvironment_TRACK_ENVIRONMENT_SEA_SURFACE:
		return env("S")
	case sentinelv1.TrackEnvironment_TRACK_ENVIRONMENT_SUBSURFACE:
		return env("U")
	case sentinelv1.TrackEnvironment_TRACK_ENVIRONMENT_GROUND:
		return env("G")
	case sentinelv1.TrackEnvironment_TRACK_ENVIRONMENT_SPACE:
		return env("P")
	}
	return env("X")
}

func affiliationLetter(a sentinelv1.Affiliation) string {
	switch a {
	case sentinelv1.Affiliation_AFFILIATION_FRIENDLY:
		return "f"
	case sentinelv1.Affiliation_AFFILIATION_ASSUMED_FRIENDLY:
		return "a"
	case sentinelv1.Affiliation_AFFILIATION_HOSTILE:
		return "h"
	case sentinelv1.Affiliation_AFFILIATION_SUSPECT:
		return "s"
	case sentinelv1.Affiliation_AFFILIATION_NEUTRAL:
		return "n"
	case sentinelv1.Affiliation_AFFILIATION_PENDING:
		return "p"
	}
	return "u"
}

func remarks(en *sentinelv1.Entity) string {
	parts := []string{"Sentinel " + pbjson.EnumName(en.EntityType)}
	if en.FeedId != "" {
		parts = append(parts, "feed "+en.FeedId)
	}
	if en.Source != sentinelv1.EntitySource_ENTITY_SOURCE_UNSPECIFIED {
		parts = append(parts, "source "+pbjson.EnumName(en.Source))
	}
	if en.CountryOfOrigin != "" {
		parts = append(parts, en.CountryOfOrigin)
	}
	if en.Classification != sentinelv1.Classification_CLASSIFICATION_UNSPECIFIED {
		parts = append(parts, pbjson.EnumName(en.Classification))
	}
	return strings.Join(parts, ", ")
}

var severityOrder = map[string]int{"INFO": 1, "LOW": 2, "MEDIUM": 3, "HIGH": 4, "CRITICAL": 5}

func severityRank(s string) int { return severityOrder[strings.ToUpper(s)] }

func severityColor(s string) int32 {
	switch strings.ToUpper(s) {
	case "CRITICAL", "HIGH":
		return -65536 // red
	case "MEDIUM":
		return -23296 // orange
	case "LOW":
		return -256 // yellow
	}
	return -16776961 // blue
}

func ts(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000Z") }

func marshal(ev Event) ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteString(xml.Header)
	enc := xml.NewEncoder(&buf)
	if err := enc.Encode(ev); err != nil {
		return nil, err
	}
	if err := enc.Flush(); err != nil {
		return nil, err
	}
	buf.WriteByte('\n')
	return buf.Bytes(), nil
}
