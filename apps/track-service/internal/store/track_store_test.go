package store

import (
	"strings"
	"testing"

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
