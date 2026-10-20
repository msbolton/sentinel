package fusion

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	sentinelv1 "github.com/sentinel/proto-gen/sentinel/v1"

	"github.com/sentinel/entity-service/internal/store"
)

// memEntities applies newest-wins in memory the way ApplyFused does.
type memEntities struct {
	lastObserved map[string]time.Time
	state        map[string]string
	fused        [][]store.FusedUpdate
	static       [][]store.StaticUpdate
	class        map[string]string
	exercise     map[string]bool
	marked       []string // ids MarkExercise changed
}

func newMemEntities() *memEntities {
	return &memEntities{lastObserved: map[string]time.Time{}, state: map[string]string{}, class: map[string]string{}, exercise: map[string]bool{}}
}

// raise mirrors sentinel.classification_max: the entity keeps the higher of
// its current level and the report's, and the previous level is reported.
func (m *memEntities) raise(id, level string) (prev, now string) {
	rank := map[string]int{"": 0, "UNCLASSIFIED": 0, "CONFIDENTIAL": 1, "SECRET": 2, "TOP_SECRET": 3}
	prev = m.class[id]
	if prev == "" {
		prev = "UNCLASSIFIED"
	}
	now = prev
	if rank[level] > rank[prev] {
		now = level
	}
	m.class[id] = now
	return prev, now
}

func (m *memEntities) ApplyFused(_ context.Context, updates []store.FusedUpdate) ([]store.FusedResult, error) {
	m.fused = append(m.fused, updates)
	var out []store.FusedResult
	for _, u := range updates {
		if last, ok := m.lastObserved[u.EntityID]; ok && u.ObservedAt.Before(last) {
			continue
		}
		prev := m.state[u.EntityID]
		m.lastObserved[u.EntityID] = u.ObservedAt
		m.state[u.EntityID] = "LIVE"
		feed := u.FeedID
		prevClass, class := m.raise(u.EntityID, u.Classification)
		m.exercise[u.EntityID] = m.exercise[u.EntityID] || u.Exercise
		out = append(out, store.FusedResult{EntityID: u.EntityID, PreviousState: prev, PreviousClassification: prevClass, Record: &store.EntityRecord{ID: u.EntityID, EntityType: "AIRCRAFT", Name: u.Name, Source: u.Source, FeedID: &feed, Classification: class, Exercise: m.exercise[u.EntityID], Position: &store.GeoPoint{Lat: u.Lat, Lon: u.Lon}, AgeoutState: "LIVE", CreatedAt: "2026-10-15 12:00:00+00", UpdatedAt: "2026-10-15 12:00:00+00"}})
	}
	return out, nil
}

func (m *memEntities) ApplyStatic(_ context.Context, updates []store.StaticUpdate) ([]store.FusedResult, error) {
	m.static = append(m.static, updates)
	var out []store.FusedResult
	for _, u := range updates {
		prevClass, class := m.raise(u.EntityID, u.Classification)
		m.exercise[u.EntityID] = m.exercise[u.EntityID] || u.Exercise
		out = append(out, store.FusedResult{EntityID: u.EntityID, PreviousState: m.state[u.EntityID], PreviousClassification: prevClass, Record: &store.EntityRecord{ID: u.EntityID, Name: u.Name, Classification: class, Exercise: m.exercise[u.EntityID], AgeoutState: "LIVE", CreatedAt: "2026-10-15 12:00:00+00", UpdatedAt: "2026-10-15 12:00:00+00"}})
	}
	return out, nil
}

// MarkExercise mirrors the store: only rows that lacked the mark are
// returned.
func (m *memEntities) MarkExercise(_ context.Context, ids []string) ([]store.EntityRecord, error) {
	var out []store.EntityRecord
	for _, id := range ids {
		if m.exercise[id] {
			continue
		}
		m.exercise[id] = true
		m.marked = append(m.marked, id)
		out = append(out, store.EntityRecord{ID: id, EntityType: "AIRCRAFT", Source: "OPENSKY", Classification: m.class[id], AgeoutState: "LIVE", Exercise: true, CreatedAt: "2026-10-15 12:00:00+00", UpdatedAt: "2026-10-15 12:00:00+00"})
	}
	return out, nil
}

