package stream

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"go.uber.org/zap"

	"github.com/sentinel/go-common/kafka"
)

func TestBounds_ValidateAndContains(t *testing.T) {
	if err := (Bounds{North: 10, South: -10, East: 20, West: -20}).Validate(); err != nil {
		t.Error(err)
	}
	for _, bad := range []Bounds{
		{North: 91, South: 0, East: 0, West: 0},
		{North: 0, South: -91, East: 0, West: 0},
		{North: 0, South: 0, East: 181, West: 0},
		{North: 0, South: 0, East: 0, West: -181},
		{North: -10, South: 10, East: 0, West: 0},
	} {
		if bad.Validate() == nil {
			t.Errorf("%+v should be invalid", bad)
		}
	}
	b := Bounds{North: 10, South: -10, East: 20, West: -20}
	if !b.Contains(0, 0) || b.Contains(11, 0) || b.Contains(0, 21) {
		t.Error("simple containment wrong")
	}
	wrap := Bounds{North: 10, South: -10, East: -170, West: 170}
	if !wrap.Contains(0, 175) || !wrap.Contains(0, -175) || wrap.Contains(0, 0) {
		t.Error("antimeridian containment wrong")
	}
	if !GlobalBounds.Contains(89, -179) {
		t.Error("global bounds should contain everything")
	}
}

// decodeFrame reads one frame from a client's queue.
func decodeFrame(t *testing.T, c *Client) Envelope {
	t.Helper()
	select {
	case frame := <-c.send:
		var env Envelope
		if err := json.Unmarshal(frame, &env); err != nil {
			t.Fatal(err)
		}
		return env
	case <-time.After(time.Second):
		t.Fatal("no frame queued")
	}
	return Envelope{}
}

func noFrame(t *testing.T, c *Client) {
	t.Helper()
	select {
	case frame := <-c.send:
		t.Fatalf("unexpected frame: %s", frame)
	case <-time.After(20 * time.Millisecond):
	}
}

func posEvent(id string, lat, lng float64) PositionedEvent {
	return PositionedEvent{Lat: lat, Lng: lng, Event: EntityEvent{Type: "updated", Entity: Entity{ID: id, Metadata: map[string]interface{}{}, Affiliations: []string{}}}}
}

func TestHub_BroadcastBatchFiltersPerClient(t *testing.T) {
	hub := NewHub(zap.NewNop())
	inView := newClient("a", hub, nil, zap.NewNop())
	inView.setViewport(Bounds{North: 10, South: 0, East: 10, West: 0})
	subscribed := newClient("b", hub, nil, zap.NewNop())
	subscribed.setViewport(Bounds{North: -80, South: -90, East: 10, West: 0})
	subscribed.subscribe("far")
	hub.register(inView)
	hub.register(subscribed)

	hub.BroadcastBatch([]PositionedEvent{posEvent("near", 5, 5), posEvent("far", 50, 50)})

	env := decodeFrame(t, inView)
	var events []EntityEvent
	_ = json.Unmarshal(env.Data, &events)
	if env.Type != MsgEntityBatch || len(events) != 1 || events[0].Entity.ID != "near" {
		t.Errorf("inView got %s %+v", env.Type, events)
	}
	env = decodeFrame(t, subscribed)
	_ = json.Unmarshal(env.Data, &events)
	if len(events) != 1 || events[0].Entity.ID != "far" {
		t.Errorf("subscribed got %+v", events)
	}
	noFrame(t, inView)

	hub.unregister(subscribed)
	hub.BroadcastAll(MsgAlertNew, map[string]string{"id": "x"})
	if env := decodeFrame(t, inView); env.Type != MsgAlertNew {
		t.Errorf("broadcast all type = %s", env.Type)
	}
	noFrame(t, subscribed)
	if hub.ClientCount() != 1 {
		t.Errorf("client count = %d", hub.ClientCount())
	}
}

func TestClient_HandleInbound(t *testing.T) {
	hub := NewHub(zap.NewNop())
	c := newClient("c", hub, nil, zap.NewNop())

	c.handleInbound([]byte(`{"type":"viewport:update","data":{"north":10,"south":0,"east":10,"west":0}}`))
	if env := decodeFrame(t, c); env.Type != MsgViewportUpdated {
		t.Errorf("type = %s", env.Type)
	}
	if c.Viewport().North != 10 {
		t.Error("viewport not stored")
	}
	c.handleInbound([]byte(`{"type":"viewport:update","data":{"north":100,"south":0,"east":10,"west":0}}`))
	if env := decodeFrame(t, c); env.Type != MsgError {
		t.Errorf("invalid viewport should error, got %s", env.Type)
	}
	c.handleInbound([]byte(`{"type":"entity:subscribe","data":{"entityId":"e1"}}`))
	if env := decodeFrame(t, c); env.Type != MsgEntitySubscribed {
		t.Errorf("type = %s", env.Type)
	}
	if _, ok := c.subs["e1"]; !ok {
		t.Error("subscription not stored")
	}
	c.handleInbound([]byte(`{"type":"entity:unsubscribe","data":{"entityId":"e1"}}`))
	decodeFrame(t, c)
	if _, ok := c.subs["e1"]; ok {
		t.Error("subscription not removed")
	}
	c.handleInbound([]byte(`{"type":"entity:subscribe","data":{}}`))
	if env := decodeFrame(t, c); env.Type != MsgError {
		t.Error("missing entityId should error")
	}
	c.handleInbound([]byte(`{"type":"ping"}`))
	if env := decodeFrame(t, c); env.Type != MsgPong {
		t.Errorf("ping should pong, got %s", env.Type)
	}
	c.handleInbound([]byte(`garbage`))
	if env := decodeFrame(t, c); env.Type != MsgError {
		t.Error("garbage should error")
	}
}

