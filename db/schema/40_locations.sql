-- =============================================================================
-- SENTINEL schema: locations
-- =============================================================================
-- Part of db/schema/, applied in file-name order by the db-schema compose
-- service and `make db-schema`. Every statement is idempotent: re-applying
-- the directory after an edit is the way the schema changes before the first
-- release. Column names are camelCase (quoted) where the Go stores use
-- camelCase. Extensions (postgis, timescaledb, age) are enabled by
-- config/postgres/init-extensions.sql at database initialisation.
-- =============================================================================

-- -----------------------------------------------------------------------------
-- locations  (API gateway)
-- -----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS sentinel.locations (
  id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  name        VARCHAR(255) NOT NULL,
  description TEXT,
  latitude    DOUBLE PRECISION NOT NULL,
  longitude   DOUBLE PRECISION NOT NULL,
  altitude    DOUBLE PRECISION NOT NULL DEFAULT 1000,
  heading     DOUBLE PRECISION NOT NULL DEFAULT 0,
  pitch       DOUBLE PRECISION NOT NULL DEFAULT -45,
  "range"     DOUBLE PRECISION NOT NULL DEFAULT 2000,
  "has3dTiles" BOOLEAN NOT NULL DEFAULT FALSE,
  category    VARCHAR(50) NOT NULL DEFAULT 'CUSTOM',
  "createdBy" VARCHAR(255),
  "createdAt" TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  "updatedAt" TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_locations_category ON sentinel.locations (category);

CREATE OR REPLACE TRIGGER trg_locations_updated_at
  BEFORE UPDATE ON sentinel.locations
  FOR EACH ROW EXECUTE FUNCTION sentinel.set_updated_at();

-- Legacy repair: TypeORM created category as an enum; convert it to VARCHAR
-- and align the remaining column definitions (no-ops on a database this file
-- created).
DO $$
BEGIN
  IF EXISTS (
    SELECT 1 FROM information_schema.columns
    WHERE table_schema = 'sentinel' AND table_name = 'locations' AND column_name = 'category' AND data_type = 'USER-DEFINED'
  ) THEN
    ALTER TABLE sentinel.locations ALTER COLUMN category TYPE VARCHAR(50) USING category::text;
    RAISE NOTICE 'Converted sentinel.locations.category from enum to varchar';
  END IF;
END
$$;

ALTER TABLE sentinel.locations
  ALTER COLUMN category    SET DEFAULT 'CUSTOM',
  ALTER COLUMN "createdAt" TYPE TIMESTAMPTZ,
  ALTER COLUMN "updatedAt" TYPE TIMESTAMPTZ,
  ALTER COLUMN id          SET DEFAULT gen_random_uuid();

DROP TYPE IF EXISTS sentinel.locations_category_enum;
