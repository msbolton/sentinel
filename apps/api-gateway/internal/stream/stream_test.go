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
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	sentinelv1 "github.com/sentinel/proto-gen/sentinel/v1"

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

func protoBytes(t *testing.T, m proto.Message) []byte {
	t.Helper()
	b, err := proto.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func sampleEntity() *sentinelv1.Entity {
	alt, spd := 9000.0, 400.0
	return &sentinelv1.Entity{
		Id: "e1", EntityType: sentinelv1.EntityType_ENTITY_TYPE_AIRCRAFT, Name: "UAL1", Source: sentinelv1.EntitySource_ENTITY_SOURCE_ADSB_LOL,
		Classification: sentinelv1.Classification_CLASSIFICATION_UNCLASSIFIED, FeedId: "adsb-lol", AgeoutState: sentinelv1.AgeoutState_AGEOUT_STATE_LIVE,
		Position: &sentinelv1.Coordinate{Latitude: 1.5, Longitude: 2.5, AltitudeMeters: &alt}, SpeedKnots: &spd,
		Kinematics: &sentinelv1.KinematicState{Velocity: &sentinelv1.GeodeticVelocity{North: 100.5, East: -20}},
		Identities: []*sentinelv1.Identity{{Type: sentinelv1.IdentityType_IDENTITY_TYPE_ICAO24, Value: "A1B2C3"}},
		Platforms:  map[string]*sentinelv1.PlatformDetail{"adsb": {Detail: &sentinelv1.PlatformDetail_Adsb{Adsb: &sentinelv1.AdsbDetail{IcaoHex: "A1B2C3", Squawk: "1200"}}}},
		CreatedAt:  timestamppb.New(time.Date(2026, 10, 14, 1, 0, 0, 0, time.UTC)), LastSeenAt: timestamppb.New(time.Date(2026, 10, 14, 0, 0, 5, 0, time.UTC)),
		LastObservedAt: timestamppb.New(time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC)),
	}
}

func TestBridge_EntityEventsFromProto(t *testing.T) {
	b, c := newTestBridge(t)
	b.Handle(kafka.TopicEntityEvents, protoBytes(t, &sentinelv1.EntityEvent{EntityId: "e1", EventTime: timestamppb.Now(),
		Event: &sentinelv1.EntityEvent_Position{Position: &sentinelv1.EntityPosition{Entity: sampleEntity()}}}))
	boat := &sentinelv1.Entity{Id: "e2", EntityType: sentinelv1.EntityType_ENTITY_TYPE_VESSEL, Name: "Boat", Position: &sentinelv1.Coordinate{Latitude: 3, Longitude: 4},
		CreatedAt: timestamppb.New(time.Date(2026, 10, 14, 1, 0, 0, 0, time.UTC))}
	b.Handle(kafka.TopicEntityEvents, protoBytes(t, &sentinelv1.EntityEvent{EntityId: "e2", Event: &sentinelv1.EntityEvent_Created{Created: &sentinelv1.EntityCreated{Entity: boat}}}))
	// Missing position is dropped.
	b.Handle(kafka.TopicEntityEvents, protoBytes(t, &sentinelv1.EntityEvent{EntityId: "e3", Event: &sentinelv1.EntityEvent_Updated{Updated: &sentinelv1.EntityUpdated{Entity: &sentinelv1.Entity{Id: "e3"}}}}))
	b.batcher.Flush()

	env := decodeFrame(t, c)
	var events []EntityEvent
	_ = json.Unmarshal(env.Data, &events)
	if len(events) != 2 {
		t.Fatalf("events = %+v", events)
	}
	e1, e2 := events[0], events[1]
	if e1.Type != "updated" || e1.Entity.ID != "e1" || e1.Entity.EntityType != "AIRCRAFT" || e1.Entity.Source != "ADSB_LOL" || e1.Entity.Classification != "UNCLASSIFIED" || e1.Entity.Position.Latitude != 1.5 || *e1.Entity.Position.Altitude != 9000 || *e1.Entity.SpeedKnots != 400 || e1.Entity.Heading != nil {
		t.Errorf("e1 = %+v", e1)
	}
	if e1.Timestamp != "2026-10-14T00:00:00Z" || e1.Entity.LastSeenAt != "2026-10-14T00:00:05Z" || e1.Entity.CreatedAt != "2026-10-14T01:00:00Z" {
		t.Errorf("e1 timestamps = %s %s %s", e1.Timestamp, e1.Entity.LastSeenAt, e1.Entity.CreatedAt)
	}
	velocity, _ := e1.Entity.Kinematics["velocity"].(map[string]interface{})
	if velocity == nil || velocity["north"] != 100.5 || velocity["east"] != -20.0 {
		t.Errorf("kinematics = %v", e1.Entity.Kinematics)
	}
	adsb, _ := e1.Entity.PlatformData["adsb"].(map[string]interface{})
	if adsb == nil || adsb["icaoHex"] != "A1B2C3" || adsb["squawk"] != "1200" {
		t.Errorf("platformData = %v", e1.Entity.PlatformData)
	}
	if e1.Entity.FeedID != "adsb-lol" || e1.Entity.AgeoutState != "LIVE" || len(e1.Entity.Identities) != 1 || e1.Entity.Identities[0].Value != "A1B2C3" {
		t.Errorf("provenance = %+v", e1.Entity)
	}
	if e2.Type != "created" || e2.Entity.Name != "Boat" || e2.Entity.Position.Altitude != nil || e2.Entity.CreatedAt != "2026-10-14T01:00:00Z" || e2.Entity.Kinematics != nil {
		t.Errorf("e2 = %+v", e2)
	}
	if e2.Entity.Metadata == nil || e2.Entity.Affiliations == nil {
		t.Error("metadata and affiliations must serialise as {} and [] not null")
	}
}

