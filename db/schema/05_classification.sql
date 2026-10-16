-- =============================================================================
-- SENTINEL schema: classification helpers
-- =============================================================================
-- Part of db/schema/, applied in file-name order by the db-schema compose
-- service and `make db-schema`. Every statement is idempotent: re-applying
-- the directory after an edit is the way the schema changes before the first
-- release. Column names are camelCase (quoted) where the Go stores use
-- camelCase. Extensions (postgis, timescaledb, age) are enabled by
-- config/postgres/init-extensions.sql at database initialisation.
-- =============================================================================

-- Classification levels are stored as their sentinel.v1.Classification names
-- (UNCLASSIFIED < CONFIDENTIAL < SECRET < TOP_SECRET). These helpers give the
-- stores one ordering for ceilings and high-water marks; the Go services use
-- the same order from the protobuf enum. Unknown or NULL values rank as
-- UNCLASSIFIED so a missing level never grants access.
CREATE OR REPLACE FUNCTION sentinel.classification_rank(level TEXT) RETURNS SMALLINT
LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
  SELECT CASE upper(level)
           WHEN 'TOP_SECRET'   THEN 3
           WHEN 'SECRET'       THEN 2
           WHEN 'CONFIDENTIAL' THEN 1
           ELSE 0
         END::smallint
$$;

CREATE OR REPLACE FUNCTION sentinel.classification_max(a TEXT, b TEXT) RETURNS TEXT
LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
  SELECT CASE WHEN sentinel.classification_rank(b) > sentinel.classification_rank(a)
              THEN upper(b)
              ELSE COALESCE(upper(a), 'UNCLASSIFIED')
         END
$$;
