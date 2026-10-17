package store

import (
	"context"
	"testing"
	"time"

	"github.com/sentinel/analytics-service/internal/baseline"
)

func TestBaselineStoreRoundTrip(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	s := NewBaselineStore(pool)
	entityID := "00000000-0000-4000-8000-00000000b001"
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM sentinel.entity_baselines WHERE "entityId" = $1`, entityID)
		pool.Exec(ctx, `DELETE FROM sentinel.track_points WHERE "entityId" = $1`, entityID)
		pool.Exec(ctx, `DELETE FROM sentinel.locations WHERE name = 'test-baseline-depot'`)
	})

	// A track at 10 s spacing thins to one point per 30 s.
	t0 := time.Date(2026, 10, 17, 10, 0, 0, 0, time.UTC)
	for i := 0; i < 30; i++ {
		_, err := pool.Exec(ctx, `INSERT INTO sentinel.track_points ("entityId", position, "speedKnots", altitude, "timestamp", "feedId")
			VALUES ($1, ST_SetSRID(ST_MakePoint(-74, 40), 4326), 0, 5, $2, 'test-baseline')`, entityID, t0.Add(time.Duration(i)*10*time.Second))
		if err != nil {
			t.Fatal(err)
		}
	}
	pts, err := s.LoadTrack(ctx, entityID, t0, t0.Add(time.Hour), 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if len(pts) != 10 {
		t.Errorf("thinned points = %d, want 10", len(pts))
	}
	if pts[0].SpeedKnots == nil || *pts[0].SpeedKnots != 0 || pts[0].AltitudeM == nil || pts[0].Lat != 40 {
		t.Errorf("point = %+v", pts[0])
	}

	// Named place: a saved location within range.
	if _, err := pool.Exec(ctx, `INSERT INTO sentinel.locations (name, latitude, longitude, "range") VALUES ('test-baseline-depot', 40.0005, -74.0005, 500)`); err != nil {
		t.Fatal(err)
	}
	name, locID, err := s.NamePlace(ctx, 40, -74, 25)
	if err != nil || name == nil || *name != "test-baseline-depot" || locID == nil {
		t.Errorf("NamePlace = %v %v %v", name, locID, err)
	}
	if n, _, _ := s.NamePlace(ctx, 41, -74, 25); n != nil {
		t.Error("a location 111 km away must not name the place")
	}

	b := &baseline.Baseline{
		EntityID: entityID, EntityType: "VEHICLE", ComputedAt: t0, WindowStart: t0.Add(-24 * time.Hour), WindowEnd: t0,
		PointCount: 10, Days: 1, Mature: false, Hours: make([]int16, baseline.Slots),
		Places: []baseline.Place{{ClusterIndex: 0, Name: name, LocationID: locID, Lat: 40, Lon: -74, RadiusM: 25, Visits: 1, MedianDwellSec: 290, PointShare: 1, FirstSeen: t0, LastSeen: t0.Add(290 * time.Second)}},
	}
	b.Hours[10] = 10
	b.Stats.Speed.N = 10
	b.Stats.Params.SolarOffsetHours = -5
	if err := s.Upsert(ctx, b); err != nil {
		t.Fatal(err)
	}
	// Upsert again with one more place: places are replaced, not appended.
	b.Places = append(b.Places, baseline.Place{ClusterIndex: 1, Lat: 40.1, Lon: -74.1, RadiusM: 40, Visits: 2, PointShare: 0.2, FirstSeen: t0, LastSeen: t0})
	if err := s.Upsert(ctx, b); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ctx, entityID)
	if err != nil || got == nil {
		t.Fatalf("Get = %v, %v", got, err)
	}
	if len(got.Hours) != baseline.Slots || got.Hours[10] != 10 {
		t.Errorf("hours round trip: len %d, slot 10 = %d", len(got.Hours), got.Hours[10])
	}
	if got.Stats.Speed.N != 10 || got.Stats.Params.SolarOffsetHours != -5 {
		t.Errorf("stats round trip = %+v", got.Stats)
	}
	if len(got.Places) != 2 || got.Places[0].Name == nil || *got.Places[0].Name != "test-baseline-depot" || got.Places[0].Lat != 40 {
		t.Errorf("places = %+v", got.Places)
	}
	if none, err := s.Get(ctx, "00000000-0000-4000-8000-00000000b002"); none != nil || err != nil {
		t.Errorf("unknown entity: %v %v", none, err)
	}
}
