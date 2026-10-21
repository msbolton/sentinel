package store

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

// These tests need a database with db/schema applied. Set TEST_DATABASE_URL to
// run them; they create entities and alerts named or titled test-ew-* and
// delete them before and after.
func testStore(t *testing.T) (*AlertStore, context.Context) {
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
	t.Cleanup(pool.Close)
	clean := func(ctx context.Context) {
		_, _ = pool.Exec(ctx, `DELETE FROM sentinel.alerts WHERE title LIKE 'test-ew-%'`)
		_, _ = pool.Exec(ctx, `DELETE FROM sentinel.entities WHERE name LIKE 'test-ew-%'`)
	}
	clean(ctx)
	t.Cleanup(func() { clean(context.Background()) })
	return NewAlertStore(pool, zap.NewNop()), ctx
}

func insertEntity(t *testing.T, ctx context.Context, s *AlertStore, name string, exercise bool) string {
	t.Helper()
	var id string
	err := s.pool.QueryRow(ctx, `
		INSERT INTO sentinel.entities ("entityType", name, source, exercise)
		VALUES ('AIRCRAFT', $1, 'test', $2) RETURNING id::text`, name, exercise).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestEntitiesExercise(t *testing.T) {
	s, ctx := testStore(t)
	live := insertEntity(t, ctx, s, "test-ew-live", false)
	sim := insertEntity(t, ctx, s, "test-ew-sim", true)
	const unknown = "00000000-0000-4000-8000-00000000dead"

	for _, c := range []struct {
		name string
		ids  []string
		want bool
	}{
		{"none are exercise", []string{live}, false},
		{"one is exercise", []string{live, sim}, true},
		{"the only one is exercise", []string{sim}, true},
		{"unknown ids are not exercise", []string{unknown}, false},
		{"unknown beside a live one", []string{unknown, live}, false},
		{"no ids", nil, false},
	} {
		got, err := s.EntitiesExercise(ctx, c.ids)
		if err != nil || got != c.want {
			t.Errorf("%s: got %v, %v; want %v", c.name, got, err, c.want)
		}
	}
	if _, err := s.EntitiesExercise(ctx, []string{"not-a-uuid"}); err == nil {
		t.Error("a malformed id should be an error, so the caller can fail closed")
	}
}

func TestAlertExerciseRoundTrips(t *testing.T) {
	s, ctx := testStore(t)
	entity := insertEntity(t, ctx, s, "test-ew-subject", true)
	lat, lng := 1.5, 2.5

	for _, exercise := range []bool{true, false} {
		rec, err := s.CreateAlert(ctx, CreateAlertParams{
			AlertType: "SPEED_ANOMALY", Severity: "HIGH", Title: "test-ew-alert", EntityID: entity,
			Lat: &lat, Lng: &lng, Exercise: exercise,
		})
		if err != nil || rec.Exercise != exercise {
			t.Fatalf("create(exercise=%v): %+v, %v", exercise, rec, err)
		}
		got, err := s.GetAlert(ctx, rec.ID)
		if err != nil || got.Exercise != exercise || got.Position == nil {
			t.Fatalf("get(exercise=%v): %+v, %v", exercise, got, err)
		}
		list, err := s.GetAlerts(ctx, AlertQueryParams{EntityID: &entity, PageSize: 50})
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, a := range list.Data {
			if a.ID == rec.ID {
				found = true
				if a.Exercise != exercise {
					t.Errorf("list(exercise=%v): %+v", exercise, a)
				}
			}
		}
		if !found {
			t.Errorf("alert %s not listed", rec.ID)
		}
	}
}
