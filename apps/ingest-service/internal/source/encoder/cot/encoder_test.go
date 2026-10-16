package cot

import (
	"encoding/xml"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	sentinelv1 "github.com/sentinel/proto-gen/sentinel/v1"
)

var t0 = time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)

func aircraft() *sentinelv1.Entity {
	return &sentinelv1.Entity{
		Id: "e1", Name: "UAL123", EntityType: sentinelv1.EntityType_ENTITY_TYPE_AIRCRAFT, Affiliation: sentinelv1.Affiliation_AFFILIATION_NEUTRAL,
		Source: sentinelv1.EntitySource_ENTITY_SOURCE_OPENSKY, FeedId: "opensky", CountryOfOrigin: "United States",
		Position:    &sentinelv1.Coordinate{Latitude: 41.9742, Longitude: -87.9073, AltitudeMeters: proto.Float64(10668)},
		SpeedKnots:  proto.Float64(450), Course: proto.Float64(270.5), Heading: proto.Float64(270.5),
		LastObservedAt: timestamppb.New(t0.Add(-5 * time.Second)), AgeoutState: sentinelv1.AgeoutState_AGEOUT_STATE_LIVE,
		Classification: sentinelv1.Classification_CLASSIFICATION_CONFIDENTIAL,
	}
}

func parse(t *testing.T, b []byte) Event {
	t.Helper()
	var ev Event
	if err := xml.Unmarshal(b, &ev); err != nil {
		t.Fatalf("not XML: %v\n%s", err, b)
	}
	return ev
}

func TestTrack(t *testing.T) {
	enc := New(Options{SiteID: "east", TrackStale: 2 * time.Minute})
	b, err := enc.Track(aircraft(), t0, false)
	if err != nil {
		t.Fatal(err)
	}
	ev := parse(t, b)
	if ev.UID != "SENTINEL-east-e1" || ev.Type != "a-n-A-C-F" || ev.How != "m-g" || ev.Version != "2.0" {
		t.Errorf("envelope = %+v", ev)
	}
	if ev.Time != "2026-10-16T11:59:55.000Z" || ev.Stale != "2026-10-16T12:01:55.000Z" {
		t.Errorf("time=%s stale=%s", ev.Time, ev.Stale)
	}
	if ev.Point.Lat != 41.9742 || ev.Point.Hae != 10668 || ev.Point.CE != Unknown {
		t.Errorf("point = %+v", ev.Point)
	}
	if ev.Detail.Contact.Callsign != "UAL123" || ev.Detail.Track == nil || ev.Detail.Track.Course != 270.5 || ev.Detail.Track.Speed < 231 || ev.Detail.Track.Speed > 232 {
		t.Errorf("detail = %+v", ev.Detail)
	}
	if ev.Detail.Sentinel == nil || ev.Detail.Sentinel.Site != "east" || ev.Detail.Sentinel.Entity != "e1" || ev.Detail.Sentinel.Feed != "opensky" {
		t.Errorf("provenance = %+v", ev.Detail.Sentinel)
	}
	if !strings.Contains(ev.Detail.Remarks.Text, "CONFIDENTIAL") || !strings.Contains(ev.Detail.Remarks.Text, "United States") {
		t.Errorf("remarks = %q", ev.Detail.Remarks.Text)
	}

	// Stale in Sentinel, or a final event, carries the short window.
	stale := aircraft()
	stale.AgeoutState = sentinelv1.AgeoutState_AGEOUT_STATE_STALE
	b, _ = enc.Track(stale, t0, false)
	if ev := parse(t, b); ev.Stale != "2026-10-16T12:00:10.000Z" {
		t.Errorf("stale window = %s", ev.Stale)
	}
	b, _ = enc.Track(aircraft(), t0, true)
	if ev := parse(t, b); ev.Stale != "2026-10-16T12:00:10.000Z" {
		t.Errorf("final window = %s", ev.Stale)
	}

	// TAK-origin entities keep their uid and type; manual ones are h-e.
	fromTAK := aircraft()
	fromTAK.Identities = []*sentinelv1.Identity{{Type: sentinelv1.IdentityType_IDENTITY_TYPE_COT_UID, Value: "ANDROID-abc"}}
	fromTAK.Platforms = map[string]*sentinelv1.PlatformDetail{"cot": {Detail: &sentinelv1.PlatformDetail_Cot{Cot: &sentinelv1.CotDetail{CotType: "a-f-G-U-C-I"}}}}
	b, _ = enc.Track(fromTAK, t0, false)
	if ev := parse(t, b); ev.UID != "ANDROID-abc" || ev.Type != "a-f-G-U-C-I" {
		t.Errorf("tak origin = %s %s", ev.UID, ev.Type)
	}
	manual := aircraft()
	manual.Source = sentinelv1.EntitySource_ENTITY_SOURCE_MANUAL
	manual.Sources = []*sentinelv1.SourceContribution{{FeedId: "manual"}, {FeedId: "opensky"}}
	b, _ = enc.Track(manual, t0, false)
	if ev := parse(t, b); ev.How != "h-e" {
		t.Errorf("manual how = %s", ev.How)
	}
	noPos := aircraft()
	noPos.Position = nil
	if _, err := enc.Track(noPos, t0, false); err == nil {
		t.Error("entity without position encoded")
	}
}

