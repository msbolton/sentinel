package store

import (
	"context"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/sentinel/track-service/internal/models"
)

func TestBuildInsertParameterNumbering(t *testing.T) {
	points := []models.BufferedPoint{{EntityID: "a"}, {EntityID: "b"}}
	query, args := buildInsert(points)
	if len(args) != 2*paramsPerRow {
		t.Fatalf("args = %d, want %d", len(args), 2*paramsPerRow)
	}
	if !strings.Contains(query, "($1, $2, ST_SetSRID(ST_MakePoint($3, $4), 4326)") || !strings.Contains(query, "($18, $19, ST_SetSRID(ST_MakePoint($20, $21), 4326)") {
		t.Errorf("unexpected parameter numbering:\n%s", query)
	}
	if args[0] != "a" || args[paramsPerRow] != "b" {
		t.Errorf("args not in row order: %v", args)
	}
}

func TestMaxRowsPerInsertStaysUnderParameterLimit(t *testing.T) {
	if maxRowsPerInsert*paramsPerRow >= 65535 {
		t.Fatalf("%d rows x %d params exceeds the PostgreSQL limit", maxRowsPerInsert, paramsPerRow)
	}
}

func TestBoxClauseSplitsAtTheAntimeridian(t *testing.T) {
	got := boxClause(models.BBox{West: 170, South: -10, East: -170, North: 10}, "t.position")
	if !strings.Contains(got, "ST_MakeEnvelope(170") || !strings.Contains(got, "ST_MakeEnvelope(-180") || !strings.Contains(got, " OR ") {
		t.Errorf("clause = %s", got)
	}
}

func TestBoxClausePlainBox(t *testing.T) {
	got := boxClause(models.BBox{West: -118, South: 33, East: -117, North: 34}, "t.position")
	if strings.Contains(got, " OR ") || !strings.Contains(got, "ST_MakeEnvelope(-118.000000, 33.000000, -117.000000, 34.000000, 4326)") {
		t.Errorf("clause = %s", got)
	}
}

func TestReplaySQLKeepsFirstAndLastPoint(t *testing.T) {
	sql := replaySQL()
	for _, want := range []string{"row_number() OVER", "rn = 1", "rn = cnt", "% stride", "<> 'SATELLITE'", "classification_rank"} {
		if !strings.Contains(sql, want) {
			t.Errorf("replay SQL lacks %q", want)
		}
	}
}

// These tests need a database with db/schema applied. Set TEST_DATABASE_URL to
// run them; they create entities named test-replay-* and track points with
// source test-replay, and delete both before and after. Every case uses its own
// small box in the Southern Ocean (latitude -60 to -59), where no feed reports.

const replayMarker = "test-replay"

func testTrackStore(t *testing.T) (*TrackStore, context.Context) {
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
		_, _ = pool.Exec(ctx, `DELETE FROM sentinel.track_points WHERE source = $1`, replayMarker)
		_, _ = pool.Exec(ctx, `DELETE FROM sentinel.entities WHERE name LIKE 'test-replay-%'`)
	}
	clean(ctx)
	t.Cleanup(func() { clean(context.Background()) })
	return NewTrackStore(pool, zap.NewNop()), ctx
}

// replayFixture seeds the cases and remembers their ids.
type replayFixture struct {
	s      *TrackStore
	ctx    context.Context
	start  time.Time
	ids    map[string]string
	orphan string
}

func (f *replayFixture) entity(t *testing.T, name, typ, level string, deleted bool) string {
	t.Helper()
	var id string
	err := f.s.pool.QueryRow(f.ctx, `
		INSERT INTO sentinel.entities ("entityType", name, source, classification, deleted)
		VALUES ($1, $2, $3, $4, $5) RETURNING id::text`,
		typ, "test-replay-"+name, replayMarker, level, deleted).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	f.ids[name] = id
	return id
}

