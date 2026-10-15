package evaluator

import (
	"encoding/json"
	"strings"
	"testing"
)

func cfg(t *testing.T, raw string) map[string]interface{} {
	t.Helper()
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestParsePolygon_BothShapes(t *testing.T) {
	pairs, ok := parsePolygon(cfg(t, `{"p":[[51,27],[56,27],[56,24],[51,24]]}`)["p"])
	if !ok || len(pairs) != 4 || pairs[0][0] != 51 || pairs[0][1] != 27 {
		t.Errorf("pairs = %v ok=%v", pairs, ok)
	}
	objs, ok := parsePolygon(cfg(t, `{"p":[{"latitude":27,"longitude":51},{"latitude":27,"longitude":56},{"latitude":24,"longitude":56},{"latitude":24,"longitude":51}]}`)["p"])
	if !ok || len(objs) != 4 || objs[0][0] != 51 || objs[0][1] != 27 {
		t.Errorf("objs = %v ok=%v (must be [lng, lat])", objs, ok)
	}
	if _, ok := parsePolygon(cfg(t, `{"p":[[1,2],[3,4]]}`)["p"]); ok {
		t.Error("two points is not a polygon")
	}
	if _, ok := parsePolygon("garbage"); ok {
		t.Error("non-array should fail")
	}
}

func TestPolygonWKT_ClosesRing(t *testing.T) {
	wkt := polygonWKT([][]float64{{51, 27}, {56, 27}, {56, 24}})
	if !strings.HasPrefix(wkt, "POLYGON((51.000000 27.000000,") || !strings.HasSuffix(wkt, ",51.000000 27.000000))") {
		t.Errorf("wkt = %s", wkt)
	}
	closed := polygonWKT([][]float64{{1, 1}, {2, 1}, {2, 2}, {1, 1}})
	if strings.Count(closed, "1.000000 1.000000") != 2 {
		t.Errorf("already closed ring should not gain a point: %s", closed)
	}
}

func TestTriggerMode(t *testing.T) {
	cases := map[string]string{
		`{}`:                    "BOTH",
		`{"triggerOn":"entry"}`: "ENTRY",
		`{"trigger_on_enter":true,"trigger_on_exit":false}`:  "ENTRY",
		`{"trigger_on_enter":false,"trigger_on_exit":true}`:  "EXIT",
		`{"trigger_on_enter":true,"trigger_on_exit":true}`:   "BOTH",
		`{"trigger_on_enter":false,"trigger_on_exit":false}`: "NONE",
		`{"triggerOn":"EXIT","trigger_on_enter":true}`:       "EXIT",
	}
	for raw, want := range cases {
		if got := triggerMode(cfg(t, raw)); got != want {
			t.Errorf("%s -> %q, want %q", raw, got, want)
		}
	}
}

func TestGeofenceAlertType(t *testing.T) {
	cases := []struct {
		contains bool
		mode     string
		want     string
	}{
		{true, "ENTRY", "GEOFENCE_ENTRY"}, {true, "BOTH", "GEOFENCE_ENTRY"}, {true, "EXIT", ""},
		{false, "EXIT", "GEOFENCE_EXIT"}, {false, "BOTH", "GEOFENCE_EXIT"}, {false, "ENTRY", ""},
		{true, "NONE", ""}, {false, "NONE", ""},
	}
	for _, c := range cases {
		if got := geofenceAlertType(c.contains, c.mode); got != c.want {
			t.Errorf("contains=%v mode=%s -> %q, want %q", c.contains, c.mode, got, c.want)
		}
	}
}

func TestEvaluateSpeedRule(t *testing.T) {
	c := cfg(t, `{"maxSpeedKnots":500,"minSpeedKnots":50}`)
	if fired, _ := evaluateSpeedRule("e", c, 300); fired {
		t.Error("in-range speed should not fire")
	}
	if fired, desc := evaluateSpeedRule("e", c, 600); !fired || !strings.Contains(desc, "exceeds max 500.0") {
		t.Errorf("fast: %v %q", fired, desc)
	}
	if fired, desc := evaluateSpeedRule("e", c, 10); !fired || !strings.Contains(desc, "below min 50.0") {
		t.Errorf("slow: %v %q", fired, desc)
	}
	if fired, _ := evaluateSpeedRule("e", cfg(t, `{"maxSpeedKnots":"fast"}`), 1000); fired {
		t.Error("non-numeric threshold must be ignored")
	}
}