type memObservations struct{ rows []store.ObservationRow }

func (m *memObservations) InsertObservations(_ context.Context, rows []store.ObservationRow) error {
	m.rows = append(m.rows, rows...)
	return nil
}

type memSources struct{ rows []store.SourceRow }

func (m *memSources) UpsertSources(_ context.Context, rows []store.SourceRow) error {
	m.rows = append(m.rows, rows...)
	return nil
}

type event struct {
	kind   string
	entity *sentinelv1.Entity
	extra  string
}

type memSink struct{ events []event }

func (m *memSink) Created(e *sentinelv1.Entity, _ *sentinelv1.ObservationRef) {
	m.events = append(m.events, event{"created", e, ""})
}
func (m *memSink) Position(e *sentinelv1.Entity, _ *sentinelv1.ObservationRef) {
	m.events = append(m.events, event{"position", e, ""})
}
func (m *memSink) Restored(e *sentinelv1.Entity, prev string) {
	m.events = append(m.events, event{"restored", e, prev})
}
func (m *memSink) Updated(e *sentinelv1.Entity, changed []string) {
	m.events = append(m.events, event{"updated", e, changed[0]})
}

func newTestProcessor() (*Processor, *memIdentities, *memEntities, *memObservations, *memSources, *memSink) {
	ids := newMemIdentities()
	ents := newMemEntities()
	obsw := &memObservations{}
	srcs := &memSources{}
	sink := &memSink{}
	p := NewProcessor(NewResolver(ids, 100, time.Hour), ents, obsw, srcs, sink, nil, nil)
	return p, ids, ents, obsw, srcs, sink
}

func withID(o *sentinelv1.Observation, id string) *sentinelv1.Observation {
	o.ObservationId = id
	o.DisplayName = "UAL1"
	o.Platform = &sentinelv1.PlatformDetail{Detail: &sentinelv1.PlatformDetail_Adsb{Adsb: &sentinelv1.AdsbDetail{IcaoHex: "A1B2C3"}}}
	return o
}

func TestProcessBatchFusesTwoFeedsIntoOneEntity(t *testing.T) {
	p, _, ents, obsw, srcs, sink := newTestProcessor()
	batch := []*sentinelv1.Observation{
		withID(obs("opensky", sentinelv1.EntitySource_ENTITY_SOURCE_OPENSKY, "A1B2C3", t0, 41.9, -87.9), "o1"),
		withID(obs("adsb-lol", sentinelv1.EntitySource_ENTITY_SOURCE_ADSB_LOL, "A1B2C3", t0.Add(-5*time.Second), 41.8, -87.8), "o2"), // older
		withID(obs("ais", sentinelv1.EntitySource_ENTITY_SOURCE_AIS, "FFFFFF", t0, 1, 1), "o3"),
	}
	stats, err := p.ProcessBatch(context.Background(), batch)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Created != 2 || stats.Accepted != 2 || stats.Rejected[RejectDuplicate] != 1 {
		t.Fatalf("stats = %+v", stats)
	}
	if len(ents.fused) != 1 || len(ents.fused[0]) != 2 {
		t.Fatalf("fused updates = %v", ents.fused)
	}
	if len(obsw.rows) != 3 {
		t.Fatalf("observation rows = %d", len(obsw.rows))
	}
	byID := map[string]store.ObservationRow{}
	for _, r := range obsw.rows {
		byID[r.ObservationID] = r
	}
	if !byID["o1"].Accepted || byID["o2"].Accepted || byID["o2"].RejectReason != RejectDuplicate || byID["o1"].EntityID != byID["o2"].EntityID {
		t.Errorf("rows = %+v", byID)
	}
	if string(byID["o1"].Platform) == "" || string(byID["o1"].Identities) == "[]" {
		t.Errorf("platform/identities not stored: %+v", byID["o1"])
	}
	if len(srcs.rows) != 3 {
		t.Errorf("source rows = %d", len(srcs.rows))
	}
	if len(sink.events) != 2 || sink.events[0].kind != "created" || sink.events[1].kind != "created" {
		t.Fatalf("events = %+v", sink.events)
	}
	if e := sink.events[0].entity; e.Name != "UAL1" || e.Position == nil || len(e.Identities) < 1 || len(e.Sources) != 1 || e.Sources[0].FeedId != "opensky" {
		t.Errorf("created snapshot = %+v", e)
	}
}