// points adds a point for entityID at each minute offset from the window start.
func (f *replayFixture) points(t *testing.T, entityID string, lon, lat float64, minutes ...int) {
	t.Helper()
	for _, m := range minutes {
		_, err := f.s.pool.Exec(f.ctx, `
			INSERT INTO sentinel.track_points ("entityId", position, source, "timestamp", altitude, heading)
			VALUES ($1::uuid, ST_SetSRID(ST_MakePoint($2, $3), 4326), $4, $5, $6, 90)`,
			entityID, lon, lat, replayMarker, f.start.Add(time.Duration(m)*time.Minute), float64(1000+m))
		if err != nil {
			t.Fatal(err)
		}
	}
}

// box is a one-degree box at longitude west..west+1 in the test latitude band.
func box(west float64) models.BBox {
	return models.BBox{West: west, South: -60, East: west + 1, North: -59}
}

var antimeridianBox = models.BBox{West: 170, South: -60, East: -170, North: -59}

func seedReplay(t *testing.T) *replayFixture {
	s, ctx := testTrackStore(t)
	f := &replayFixture{s: s, ctx: ctx, start: time.Now().UTC().Truncate(time.Hour).Add(-6 * time.Hour), ids: map[string]string{}}

	// Busiest first: a1 has 3 points, a2 2, a3 1.
	f.points(t, f.entity(t, "a1", "AIRCRAFT", "UNCLASSIFIED", false), -149.5, -59.5, 1, 2, 3)
	f.points(t, f.entity(t, "a2", "VESSEL", "UNCLASSIFIED", false), -149.5, -59.5, 11, 12)
	f.points(t, f.entity(t, "a3", "DRONE", "UNCLASSIFIED", false), -149.5, -59.5, 21)

	// Clearance: a SECRET track is absent for an UNCLASSIFIED caller.
	f.points(t, f.entity(t, "secret", "AIRCRAFT", "SECRET", false), -147.5, -59.5, 1, 2)
	f.points(t, f.entity(t, "open", "AIRCRAFT", "UNCLASSIFIED", false), -147.5, -59.5, 3)

	// Satellites are never replayed or counted.
	f.points(t, f.entity(t, "sat", "SATELLITE", "UNCLASSIFIED", false), -145.5, -59.5, 1, 2, 3, 4, 5)
	f.points(t, f.entity(t, "ship", "VESSEL", "UNCLASSIFIED", false), -145.5, -59.5, 6)

	// A deleted entity keeps its points; a point with no entity row is UNKNOWN.
	f.points(t, f.entity(t, "gone", "AIRCRAFT", "UNCLASSIFIED", true), -143.5, -59.5, 1, 2)
	f.orphan = "00000000-0000-4000-8000-00000000beef"
	f.points(t, f.orphan, -143.5, -59.5, 3)

	// Either side of the antimeridian, and one far from it.
	f.points(t, f.entity(t, "east", "VESSEL", "UNCLASSIFIED", false), 175, -59.5, 1)
	f.points(t, f.entity(t, "west", "VESSEL", "UNCLASSIFIED", false), -175, -59.5, 2)
	f.points(t, f.entity(t, "far", "VESSEL", "UNCLASSIFIED", false), 0, -59.5, 3)

	// Thinning: ten points, one a minute.
	f.points(t, f.entity(t, "long", "AIRCRAFT", "UNCLASSIFIED", false), -141.5, -59.5, 0, 1, 2, 3, 4, 5, 6, 7, 8, 9)
	return f
}

func (f *replayFixture) replay(t *testing.T, b models.BBox, ceiling string, maxEntities, maxPoints int) ([]models.ReplayEntity, bool) {
	t.Helper()
	got, truncated, err := f.s.GetReplay(f.ctx, ReplayQuery{
		Start: f.start, End: f.start.Add(time.Hour), Box: b,
		MaxEntities: maxEntities, MaxPoints: maxPoints, Ceiling: ceiling,
	})
	if err != nil {
		t.Fatal(err)
	}
	return got, truncated
}

