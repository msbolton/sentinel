package stream

import (
	"math"
	"testing"
)

func near(a, b float64) bool { return math.Abs(a-b) < 1e-6 }

func TestAggregate_NoViewports(t *testing.T) {
	v := Aggregate(nil, 0.1, 0.25)
	if v.Active || len(v.Boxes) != 0 || v.Boxes == nil {
		t.Errorf("expected inactive with empty boxes, got %+v", v)
	}
}

func TestAggregate_SingleViewportIsPadded(t *testing.T) {
	v := Aggregate([]Bounds{{North: 45, South: 40, East: -80, West: -90}}, 0.1, 0.25)
	if !v.Active || len(v.Boxes) != 1 {
		t.Fatalf("unexpected %+v", v)
	}
	b := v.Boxes[0]
	// 10 % of a 5° lat span is 0.5°, 10 % of a 10° lon span is 1°.
	if !near(b.North, 45.5) || !near(b.South, 39.5) || !near(b.East, -79) || !near(b.West, -91) {
		t.Errorf("padding wrong: %+v", b)
	}
	if !near(v.AreaSqDeg, 6*12) {
		t.Errorf("area = %v, want 72", v.AreaSqDeg)
	}
	if !near(v.RawAreaSqDeg, 50) || v.RawUnion.North != 45 {
		t.Errorf("raw union wrong: %+v area %v", v.RawUnion, v.RawAreaSqDeg)
	}
}

func TestAggregate_MinimumPaddingAndClamping(t *testing.T) {
	v := Aggregate([]Bounds{{North: 90, South: 89.9, East: 0.1, West: 0}}, 0.1, 0.25)
	b := v.Boxes[0]
	if b.North != 90 || !near(b.South, 89.65) || !near(b.East, 0.35) || !near(b.West, -0.25) {
		t.Errorf("min padding / clamping wrong: %+v", b)
	}
}

func TestAggregate_TwoDisjointViewportsCoverBoth(t *testing.T) {
	v := Aggregate([]Bounds{
		{North: 45, South: 40, East: -80, West: -90},
		{North: 55, South: 50, East: 10, West: 0},
	}, 0.1, 0.25)
	if len(v.Boxes) != 1 {
		t.Fatalf("expected one covering box, got %+v", v.Boxes)
	}
	b := v.Boxes[0]
	if !near(b.South, 39.5) || !near(b.North, 55.5) || !near(b.West, -91) || !near(b.East, 11) {
		t.Errorf("covering box wrong: %+v", b)
	}
	if v.Clients != 2 {
		t.Errorf("clients = %d", v.Clients)
	}
}

func TestAggregate_AntimeridianSplitsIntoTwoBoxes(t *testing.T) {
	v := Aggregate([]Bounds{{North: 20, South: 10, East: -170, West: 170}}, 0.1, 0.25)
	if len(v.Boxes) != 2 {
		t.Fatalf("expected two boxes, got %+v", v.Boxes)
	}
	first, second := v.Boxes[0], v.Boxes[1]
	if !near(first.West, 168) || first.East != 180 || first.North != 21 || first.South != 9 {
		t.Errorf("box up to the antimeridian wrong: %+v", first)
	}
	if second.West != -180 || !near(second.East, -168) {
		t.Errorf("box past the antimeridian wrong: %+v", second)
	}
	if !near(v.AreaSqDeg, 12*24) {
		t.Errorf("area = %v, want 288", v.AreaSqDeg)
	}
	if v.Union.West <= v.Union.East {
		t.Errorf("union should wrap: %+v", v.Union)
	}
}