func TestTypeFor(t *testing.T) {
	cases := []struct {
		typ  sentinelv1.EntityType
		aff  sentinelv1.Affiliation
		env  sentinelv1.TrackEnvironment
		want string
	}{
		{sentinelv1.EntityType_ENTITY_TYPE_AIRCRAFT, sentinelv1.Affiliation_AFFILIATION_FRIENDLY, 0, "a-f-A-C-F"},
		{sentinelv1.EntityType_ENTITY_TYPE_AIRCRAFT, sentinelv1.Affiliation_AFFILIATION_HOSTILE, 0, "a-h-A-C-F"},
		{sentinelv1.EntityType_ENTITY_TYPE_DRONE, sentinelv1.Affiliation_AFFILIATION_SUSPECT, 0, "a-s-A-M-F-Q"},
		{sentinelv1.EntityType_ENTITY_TYPE_VESSEL, sentinelv1.Affiliation_AFFILIATION_NEUTRAL, 0, "a-n-S-X-M"},
		{sentinelv1.EntityType_ENTITY_TYPE_VESSEL, sentinelv1.Affiliation_AFFILIATION_UNKNOWN, sentinelv1.TrackEnvironment_TRACK_ENVIRONMENT_SUBSURFACE, "a-u-U"},
		{sentinelv1.EntityType_ENTITY_TYPE_VEHICLE, sentinelv1.Affiliation_AFFILIATION_ASSUMED_FRIENDLY, 0, "a-a-G-E-V"},
		{sentinelv1.EntityType_ENTITY_TYPE_PERSON, sentinelv1.Affiliation_AFFILIATION_PENDING, 0, "a-p-G-U-C"},
		{sentinelv1.EntityType_ENTITY_TYPE_UNIT, 0, 0, "a-u-G-U"},
		{sentinelv1.EntityType_ENTITY_TYPE_FACILITY, 0, 0, "a-u-G-I"},
		{sentinelv1.EntityType_ENTITY_TYPE_SENSOR, 0, 0, "a-u-G-E-S"},
		{sentinelv1.EntityType_ENTITY_TYPE_SATELLITE, 0, 0, "a-u-P-S"},
		{sentinelv1.EntityType_ENTITY_TYPE_UNKNOWN, 0, sentinelv1.TrackEnvironment_TRACK_ENVIRONMENT_AIR, "a-u-A"},
		{sentinelv1.EntityType_ENTITY_TYPE_UNKNOWN, 0, sentinelv1.TrackEnvironment_TRACK_ENVIRONMENT_SEA_SURFACE, "a-u-S"},
		{sentinelv1.EntityType_ENTITY_TYPE_CYBER, 0, 0, "a-u-X"},
	}
	for _, tc := range cases {
		got := TypeFor(&sentinelv1.Entity{EntityType: tc.typ, Affiliation: tc.aff, TrackEnvironment: tc.env})
		if got != tc.want {
			t.Errorf("%v/%v/%v = %s, want %s", tc.typ, tc.aff, tc.env, got, tc.want)
		}
	}
	mil := &sentinelv1.Entity{EntityType: sentinelv1.EntityType_ENTITY_TYPE_AIRCRAFT, Metadata: map[string]string{"military": "true"}}
	if got := TypeFor(mil); got != "a-u-A-M-F" {
		t.Errorf("military = %s", got)
	}
	heli := &sentinelv1.Entity{EntityType: sentinelv1.EntityType_ENTITY_TYPE_AIRCRAFT, Platforms: map[string]*sentinelv1.PlatformDetail{"adsb": {Detail: &sentinelv1.PlatformDetail_Adsb{Adsb: &sentinelv1.AdsbDetail{Category: "A7"}}}}}
	if got := TypeFor(heli); got != "a-u-A-C-H" {
		t.Errorf("rotorcraft = %s", got)
	}
}