func entityIDs(es []models.ReplayEntity) []string {
	out := []string{}
	for _, e := range es {
		out = append(out, e.EntityID)
	}
	return out
}

// pointMinutes is each point's offset from the window start, in minutes.
func (f *replayFixture) pointMinutes(e models.ReplayEntity) []int {
	out := []int{}
	for _, p := range e.Points {
		out = append(out, int((int64(*p[0])-f.start.UnixMilli())/60_000))
	}
	return out
}

func TestGetReplay(t *testing.T) {
	f := seedReplay(t)
	const u = "UNCLASSIFIED"

	t.Run("busiest first, truncated past maxEntities", func(t *testing.T) {
		got, truncated := f.replay(t, box(-150), u, 2, 100)
		if want := []string{f.ids["a1"], f.ids["a2"]}; !reflect.DeepEqual(entityIDs(got), want) || !truncated {
			t.Fatalf("got %v truncated=%v, want %v truncated", entityIDs(got), truncated, want)
		}
		if got[0].EntityType != "AIRCRAFT" || got[0].Name != "test-replay-a1" || len(got[0].Points) != 3 {
			t.Errorf("a1 = %+v", got[0])
		}
		p := got[0].Points[0]
		if *p[1] != -149.5 || *p[2] != -59.5 || p[3] == nil || *p[3] != 1001 || p[4] == nil || *p[4] != 90 {
			t.Errorf("a1 first point = [%v %v %v %v]", *p[1], *p[2], p[3], p[4])
		}

		got, truncated = f.replay(t, box(-150), u, 3, 100)
		if len(got) != 3 || truncated {
			t.Errorf("maxEntities 3: got %v truncated=%v", entityIDs(got), truncated)
		}
	})

	t.Run("an entity above the caller's clearance is left out", func(t *testing.T) {
		got, _ := f.replay(t, box(-148), u, 10, 100)
		if want := []string{f.ids["open"]}; !reflect.DeepEqual(entityIDs(got), want) {
			t.Errorf("UNCLASSIFIED caller got %v, want %v", entityIDs(got), want)
		}
		got, _ = f.replay(t, box(-148), "SECRET", 10, 100)
		if want := []string{f.ids["secret"], f.ids["open"]}; !reflect.DeepEqual(entityIDs(got), want) {
			t.Errorf("SECRET caller got %v, want %v", entityIDs(got), want)
		}
	})

	t.Run("satellites are left out", func(t *testing.T) {
		got, truncated := f.replay(t, box(-146), u, 10, 100)
		if want := []string{f.ids["ship"]}; !reflect.DeepEqual(entityIDs(got), want) || truncated {
			t.Errorf("got %v truncated=%v, want %v", entityIDs(got), truncated, want)
		}
	})

	t.Run("deleted entities and orphan points are replayed", func(t *testing.T) {
		got, _ := f.replay(t, box(-144), u, 10, 100)
		if want := []string{f.ids["gone"], f.orphan}; !reflect.DeepEqual(entityIDs(got), want) {
			t.Fatalf("got %v, want %v", entityIDs(got), want)
		}
		if got[0].Name != "test-replay-gone" || got[0].EntityType != "AIRCRAFT" {
			t.Errorf("deleted entity = %+v", got[0])
		}
		orphan := got[1]
		if orphan.EntityType != "UNKNOWN" || orphan.Name != f.orphan || orphan.Classification != "UNCLASSIFIED" || len(orphan.Points) != 1 {
			t.Errorf("orphan = %+v", orphan)
		}
	})

	t.Run("an antimeridian box matches points on both sides", func(t *testing.T) {
		got, _ := f.replay(t, antimeridianBox, u, 10, 100)
		ids := map[string]bool{}
		for _, id := range entityIDs(got) {
			ids[id] = true
		}
		if len(got) != 2 || !ids[f.ids["east"]] || !ids[f.ids["west"]] || ids[f.ids["far"]] {
			t.Errorf("got %v, want east %s and west %s only", entityIDs(got), f.ids["east"], f.ids["west"])
		}
	})

	t.Run("thinning keeps the first and last point and a stride", func(t *testing.T) {
		got, _ := f.replay(t, box(-142), u, 10, 4)
		if len(got) != 1 {
			t.Fatalf("got %v", entityIDs(got))
		}
		// stride = ceil(10 / 4) = 3: rows 1, 4, 7 and the last, 10.
		if minutes, want := f.pointMinutes(got[0]), []int{0, 3, 6, 9}; !reflect.DeepEqual(minutes, want) {
			t.Errorf("kept minutes %v, want %v", minutes, want)
		}
		got, _ = f.replay(t, box(-142), u, 10, 100)
		if n := len(got[0].Points); n != 10 {
			t.Errorf("unthinned: %d points, want 10", n)
		}
	})
}

