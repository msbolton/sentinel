package fusion_test

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/types/known/timestamppb"

	sentinelv1 "github.com/sentinel/proto-gen/sentinel/v1"

	"github.com/sentinel/entity-service/internal/fusion"
	"github.com/sentinel/entity-service/internal/identity"
	"github.com/sentinel/entity-service/internal/store"
)

// Needs a database with the baseline applied: TEST_DATABASE_URL.
func testPool(t *testing.T) (*pgxpool.Pool, context.Context) {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM sentinel.observations WHERE "feedId" LIKE 'test-%'`)
		_, _ = pool.Exec(context.Background(), `DELETE FROM sentinel.entities WHERE "feedId" LIKE 'test-%'`)
		pool.Close()
	})
	return pool, ctx
}

func report(feed, icao string, at time.Time, lat, lon float64) *fusion.Report {
	o := &sentinelv1.Observation{
		ObservationId: "", FeedId: feed, Driver: "opensky", Decoder: "opensky-states", SourceEntityId: "ICAO-" + icao,
		Source: sentinelv1.EntitySource_ENTITY_SOURCE_OPENSKY, EntityType: sentinelv1.EntityType_ENTITY_TYPE_AIRCRAFT,
		DisplayName: "TEST" + icao, SourceTimestamp: timestamppb.New(at), ReceivedAt: timestamppb.New(at.Add(time.Second)),
		Identities: []*sentinelv1.Identity{{Type: sentinelv1.IdentityType_IDENTITY_TYPE_ICAO24, Value: icao}},
		Position:   &sentinelv1.Coordinate{Latitude: lat, Longitude: lon},
	}
	return fusion.Prepare(o, nil)
}

func TestIdentityResolveAndFusedApply(t *testing.T) {
	pool, ctx := testPool(t)
	ids := store.NewIdentityStore(pool, nil)
	ents := store.NewEntityStore(pool, nil)
	obsStore := store.NewObservationStore(pool, nil)
	srcs := store.NewSourceStore(pool, nil)
	t0 := time.Now().UTC().Truncate(time.Millisecond)
	icao := "TST" + t0.Format("150405")

	r1 := report("test-a", icao, t0, 41.9, -87.9)
	found, err := ids.Lookup(ctx, []identity.Key{r1.Key})
	if err != nil || len(found) != 0 {
		t.Fatalf("lookup before create = %v %v", found, err)
	}
	created, isNew, err := ids.CreateEntities(ctx, []identity.NewEntity{fusion.SpecFor(r1)})
	if err != nil || !isNew[r1.Key] || created[r1.Key] == "" {
		t.Fatalf("create = %v %v %v", created, isNew, err)
	}
	entityID := created[r1.Key]
	// Creating again for the same key must return the existing entity.
	again, isNew2, err := ids.CreateEntities(ctx, []identity.NewEntity{fusion.SpecFor(r1)})
	if err != nil || isNew2[r1.Key] || again[r1.Key] != entityID {
		t.Fatalf("second create = %v %v %v", again, isNew2, err)
	}

	apply := func(r *fusion.Report) []store.FusedResult {
		o := r.Obs
		res, err := ents.ApplyFused(ctx, []store.FusedUpdate{{EntityID: entityID, ObservedAt: r.ObservedAt, FeedID: o.FeedId, Source: "OPENSKY", SourceEntityID: o.SourceEntityId,
			KeyType: r.Key.Type, KeyValue: r.Key.Value, Name: o.DisplayName, Lat: o.Position.Latitude, Lon: o.Position.Longitude, TrackEnvironment: "AIR",
			Platform: json.RawMessage(`{"adsb":{"icaoHex":"` + icao + `"}}`)}})
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	res := apply(r1)
	if len(res) != 1 || res[0].PreviousState != "LIVE" || res[0].Record.Position == nil || res[0].Record.Position.Lat != 41.9 || *res[0].Record.LastFeedID != "test-a" {
		t.Fatalf("first apply = %+v", res)
	}
	// An older fix from another feed must not move the entity.
	old := report("test-b", icao, t0.Add(-time.Minute), 1, 1)
	if res := apply(old); len(res) != 0 {
		t.Fatalf("stale fix applied: %+v", res)
	}
	// A newer fix moves it and reports the previous state.
	_, _ = pool.Exec(ctx, `UPDATE sentinel.entities SET "ageoutState" = 'STALE' WHERE id = $1`, entityID)
	newer := report("test-b", icao, t0.Add(time.Minute), 42.0, -88.0)
	res = apply(newer)
	if len(res) != 1 || res[0].PreviousState != "STALE" || res[0].Record.AgeoutState != "LIVE" || res[0].Record.Position.Lat != 42.0 || *res[0].Record.LastFeedID != "test-b" {
		t.Fatalf("newer apply = %+v", res)
	}
	if pd, ok := res[0].Record.PlatformData["adsb"].(map[string]interface{}); !ok || pd["icaoHex"] != icao {
		t.Fatalf("platform merge = %v", res[0].Record.PlatformData)
	}

	// Observations: idempotent insert.
	rows := []store.ObservationRow{{ObservationID: "00000000-0000-5000-8000-" + t0.Format("150405000000")[:12], ObservedAt: r1.ObservedAt, ReceivedAt: r1.ReceivedAt, EntityID: entityID, FeedID: "test-a", Driver: "opensky", Decoder: "opensky-states", SourceEntityID: r1.Obs.SourceEntityId, Source: "OPENSKY", Accepted: true, EntityType: "AIRCRAFT", HasPosition: true, Lat: 41.9, Lon: -87.9, Identities: json.RawMessage(`[{"type":"ICAO24","value":"` + icao + `"}]`), Platform: json.RawMessage(`{"adsb":{"icaoHex":"` + icao + `"}}`), RawContentType: "application/json", Raw: []byte(`["x"]`)}}
	if err := obsStore.InsertObservations(ctx, rows); err != nil {
		t.Fatal(err)
	}
	if err := obsStore.InsertObservations(ctx, rows); err != nil {
		t.Fatalf("second insert must be a no-op: %v", err)
	}
	list, err := obsStore.ListByEntity(ctx, entityID, 10)
	if err != nil || len(list) != 1 || !list[0].Accepted || list[0].Position == nil || list[0].FeedID != "test-a" {
		t.Fatalf("list = %+v err %v", list, err)
	}

	// Sources: counts accumulate, times take the max.
	src := store.SourceRow{EntityID: entityID, FeedID: "test-a", Source: "OPENSKY", SourceEntityID: r1.Obs.SourceEntityId, ObservedAt: t0, ReceivedAt: t0}
	if err := srcs.UpsertSources(ctx, []store.SourceRow{src, src, {EntityID: entityID, FeedID: "test-b", Source: "OPENSKY", SourceEntityID: "x", ObservedAt: t0.Add(time.Minute), ReceivedAt: t0.Add(time.Minute)}}); err != nil {
		t.Fatal(err)
	}
	var count int64
	var n int
	if err := pool.QueryRow(ctx, `SELECT "observationCount", (SELECT count(*) FROM sentinel.entity_sources WHERE "entityId" = $1) FROM sentinel.entity_sources WHERE "entityId" = $1 AND "feedId" = 'test-a'`, entityID).Scan(&count, &n); err != nil || count != 2 || n != 2 {
		t.Fatalf("sources count=%d rows=%d err=%v", count, n, err)
	}

	// Secondary identities attach; a conflicting one is counted, not moved.
	otherKey := identity.Key{Type: "ICAO24", Value: icao + "X"}
	other, _, _ := ids.CreateEntities(ctx, []identity.NewEntity{fusion.SpecFor(report("test-c", icao+"X", t0, 1, 1))})
	conflicts, err := ids.AttachIdentities(ctx, []identity.Binding{
		{Key: identity.Key{Type: "CALLSIGN", Value: "TST" + icao}, EntityID: entityID, SeenAt: t0},
		{Key: identity.Key{Type: "CALLSIGN", Value: "TST" + icao}, EntityID: other[otherKey], SeenAt: t0},
	})
	if err != nil {
		t.Fatal(err)
	}
	bound, _ := ids.IdentitiesOf(ctx, entityID)
	if len(bound) != 2 {
		t.Fatalf("identities of entity = %v (conflicts %d)", bound, conflicts)
	}
}

// Feeds raise an entity's classification and never lower it; an operator
// may lower it through the operator-field update.
func TestClassificationHighWaterMark(t *testing.T) {
	pool, ctx := testPool(t)
	ids := store.NewIdentityStore(pool, nil)
	ents := store.NewEntityStore(pool, nil)
	t0 := time.Now().UTC().Truncate(time.Millisecond)
	icao := "TSC" + t0.Format("150405")

	r1 := report("test-a", icao, t0, 41.9, -87.9)
	spec := fusion.SpecFor(r1)
	spec.Classification = "CONFIDENTIAL"
	created, _, err := ids.CreateEntities(ctx, []identity.NewEntity{spec})
	if err != nil {
		t.Fatal(err)
	}
	entityID := created[r1.Key]
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM sentinel.entities WHERE id = $1`, entityID) })

	apply := func(level string, at time.Time) store.FusedResult {
		res, err := ents.ApplyFused(ctx, []store.FusedUpdate{{EntityID: entityID, ObservedAt: at, FeedID: "test-a", Source: "OPENSKY", SourceEntityID: r1.Obs.SourceEntityId,
			KeyType: r1.Key.Type, KeyValue: r1.Key.Value, Name: "X", Lat: 1, Lon: 1, Classification: level}})
		if err != nil || len(res) != 1 {
			t.Fatalf("apply %s = %+v %v", level, res, err)
		}
		return res[0]
	}
	if r := apply("UNCLASSIFIED", t0); r.Record.Classification != "CONFIDENTIAL" || r.PreviousClassification != "CONFIDENTIAL" {
		t.Fatalf("lower report changed level: %+v", r)
	}
	if r := apply("SECRET", t0.Add(time.Second)); r.Record.Classification != "SECRET" || r.PreviousClassification != "CONFIDENTIAL" {
		t.Fatalf("raise = %+v", r)
	}
	if r := apply("", t0.Add(2*time.Second)); r.Record.Classification != "SECRET" {
		t.Fatalf("empty level changed it: %+v", r)
	}
	static, err := ents.ApplyStatic(ctx, []store.StaticUpdate{{EntityID: entityID, FeedID: "test-a", Source: "OPENSKY", SourceEntityID: "x", Classification: "TOP_SECRET"}})
	if err != nil || len(static) != 1 || static[0].Record.Classification != "TOP_SECRET" || static[0].PreviousClassification != "SECRET" {
		t.Fatalf("static raise = %+v %v", static, err)
	}

	level := "UNCLASSIFIED"
	name := "Renamed"
	rec, err := ents.UpdateOperatorFields(ctx, entityID, store.OperatorUpdate{Classification: &level, Name: &name, Affiliations: []string{"blue"}})
	if err != nil || rec.Classification != "UNCLASSIFIED" || rec.Name != "Renamed" || len(rec.Affiliations) != 1 {
		t.Fatalf("operator update = %+v %v", rec, err)
	}
	if _, err := ents.UpdateOperatorFields(ctx, "00000000-0000-4000-8000-000000000000", store.OperatorUpdate{Name: &name}); err == nil {
		t.Fatal("update of a missing entity must fail")
	}
}