func TestClient_SlowClientIsDropped(t *testing.T) {
	hub := NewHub(zap.NewNop())
	c := newClient("slow", hub, nil, zap.NewNop())
	for i := 0; i < sendBufferSize+1; i++ {
		c.trySend([]byte("x"))
	}
	select {
	case <-c.done:
	default:
		t.Error("client with a full buffer should be closed")
	}
}

func TestBatcher_CoalescesIntoOneFrame(t *testing.T) {
	hub := NewHub(zap.NewNop())
	c := newClient("c", hub, nil, zap.NewNop())
	hub.register(c)
	b := NewBatcher(hub, time.Hour)
	b.Add(posEvent("1", 1, 1))
	b.Add(posEvent("2", 2, 2))
	noFrame(t, c)
	b.Flush()
	env := decodeFrame(t, c)
	var events []EntityEvent
	_ = json.Unmarshal(env.Data, &events)
	if len(events) != 2 {
		t.Errorf("expected 2 events in one frame, got %d", len(events))
	}
	b.Start()
	b.Stop()
}

func newTestBridge(t *testing.T) (*Bridge, *Client) {
	t.Helper()
	hub := NewHub(zap.NewNop())
	c := newClient("c", hub, nil, zap.NewNop())
	hub.register(c)
	return &Bridge{hub: hub, batcher: NewBatcher(hub, time.Hour), logger: zap.NewNop()}, c
}

func TestBridge_EntityEventsFromBothShapes(t *testing.T) {
	b, c := newTestBridge(t)
	// NestJS-era position event.
	b.Handle(kafka.TopicEntityPosition, []byte(`{"entity_id":"e1","entity_type":"AIRCRAFT","latitude":1.5,"longitude":2.5,"altitude_meters":9000,"heading":90,"speed_knots":400,"classification":"UNCLASSIFIED","source":"ADS_B","timestamp":"2026-10-14T00:00:00Z"}`))
	// Go entity-service created event (id + altitude).
	b.Handle(kafka.TopicEntityCreated, []byte(`{"id":"e2","entity_type":"VESSEL","name":"Boat","latitude":3,"longitude":4,"altitude":0,"created_at":"2026-10-14T01:00:00Z"}`))
	// Missing position is dropped.
	b.Handle(kafka.TopicEntityUpdated, []byte(`{"entity_id":"e3"}`))
	b.batcher.Flush()

	env := decodeFrame(t, c)
	var events []EntityEvent
	_ = json.Unmarshal(env.Data, &events)
	if len(events) != 2 {
		t.Fatalf("events = %+v", events)
	}
	e1, e2 := events[0], events[1]
	if e1.Type != "updated" || e1.Entity.ID != "e1" || e1.Entity.Position.Latitude != 1.5 || *e1.Entity.Position.Altitude != 9000 || *e1.Entity.SpeedKnots != 400 || e1.Timestamp != "2026-10-14T00:00:00Z" {
		t.Errorf("e1 = %+v", e1)
	}
	if e2.Type != "created" || e2.Entity.ID != "e2" || e2.Entity.Name != "Boat" || *e2.Entity.Position.Altitude != 0 || e2.Entity.CreatedAt != "2026-10-14T01:00:00Z" {
		t.Errorf("e2 = %+v", e2)
	}
	if e2.Entity.Metadata == nil || e2.Entity.Affiliations == nil {
		t.Error("metadata and affiliations must serialise as {} and [] not null")
	}
	// Without velocity fields the kinematics object is omitted; lastSeenAt
	// falls back to the event timestamp.
	if e1.Entity.Kinematics != nil || e1.Entity.LastSeenAt != "2026-10-14T00:00:00Z" {
		t.Errorf("e1 kinematics/lastSeenAt = %v / %s", e1.Entity.Kinematics, e1.Entity.LastSeenAt)
	}
}

