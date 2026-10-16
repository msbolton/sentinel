-- =============================================================================
-- SENTINEL schema: links
-- =============================================================================
-- Part of db/schema/, applied in file-name order by the db-schema compose
-- service and `make db-schema`. Every statement is idempotent: re-applying
-- the directory after an edit is the way the schema changes before the first
-- release. Column names are camelCase (quoted) where the Go stores use
-- camelCase. Extensions (postgis, timescaledb, age) are enabled by
-- config/postgres/init-extensions.sql at database initialisation.
-- =============================================================================

-- -----------------------------------------------------------------------------
-- links  (apps/link-analysis-service/internal/store/link_store.go)
-- -----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS sentinel.links (
  id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  "sourceEntityId" UUID NOT NULL,
  "targetEntityId" UUID NOT NULL,
  "linkType"       VARCHAR(50) NOT NULL,
  confidence       DOUBLE PRECISION NOT NULL DEFAULT 0.5,
  description      TEXT,
  evidence         TEXT[] DEFAULT ARRAY[]::text[],
  "firstObserved"  TIMESTAMPTZ,
  "lastObserved"   TIMESTAMPTZ,
  metadata         JSONB DEFAULT '{}'::jsonb,
  "createdAt"      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_links_source_entity ON sentinel.links ("sourceEntityId");
CREATE INDEX IF NOT EXISTS idx_links_target_entity ON sentinel.links ("targetEntityId");
CREATE INDEX IF NOT EXISTS idx_links_type          ON sentinel.links ("linkType");