func TestBridge_EntityCarriesExerciseMark(t *testing.T) {
	b, c := newTestBridge(t)
	exercise := sampleEntity()
	exercise.Exercise = true
	live := sampleEntity()
	live.Id = "e2"
	for _, en := range []*sentinelv1.Entity{exercise, live} {
		b.Handle(kafka.TopicEntityEvents, protoBytes(t, &sentinelv1.EntityEvent{EntityId: en.Id, EventTime: timestamppb.Now(),
			Event: &sentinelv1.EntityEvent_Position{Position: &sentinelv1.EntityPosition{Entity: en}}}))
	}
	b.batcher.Flush()

	env := decodeFrame(t, c)
	var events []EntityEvent
	if err := json.Unmarshal(env.Data, &events); err != nil || len(events) != 2 {
		t.Fatalf("events = %+v err %v", events, err)
	}
	if !events[0].Entity.Exercise {
		t.Error("an exercise entity must carry exercise true")
	}
	if events[1].Entity.Exercise {
		t.Error("a live entity must not read as exercise")
	}
	var raw []struct {
		Entity map[string]json.RawMessage `json:"entity"`
	}
	if err := json.Unmarshal(env.Data, &raw); err != nil {
		t.Fatal(err)
	}
	if string(raw[0].Entity["exercise"]) != "true" {
		t.Errorf("exercise entity JSON = %s", raw[0].Entity["exercise"])
	}
	if _, present := raw[1].Entity["exercise"]; present {
		t.Error("a live entity's JSON must have no exercise key")
	}
}

