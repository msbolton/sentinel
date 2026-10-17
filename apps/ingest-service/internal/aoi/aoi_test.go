package aoi

import (
	"testing"
	"time"
)

func TestBBoxValidate(t *testing.T) {
	good := BBox{South: 40, North: 45, West: -90, East: -80}
	if err := good.Validate(); err != nil {
		t.Fatal(err)
	}
	bad := []BBox{
		{South: 46, North: 45, West: -90, East: -80},
		{South: 40, North: 45, West: 170, East: -170},
		{South: -91, North: 45, West: -90, East: -80},
		{South: 40, North: 45, West: -181, East: -80},
	}
	for _, b := range bad {
		if err := b.Validate(); err == nil {
			t.Errorf("%+v should be invalid", b)
		}
	}
}

func TestAreaSqDegAndGlobal(t *testing.T) {
	if got := (BBox{South: 40, North: 45, West: -90, East: -80}).Area(); got != 50 {
		t.Errorf("area = %v, want 50", got)
	}
	if !(BBox{South: -90, North: 90, West: -180, East: 180}).IsGlobal() {
		t.Error("full globe box should be global")
	}
	if (Area{}).SqDeg() != GlobalArea {
		t.Error("no boxes means the globe")
	}
	a := Area{Boxes: []BBox{{South: 0, North: 10, West: 0, East: 10}, {South: 0, North: 10, West: 20, East: 30}}}
	if a.SqDeg() != 200 {
		t.Errorf("summed area = %v", a.SqDeg())
	}
}

func TestAreaEqualAndExpiry(t *testing.T) {
	b1, b2 := BBox{South: 0, North: 1, West: 0, East: 1}, BBox{South: 2, North: 3, West: 2, East: 3}
	a := Area{Active: true, Clients: 1, Boxes: []BBox{b1, b2}}
	if !a.Equal(Area{Active: true, Clients: 7, Boxes: []BBox{b2, b1}}) {
		t.Error("order and clients must not matter")
	}
	if a.Equal(Area{Active: false, Boxes: []BBox{b1, b2}}) || a.Equal(Area{Active: true, Boxes: []BBox{b1}}) {
		t.Error("active flag and box set must matter")
	}
	now := time.Now()
	if (Area{}).Expired(now) {
		t.Error("zero expiry never expires")
	}
	if !(Area{ExpiresAt: now.Add(-time.Second)}).Expired(now) || (Area{ExpiresAt: now.Add(time.Second)}).Expired(now) {
		t.Error("expiry comparison wrong")
	}
}

func TestBBoxContains(t *testing.T) {
	b := BBox{North: 45, South: 40, East: -70, West: -80}
	cases := []struct {
		lat, lon float64
		want     bool
	}{
		{42, -75, true}, {40, -80, true}, {45, -70, true}, // inside and on the edges
		{39.9, -75, false}, {42, -69.9, false}, {42, -81, false}, {-42, -75, false},
	}
	for _, c := range cases {
		if got := b.Contains(c.lat, c.lon); got != c.want {
			t.Errorf("Contains(%v, %v) = %v, want %v", c.lat, c.lon, got, c.want)
		}
	}
	global := BBox{North: 90, South: -90, East: 170, West: -170} // 90 % of each span counts as global
	if !global.Contains(0, 179.9) {
		t.Error("a global box must contain a point outside its nominal span")
	}
}