func TestAlertsShapesAndTaskings(t *testing.T) {
	enc := New(Options{SiteID: "east", EmergencyMinSeverity: "CRITICAL", AlertStale: 5 * time.Minute})
	alert := &sentinelv1.Alert{AlertId: "a1", AlertType: sentinelv1.AlertType_ALERT_TYPE_GEOFENCE_BREACH, Severity: sentinelv1.AlertSeverity_ALERT_SEVERITY_HIGH, Title: "Geofence breach", Description: "UAL123 entered Zone A", EntityId: "e1", Position: &sentinelv1.Coordinate{Latitude: 1, Longitude: 2}, CreatedAt: timestamppb.New(t0)}
	b, err := enc.Alert(alert, "SENTINEL-east-e1", t0)
	if err != nil {
		t.Fatal(err)
	}
	ev := parse(t, b)
	if ev.UID != "SENTINEL-east-alert-a1" || ev.Type != "b-m-p-s-m" || ev.Detail.Contact.Callsign != "Geofence breach" || ev.Detail.Emergency != nil {
		t.Errorf("alert = %+v", ev)
	}
	if len(ev.Detail.Links) != 1 || ev.Detail.Links[0].UID != "SENTINEL-east-e1" || ev.Detail.Links[0].Relation != "p-p" {
		t.Errorf("alert link = %+v", ev.Detail.Links)
	}
	if ev.Detail.Color == nil || ev.Detail.Color.Argb != -65536 {
		t.Errorf("alert colour = %+v", ev.Detail.Color)
	}
	critical := proto.Clone(alert).(*sentinelv1.Alert)
	critical.Severity = sentinelv1.AlertSeverity_ALERT_SEVERITY_CRITICAL
	b, _ = enc.Alert(critical, "", t0)
	if ev := parse(t, b); ev.Type != "b-a-o-tbl" || ev.Detail.Emergency == nil || ev.Detail.Emergency.Type != "911 Alert" || len(ev.Detail.Links) != 0 {
		t.Errorf("emergency = %+v", ev)
	}
	b, _ = enc.AlertCancel(critical, t0)
	if ev := parse(t, b); ev.Type != "b-a-o-can" || ev.Detail.Emergency == nil || !ev.Detail.Emergency.Cancel {
		t.Errorf("cancel = %+v", ev)
	}

	b, err = enc.Polygon("SENTINEL-east-geofence-r1", "Zone A", [][2]float64{{1, 2}, {1, 3}, {2, 3}}, t0)
	if err != nil {
		t.Fatal(err)
	}
	if ev := parse(t, b); ev.Type != "u-d-f" || len(ev.Detail.Links) != 3 || ev.Detail.Links[1].Point != "1.000000,3.000000" || ev.Detail.StrokeColor == nil || ev.Detail.FillColor == nil || ev.Detail.Contact.Callsign != "Zone A" {
		t.Errorf("polygon = %+v", ev)
	}
	if _, err := enc.Polygon("x", "x", [][2]float64{{1, 2}, {1, 3}}, t0); err == nil {
		t.Error("two-point polygon accepted")
	}
	b, _ = enc.Circle("c1", "Perimeter", 1, 2, 500, t0)
	if ev := parse(t, b); ev.Type != "u-d-c-c" || ev.Detail.Shape == nil || ev.Detail.Shape.Ellipse.Major != 500 {
		t.Errorf("circle = %+v", ev)
	}
	b, _ = enc.Marker("SENTINEL-east-location-l1", "HQ", "Main site", 1, 2, t0)
	if ev := parse(t, b); ev.Type != "b-m-p-s-m" || ev.Detail.Contact.Callsign != "HQ" || ev.Detail.Remarks.Text != "Main site" {
		t.Errorf("marker = %+v", ev)
	}
	b, _ = enc.Delete("SENTINEL-east-e1", t0)
	if ev := parse(t, b); ev.Type != "t-x-d-d" || len(ev.Detail.Links) != 1 || ev.Detail.Links[0].UID != "SENTINEL-east-e1" || ev.Detail.ForceDelete == nil {
		t.Errorf("delete = %+v", ev)
	}
	b, _ = enc.Ping(t0)
	if ev := parse(t, b); ev.Type != "t-x-c-t" || ev.UID != "east-ping" {
		t.Errorf("ping = %+v", ev)
	}
	if !strings.HasPrefix(string(b), xml.Header) || !strings.HasSuffix(string(b), "</event>\n") {
		t.Errorf("framing: %q", b)
	}
}