func TestBridge_DeletedAgeoutRestoredAndAlerts(t *testing.T) {
	b, c := newTestBridge(t)
	b.Handle(kafka.TopicEntityEvents, protoBytes(t, &sentinelv1.EntityEvent{EventTime: timestamppb.New(time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC)),
		Event: &sentinelv1.EntityEvent_Deleted{Deleted: &sentinelv1.EntityDeleted{Bulk: true, Count: 42}}}))
	env := decodeFrame(t, c)
	var del EntityEvent
	_ = json.Unmarshal(env.Data, &del)
	if env.Type != MsgEntityDeleted || del.Type != "deleted" || !del.Bulk || del.Count != 42 || del.Timestamp != "2026-10-14T00:00:00Z" {
		t.Errorf("deleted = %s %+v", env.Type, del)
	}
	b.Handle(kafka.TopicEntityEvents, protoBytes(t, &sentinelv1.EntityEvent{EntityId: "e7", Event: &sentinelv1.EntityEvent_Deleted{Deleted: &sentinelv1.EntityDeleted{}}}))
	env = decodeFrame(t, c)
	var single EntityEvent
	_ = json.Unmarshal(env.Data, &single)
	if single.Entity.ID != "e7" || single.Bulk {
		t.Errorf("single delete = %+v", single)
	}

	// Ageout events are coalesced into one array frame per type per flush.
	stale := func(id string) []byte {
		return protoBytes(t, &sentinelv1.EntityEvent{EntityId: id, Event: &sentinelv1.EntityEvent_Stale{Stale: &sentinelv1.EntityAgeout{EntityType: sentinelv1.EntityType_ENTITY_TYPE_AIRCRAFT, FeedId: "opensky", ThresholdMs: 60000}}})
	}
	b.Handle(kafka.TopicEntityEvents, stale("e9"))
	b.Handle(kafka.TopicEntityEvents, stale("e10"))
	b.Handle(kafka.TopicEntityEvents, protoBytes(t, &sentinelv1.EntityEvent{EntityId: "e11", Event: &sentinelv1.EntityEvent_AgedOut{AgedOut: &sentinelv1.EntityAgeout{}}}))
	b.Handle(kafka.TopicEntityEvents, []byte("not proto at all \xff\xff"))
	noFrame(t, c)
	b.batcher.Flush()
	env = decodeFrame(t, c)
	var staleBatch []map[string]interface{}
	_ = json.Unmarshal(env.Data, &staleBatch)
	if env.Type != MsgEntityStale || len(staleBatch) != 2 || staleBatch[0]["entity_id"] != "e9" || staleBatch[0]["ageout_state"] != "STALE" || staleBatch[0]["feed_id"] != "opensky" || staleBatch[0]["entity_type"] != "AIRCRAFT" {
		t.Errorf("stale batch = %s %s", env.Type, env.Data)
	}
	env = decodeFrame(t, c)
	var aged []map[string]interface{}
	_ = json.Unmarshal(env.Data, &aged)
	if env.Type != MsgEntityAgedOut || len(aged) != 1 || aged[0]["entity_id"] != "e11" || aged[0]["ageout_state"] != "AGED_OUT" {
		t.Errorf("agedout batch = %s %s", env.Type, env.Data)
	}
	noFrame(t, c)

	// Restored: an ageout frame plus the entity itself in the batch.
	b.Handle(kafka.TopicEntityEvents, protoBytes(t, &sentinelv1.EntityEvent{EntityId: "e1", Event: &sentinelv1.EntityEvent_Restored{Restored: &sentinelv1.EntityRestored{Entity: sampleEntity(), PreviousState: sentinelv1.AgeoutState_AGEOUT_STATE_STALE}}}))
	b.batcher.Flush()
	frames := map[string]json.RawMessage{}
	for i := 0; i < 2; i++ {
		env = decodeFrame(t, c)
		frames[env.Type] = env.Data
	}
	var restored []map[string]interface{}
	_ = json.Unmarshal(frames[MsgEntityRestored], &restored)
	if len(restored) != 1 || restored[0]["entity_id"] != "e1" || restored[0]["previous_state"] != "STALE" || restored[0]["latitude"] != 1.5 {
		t.Errorf("restored = %s", frames[MsgEntityRestored])
	}
	var batch []EntityEvent
	_ = json.Unmarshal(frames[MsgEntityBatch], &batch)
	if len(batch) != 1 || batch[0].Entity.ID != "e1" {
		t.Errorf("restored entity batch = %s", frames[MsgEntityBatch])
	}

	b.Handle(kafka.TopicAlertEvents, protoBytes(t, &sentinelv1.AlertEvent{Producer: "analytics-service", AnomalyType: "speed", Alert: &sentinelv1.Alert{
		AlertId: "a1", AlertType: sentinelv1.AlertType_ALERT_TYPE_GEOFENCE_ENTRY, Severity: sentinelv1.AlertSeverity_ALERT_SEVERITY_HIGH, Title: "Breach", Description: "Entered zone",
		EntityId: "e1", Position: &sentinelv1.Coordinate{Latitude: 1, Longitude: 2}, CreatedAt: timestamppb.New(time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC))}}))
	env = decodeFrame(t, c)
	var alert ClientAlert
	_ = json.Unmarshal(env.Data, &alert)
	if env.Type != MsgAlertNew || alert.ID != "a1" || alert.AlertType != "GEOFENCE_ENTRY" || alert.Severity != "HIGH" || alert.Message != "Entered zone" || alert.Position.Longitude != 2 || alert.Acknowledged || alert.UpdatedAt != alert.CreatedAt || alert.Metadata["anomalyType"] != "speed" {
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

func TestBridge_DensityGridsReachClearedClients(t *testing.T) {
	b, viewer := newTestBridge(t)
	analyst := newClient("analyst", b.hub, nil, zap.NewNop())
	analyst.clearance = 2 // SECRET
	b.hub.register(analyst)

	grid := &sentinelv1.DensityGrid{
		FeedId: "aisstream", Source: sentinelv1.EntitySource_ENTITY_SOURCE_AIS, EntityType: sentinelv1.EntityType_ENTITY_TYPE_VESSEL,
		CellDegrees: 1, WindowSeconds: 600, CapSquareDegrees: 2500, Total: 3,
		GeneratedAt:    timestamppb.New(time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)),
		Classification: sentinelv1.Classification_CLASSIFICATION_UNCLASSIFIED,
		Cells:          []*sentinelv1.DensityCell{{Latitude: 50.5, Longitude: -1.5, Count: 2}, {Latitude: 51.5, Longitude: 1.5, Count: 1}},
	}
	b.Handle(kafka.TopicDensityEvents, protoBytes(t, grid))
	for _, c := range []*Client{viewer, analyst} {
		env := decodeFrame(t, c)
		var d ClientDensity
		_ = json.Unmarshal(env.Data, &d)
		if env.Type != MsgDensity || d.FeedID != "aisstream" || d.Source != "AIS" || d.EntityType != "VESSEL" || d.CapSquareDegrees != 2500 || d.Total != 3 || d.GeneratedAt != "2026-10-18T12:00:00Z" {
			t.Errorf("%s: density = %s %+v", c.ID, env.Type, d)
		}
		if len(d.Cells) != 2 || d.Cells[0] != [3]float64{50.5, -1.5, 2} {
			t.Errorf("%s: cells = %v", c.ID, d.Cells)
		}
	}

	// A grid from a SECRET feed reaches only cleared clients.
	grid.Classification = sentinelv1.Classification_CLASSIFICATION_SECRET
	b.Handle(kafka.TopicDensityEvents, protoBytes(t, grid))
	noFrame(t, viewer)
	if env := decodeFrame(t, analyst); env.Type != MsgDensity {
		t.Errorf("analyst frame = %+v", env)
	}

	// Garbage and grids without a feed are dropped.
	b.Handle(kafka.TopicDensityEvents, []byte("nope"))
	b.Handle(kafka.TopicDensityEvents, protoBytes(t, &sentinelv1.DensityGrid{Total: 1}))
	noFrame(t, viewer)
	noFrame(t, analyst)
}
