package sources

import "testing"

func TestBBoxValidate(t *testing.T) {
	valid := BBox{South: 40, West: -90, North: 45, East: -80}
	if err := valid.Validate(); err != nil {
		t.Errorf("valid box rejected: %v", err)
	}
	bad := []BBox{
		{South: 46, West: -90, North: 45, East: -80},  // south north of north
		{South: 40, West: -80, North: 45, East: -90},  // crosses the antimeridian unsplit
		{South: -91, West: -90, North: 45, East: -80}, // latitude range
		{South: 40, West: -181, North: 45, East: -80}, // longitude range
	}
	for i, b := range bad {
		if err := b.Validate(); err == nil {
			t.Errorf("box %d should be rejected: %+v", i, b)
		}
	}
}

func TestBBoxAreaAndGlobal(t *testing.T) {
	b := BBox{South: 40, West: -90, North: 45, East: -80}
	if got := b.Area(); got != 50 {
		t.Errorf("Area = %v, want 50", got)
	}
	if b.IsGlobal() {
		t.Error("small box must not be global")
	}
	g := BBox{South: -85, West: -170, North: 85, East: 170}
	if !g.IsGlobal() {
		t.Error("near-global box should be global")
	}
	if got := (Viewport{Boxes: []BBox{g}}).Area(); got != GlobalArea {
		t.Errorf("global viewport area = %v, want %v", got, GlobalArea)
	}
	if got := (Viewport{}).Area(); got != GlobalArea {
		t.Errorf("empty viewport area = %v, want %v", got, GlobalArea)
	}
	two := Viewport{Boxes: []BBox{b, {South: 0, West: 0, North: 1, East: 1}}}
	if got := two.Area(); got != 51 {
		t.Errorf("summed area = %v, want 51", got)
	}
}

func TestViewportEqual(t *testing.T) {
	a := BBox{South: 1, West: 1, North: 2, East: 2}
	b := BBox{South: 3, West: 3, North: 4, East: 4}
	v1 := Viewport{Active: true, Clients: 1, Boxes: []BBox{a, b}}
	v2 := Viewport{Active: true, Clients: 5, Boxes: []BBox{b, a}}
	if !v1.Equal(v2) {
		t.Error("box order and client count must not matter")
	}
	if v1.Equal(Viewport{Active: true, Boxes: []BBox{a}}) {
		t.Error("different box sets must differ")
	}
	if v1.Equal(Viewport{Active: false, Boxes: []BBox{a, b}}) {
		t.Error("active flag must matter")
	}
}
