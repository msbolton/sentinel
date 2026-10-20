package policy

import (
	"encoding/json"
	"testing"

	sentinelv1 "github.com/sentinel/proto-gen/sentinel/v1"
)

func aircraft(feed string, lat, lon float64) *sentinelv1.Entity {
	return &sentinelv1.Entity{
		Id: "e1", EntityType: sentinelv1.EntityType_ENTITY_TYPE_AIRCRAFT, FeedId: feed, LastFeedId: feed,
		Classification: sentinelv1.Classification_CLASSIFICATION_UNCLASSIFIED,
		Position:       &sentinelv1.Coordinate{Latitude: lat, Longitude: lon},
		Sources:        []*sentinelv1.SourceContribution{{FeedId: feed}},
	}
}

func TestBoxContainsWrapsAntimeridian(t *testing.T) {
	plain := Box{North: 10, South: -10, East: 20, West: -20}
	if !plain.Contains(0, 0) || plain.Contains(11, 0) || plain.Contains(0, 21) {
		t.Error("plain box")
	}
	wrap := Box{North: 10, South: -10, East: -170, West: 170}
	if !wrap.Contains(0, 175) || !wrap.Contains(0, -175) || wrap.Contains(0, 0) {
		t.Error("wrapped box")
	}
}

func TestParse(t *testing.T) {
	p, err := Parse(nil)
	if err != nil || len(p.Boxes) != 0 || p.ClassificationCeiling != "" || !p.Alerts.On() {
		t.Fatalf("empty policy = %+v %v", p, err)
	}
	p, err = Parse(json.RawMessage(`{"entityTypes":["AIRCRAFT"],"boxes":[{"north":1,"south":0,"east":1,"west":0}],"classificationCeiling":"SECRET","alerts":{"enabled":false}}`))
	if err != nil || len(p.EntityTypes) != 1 || p.ClassificationCeiling != "SECRET" || p.Alerts.On() {
		t.Fatalf("parsed = %+v %v", p, err)
	}
	if _, err := Parse(json.RawMessage(`{"boxes":[{"north":0,"south":1,"east":1,"west":0}]}`)); err == nil {
		t.Error("inverted box accepted")
	}
	if _, err := Parse(json.RawMessage(`{not json`)); err == nil {
		t.Error("bad json accepted")
	}
}

func TestEvaluateReasonsInOrder(t *testing.T) {
	ctx := Context{SelfFeedID: "tak-hq", ConnectionFeedIDs: map[string]bool{"tak-hq": true, "tak-field": true}}
	base := Policy{EntityTypes: []string{"AIRCRAFT"}, Feeds: []string{"opensky"}, Boxes: []Box{{North: 45, South: 35, East: -70, West: -80}}, ClassificationCeiling: "CONFIDENTIAL"}

	ok := aircraft("opensky", 40, -75)
	if d := Evaluate(base, ctx, ok); !d.Allow {
		t.Fatalf("good entity denied: %+v", d)
	}

	// Loop via each of the three id fields.
	for name, e := range map[string]*sentinelv1.Entity{
		"feed_id":      {FeedId: "tak-hq", Position: ok.Position},
		"last_feed_id": {FeedId: "opensky", LastFeedId: "tak-hq", Position: ok.Position},
		"sources":      {FeedId: "opensky", Sources: []*sentinelv1.SourceContribution{{FeedId: "x"}, {FeedId: "tak-hq"}}, Position: ok.Position},
	} {
		if d := Evaluate(base, ctx, e); d.Allow || d.Reason != ReasonLoop {
			t.Errorf("%s: %+v", name, d)
		}
	}

	// Another connection's contribution is held back unless reexport is on.
	other := aircraft("opensky", 40, -75)
	other.Sources = append(other.Sources, &sentinelv1.SourceContribution{FeedId: "tak-field"})
	if d := Evaluate(base, ctx, other); d.Allow || d.Reason != ReasonReexport {
		t.Errorf("reexport off: %+v", d)
	}
	re := base
	re.Reexport = true
	if d := Evaluate(re, ctx, other); !d.Allow {
		t.Errorf("reexport on: %+v", d)
	}

	// Exercise comes after the loop and re-export guards and before the
	// classification ceiling.
	exLoop := aircraft("tak-hq", 40, -75)
	exLoop.Exercise = true
	if d := Evaluate(base, ctx, exLoop); d.Allow || d.Reason != ReasonLoop {
		t.Errorf("exercise loop: %+v", d)
	}
	exOther := aircraft("opensky", 40, -75)
	exOther.Exercise = true
	exOther.Sources = append(exOther.Sources, &sentinelv1.SourceContribution{FeedId: "tak-field"})
	if d := Evaluate(base, ctx, exOther); d.Allow || d.Reason != ReasonReexport {
		t.Errorf("exercise reexport: %+v", d)
	}
	exSecret := aircraft("opensky", 40, -75)
	exSecret.Exercise = true
	exSecret.Classification = sentinelv1.Classification_CLASSIFICATION_SECRET
	if d := Evaluate(base, ctx, exSecret); d.Allow || d.Reason != ReasonExercise {
		t.Errorf("exercise before classification: %+v", d)
	}
	allowEx := base
	allowEx.AllowExercise = true
	if d := Evaluate(allowEx, ctx, exSecret); d.Allow || d.Reason != ReasonClassification {
		t.Errorf("allowed exercise still meets the ceiling: %+v", d)
	}

	secret := aircraft("opensky", 40, -75)
	secret.Classification = sentinelv1.Classification_CLASSIFICATION_SECRET
	if d := Evaluate(base, ctx, secret); d.Allow || d.Reason != ReasonClassification {
		t.Errorf("classification: %+v", d)
	}
	unspecified := aircraft("opensky", 40, -75)
	unspecified.Classification = sentinelv1.Classification_CLASSIFICATION_UNSPECIFIED
	if d := Evaluate(Policy{}, Context{}, unspecified); !d.Allow {
		t.Errorf("unspecified must read as unclassified: %+v", d)
	}

	vessel := aircraft("opensky", 40, -75)
	vessel.EntityType = sentinelv1.EntityType_ENTITY_TYPE_VESSEL
	if d := Evaluate(base, ctx, vessel); d.Allow || d.Reason != ReasonType {
		t.Errorf("type: %+v", d)
	}
	if d := Evaluate(base, ctx, aircraft("adsb-lol", 40, -75)); d.Allow || d.Reason != ReasonFeed {
		t.Errorf("feed: %+v", d)
	}
	// Any contributing feed satisfies the feed list.
	fused := aircraft("adsb-lol", 40, -75)
	fused.Sources = append(fused.Sources, &sentinelv1.SourceContribution{FeedId: "opensky"})
	if d := Evaluate(base, ctx, fused); !d.Allow {
		t.Errorf("fused feed: %+v", d)
	}
	if d := Evaluate(base, ctx, aircraft("opensky", 50, -75)); d.Allow || d.Reason != ReasonBox {
		t.Errorf("box: %+v", d)
	}
	noPos := aircraft("opensky", 0, 0)
	noPos.Position = nil
	if d := Evaluate(base, ctx, noPos); d.Allow || d.Reason != ReasonNoPosition {
		t.Errorf("no position with boxes: %+v", d)
	}
	if d := Evaluate(Policy{}, ctx, noPos); !d.Allow {
		t.Errorf("no position without boxes: %+v", d)
	}
	if d := Evaluate(base, ctx, nil); d.Allow {
		t.Error("nil entity allowed")
	}
}

