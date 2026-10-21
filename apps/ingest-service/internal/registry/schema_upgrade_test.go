package registry

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// globalDefaults is every age-out row with no feed: the per-source and global
// defaults every entity falls back to.
func globalDefaults(t *testing.T, ctx context.Context, pool *pgxpool.Pool) map[string][2]int {
	t.Helper()
	rows, err := pool.Query(ctx, `SELECT COALESCE("sourceType", '*'), "staleThresholdMs", "ageoutThresholdMs"
		FROM sentinel.feed_ageout_config WHERE "feedId" IS NULL`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string][2]int{}
	for rows.Next() {
		var src string
		var stale, ageout int
		if err := rows.Scan(&src, &stale, &ageout); err != nil {
			t.Fatal(err)
		}
		out[src] = [2]int{stale, ageout}
	}
	return out
}

// Applies db/schema/72_emplacements.sql twice over a database shaped as it was
// before heartbeats: the mirror feed with its age-out rows and entities, a
// foreign key that nulls, no CHECK, and active emplacements with no connection.
func TestEmplacementsSchemaUpgrade(t *testing.T) {
	s, ctx := testStore(t)
	pool := s.Pool()
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "db", "schema", "72_emplacements.sql"))
	if err != nil {
		t.Fatal(err)
	}
	const (
		unbound  = "test-upg-active-unbound"
		mirrored = "test-upg-bound-to-mirror"
		entityID = "7b0d6c1e-1f6a-4d1e-9c55-0d6c1e1f6a4d"
	)
	cleanup := func(ctx context.Context) {
		_, _ = pool.Exec(ctx, `DELETE FROM sentinel.entity_sources WHERE "entityId" = $1::uuid`, entityID)
		_, _ = pool.Exec(ctx, `DELETE FROM sentinel.entities WHERE id = $1::uuid`, entityID)
		_, _ = pool.Exec(ctx, `DELETE FROM sentinel.emplacements WHERE name LIKE 'test-upg-%'`)
	}
	cleanup(ctx)
	t.Cleanup(func() { cleanup(context.Background()) })

	setup := []string{
		`ALTER TABLE sentinel.emplacements DROP CONSTRAINT IF EXISTS emplacements_active_needs_feed`,
		`DO $$ DECLARE fk text; BEGIN
		   SELECT conname INTO fk FROM pg_constraint
		    WHERE conrelid = 'sentinel.emplacements'::regclass AND contype = 'f' AND confrelid = 'sentinel.feeds'::regclass;
		   IF fk IS NOT NULL THEN EXECUTE format('ALTER TABLE sentinel.emplacements DROP CONSTRAINT %I', fk); END IF;
		 END $$`,
		`ALTER TABLE sentinel.emplacements ADD CONSTRAINT "emplacements_feedId_fkey"
		   FOREIGN KEY ("feedId") REFERENCES sentinel.feeds(id) ON DELETE SET NULL`,
		`INSERT INTO sentinel.feeds (id, name, description, driver, config, secret_refs, enabled, builtin)
		 VALUES ('emplacements', 'Emplacements', '', 'emplacement', '{"heartbeatSec": 60}', '{}', TRUE, TRUE)
		 ON CONFLICT (id) DO NOTHING`,
		`INSERT INTO sentinel.feed_ageout_config ("feedId", "sourceType", "staleThresholdMs", "ageoutThresholdMs")
		 VALUES ('emplacements', 'RADAR', 180000, 600000)
		 ON CONFLICT ("feedId", "sourceType") WHERE "feedId" IS NOT NULL AND "sourceType" IS NOT NULL DO NOTHING`,
		`INSERT INTO sentinel.emplacements (name, mobility, "sensorType", position, active, "createdBy")
		 VALUES ('` + unbound + `', 'FIXED', 'RADAR', ST_SetSRID(ST_MakePoint(-5, 50), 4326), TRUE, 'u1')`,
		`INSERT INTO sentinel.emplacements (name, mobility, "sensorType", position, active, "feedId", "createdBy")
		 VALUES ('` + mirrored + `', 'FIXED', 'RADAR', ST_SetSRID(ST_MakePoint(-5, 50), 4326), TRUE, 'emplacements', 'u1')`,
		`INSERT INTO sentinel.entities (id, "entityType", name, source, "feedId")
		 VALUES ('` + entityID + `', 'SENSOR', 'test-upg', 'RADAR', 'emplacements')`,
		`INSERT INTO sentinel.entity_sources ("entityId", "feedId", source, "sourceEntityId", "lastObservedAt", "lastReceivedAt")
		 VALUES ('` + entityID + `', 'emplacements', 'RADAR', 'x', NOW(), NOW())`,
	}
	for _, q := range setup {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	before := globalDefaults(t, ctx, pool)

	for run := 1; run <= 2; run++ {
		if _, err := pool.Exec(ctx, string(raw)); err != nil {
			t.Fatalf("applying 72_emplacements.sql, run %d: %v", run, err)
		}
	}

	count := func(q string, args ...any) int {
		t.Helper()
		var n int
		if err := pool.QueryRow(ctx, q, args...).Scan(&n); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		return n
	}
	if n := count(`SELECT count(*) FROM sentinel.feeds WHERE id = 'emplacements'`); n != 0 {
		t.Errorf("the mirror feed is still there")
	}
	if n := count(`SELECT count(*) FROM sentinel.feed_ageout_config WHERE "feedId" = 'emplacements'`); n != 0 {
		t.Errorf("%d age-out rows of the mirror feed remain", n)
	}
	for _, name := range []string{unbound, mirrored} {
		var active bool
		var feed *string
		if err := pool.QueryRow(ctx, `SELECT active, "feedId" FROM sentinel.emplacements WHERE name = $1`, name).Scan(&active, &feed); err != nil {
			t.Fatal(err)
		}
		if active || feed != nil {
			t.Errorf("%s: active %v, feed %v; want inactive with no connection", name, active, feed)
		}
	}
	if n := count(`SELECT count(*) FROM pg_constraint WHERE conname = 'emplacements_active_needs_feed'`); n != 1 {
		t.Errorf("%d emplacements_active_needs_feed constraints, want 1", n)
	}
	_, err = pool.Exec(ctx, `INSERT INTO sentinel.emplacements (name, mobility, "sensorType", position, active, "createdBy")
		VALUES ('test-upg-refused', 'FIXED', 'RADAR', ST_SetSRID(ST_MakePoint(-5, 50), 4326), TRUE, 'u1')`)
	var pg *pgconn.PgError
	if !errors.As(err, &pg) || pg.Code != "23514" || pg.ConstraintName != "emplacements_active_needs_feed" {
		t.Errorf("an active emplacement with no connection was not refused: %v", err)
	}
	var del []string
	rows, err := pool.Query(ctx, `SELECT confdeltype::text FROM pg_constraint
		WHERE conrelid = 'sentinel.emplacements'::regclass AND contype = 'f' AND confrelid = 'sentinel.feeds'::regclass`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err != nil {
			t.Fatal(err)
		}
		del = append(del, d)
	}
	rows.Close()
	if !reflect.DeepEqual(del, []string{"r"}) {
		t.Errorf("feed foreign keys on delete = %v, want one RESTRICT (r)", del)
	}
	if n := count(`SELECT count(*) FROM information_schema.columns WHERE table_schema = 'sentinel' AND table_name = 'emplacements'
		AND column_name IN ('heartbeatIntervalSec', 'lastHeartbeatAt')`); n != 2 {
		t.Errorf("%d heartbeat columns, want 2", n)
	}
	if n := count(`SELECT count(*) FROM sentinel.entities WHERE id = $1::uuid AND "feedId" IS NULL`, entityID); n != 1 {
		t.Errorf("the mirror's entity still names the mirror feed")
	}
	if n := count(`SELECT count(*) FROM sentinel.entity_sources WHERE "feedId" = 'emplacements'`); n != 0 {
		t.Errorf("%d entity_sources rows of the mirror feed remain", n)
	}
	if after := globalDefaults(t, ctx, pool); !reflect.DeepEqual(before, after) {
		t.Errorf("the global age-out defaults changed: %v, then %v", before, after)
	}
}
