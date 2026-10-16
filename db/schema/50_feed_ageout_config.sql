-- =============================================================================
-- SENTINEL schema: feed_ageout_config
-- =============================================================================
-- Part of db/schema/, applied in file-name order by the db-schema compose
-- service and `make db-schema`. Every statement is idempotent: re-applying
-- the directory after an edit is the way the schema changes before the first
-- release. Column names are camelCase (quoted) where the Go stores use
-- camelCase. Extensions (postgis, timescaledb, age) are enabled by
-- config/postgres/init-extensions.sql at database initialisation.
-- =============================================================================

-- -----------------------------------------------------------------------------
-- feed_ageout_config  (apps/entity-service/internal/store/ageout_config_store.go)
-- Three partial unique indexes: the first is the ON CONFLICT arbiter used by
-- Upsert; the other two stop SeedDefaults from inserting duplicate per-source
-- and global defaults on every service restart.
-- -----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS sentinel.feed_ageout_config (
  id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  "feedId"            VARCHAR(255),
  "sourceType"        VARCHAR(50),
  "staleThresholdMs"  INTEGER NOT NULL,
  "ageoutThresholdMs" INTEGER NOT NULL,
  "createdAt"         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  "updatedAt"         TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_ageout_config_feed_source
  ON sentinel.feed_ageout_config ("feedId", "sourceType")
  WHERE "feedId" IS NOT NULL AND "sourceType" IS NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS idx_ageout_config_source_default
  ON sentinel.feed_ageout_config ("sourceType")
  WHERE "feedId" IS NULL AND "sourceType" IS NOT NULL;
-- Databases created by the former TypeORM schema may already hold duplicate
-- global defaults (one per entity-service restart). Keep the newest row.
DELETE FROM sentinel.feed_ageout_config a
  USING sentinel.feed_ageout_config b
  WHERE a."feedId" IS NULL AND a."sourceType" IS NULL
    AND b."feedId" IS NULL AND b."sourceType" IS NULL
    AND (a."createdAt", a.id) < (b."createdAt", b.id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_ageout_config_global_default
  ON sentinel.feed_ageout_config ((1))
  WHERE "feedId" IS NULL AND "sourceType" IS NULL;

CREATE OR REPLACE TRIGGER trg_feed_ageout_config_updated_at
  BEFORE UPDATE ON sentinel.feed_ageout_config
  FOR EACH ROW EXECUTE FUNCTION sentinel.set_updated_at();

-- Legacy repair: align TypeORM-era column definitions (no-ops otherwise).
ALTER TABLE sentinel.feed_ageout_config
  ALTER COLUMN "feedId"    TYPE VARCHAR(255) USING "feedId"::text,
  ALTER COLUMN "createdAt" TYPE TIMESTAMPTZ,
  ALTER COLUMN "updatedAt" TYPE TIMESTAMPTZ,
  ALTER COLUMN id          SET DEFAULT gen_random_uuid();