func TestProcessBatchStaleFixRestoreAndStatic(t *testing.T) {
	p, _, ents, obsw, _, sink := newTestProcessor()
	first := withID(obs("opensky", sentinelv1.EntitySource_ENTITY_SOURCE_OPENSKY, "A1B2C3", t0, 41.9, -87.9), "o1")
	if _, err := p.ProcessBatch(context.Background(), []*sentinelv1.Observation{first}); err != nil {
		t.Fatal(err)
	}
	entityID := obsw.rows[0].EntityID
	ents.state[entityID] = "STALE" // the ageout service marked it meanwhile

	late := withID(obs("adsb-lol", sentinelv1.EntitySource_ENTITY_SOURCE_ADSB_LOL, "A1B2C3", t0.Add(-time.Minute), 0.1, 0.1), "o2")
	newer := withID(obs("opensky", sentinelv1.EntitySource_ENTITY_SOURCE_OPENSKY, "A1B2C3", t0.Add(10*time.Second), 42.0, -88.0), "o3")
	static := withID(obs("ais", sentinelv1.EntitySource_ENTITY_SOURCE_AIS, "A1B2C3", t0.Add(20*time.Second), 0, 0), "o4")
	stats, err := p.ProcessBatch(context.Background(), []*sentinelv1.Observation{late})
	if err != nil {
		t.Fatal(err)
	}
	if stats.Accepted != 0 || stats.Rejected[RejectStaleFix] != 1 || len(sink.events) != 1 {
		t.Fatalf("late fix stats = %+v events=%d", stats, len(sink.events))
	}

	stats, err = p.ProcessBatch(context.Background(), []*sentinelv1.Observation{newer, static})
	if err != nil {
		t.Fatal(err)
	}
	if stats.Accepted != 1 || stats.Restored != 1 || stats.Rejected[RejectNoPosition] != 1 {
		t.Fatalf("stats = %+v", stats)
	}
	kinds := []string{}
	for _, e := range sink.events[1:] {
		kinds = append(kinds, e.kind)
	}
	if len(kinds) != 2 || kinds[0] != "restored" || kinds[1] != "position" || sink.events[1].extra != "STALE" {
		t.Fatalf("events after stale = %v (%+v)", kinds, sink.events[1:])
	}
	if len(ents.static) != 1 || ents.static[0][0].EntityID != entityID {
		t.Errorf("static update not applied to the same entity: %v", ents.static)
	}

	// A static-only batch on a known entity is an attribute update.
	stats, _ = p.ProcessBatch(context.Background(), []*sentinelv1.Observation{withID(obs("ais", sentinelv1.EntitySource_ENTITY_SOURCE_AIS, "A1B2C3", t0.Add(30*time.Second), 0, 0), "o5")})
	if stats.Static != 1 || sink.events[len(sink.events)-1].kind != "updated" {
		t.Errorf("static-only batch: stats=%+v last=%+v", stats, sink.events[len(sink.events)-1])
	}
}

func TestProcessBatchEmptyAndNil(t *testing.T) {
	p, _, _, _, _, _ := newTestProcessor()
	if stats, err := p.ProcessBatch(context.Background(), nil); err != nil || stats.Reports != 0 {
		t.Fatalf("empty batch: %+v %v", stats, err)
	}
	if _, err := p.ProcessBatch(context.Background(), []*sentinelv1.Observation{nil}); err != nil {
		t.Fatal(err)
	}
}

