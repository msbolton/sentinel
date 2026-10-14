-- =============================================================================
-- SENTINEL baseline schema
-- =============================================================================
-- Source of truth for every table the Go services, the API gateway, and the
-- analytics service read or write. Column names are camelCase (quoted) where
-- the Go stores use camelCase, matching the original TypeORM-created schema.
-- Extensions (postgis, timescaledb, age) are enabled by
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
  "countryOfOrigin"       VARCHAR(8),
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

-- -----------------------------------------------------------------------------
-- track_points  (apps/track-service/internal/store/track_store.go)
-- No primary key: TimescaleDB requires the partition column in every unique
-- index, and the service never looks points up by id.
-- -----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS sentinel.track_points (
  id                     UUID NOT NULL DEFAULT gen_random_uuid(),
  "entityId"             UUID NOT NULL,
  position               geometry(Point, 4326) NOT NULL,
  heading                DOUBLE PRECISION,
  "speedKnots"           DOUBLE PRECISION,
  course                 DOUBLE PRECISION,
  source                 VARCHAR(50),
  "feedId"               VARCHAR(255),
  altitude               DOUBLE PRECISION,
  "trackProcessingState" VARCHAR(50),
  "velocityNorth"        DOUBLE PRECISION,
  "velocityEast"         DOUBLE PRECISION,
  "velocityUp"           DOUBLE PRECISION,
  "accelNorth"           DOUBLE PRECISION,
  "accelEast"            DOUBLE PRECISION,
  "accelUp"              DOUBLE PRECISION,
  "posCovariance"        DOUBLE PRECISION[],
  "posVelCovariance"     DOUBLE PRECISION[],
  "velCovariance"        DOUBLE PRECISION[],
  "circularError"        DOUBLE PRECISION,
  "altitudeError"        DOUBLE PRECISION,
  "sensorId"             VARCHAR(255),
  "timestamp"            TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_track_points_entity_time ON sentinel.track_points ("entityId", "timestamp" DESC);

DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'timescaledb') THEN
    PERFORM create_hypertable('sentinel.track_points', 'timestamp', if_not_exists => TRUE, migrate_data => TRUE);
    RAISE NOTICE 'sentinel.track_points is a TimescaleDB hypertable.';
  ELSE
    RAISE NOTICE 'TimescaleDB not installed; sentinel.track_points is a regular table.';
  END IF;
END
$$;

-- -----------------------------------------------------------------------------
-- alerts / alert_rules  (apps/alert-service/internal/store/alert_store.go)
-- -----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS sentinel.alerts (
  id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  "alertType"        VARCHAR(50) NOT NULL,
  severity           VARCHAR(20) NOT NULL,
  title              VARCHAR(500) NOT NULL,
  description        TEXT,
  "entityId"         UUID NOT NULL,
  "relatedEntityIds" UUID[] DEFAULT ARRAY[]::uuid[],
  position           geometry(Point, 4326),
  "ruleId"           UUID,
  metadata           JSONB DEFAULT '{}'::jsonb,
  "createdAt"        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  "acknowledgedAt"   TIMESTAMPTZ,
  "acknowledgedBy"   VARCHAR(255),
  "resolvedAt"       TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_alerts_entity   ON sentinel.alerts ("entityId");
CREATE INDEX IF NOT EXISTS idx_alerts_severity ON sentinel.alerts (severity);
CREATE INDEX IF NOT EXISTS idx_alerts_type     ON sentinel.alerts ("alertType");
CREATE INDEX IF NOT EXISTS idx_alerts_created  ON sentinel.alerts ("createdAt" DESC);

CREATE TABLE IF NOT EXISTS sentinel.alert_rules (
  id                     UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  name                   VARCHAR(255) NOT NULL,
  "ruleType"             VARCHAR(50) NOT NULL,
  config                 JSONB DEFAULT '{}'::jsonb,
  "monitoredEntityTypes" TEXT[] DEFAULT ARRAY[]::text[],
  severity               VARCHAR(20) NOT NULL DEFAULT 'MEDIUM',
  enabled                BOOLEAN NOT NULL DEFAULT TRUE,
  "createdAt"            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  "updatedAt"            TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE OR REPLACE TRIGGER trg_alert_rules_updated_at
  BEFORE UPDATE ON sentinel.alert_rules
  FOR EACH ROW EXECUTE FUNCTION sentinel.set_updated_at();

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
CREATE UNIQUE INDEX IF NOT EXISTS idx_ageout_config_global_default
  ON sentinel.feed_ageout_config ((1))
  WHERE "feedId" IS NULL AND "sourceType" IS NULL;

CREATE OR REPLACE TRIGGER trg_feed_ageout_config_updated_at
  BEFORE UPDATE ON sentinel.feed_ageout_config
  FOR EACH ROW EXECUTE FUNCTION sentinel.set_updated_at();

-- -----------------------------------------------------------------------------
-- anomalies  (apps/analytics-service/sentinel_analytics/db.py)
-- -----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS sentinel.anomalies (
  id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  entity_id      UUID NOT NULL,
  anomaly_type   VARCHAR(50) NOT NULL,
  severity       VARCHAR(20) NOT NULL,
  score          DOUBLE PRECISION,
  description    TEXT,
  expected_value DOUBLE PRECISION,
  actual_value   DOUBLE PRECISION,
  position       geometry(Point, 4326),
  detected_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_anomalies_entity_time ON sentinel.anomalies (entity_id, detected_at DESC);
