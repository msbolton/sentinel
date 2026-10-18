package registry

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

func TestSchemaErrorNamesColumnsFileAndCommands(t *testing.T) {
	const file = "db/schema/70_feeds.sql"
	err := &SchemaError{Missing: []SchemaColumn{
		{"ingest_replicas", "listener", file},
		{"ingest_replicas", "listener_host", file},
	}}
	msg := err.Error()
	for _, want := range []string{
		"sentinel.ingest_replicas.listener",
		"sentinel.ingest_replicas.listener_host",
		file,
		"docker compose up db-schema",
		"make db-schema",
		"psql",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("message lacks %q: %s", want, msg)
		}
	}
	if n := strings.Count(msg, file); n != 1 {
		t.Errorf("the file is named %d times, want once: %s", n, msg)
	}
	if strings.ContainsAny(msg, "\r\n") {
		t.Errorf("the message must be one line for the JSON logs: %q", msg)
	}
}

// RequiredColumns decides whether the service starts, so a name that is not
// in its schema file would stop it against a correct database. The database
// tests below skip without TEST_DATABASE_URL; this one reads the file.
func TestRequiredColumnsAreInTheirSchemaFiles(t *testing.T) {
	comment := regexp.MustCompile(`--[^\n]*`)
	for _, c := range RequiredColumns {
		raw, err := os.ReadFile(filepath.Join("..", "..", "..", "..", c.File))
		if err != nil {
			t.Errorf("%s.%s: %v", c.Table, c.Column, err)
			continue
		}
		sql := comment.ReplaceAllString(string(raw), "")
		table := regexp.MustCompile(`sentinel\.` + regexp.QuoteMeta(c.Table) + `\b`)
		column := regexp.MustCompile(`\b` + regexp.QuoteMeta(c.Column) + `\b`)
		if !table.MatchString(sql) || !column.MatchString(sql) {
			t.Errorf("%s.%s is not in %s", c.Table, c.Column, c.File)
		}
	}
}

func TestCheckSchemaPassesOnAppliedSchema(t *testing.T) {
	s, ctx := testStore(t)
	if err := s.CheckSchema(ctx); err != nil {
		t.Fatalf("CheckSchema on the applied schema: %v", err)
	}
}

func TestCheckSchemaReportsMissingColumn(t *testing.T) {
	s, ctx := testStore(t)
	schema := fmt.Sprintf("test_schema_check_%d", os.Getpid())
	t.Cleanup(func() {
		_, _ = s.pool.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+schema+" CASCADE")
	})
	// An ingest_replicas table from before the listener columns existed.
	for _, ddl := range []string{
		"CREATE SCHEMA " + schema,
		"CREATE TABLE " + schema + ".ingest_replicas (replica_id text)",
	} {
		if _, err := s.pool.Exec(ctx, ddl); err != nil {
			t.Fatal(err)
		}
	}

	const file = "db/schema/70_feeds.sql"
	err := checkColumns(ctx, s.pool, schema, []SchemaColumn{
		{"ingest_replicas", "replica_id", file},
		{"ingest_replicas", "listener", file},
		{"ingest_replicas", "listener_host", file},
	})
	var se *SchemaError
	if !errors.As(err, &se) {
		t.Fatalf("err = %v, want a *SchemaError", err)
	}
	want := []SchemaColumn{{"ingest_replicas", "listener", file}, {"ingest_replicas", "listener_host", file}}
	if !slices.Equal(se.Missing, want) {
		t.Fatalf("missing = %+v, want %+v", se.Missing, want)
	}
}