var _ = timestamppb.Now

func TestClassificationRaiseIsAnnounced(t *testing.T) {
	p, _, ents, _, _, sink := newTestProcessor()
	t0 := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	first := withID(obs("opensky", sentinelv1.EntitySource_ENTITY_SOURCE_OPENSKY, "A1B2C3", t0, 40, -80), "o1")
	first.Classification = sentinelv1.Classification_CLASSIFICATION_CONFIDENTIAL
	if _, err := p.ProcessBatch(context.Background(), []*sentinelv1.Observation{first}); err != nil {
		t.Fatal(err)
	}
	if len(sink.events) != 1 || sink.events[0].kind != "created" || sink.events[0].entity.Classification != sentinelv1.Classification_CLASSIFICATION_CONFIDENTIAL {
		t.Fatalf("first batch events = %+v", sink.events)
	}
	id := sink.events[0].entity.Id

	// A lower-level report does not lower the entity and announces nothing extra.
	second := withID(obs("adsb-lol", sentinelv1.EntitySource_ENTITY_SOURCE_ADSB_LOL, "A1B2C3", t0.Add(time.Second), 40.1, -80), "o2")
	second.Classification = sentinelv1.Classification_CLASSIFICATION_UNCLASSIFIED
	if _, err := p.ProcessBatch(context.Background(), []*sentinelv1.Observation{second}); err != nil {
		t.Fatal(err)
	}
	if len(sink.events) != 2 || sink.events[1].kind != "position" || ents.class[id] != "CONFIDENTIAL" {
		t.Fatalf("after lower report: events=%+v class=%q", sink.events, ents.class[id])
	}

	// A higher-level report raises the entity and says so before the fix.
	third := withID(obs("opensky", sentinelv1.EntitySource_ENTITY_SOURCE_OPENSKY, "A1B2C3", t0.Add(2*time.Second), 40.2, -80), "o3")
	third.Classification = sentinelv1.Classification_CLASSIFICATION_SECRET
	if _, err := p.ProcessBatch(context.Background(), []*sentinelv1.Observation{third}); err != nil {
		t.Fatal(err)
	}
	if len(sink.events) != 4 || sink.events[2].kind != "updated" || sink.events[2].extra != "classification" || sink.events[3].kind != "position" {
		t.Fatalf("after raise: events=%+v", sink.events)
	}
	if sink.events[3].entity.Classification != sentinelv1.Classification_CLASSIFICATION_SECRET {
		t.Errorf("position snapshot classification = %v", sink.events[3].entity.Classification)
	}
}

// An exercise report marks the entity, and the mark is never lowered by a
// later live report.
func TestExerciseObservationMarksEntity(t *testing.T) {
	p, _, _, _, _, sink := newTestProcessor()
	t0 := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	live := withID(obs("opensky", sentinelv1.EntitySource_ENTITY_SOURCE_OPENSKY, "A1B2C3", t0, 40, -80), "o1")
	if _, err := p.ProcessBatch(context.Background(), []*sentinelv1.Observation{live}); err != nil {
		t.Fatal(err)
	}
	if len(sink.events) != 1 || sink.events[0].entity.Exercise {
		t.Fatalf("live first report = %+v", sink.events)
	}

	ex := withID(obs("sim", sentinelv1.EntitySource_ENTITY_SOURCE_OPENSKY, "A1B2C3", t0.Add(time.Second), 40.1, -80), "o2")
	ex.Exercise = true
	if _, err := p.ProcessBatch(context.Background(), []*sentinelv1.Observation{ex}); err != nil {
		t.Fatal(err)
	}
	last := sink.events[len(sink.events)-1]
	if last.kind != "position" || !last.entity.Exercise {
		t.Fatalf("exercise report event = %+v", last)
	}

	later := withID(obs("opensky", sentinelv1.EntitySource_ENTITY_SOURCE_OPENSKY, "A1B2C3", t0.Add(2*time.Second), 40.2, -80), "o3")
	if _, err := p.ProcessBatch(context.Background(), []*sentinelv1.Observation{later}); err != nil {
		t.Fatal(err)
	}
	last = sink.events[len(sink.events)-1]
	if last.kind != "position" || !last.entity.Exercise {
		t.Fatalf("live report after exercise = %+v", last)
	}
}