func TestBridge_VelocityAndLastSeenPassThrough(t *testing.T) {
	b, c := newTestBridge(t)
	b.Handle(kafka.TopicEntityPosition, []byte(`{"entity_id":"e1","entity_type":"AIRCRAFT","latitude":1,"longitude":2,"altitude":9000,"velocity_north":100.5,"velocity_east":-20,"velocity_up":null,"timestamp":"2026-10-14T00:00:00Z","last_seen_at":"2026-10-14T00:00:05Z"}`))
	b.batcher.Flush()

	env := decodeFrame(t, c)
	var events []EntityEvent
	_ = json.Unmarshal(env.Data, &events)
	if len(events) != 1 {
		t.Fatalf("events = %+v", events)
	}
	e := events[0].Entity
	if e.LastSeenAt != "2026-10-14T00:00:05Z" {
		t.Errorf("lastSeenAt = %s", e.LastSeenAt)
	}
	velocity, _ := e.Kinematics["velocity"].(map[string]interface{})
	if velocity == nil || velocity["north"] != 100.5 || velocity["east"] != -20.0 {
		t.Errorf("kinematics = %v", e.Kinematics)
	}
	if _, present := velocity["up"]; present {
		t.Error("a null velocity component must be omitted")
	}
}

func TestBridge_DeletedAgeoutAndAlerts(t *testing.T) {
	b, c := newTestBridge(t)
	b.Handle(kafka.TopicEntityDeleted, []byte(`{"bulk":true,"count":42,"timestamp":"2026-10-14T00:00:00Z"}`))
	env := decodeFrame(t, c)
	var del EntityEvent
	_ = json.Unmarshal(env.Data, &del)
	if env.Type != MsgEntityDeleted || del.Type != "deleted" || !del.Bulk || del.Count != 42 {
		t.Errorf("deleted = %s %+v", env.Type, del)
	}

	// Ageout events are coalesced into one array frame per type per flush:
	// the ageout service emits up to a thousand per cycle.
	b.Handle(kafka.TopicEntityStale, []byte(`{"entity_id":"e9","ageout_state":"STALE"}`))
	b.Handle(kafka.TopicEntityStale, []byte(`{"entity_id":"e10","ageout_state":"STALE"}`))
	b.Handle(kafka.TopicEntityAgedOut, []byte(`{"entity_id":"e11","ageout_state":"AGED_OUT"}`))
	b.Handle(kafka.TopicEntityAgedOut, []byte(`not json`))
	noFrame(t, c)
	b.batcher.Flush()
	env = decodeFrame(t, c)
	if env.Type != MsgEntityStale || string(env.Data) != `[{"entity_id":"e9","ageout_state":"STALE"},{"entity_id":"e10","ageout_state":"STALE"}]` {
		t.Errorf("stale batch = %s %s", env.Type, env.Data)
	}
	env = decodeFrame(t, c)
	if env.Type != MsgEntityAgedOut || string(env.Data) != `[{"entity_id":"e11","ageout_state":"AGED_OUT"}]` {
		t.Errorf("agedout batch = %s %s", env.Type, env.Data)
	}
	noFrame(t, c)
	b.batcher.Flush() // nothing pending: no empty frames
	noFrame(t, c)

	b.Handle(kafka.TopicAlertGeofence, []byte(`{"alertId":"a1","alertType":"GEOFENCE_BREACH","severity":"HIGH","title":"Breach","description":"Entered zone","entityId":"e1","position":{"lat":1,"lon":2},"createdAt":"2026-10-14T00:00:00Z"}`))
	env = decodeFrame(t, c)
	var alert ClientAlert
	_ = json.Unmarshal(env.Data, &alert)
	if env.Type != MsgAlertNew || alert.ID != "a1" || alert.Message != "Entered zone" || alert.Position.Longitude != 2 || alert.Acknowledged || alert.UpdatedAt != alert.CreatedAt {
		t.Errorf("alert = %s %+v", env.Type, alert)
	}
	b.Handle("some.other.topic", []byte(`{}`))
	noFrame(t, c)
}

func TestHandler_WebSocketRoundTrip(t *testing.T) {
	hub := NewHub(zap.NewNop())
	h := NewHandler(hub, "*", zap.NewNop())
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http")+"/ws/entities", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	read := func() Envelope {
		_, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var env Envelope
		_ = json.Unmarshal(data, &env)
		return env
	}
	if env := read(); env.Type != MsgConnectionAck {
		t.Fatalf("first frame = %s", env.Type)
	}
	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"type":"viewport:update","data":{"north":10,"south":0,"east":10,"west":0}}`)); err != nil {
		t.Fatal(err)
	}
	if env := read(); env.Type != MsgViewportUpdated {
		t.Fatalf("expected viewport:updated, got %s", env.Type)
	}
	// Wait for the hub to see the client, then broadcast.
	deadline := time.Now().Add(time.Second)
	for hub.ClientCount() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	hub.BroadcastBatch([]PositionedEvent{posEvent("in", 5, 5), posEvent("out", 50, 50)})
	env := read()
	var events []EntityEvent
	_ = json.Unmarshal(env.Data, &events)
	if env.Type != MsgEntityBatch || len(events) != 1 || events[0].Entity.ID != "in" {
		t.Errorf("batch = %s %+v", env.Type, events)
	}
}

func TestHandler_OriginPatterns(t *testing.T) {
	h := NewHandler(NewHub(zap.NewNop()), "http://localhost:4200, https://sentinel.local", zap.NewNop())
	if h.skipOrigin || len(h.originPatterns) != 2 || h.originPatterns[0] != "localhost:4200" {
		t.Errorf("patterns = %v skip=%v", h.originPatterns, h.skipOrigin)
	}
}
