package main

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/sentinel/ingest-service/internal/registry"
)

// A failed schema check only means the schema is out of date when CheckSchema
// found columns missing. A timeout or a permissions error says nothing about
// the schema, and telling the operator to apply db/schema for one would hide
// the real cause.
func TestSchemaCheckMessage(t *testing.T) {
	missing := &registry.SchemaError{Missing: []registry.SchemaColumn{
		{Table: "ingest_replicas", Column: "listener", File: "db/schema/70_feeds.sql"},
	}}
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{"columns missing", missing, "database schema is out of date"},
		{"columns missing, wrapped", fmt.Errorf("startup: %w", missing), "database schema is out of date"},
		{
			"a query failure, wrapped as CheckSchema wraps it",
			fmt.Errorf("checking the database schema: %w", errors.New("permission denied for view columns")),
			"checking the database schema failed",
		},
		{
			"a timeout",
			fmt.Errorf("checking the database schema: %w", context.DeadlineExceeded),
			"checking the database schema failed",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := schemaCheckMessage(tc.err); got != tc.want {
				t.Errorf("schemaCheckMessage(%v) = %q, want %q", tc.err, got, tc.want)
			}
		})
	}
}