// An exercise report that loses to a live report in its own batch (it is the
// older duplicate) still marks the entity, and the mark is announced.
func TestExerciseDuplicateMarksEntity(t *testing.T) {
	p, _, ents, obsw, _, sink := newTestProcessor()
	t1 := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	live := withID(obs("opensky", sentinelv1.EntitySource_ENTITY_SOURCE_OPENSKY, "A1B2C3", t1, 40, -80), "o1")
	ex := withID(obs("sim", sentinelv1.EntitySource_ENTITY_SOURCE_OPENSKY, "A1B2C3", t1.Add(-5*time.Second), 40.1, -80), "o2")
	ex.Exercise = true
	stats, err := p.ProcessBatch(context.Background(), []*sentinelv1.Observation{live, ex})
	if err != nil {
		t.Fatal(err)
	}
	if stats.Rejected[RejectDuplicate] != 1 {
		t.Fatalf("stats = %+v", stats)
	}
	id := obsw.rows[0].EntityID
	if !ents.exercise[id] || len(ents.marked) != 1 {
		t.Fatalf("exercise = %v marked = %v", ents.exercise, ents.marked)
	}
	var updated, positionExercise bool
	for _, e := range sink.events {
		if e.kind == "updated" && e.extra == "exercise" && e.entity.Exercise && e.entity.Id == id {
			updated = true
		}
		if e.kind == "created" && e.entity.Exercise {
			positionExercise = true
		}
	}
	if !updated || !positionExercise {
		t.Fatalf("updated=%v createdCarriesMark=%v events=%+v", updated, positionExercise, sink.events)
	}
}

// An exercise report older than the entity's last fix loses the fix but
// still marks the entity.
func TestExerciseStaleFixMarksEntity(t *testing.T) {
	p, _, ents, obsw, _, sink := newTestProcessor()
	t1 := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	live := withID(obs("opensky", sentinelv1.EntitySource_ENTITY_SOURCE_OPENSKY, "A1B2C3", t1, 40, -80), "o1")
	if _, err := p.ProcessBatch(context.Background(), []*sentinelv1.Observation{live}); err != nil {
		t.Fatal(err)
	}
	id := obsw.rows[0].EntityID
	before := len(sink.events)

	ex := withID(obs("sim", sentinelv1.EntitySource_ENTITY_SOURCE_OPENSKY, "A1B2C3", t1.Add(-time.Minute), 40.1, -80), "o2")
	ex.Exercise = true
	stats, err := p.ProcessBatch(context.Background(), []*sentinelv1.Observation{ex})
	if err != nil {
		t.Fatal(err)
	}
	if stats.Rejected[RejectStaleFix] != 1 || !ents.exercise[id] {
		t.Fatalf("stats = %+v exercise = %v", stats, ents.exercise)
	}
	if got := sink.events[before:]; len(got) != 1 || got[0].kind != "updated" || got[0].extra != "exercise" || !got[0].entity.Exercise {
		t.Fatalf("events after stale exercise report = %+v", got)
	}

	// Already marked: another exercise report announces nothing extra.
	again := withID(obs("sim", sentinelv1.EntitySource_ENTITY_SOURCE_OPENSKY, "A1B2C3", t1.Add(-time.Second), 40.1, -80), "o3")
	again.Exercise = true
	n := len(sink.events)
	if _, err := p.ProcessBatch(context.Background(), []*sentinelv1.Observation{again}); err != nil {
		t.Fatal(err)
	}
	if len(sink.events) != n || len(ents.marked) != 1 {
		t.Fatalf("second stale exercise report re-announced: %+v marked=%v", sink.events[n:], ents.marked)
	}
}

