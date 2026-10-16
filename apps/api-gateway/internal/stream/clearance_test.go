package stream

import (
	"encoding/json"
	"testing"

	"go.uber.org/zap"
)

// A client never receives an entity or alert ranked above its clearance,
// whether it is subscribed to the entity or has it in view.
func TestHub_ClearanceFiltersEntitiesAndAlerts(t *testing.T) {
	hub := NewHub(zap.NewNop())
	viewer := newClient("viewer", hub, nil, zap.NewNop()) // UNCLASSIFIED by default
	analyst := newClient("analyst", hub, nil, zap.NewNop())
	analyst.clearance = 2 // SECRET
	hub.register(viewer)
	hub.register(analyst)
	viewer.subscribe("secret-1")

	secret := posEvent("secret-1", 0, 0)
	secret.Rank = 2
	hub.BroadcastBatch([]PositionedEvent{secret, posEvent("open-1", 0, 0)})

	var events []EntityEvent
	env := decodeFrame(t, viewer)
	_ = json.Unmarshal(env.Data, &events)
	if env.Type != MsgEntityBatch || len(events) != 1 || events[0].Entity.ID != "open-1" {
		t.Fatalf("viewer got %s %+v", env.Type, events)
	}
	env = decodeFrame(t, analyst)
	_ = json.Unmarshal(env.Data, &events)
	if len(events) != 2 {
		t.Fatalf("analyst got %+v", events)
	}

	// A batch with nothing the viewer may see queues no frame at all.
	hub.BroadcastBatch([]PositionedEvent{secret})
	noFrame(t, viewer)
	decodeFrame(t, analyst)

	hub.BroadcastRanked(MsgAlertNew, map[string]string{"id": "a1"}, 2)
	noFrame(t, viewer)
	if env := decodeFrame(t, analyst); env.Type != MsgAlertNew {
		t.Fatalf("analyst alert frame = %+v", env)
	}
	hub.BroadcastRanked(MsgAlertNew, map[string]string{"id": "a2"}, 0)
	decodeFrame(t, viewer)
	decodeFrame(t, analyst)
}