func TestEvaluateDeniesExercise(t *testing.T) {
	ex := aircraft("opensky", 40, -75)
	ex.Exercise = true
	if d := Evaluate(Policy{}, Context{}, ex); d.Allow || d.Reason != ReasonExercise {
		t.Errorf("exercise with a zero policy: %+v", d)
	}
	if d := Evaluate(Policy{AllowExercise: true}, Context{}, ex); !d.Allow {
		t.Errorf("exercise with allowExercise: %+v", d)
	}
	live := aircraft("opensky", 40, -75)
	for _, p := range []Policy{{}, {AllowExercise: true}} {
		if d := Evaluate(p, Context{}, live); !d.Allow {
			t.Errorf("live entity, %+v: %+v", p, d)
		}
	}
	p, err := Parse([]byte(`{"allowExercise": true}`))
	if err != nil || !p.AllowExercise {
		t.Errorf("parse allowExercise: %+v, %v", p, err)
	}
}

func TestAllowAlert(t *testing.T) {
	ctx := Context{SelfFeedID: "c"}
	p := Policy{Boxes: []Box{{North: 1, South: 0, East: 1, West: 0}}, ClassificationCeiling: "UNCLASSIFIED", Alerts: AlertPolicy{MinSeverity: "MEDIUM", Types: []string{"GEOFENCE_BREACH"}}}
	a := &sentinelv1.Alert{AlertType: sentinelv1.AlertType_ALERT_TYPE_GEOFENCE_BREACH, Severity: sentinelv1.AlertSeverity_ALERT_SEVERITY_HIGH}
	if d := AllowAlert(p, ctx, a, true); !d.Allow {
		t.Fatalf("tracked alert denied: %+v", d)
	}
	if d := AllowAlert(p, ctx, a, false); d.Allow || d.Reason != ReasonNotTracked {
		t.Errorf("untracked without position: %+v", d)
	}
	a.Position = &sentinelv1.Coordinate{Latitude: 0.5, Longitude: 0.5}
	if d := AllowAlert(p, ctx, a, false); !d.Allow {
		t.Errorf("untracked inside box: %+v", d)
	}
	low := &sentinelv1.Alert{AlertType: a.AlertType, Severity: sentinelv1.AlertSeverity_ALERT_SEVERITY_LOW}
	if d := AllowAlert(p, ctx, low, true); d.Allow || d.Reason != ReasonSeverity {
		t.Errorf("severity: %+v", d)
	}
	wrongType := &sentinelv1.Alert{AlertType: sentinelv1.AlertType_ALERT_TYPE_SPEED_ANOMALY, Severity: a.Severity}
	if d := AllowAlert(p, ctx, wrongType, true); d.Allow || d.Reason != ReasonAlertType {
		t.Errorf("alert type: %+v", d)
	}
	secret := &sentinelv1.Alert{AlertType: a.AlertType, Severity: a.Severity, Classification: sentinelv1.Classification_CLASSIFICATION_SECRET}
	if d := AllowAlert(p, ctx, secret, true); d.Allow || d.Reason != ReasonClassification {
		t.Errorf("classification: %+v", d)
	}
	off := false
	if d := AllowAlert(Policy{Alerts: AlertPolicy{Enabled: &off}}, ctx, a, true); d.Allow || d.Reason != ReasonAlertsOff {
		t.Errorf("alerts off: %+v", d)
	}
	if d := AllowAlert(p, ctx, nil, true); d.Allow {
		t.Error("nil alert allowed")
	}
}