// Reads apply the caller's clearance as a ceiling.
func TestCeilingOnReads(t *testing.T) {
	pool, ctx := testPool(t)
	ids := store.NewIdentityStore(pool, nil)
	ents := store.NewEntityStore(pool, nil)
	t0 := time.Now().UTC().Truncate(time.Millisecond)
	icao := "TSR" + t0.Format("150405")

	r1 := report("test-ceiling", icao, t0, 10.5, 10.5)
	spec := fusion.SpecFor(r1)
	spec.Classification = "SECRET"
	created, _, err := ids.CreateEntities(ctx, []identity.NewEntity{spec})
	if err != nil {
		t.Fatal(err)
	}
	entityID := created[r1.Key]
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM sentinel.entities WHERE id = $1`, entityID) })
	if _, err := ents.ApplyFused(ctx, []store.FusedUpdate{{EntityID: entityID, ObservedAt: t0, FeedID: "test-ceiling", Source: "OPENSKY", SourceEntityID: r1.Obs.SourceEntityId,
		KeyType: r1.Key.Type, KeyValue: r1.Key.Value, Name: "X", Lat: 10.5, Lon: 10.5, Classification: "SECRET"}}); err != nil {
		t.Fatal(err)
	}

	north, south, east, west := 11.0, 10.0, 11.0, 10.0
	find := func(ceiling string, requested *string) int {
		res, err := ents.FindWithinBoundingBox(ctx, store.QueryParams{North: &north, South: &south, East: &east, West: &west, FeedIDs: []string{"test-ceiling"}, Ceiling: ceiling, Classification: requested, PageSize: 10})
		if err != nil {
			t.Fatal(err)
		}
		return res.Total
	}
	if n := find("CONFIDENTIAL", nil); n != 0 {
		t.Errorf("CONFIDENTIAL caller sees %d SECRET entities", n)
	}
	if n := find("TOP_SECRET", nil); n != 1 {
		t.Errorf("TOP_SECRET caller sees %d, want 1", n)
	}
	lower := "UNCLASSIFIED"
	if n := find("TOP_SECRET", &lower); n != 0 {
		t.Errorf("requested UNCLASSIFIED still sees %d", n)
	}
	if n := find("", nil); n != 0 {
		t.Errorf("missing clearance sees %d, want 0 (fail closed)", n)
	}

	near, err := ents.FindNearby(ctx, 10.5, 10.5, 1000, "SECRET")
	if err != nil {
		t.Fatal(err)
	}
	seen := false
	for _, e := range near {
		if e.ID == entityID {
			seen = true
		}
	}
	if !seen {
		t.Error("SECRET caller cannot find the SECRET entity nearby")
	}
	near, _ = ents.FindNearby(ctx, 10.5, 10.5, 1000, "UNCLASSIFIED")
	for _, e := range near {
		if e.ID == entityID {
			t.Error("UNCLASSIFIED caller found the SECRET entity nearby")
		}
	}

	counts, err := ents.GetEntityCounts(ctx, "CONFIDENTIAL")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range counts {
		if c.Classification == "SECRET" || c.Classification == "TOP_SECRET" {
			t.Errorf("counts leak %s rows to a CONFIDENTIAL caller", c.Classification)
		}
	}
}
