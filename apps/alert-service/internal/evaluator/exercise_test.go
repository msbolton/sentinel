package evaluator

import (
	"context"
	"errors"
	"slices"
	"testing"

	"go.uber.org/zap"

	sentinelv1 "github.com/sentinel/proto-gen/sentinel/v1"

	"github.com/sentinel/alert-service/internal/store"
)

// exerciseEvaluator stands in for the entity table: the listed ids are exercise
// entities, any other id is live, and failWith makes every lookup fail.
func exerciseEvaluator(failWith error, exerciseIDs ...string) (*Evaluator, *[][]string) {
	var asked [][]string
	return &Evaluator{
		logger: zap.NewNop(),
		entitiesExercise: func(_ context.Context, ids []string) (bool, error) {
			asked = append(asked, ids)
			if failWith != nil {
				return false, failWith
			}
			for _, id := range ids {
				if slices.Contains(exerciseIDs, id) {
					return true, nil
				}
			}
			return false, nil
		},
		entityLevel: func(context.Context, string) (string, error) { return "UNCLASSIFIED", nil },
	}, &asked
}

func externalEvent(entityID string, opts ...func(*sentinelv1.Alert)) *sentinelv1.AlertEvent {
	a := &sentinelv1.Alert{AlertId: "a1", EntityId: entityID, AlertType: sentinelv1.AlertType_ALERT_TYPE_ENTITY_LOST, Severity: sentinelv1.AlertSeverity_ALERT_SEVERITY_HIGH, Title: "t"}
	for _, o := range opts {
		o(a)
	}
	return &sentinelv1.AlertEvent{Alert: a, Producer: "analytics-service"}
}

func TestExternalAlertExercise(t *testing.T) {
	boom := errors.New("db down")
	cases := []struct {
		name string
		fail error
		ex   []string
		ev   *sentinelv1.AlertEvent
		want bool
	}{
		{"about a live entity", nil, []string{"sim"}, externalEvent("live"), false},
		{"about an exercise entity", nil, []string{"sim"}, externalEvent("sim"), true},
		{"a related entity is exercise", nil, []string{"sim"}, externalEvent("live", func(a *sentinelv1.Alert) { a.RelatedEntityIds = []string{"other", "sim"} }), true},
		{"related entities are all live", nil, []string{"sim"}, externalEvent("live", func(a *sentinelv1.Alert) { a.RelatedEntityIds = []string{"other"} }), false},
		{"already marked by the producer", nil, nil, externalEvent("live", func(a *sentinelv1.Alert) { a.Exercise = true }), true},
		{"a lookup error marks it", boom, nil, externalEvent("live"), true},
	}
	for _, c := range cases {
		e, _ := exerciseEvaluator(c.fail, c.ex...)
		p, err := e.externalAlertParams(context.Background(), c.ev)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if p.Exercise != c.want {
			t.Errorf("%s: Exercise = %v, want %v", c.name, p.Exercise, c.want)
		}
	}
}

func TestExternalAlertAsksAboutThePrimaryAndRelatedEntities(t *testing.T) {
	e, asked := exerciseEvaluator(nil)
	ev := externalEvent("primary", func(a *sentinelv1.Alert) { a.RelatedEntityIds = []string{"r1", "r2"} })
	if _, err := e.externalAlertParams(context.Background(), ev); err != nil {
		t.Fatal(err)
	}
	if len(*asked) != 1 || !slices.Equal((*asked)[0], []string{"primary", "r1", "r2"}) {
		t.Fatalf("asked %v", *asked)
	}
	// A producer's own mark settles it without a lookup.
	e, asked = exerciseEvaluator(nil)
	if p, _ := e.externalAlertParams(context.Background(), externalEvent("live", func(a *sentinelv1.Alert) { a.Exercise = true })); !p.Exercise || len(*asked) != 0 {
		t.Fatalf("exercise=%v asked=%v", p.Exercise, *asked)
	}
}

func TestRuleAlertsAreMarkedFromTheirEntity(t *testing.T) {
	rule := store.AlertRuleRecord{ID: "r1", Name: "zone", Severity: "HIGH"}
	ctx := context.Background()
	for _, c := range []struct {
		name   string
		entity string
		fail   error
		want   bool
	}{
		{"exercise entity", "sim", nil, true},
		{"live entity", "live", nil, false},
		{"lookup error", "live", errors.New("db down"), true},
	} {
		e, _ := exerciseEvaluator(c.fail, "sim")
		if g := e.geofenceAlertParams(ctx, rule, "GEOFENCE_ENTRY", c.entity, 1, 2, "UNCLASSIFIED"); g.Exercise != c.want || g.EntityID != c.entity {
			t.Errorf("geofence, %s: %+v", c.name, g)
		}
		if s := e.speedAlertParams(ctx, rule, "too fast", c.entity, "UNCLASSIFIED"); s.Exercise != c.want || s.EntityID != c.entity {
			t.Errorf("speed, %s: %+v", c.name, s)
		}
	}
}

func TestAlertProtoCarriesExercise(t *testing.T) {
	for _, want := range []bool{true, false} {
		got := AlertProto(&store.AlertRecord{ID: "a1", AlertType: "SPEED_ANOMALY", Severity: "HIGH", EntityID: "e1", CreatedAt: "2026-10-20T00:00:00Z", Exercise: want})
		if got.Exercise != want {
			t.Errorf("Exercise = %v, want %v", got.Exercise, want)
		}
	}
}
