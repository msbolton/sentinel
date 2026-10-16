-- =============================================================================
-- SENTINEL schema: entities
-- =============================================================================
-- Part of db/schema/, applied in file-name order by the db-schema compose
-- service and `make db-schema`. Every statement is idempotent: re-applying
-- the directory after an edit is the way the schema changes before the first
-- release. Column names are camelCase (quoted) where the Go stores use
-- camelCase. Extensions (postgis, timescaledb, age) are enabled by
-- config/postgres/init-extensions.sql at database initialisation.
-- =============================================================================

-- -----------------------------------------------------------------------------
-- entities  (apps/entity-service/internal/store/entity_store.go)
-- -----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS sentinel.entities (
  id                      UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  "entityType"            VARCHAR(50) NOT NULL,
  name                    VARCHAR(255) NOT NULL,
  description             TEXT,
  source                  VARCHAR(50) NOT NULL,
  classification          VARCHAR(50) NOT NULL DEFAULT 'UNCLASSIFIED',
  "feedId"                VARCHAR(255),
  position                geometry(Point, 4326),
  heading                 DOUBLE PRECISION,
  "speedKnots"            DOUBLE PRECISION,
  course                  DOUBLE PRECISION,
  altitude                DOUBLE PRECISION,
  "milStd2525dSymbol"     VARCHAR(50),
  metadata                JSONB DEFAULT '{}'::jsonb,
  affiliations            TEXT[] DEFAULT ARRAY[]::text[],
  affiliation             VARCHAR(50) DEFAULT 'UNKNOWN',
  "identityConfidence"    SMALLINT DEFAULT 0,
  characterization        VARCHAR(50) DEFAULT 'UNCHARACTERIZED',
  "trackEnvironment"      VARCHAR(50) DEFAULT 'UNKNOWN',
  "trackProcessingState"  VARCHAR(50) DEFAULT 'LIVE',
  pitch                   DOUBLE PRECISION,
  roll                    DOUBLE PRECISION,
  "operationalStatus"     VARCHAR(50) DEFAULT 'UNKNOWN',
  "damageAssessment"      VARCHAR(50) DEFAULT 'UNKNOWN',
  "damageConfidence"      SMALLINT DEFAULT 0,
  "dimensionLength"       DOUBLE PRECISION,
  "dimensionWidth"        DOUBLE PRECISION,
  "dimensionHeight"       DOUBLE PRECISION,
  "countryOfOrigin"       VARCHAR(100),   -- OpenSky sends full country names
  kinematics              JSONB DEFAULT '{}'::jsonb,
  "platformData"          JSONB DEFAULT '{}'::jsonb,
  "circularError"         DOUBLE PRECISION,
  "lastObservationSource" VARCHAR(100),
  "sourceEntityId"        VARCHAR(255),
  "ageoutState"           VARCHAR(20) NOT NULL DEFAULT 'LIVE',
  "createdAt"             TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  "updatedAt"             TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  "lastSeenAt"            TIMESTAMPTZ,
  deleted                 BOOLEAN NOT NULL DEFAULT FALSE,
  "deletedAt"             TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_entities_position         ON sentinel.entities USING GIST (position);
CREATE INDEX IF NOT EXISTS idx_entities_entity_type      ON sentinel.entities ("entityType");
CREATE INDEX IF NOT EXISTS idx_entities_source           ON sentinel.entities (source);
CREATE INDEX IF NOT EXISTS idx_entities_classification   ON sentinel.entities (classification);
CREATE INDEX IF NOT EXISTS idx_entities_last_seen_at     ON sentinel.entities ("lastSeenAt");
CREATE INDEX IF NOT EXISTS idx_entities_feed_id          ON sentinel.entities ("feedId");
CREATE INDEX IF NOT EXISTS idx_entities_source_entity_id ON sentinel.entities ("sourceEntityId") WHERE deleted = FALSE;
CREATE INDEX IF NOT EXISTS idx_entities_ageout_state     ON sentinel.entities ("ageoutState", "lastSeenAt");

CREATE OR REPLACE TRIGGER trg_entities_updated_at
  BEFORE UPDATE ON sentinel.entities
  FOR EACH ROW EXECUTE FUNCTION sentinel.set_updated_at();

-- Columns added with the observation store; a no-op on a database this file
-- created, present for databases created before them.
ALTER TABLE sentinel.entities
  ADD COLUMN IF NOT EXISTS "lastObservedAt"       TIMESTAMPTZ,
  ADD COLUMN IF NOT EXISTS "lastFeedId"           VARCHAR(64),
  ADD COLUMN IF NOT EXISTS "primaryIdentityType"  VARCHAR(30),
  ADD COLUMN IF NOT EXISTS "primaryIdentityValue" VARCHAR(255);

-- Legacy repair: TypeORM created several columns as enum types whose value
-- sets drifted from what the feeds and client emit. Convert them to VARCHAR so
-- the column types match the definition above on every database.
DO $$
DECLARE
  col TEXT;
BEGIN
  FOREACH col IN ARRAY ARRAY['entityType', 'source', 'classification'] LOOP
    IF EXISTS (
      SELECT 1 FROM information_schema.columns
      WHERE table_schema = 'sentinel' AND table_name = 'entities' AND column_name = col AND data_type = 'USER-DEFINED'
    ) THEN
      EXECUTE format('ALTER TABLE sentinel.entities ALTER COLUMN %I TYPE VARCHAR(50) USING %I::text', col, col);
      RAISE NOTICE 'Converted sentinel.entities.% from enum to varchar', col;
    END IF;
  END LOOP;
END
$$;

-- Legacy repair: align the remaining TypeORM-era column definitions with the
-- CREATE TABLE above. Every statement is a no-op on a database this file
-- created. On a legacy database it relaxes NOT NULL on columns the Go stores
-- write as NULL, widens countryOfOrigin, turns feedId from UUID into VARCHAR,
-- moves timestamps to TIMESTAMPTZ, and replaces enum-typed defaults so the
-- enum types themselves can be dropped.
ALTER TABLE sentinel.entities
  ALTER COLUMN metadata               DROP NOT NULL,
  ALTER COLUMN affiliations           DROP NOT NULL,
  ALTER COLUMN affiliation            DROP NOT NULL,
  ALTER COLUMN "identityConfidence"   DROP NOT NULL,
  ALTER COLUMN characterization       DROP NOT NULL,
  ALTER COLUMN "trackEnvironment"     DROP NOT NULL,
  ALTER COLUMN "trackProcessingState" DROP NOT NULL,
  ALTER COLUMN "operationalStatus"    DROP NOT NULL,
  ALTER COLUMN "damageAssessment"     DROP NOT NULL,
  ALTER COLUMN "damageConfidence"     DROP NOT NULL,
  ALTER COLUMN kinematics             DROP NOT NULL,
  ALTER COLUMN "platformData"         DROP NOT NULL,
  ALTER COLUMN "countryOfOrigin"      TYPE VARCHAR(100),
  ALTER COLUMN "feedId"               TYPE VARCHAR(255) USING "feedId"::text,
  ALTER COLUMN "createdAt"            TYPE TIMESTAMPTZ,
  ALTER COLUMN "updatedAt"            TYPE TIMESTAMPTZ,
  ALTER COLUMN classification         SET DEFAULT 'UNCLASSIFIED',
  ALTER COLUMN id                     SET DEFAULT gen_random_uuid();

DROP TYPE IF EXISTS sentinel.entities_entitytype_enum;
DROP TYPE IF EXISTS sentinel.entities_source_enum;
DROP TYPE IF EXISTS sentinel.entities_classification_enum;
