-- =============================================================================
-- SENTINEL schema: legacy cleanup
-- =============================================================================
-- Part of db/schema/, applied in file-name order by the db-schema compose
-- service and `make db-schema`. Every statement is idempotent: re-applying
-- the directory after an edit is the way the schema changes before the first
-- release. Column names are camelCase (quoted) where the Go stores use
-- camelCase. Extensions (postgis, timescaledb, age) are enabled by
-- config/postgres/init-extensions.sql at database initialisation.
-- =============================================================================

-- The schema used to be applied with golang-migrate, which recorded the
-- applied version in schema_migrations (in "sentinel" when the connection set
-- search_path, otherwise in "public"). The schema directory is applied in
-- full every time, so the bookkeeping table is removed wherever it is.
DROP TABLE IF EXISTS sentinel.schema_migrations;
DROP TABLE IF EXISTS public.schema_migrations;
