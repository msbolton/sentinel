package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
	"google.golang.org/protobuf/proto"

	"github.com/sentinel/entity-service/internal/events"
	"github.com/sentinel/entity-service/internal/store"
)

type fakeInvalidator struct{ calls int }

func (f *fakeInvalidator) Invalidate() { f.calls++ }

type nopProducer struct{}

func (nopProducer) ProduceProto(string, string, proto.Message, string, map[string]string) error {
	return nil
}

// Needs a database with the baseline applied: TEST_DATABASE_URL.
func deleteTestHandler(t *testing.T) (*pgxpool.Pool, *http.ServeMux, *fakeInvalidator) {
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
		_, _ = pool.Exec(context.Background(), `DELETE FROM sentinel.entities WHERE "feedId" = 'test-handler-delete'`)
		pool.Close()
	})
	log := zap.NewNop()
	inv := &fakeInvalidator{}
	h := NewEntityHandler(store.NewEntityStore(pool, log), store.NewObservationStore(pool, log), events.NewPublisher(nopProducer{}, log), inv, log)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	return pool, mux, inv
}

func insertTestEntity(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(), `
		INSERT INTO sentinel.entities ("entityType", name, source, "feedId", metadata, affiliations)
		VALUES ('AIRCRAFT', 'delete-me', 'OPENSKY', 'test-handler-delete', '{}'::jsonb, ARRAY[]::text[])
		RETURNING id`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestDeleteInvalidatesResolverCache(t *testing.T) {
	pool, mux, inv := deleteTestHandler(t)
	id := insertTestEntity(t, pool)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/api/v1/entities/"+id, nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d %s", rec.Code, rec.Body)
	}
	if inv.calls != 1 {
		t.Fatalf("invalidate calls after delete = %d, want 1", inv.calls)
	}

	// A second delete finds nothing and must not invalidate again.
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/api/v1/entities/"+id, nil))
	if rec.Code != http.StatusNotFound || inv.calls != 1 {
		t.Fatalf("repeat delete status = %d calls = %d, want 404 and 1", rec.Code, inv.calls)
	}
}

func TestDeleteAllInvalidatesResolverCache(t *testing.T) {
	pool, mux, inv := deleteTestHandler(t)
	insertTestEntity(t, pool)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/api/v1/entities", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("delete all status = %d %s", rec.Code, rec.Body)
	}
	if inv.calls != 1 {
		t.Fatalf("invalidate calls after delete all = %d, want 1", inv.calls)
	}
}