func TestAggregate_ViewportsAcrossTheAntimeridianStayCompact(t *testing.T) {
	// Tokyo and Hawaii: the short way round crosses 180, not Greenwich.
	v := Aggregate([]Bounds{
		{North: 36, South: 35, East: 140, West: 139},
		{North: 22, South: 21, East: -157, West: -158},
	}, 0, 0.25)
	if len(v.Boxes) != 2 {
		t.Fatalf("expected a wrapped union split in two, got %+v", v.Boxes)
	}
	if !near(v.Union.West, 138.75) || !near(v.Union.East, -156.75) {
		t.Errorf("union = %+v", v.Union)
	}
	if lonSpanOf(v.Union) > 70 {
		t.Errorf("union took the long way round: span %v", lonSpanOf(v.Union))
	}
}

func TestAggregate_NearGlobalBecomesGlobal(t *testing.T) {
	v := Aggregate([]Bounds{{North: 85, South: -85, East: 170, West: -170}}, 0.1, 0.25)
	if len(v.Boxes) != 1 || v.Boxes[0] != globalIngestBox {
		t.Errorf("expected the global box, got %+v", v.Boxes)
	}
	if v.AreaSqDeg != 180*360 {
		t.Errorf("area = %v", v.AreaSqDeg)
	}
	// The whole-globe viewport the client sends when zoomed out.
	v = Aggregate([]Bounds{GlobalBounds}, 0.1, 0.25)
	if len(v.Boxes) != 1 || v.Boxes[0] != globalIngestBox {
		t.Errorf("GlobalBounds should map to the global box, got %+v", v.Boxes)
	}
}

func TestShouldPush(t *testing.T) {
	small := Aggregate([]Bounds{{North: 45, South: 40, East: -80, West: -90}}, 0.1, 0.25)
	if !ShouldPush(nil, small) {
		t.Error("first push always happens")
	}
	// A small pan inside the padding is not worth a push.
	pan := Aggregate([]Bounds{{North: 45.2, South: 40.2, East: -79.5, West: -89.5}}, 0.1, 0.25)
	if ShouldPush(&small, pan) {
		t.Error("pan inside the padded box should not push")
	}
	// Panning out of the padded box pushes.
	far := Aggregate([]Bounds{{North: 45, South: 40, East: -70, West: -80}}, 0.1, 0.25)
	if !ShouldPush(&small, far) {
		t.Error("pan outside the padded box should push")
	}
	// Zooming in by half pushes even though it is still covered.
	zoomIn := Aggregate([]Bounds{{North: 43, South: 41, East: -84, West: -88}}, 0.1, 0.25)
	if !ShouldPush(&small, zoomIn) {
		t.Error("zooming in well below the previous area should push")
	}
	// Zooming out pushes (no longer covered).
	zoomOut := Aggregate([]Bounds{{North: 50, South: 35, East: -70, West: -100}}, 0.1, 0.25)
	if !ShouldPush(&small, zoomOut) {
		t.Error("zooming out should push")
	}
	// Last client leaving pushes inactive; repeated inactive does not.
	none := Aggregate(nil, 0.1, 0.25)
	if !ShouldPush(&small, none) {
		t.Error("going inactive should push")
	}
	if ShouldPush(&none, none) {
		t.Error("inactive to inactive should not push")
	}
	if !ShouldPush(&none, small) {
		t.Error("becoming active should push")
	}
}

func TestCoveringArc(t *testing.T) {
	w, e, l := coveringArc([]lonArc{{start: 10, length: 20}})
	if !near(w, 10) || !near(e, 30) || !near(l, 20) {
		t.Errorf("single arc: %v %v %v", w, e, l)
	}
	w, e, l = coveringArc([]lonArc{{start: 350, length: 20}, {start: 5, length: 10}})
	if !near(w, -10) || !near(e, 15) || !near(l, 25) {
		t.Errorf("wrapped arcs: %v %v %v", w, e, l)
	}
	w, e, l = coveringArc([]lonArc{{start: 0, length: 200}, {start: 150, length: 200}, {start: 300, length: 80}})
	if l != 360 || w != -180 || e != 180 {
		t.Errorf("full coverage: %v %v %v", w, e, l)
	}
}
