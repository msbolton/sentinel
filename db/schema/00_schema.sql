-- =============================================================================
-- SENTINEL schema: schema and shared functions
-- =============================================================================
-- Part of db/schema/, applied in file-name order by the db-schema compose
-- service and `make db-schema`. Every statement is idempotent: re-applying
-- the directory after an edit is the way the schema changes before the first
-- release. Column names are camelCase (quoted) where the Go stores use
-- camelCase. Extensions (postgis, timescaledb, age) are enabled by
-- config/postgres/init-extensions.sql at database initialisation.
-- =============================================================================

CREATE SCHEMA IF NOT EXISTS sentinel;

-- Shared trigger: keep "updatedAt" current on row updates.
CREATE OR REPLACE FUNCTION sentinel.set_updated_at() RETURNS trigger AS $$
BEGIN
  NEW."updatedAt" = NOW();
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;
