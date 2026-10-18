package stream

import (
	"encoding/json"
	"testing"

	"go.uber.org/zap"
)

func TestBoundsAreaSqDeg(t *testing.T) {
	for _, tc := range []struct {
		b    Bounds
		want float64
	}{
		{Bounds{North: 10, South: 0, East: 20, West: 0}, 200},
		{Bounds{North: 10, South: 0, East: -170, West: 170}, 200}, // across the antimeridian
		{GlobalBounds, 180 * 360},
	} {
		if got := BoundsAreaSqDeg(tc.b); got != tc.want {
			t.Errorf("%+v: area %v, want %v", tc.b, got, tc.want)
		}
	}
}

// A client looking at a continent or the globe gets no live positions
// (its map draws counts), except for what it subscribed to, and does not
// steer ingestion.
func TestWideViewsGetNoPositionsAndDoNotSteerIngestion(t *testing.T) {
	hub := NewHub(zap.NewNop())
	hub.SetDetailMaxArea(2500)
	near := newClient("near", hub, nil, zap.NewNop())
	wide := newClient("wide", hub, nil, zap.NewNop())
	hub.register(near)
	hub.register(wide)
	near.setViewport(Bounds{North: 52, South: 49, East: 3, West: -3})
	wide.setViewport(GlobalBounds)
	wide.subscribe("selected")

	hub.BroadcastBatch([]PositionedEvent{posEvent("ship", 50, 0), posEvent("selected", -30, 100)})

	var events []EntityEvent
	env := decodeFrame(t, near)
	_ = json.Unmarshal(env.Data, &events)
	if len(events) != 1 || events[0].Entity.ID != "ship" {
		t.Errorf("near view got %+v", events)
	}
	env = decodeFrame(t, wide)
	_ = json.Unmarshal(env.Data, &events)
	if len(events) != 1 || events[0].Entity.ID != "selected" {
		t.Errorf("wide view got %+v, want only its subscription", events)
	}

	vps := hub.DetailViewports()
	if len(vps) != 1 || vps[0] != near.Viewport() {
		t.Errorf("detail viewports = %+v", vps)
	}
	if all := hub.Viewports(); len(all) != 2 {
		t.Errorf("viewports = %+v", all)
	}

	// Without a limit every view is a detail view.
	hub.SetDetailMaxArea(0)
	if len(hub.DetailViewports()) != 2 {
		t.Error("no limit: every viewport steers ingestion")
	}
}