func TestGetTimelineBox(t *testing.T) {
	f := seedReplay(t)
	total := func(t *testing.T, b *models.BBox, ceiling string) (int64, map[string]int) {
		t.Helper()
		buckets, err := f.s.GetTimeline(f.ctx, f.start, f.start.Add(time.Hour), 6, ceiling, b)
		if err != nil {
			t.Fatal(err)
		}
		if len(buckets) != 6 {
			t.Fatalf("%d buckets, want 6", len(buckets))
		}
		var n int64
		types := map[string]int{}
		for _, bk := range buckets {
			n += bk.Count
			for k, v := range bk.EntityTypes {
				types[k] += v
			}
		}
		return n, types
	}

	t.Run("counts only points inside the box, per type and bucket", func(t *testing.T) {
		b := box(-150)
		buckets, err := f.s.GetTimeline(f.ctx, f.start, f.start.Add(time.Hour), 6, "UNCLASSIFIED", &b)
		if err != nil {
			t.Fatal(err)
		}
		counts := []int64{}
		for _, bk := range buckets {
			counts = append(counts, bk.Count)
		}
		// a1 at minutes 1-3, a2 at 11-12, a3 at 21; ten-minute buckets.
		if want := []int64{3, 2, 1, 0, 0, 0}; !reflect.DeepEqual(counts, want) {
			t.Errorf("counts %v, want %v", counts, want)
		}
		if got := buckets[0].EntityTypes; !reflect.DeepEqual(got, map[string]int{"AIRCRAFT": 3}) {
			t.Errorf("bucket 0 types %v", got)
		}
	})

	t.Run("leaves out satellites and points above the clearance", func(t *testing.T) {
		b := box(-146)
		if n, types := total(t, &b, "UNCLASSIFIED"); n != 1 || types["VESSEL"] != 1 || types["SATELLITE"] != 0 {
			t.Errorf("satellite box: %d %v, want the one ship point", n, types)
		}
		b = box(-148)
		if n, _ := total(t, &b, "UNCLASSIFIED"); n != 1 {
			t.Errorf("clearance box, UNCLASSIFIED: %d, want 1", n)
		}
		if n, _ := total(t, &b, "SECRET"); n != 3 {
			t.Errorf("clearance box, SECRET: %d, want 3", n)
		}
	})

	t.Run("an antimeridian box counts both sides", func(t *testing.T) {
		b := antimeridianBox
		if n, _ := total(t, &b, "UNCLASSIFIED"); n != 2 {
			t.Errorf("antimeridian box: %d, want 2", n)
		}
	})

	t.Run("deleted and orphan points count, the orphan as UNKNOWN", func(t *testing.T) {
		b := box(-144)
		if n, types := total(t, &b, "UNCLASSIFIED"); n != 3 || types["UNKNOWN"] != 1 || types["AIRCRAFT"] != 2 {
			t.Errorf("deleted box: %d %v", n, types)
		}
	})
}