// A no-position exercise report marks the row after the batch's fused
// winner was applied; the winner's Position event still carries the mark.
func TestExerciseStaticReportMarksPositionEvent(t *testing.T) {
	p, _, ents, obsw, _, sink := newTestProcessor()
	t1 := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	live := withID(obs("opensky", sentinelv1.EntitySource_ENTITY_SOURCE_OPENSKY, "A1B2C3", t1, 40, -80), "o1")
	static := withID(obs("sim", sentinelv1.EntitySource_ENTITY_SOURCE_OPENSKY, "A1B2C3", t1.Add(time.Second), 0, 0), "o2")
	static.Exercise = true
	stats, err := p.ProcessBatch(context.Background(), []*sentinelv1.Observation{live, static})
	if err != nil {
		t.Fatal(err)
	}
	if stats.Rejected[RejectNoPosition] != 1 || len(ents.static) != 1 {
		t.Fatalf("stats = %+v static = %v", stats, ents.static)
	}
	id := obsw.rows[0].EntityID
	if !ents.exercise[id] {
		t.Fatalf("entity not marked: %v", ents.exercise)
	}
	// The entity is new, so the fix is announced as its created snapshot.
	if len(sink.events) != 1 || sink.events[0].kind != "created" || !sink.events[0].entity.Exercise {
		t.Fatalf("fix event lacks the mark: %+v", sink.events)
	}

	// An existing entity gets a position event; same mark, new batch.
	p2, _, _, _, _, sink2 := newTestProcessor()
	if _, err := p2.ProcessBatch(context.Background(), []*sentinelv1.Observation{withID(obs("opensky", sentinelv1.EntitySource_ENTITY_SOURCE_OPENSKY, "A1B2C3", t1, 40, -80), "o3")}); err != nil {
		t.Fatal(err)
	}
	live2 := withID(obs("opensky", sentinelv1.EntitySource_ENTITY_SOURCE_OPENSKY, "A1B2C3", t1.Add(time.Minute), 40.1, -80), "o4")
	static2 := withID(obs("sim", sentinelv1.EntitySource_ENTITY_SOURCE_OPENSKY, "A1B2C3", t1.Add(time.Minute), 0, 0), "o5")
	static2.Exercise = true
	if _, err := p2.ProcessBatch(context.Background(), []*sentinelv1.Observation{live2, static2}); err != nil {
		t.Fatal(err)
	}
	pos := sink2.events[len(sink2.events)-1]
	if pos.kind != "position" || !pos.entity.Exercise {
		t.Fatalf("position event lacks the mark: %+v", sink2.events)
	}
}

func TestKinematicsJSONCarriesCovarianceAndClearsOnFixWithoutCovariance(t *testing.T) {
	// Observation with position covariance serialises positionCovariance.
	withCov := &sentinelv1.Observation{
		Position:           &sentinelv1.Coordinate{Latitude: 50, Longitude: -4},
		PositionCovariance: &sentinelv1.PositionCovariance{PnPn: 400, PnPe: 50, PePe: 100},
	}
	raw := kinematicsJSON(withCov)
	var parsed map[string]map[string]float64
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatal(err)
	}
	cov := parsed["positionCovariance"]
	if cov == nil || cov["pnPn"] != 400 || cov["pnPe"] != 50 || cov["pePe"] != 100 {
		t.Fatalf("positionCovariance = %v", cov)
	}

	// A position fix with no velocity, acceleration or covariance writes "{}"
	// so COALESCE(b.kinematics, e.kinematics) clears any prior covariance.
	plainPos := &sentinelv1.Observation{
		Position: &sentinelv1.Coordinate{Latitude: 50.1, Longitude: -4.1},
	}
	if got := string(kinematicsJSON(plainPos)); got != "{}" {
		t.Fatalf("kinematicsJSON with position only = %q, want \"{}\"", got)
	}

	// An observation without a position and without kinematics returns nil.
	noPos := &sentinelv1.Observation{}
	if got := kinematicsJSON(noPos); got != nil {
		t.Fatalf("kinematicsJSON without position = %s, want nil", got)
	}
}
