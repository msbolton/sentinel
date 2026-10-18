package registry

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"
)

const (
	// schemaName is the Postgres schema the feed tables live in.
	schemaName = "sentinel"
	// feedsSchemaFile is the db/schema file that defines the feed tables.
	feedsSchemaFile = "db/schema/70_feeds.sql"
)

// SchemaColumn is a column the service depends on and the db/schema file that
// defines it.
type SchemaColumn struct{ Table, Column, File string }

// RequiredColumns are the columns CheckSchema looks for: at least one for each
// feed table (which proves the table exists), and every column that was added
// later with ALTER TABLE ... ADD COLUMN, because those are what a database that
// predates the change lacks. A column added that way in db/schema must be
// listed here.
var RequiredColumns = []SchemaColumn{
	{"feeds", "secret_refs", feedsSchemaFile},
	{"feed_status", "detail", feedsSchemaFile},
	{"feed_areas", "boxes", feedsSchemaFile},
	{"feed_leases", "fencing_token", feedsSchemaFile},
	{"ingest_replicas", "listener", feedsSchemaFile},
	{"ingest_replicas", "listener_host", feedsSchemaFile},
	{"ingest_replicas", "listener_shared", feedsSchemaFile},
}

// SchemaError reports the required columns the database lacks.
type SchemaError struct{ Missing []SchemaColumn }

// Error is one line, so it stays one record in the JSON logs: what is missing,
// the files that define it, and how to apply db/schema.
func (e *SchemaError) Error() string {
	var missing, files []string
	for _, c := range e.Missing {
		missing = append(missing, schemaName+"."+c.Table+"."+c.Column)
		if !slices.Contains(files, c.File) {
			files = append(files, c.File)
		}
	}
	return "database schema is older than this ingest-service: missing " + strings.Join(missing, ", ") +
		" (" + strings.Join(files, ", ") + "). Apply db/schema in name order, every file is idempotent: " +
		"compose `docker compose up db-schema` or `make db-schema`; on Helm the dbSchema Job applies it " +
		"on install and upgrade (`dbSchema.enabled`, default true); otherwise " +
		"`psql \"$DATABASE_URL\" -v ON_ERROR_STOP=1 -f <file>` for each file"
}

// CheckSchema returns a *SchemaError when the database lacks a column in
// RequiredColumns. db/schema is applied outside the service (the db-schema
// compose job, make db-schema, the Helm dbSchema Job), so an older database is
// a deployment mistake to refuse at startup, not something to repair.
func (s *Store) CheckSchema(ctx context.Context) error {
	return checkColumns(ctx, s.pool, schemaName, RequiredColumns)
}

// querier is the part of a pgx pool that checkColumns uses.
type querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// checkColumns returns a *SchemaError naming the cols that schema lacks, in
// the order given.
func checkColumns(ctx context.Context, q querier, schema string, cols []SchemaColumn) error {
	absent, err := absentColumns(ctx, q, schema, cols)
	if err != nil {
		return fmt.Errorf("checking the database schema: %w", err)
	}
	var missing []SchemaColumn
	for _, c := range cols {
		if absent[c.Table+"."+c.Column] {
			missing = append(missing, c)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return &SchemaError{Missing: missing}
}

// absentColumnsQuery takes the schema ($1) and the tables ($2) and columns
// ($3) as parallel arrays, and returns the pairs the schema does not have.
const absentColumnsQuery = `
	SELECT r.t, r.c
	  FROM unnest($2::text[], $3::text[]) AS r(t, c)
	 WHERE NOT EXISTS (
	       SELECT 1 FROM information_schema.columns ic
	        WHERE ic.table_schema = $1 AND ic.table_name = r.t AND ic.column_name = r.c)`

// absentColumns returns the "table.column" keys of the cols that schema lacks.
func absentColumns(ctx context.Context, q querier, schema string, cols []SchemaColumn) (map[string]bool, error) {
	tables := make([]string, len(cols))
	columns := make([]string, len(cols))
	for i, c := range cols {
		tables[i], columns[i] = c.Table, c.Column
	}
	rows, err := q.Query(ctx, absentColumnsQuery, schema, tables, columns)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	absent := map[string]bool{}
	for rows.Next() {
		var table, column string
		if err := rows.Scan(&table, &column); err != nil {
			return nil, err
		}
		absent[table+"."+column] = true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return absent, nil
}
