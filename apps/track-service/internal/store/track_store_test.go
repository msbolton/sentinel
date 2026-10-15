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
	if !strings.Contains(query, "($1, $2, ST_SetSRID(ST_MakePoint($3, $4), 4326)") || !strings.Contains(query, "($14, $15, ST_SetSRID(ST_MakePoint($16, $17), 4326)") {
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
