package store

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestEntityMarksReadsExerciseAndLevel(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	const id = "44444444-4444-4444-8444-444444444444"
	t.Cleanup(func() { pool.Exec(ctx, `DELETE FROM sentinel.entities WHERE id = $1`, id) })
	if _, err := pool.Exec(ctx,
		`INSERT INTO sentinel.entities (id, "entityType", name, source, classification, exercise)
		 VALUES ($1, 'AIRCRAFT', 'marks', 'test', 'SECRET', true)`, id); err != nil {
		t.Fatal(err)
	}
	s := NewEntityStore(pool)
	exercise, level, err := s.EntityMarks(ctx, id)
	if err != nil || !exercise || level != "SECRET" {
		t.Fatalf("exercise=%v level=%q err=%v", exercise, level, err)
	}
	if _, _, err := s.EntityMarks(ctx, "33333333-3333-4333-8333-333333333333"); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("unknown entity: err = %v, want pgx.ErrNoRows", err)
	}
}
