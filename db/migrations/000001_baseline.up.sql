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

-- -----------------------------------------------------------------------------
-- track_points  (apps/track-service/internal/store/track_store.go)
-- No primary key: TimescaleDB requires the partition column in every unique
-- index, and the service never looks points up by id.
-- -----------------------------------------------------------------------------
DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'sentinel' AND table_name = 'track_points' AND column_name = 'entity_id') THEN
    DROP TABLE sentinel.track_points;
    RAISE NOTICE 'Dropped legacy snake_case sentinel.track_points';
  END IF;
END
$$;

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
-- Legacy repair: the retired scripts/seed-data.ts created alerts, alert_rules,
-- and links with snake_case columns that no service could ever write to. When
-- such a table is found it is dropped so the camelCase definition below can
-- replace it. Any seed rule is restored by db/seed/seed.sql.
-- -----------------------------------------------------------------------------
DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'sentinel' AND table_name = 'alerts' AND column_name = 'entity_id') THEN
    DROP TABLE sentinel.alerts;
    RAISE NOTICE 'Dropped legacy snake_case sentinel.alerts';
  END IF;
  IF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'sentinel' AND table_name = 'alert_rules' AND column_name = 'rule_type') THEN
    DROP TABLE sentinel.alert_rules;
    RAISE NOTICE 'Dropped legacy snake_case sentinel.alert_rules';
  END IF;
  IF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'sentinel' AND table_name = 'links' AND column_name = 'source_entity_id') THEN
    DROP TABLE sentinel.links;
    RAISE NOTICE 'Dropped legacy snake_case sentinel.links';
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

-- Legacy repair: TypeORM created several columns as enum types whose value
-- sets drifted from what the feeds and client emit. Convert them to VARCHAR so
-- the column types match this baseline on every database.
DO $$
DECLARE
  pair TEXT[];
BEGIN
  FOREACH pair SLICE 1 IN ARRAY ARRAY[
    ['entities', 'entityType'], ['entities', 'source'], ['entities', 'classification'],
    ['locations', 'category']
  ] LOOP
    IF EXISTS (
      SELECT 1 FROM information_schema.columns
      WHERE table_schema = 'sentinel' AND table_name = pair[1] AND column_name = pair[2] AND data_type = 'USER-DEFINED'
    ) THEN
      EXECUTE format('ALTER TABLE sentinel.%I ALTER COLUMN %I TYPE VARCHAR(50) USING %I::text', pair[1], pair[2], pair[2]);
      RAISE NOTICE 'Converted sentinel.%.% from enum to varchar', pair[1], pair[2];
    END IF;
  END LOOP;
END
$$;

-- Legacy repair: align the remaining TypeORM-era column definitions with the
-- CREATE TABLE statements above. Every statement is a no-op on a database this
-- file created. On a legacy database it relaxes NOT NULL on columns the Go
-- stores write as NULL, widens countryOfOrigin, turns feedId from UUID into
-- VARCHAR, moves timestamps to TIMESTAMPTZ, and replaces enum-typed defaults
-- so the enum types themselves can be dropped.
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

ALTER TABLE sentinel.track_points
  ALTER COLUMN "feedId" TYPE VARCHAR(255) USING "feedId"::text;

ALTER TABLE sentinel.locations
  ALTER COLUMN category    SET DEFAULT 'CUSTOM',
  ALTER COLUMN "createdAt" TYPE TIMESTAMPTZ,
  ALTER COLUMN "updatedAt" TYPE TIMESTAMPTZ,
  ALTER COLUMN id          SET DEFAULT gen_random_uuid();

ALTER TABLE sentinel.feed_ageout_config
  ALTER COLUMN "feedId"    TYPE VARCHAR(255) USING "feedId"::text,
  ALTER COLUMN "createdAt" TYPE TIMESTAMPTZ,
  ALTER COLUMN "updatedAt" TYPE TIMESTAMPTZ,
  ALTER COLUMN id          SET DEFAULT gen_random_uuid();

DROP TYPE IF EXISTS sentinel.entities_entitytype_enum;
DROP TYPE IF EXISTS sentinel.entities_source_enum;
DROP TYPE IF EXISTS sentinel.entities_classification_enum;
DROP TYPE IF EXISTS sentinel.locations_category_enum;

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

-- =============================================================================
-- Feed architecture (observations, identity-based fusion, feed registry)
-- =============================================================================

-- -----------------------------------------------------------------------------
-- feeds  (apps/ingest-service/internal/registry)
-- Desired state of every feed, built-in or user-defined. id is an immutable
-- slug that entities."feedId", track_points."feedId" and
-- feed_ageout_config."feedId" store as-is.
-- -----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS sentinel.feeds (
  id           VARCHAR(64)  PRIMARY KEY CHECK (id ~ '^[a-z0-9][a-z0-9-]{0,62}$'),
  name         VARCHAR(255) NOT NULL,
  description  TEXT         NOT NULL DEFAULT '',
  driver       VARCHAR(64)  NOT NULL,                 -- key in the ingest driver registry
  config       JSONB        NOT NULL DEFAULT '{}',    -- validated against the driver's JSON schema
  secret_refs  JSONB        NOT NULL DEFAULT '{}',    -- {"clientSecret": "env:OPENSKY_CLIENT_SECRET"}; never plaintext
  enabled      BOOLEAN      NOT NULL DEFAULT FALSE,   -- desired state
  builtin      BOOLEAN      NOT NULL DEFAULT FALSE,   -- seeded; id and driver immutable, not deletable
  version      INTEGER      NOT NULL DEFAULT 1,       -- bumped on every write; the owner restarts when behind
  created_by   VARCHAR(255),
  created_at   TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
  updated_at   TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

-- Observed state, written only by the ingest replica that owns the feed.
CREATE TABLE IF NOT EXISTS sentinel.feed_status (
  feed_id          VARCHAR(64) PRIMARY KEY REFERENCES sentinel.feeds(id) ON DELETE CASCADE,
  owner            VARCHAR(128),
  state            VARCHAR(16) NOT NULL DEFAULT 'stopped',   -- stopped|starting|running|idle|degraded|error
  health           VARCHAR(16) NOT NULL DEFAULT 'unknown',   -- healthy|warn|critical|unknown
  last_error       TEXT,
  last_message_at  TIMESTAMPTZ,
  last_success_at  TIMESTAMPTZ,
  messages_total   BIGINT  NOT NULL DEFAULT 0,
  entities_last    INTEGER NOT NULL DEFAULT 0,
  errors_total     BIGINT  NOT NULL DEFAULT 0,
  applied_version  INTEGER NOT NULL DEFAULT 0,
  detail           JSONB   NOT NULL DEFAULT '{}',            -- driver-specific: credits, effectiveIntervalSec, idle
  updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Area-of-interest lease pushed by the API gateway from the connected
-- clients' viewports; read by whichever replica owns the feed.
CREATE TABLE IF NOT EXISTS sentinel.feed_areas (
  feed_id     VARCHAR(64) PRIMARY KEY REFERENCES sentinel.feeds(id) ON DELETE CASCADE,
  active      BOOLEAN NOT NULL DEFAULT FALSE,
  clients     INTEGER NOT NULL DEFAULT 0,
  boxes       JSONB   NOT NULL DEFAULT '[]',
  expires_at  TIMESTAMPTZ,
  updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Feed ownership across ingest replicas. A feed runs on the replica holding
-- an unexpired lease; the fencing token guards against a stale owner.
CREATE TABLE IF NOT EXISTS sentinel.ingest_replicas (
  replica_id    VARCHAR(128) PRIMARY KEY,
  started_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  heartbeat_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  expires_at    TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS sentinel.feed_leases (
  feed_id        VARCHAR(64) PRIMARY KEY REFERENCES sentinel.feeds(id) ON DELETE CASCADE,
  owner_id       VARCHAR(128) NOT NULL,
  fencing_token  BIGINT NOT NULL DEFAULT 1,
  acquired_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  heartbeat_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  expires_at     TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_feed_leases_expires ON sentinel.feed_leases (expires_at);

-- The ingest service used to create its own custom_feeds table at startup in
-- the default schema; the feeds table above replaces it.
DROP TABLE IF EXISTS public.custom_feeds;
DROP TABLE IF EXISTS sentinel.custom_feeds;

-- -----------------------------------------------------------------------------
-- observations  (apps/entity-service/internal/store/observation_store.go)
-- One row per report from any feed, accepted or not, keyed by the ingest
-- service's deterministic observation id so redelivery is idempotent.
-- -----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS sentinel.observations (
  "observationId"    UUID NOT NULL,
  "observedAt"       TIMESTAMPTZ NOT NULL,          -- source timestamp, clamped to receivedAt + 60 s
  "receivedAt"       TIMESTAMPTZ NOT NULL,
  "ingestedAt"       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  "entityId"         UUID NOT NULL,
  "feedId"           VARCHAR(64) NOT NULL,
  driver             VARCHAR(50) NOT NULL,
  decoder            VARCHAR(50),
  "sourceEntityId"   VARCHAR(255) NOT NULL,
  source             VARCHAR(50) NOT NULL,
  "sensorId"         VARCHAR(255),
  accepted           BOOLEAN NOT NULL,
  "rejectReason"     VARCHAR(30),                   -- DUPLICATE | STALE_FIX | INVALID_POSITION
  "entityType"       VARCHAR(50),
  position           geometry(Point, 4326),
  altitude           DOUBLE PRECISION,
  heading            DOUBLE PRECISION,
  "speedKnots"       DOUBLE PRECISION,
  course             DOUBLE PRECISION,
  "velocityNorth"    DOUBLE PRECISION,
  "velocityEast"     DOUBLE PRECISION,
  "velocityUp"       DOUBLE PRECISION,
  "accelNorth"       DOUBLE PRECISION,
  "accelEast"        DOUBLE PRECISION,
  "accelUp"          DOUBLE PRECISION,
  "circularError"    DOUBLE PRECISION,
  "altitudeError"    DOUBLE PRECISION,
  "posCovariance"    DOUBLE PRECISION[],
  "posVelCovariance" DOUBLE PRECISION[],
  "velCovariance"    DOUBLE PRECISION[],
  identities         JSONB NOT NULL DEFAULT '[]',
  platform           JSONB,                         -- PlatformDetail as platform JSON
  "rawContentType"   VARCHAR(100),
  raw                BYTEA,
  "rawTruncated"     BOOLEAN NOT NULL DEFAULT FALSE,
  PRIMARY KEY ("observedAt", "observationId")
);

CREATE INDEX IF NOT EXISTS idx_observations_entity_time ON sentinel.observations ("entityId", "observedAt" DESC);
CREATE INDEX IF NOT EXISTS idx_observations_feed_source ON sentinel.observations ("feedId", "sourceEntityId", "observedAt" DESC);

DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'timescaledb') THEN
    PERFORM create_hypertable('sentinel.observations', 'observedAt', chunk_time_interval => INTERVAL '6 hours', if_not_exists => TRUE, migrate_data => TRUE);
    IF NOT EXISTS (SELECT 1 FROM timescaledb_information.compression_settings WHERE hypertable_schema = 'sentinel' AND hypertable_name = 'observations') THEN
      ALTER TABLE sentinel.observations SET (timescaledb.compress, timescaledb.compress_segmentby = '"entityId"', timescaledb.compress_orderby = '"observedAt" DESC');
    END IF;
    PERFORM add_compression_policy('sentinel.observations', INTERVAL '1 day', if_not_exists => TRUE);
    PERFORM add_retention_policy('sentinel.observations', INTERVAL '7 days', if_not_exists => TRUE);
    RAISE NOTICE 'sentinel.observations is a TimescaleDB hypertable (compress after 1 day, drop after 7 days).';
  ELSE
    RAISE NOTICE 'TimescaleDB not installed; sentinel.observations is a regular table.';
  END IF;
END
$$;

-- -----------------------------------------------------------------------------
-- entity_identities / entity_sources  (apps/entity-service/internal/fusion)
-- Natural identifiers resolve observations from different feeds to one
-- entity; the FEED_LOCAL value is "<feedId>:<sourceEntityId>".
-- -----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS sentinel.entity_identities (
  "identityType"  VARCHAR(30) NOT NULL,
  "identityValue" VARCHAR(255) NOT NULL,
  "entityId"      UUID NOT NULL REFERENCES sentinel.entities(id) ON DELETE CASCADE,
  "firstSeenAt"   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  "lastSeenAt"    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  PRIMARY KEY ("identityType", "identityValue")
);

CREATE INDEX IF NOT EXISTS idx_entity_identities_entity ON sentinel.entity_identities ("entityId");

CREATE TABLE IF NOT EXISTS sentinel.entity_sources (
  "entityId"         UUID NOT NULL REFERENCES sentinel.entities(id) ON DELETE CASCADE,
  "feedId"           VARCHAR(64) NOT NULL,
  source             VARCHAR(50) NOT NULL,
  "sourceEntityId"   VARCHAR(255) NOT NULL,
  "lastObservedAt"   TIMESTAMPTZ NOT NULL,
  "lastReceivedAt"   TIMESTAMPTZ NOT NULL,
  "observationCount" BIGINT NOT NULL DEFAULT 0,
  PRIMARY KEY ("entityId", "feedId")
);

ALTER TABLE sentinel.entities
  ADD COLUMN IF NOT EXISTS "lastObservedAt"       TIMESTAMPTZ,
  ADD COLUMN IF NOT EXISTS "lastFeedId"           VARCHAR(64),
  ADD COLUMN IF NOT EXISTS "primaryIdentityType"  VARCHAR(30),
  ADD COLUMN IF NOT EXISTS "primaryIdentityValue" VARCHAR(255);

ALTER TABLE sentinel.track_points
  ADD COLUMN IF NOT EXISTS "observationId"  UUID,
  ADD COLUMN IF NOT EXISTS "sourceEntityId" VARCHAR(255),
  ADD COLUMN IF NOT EXISTS "entityType"     VARCHAR(50);

-- Development backfill: entities ingested before the identity table existed
-- carry "ICAO-<hex>" or "MMSI-<digits>" source ids. Register them so the
-- fusion path finds them instead of creating duplicates. Where two rows share
-- a source id only the most recently seen one is registered.
INSERT INTO sentinel.entity_identities ("identityType", "identityValue", "entityId", "firstSeenAt", "lastSeenAt")
SELECT DISTINCT ON (upper(substr(e."sourceEntityId", 6)))
       'ICAO24', upper(substr(e."sourceEntityId", 6)), e.id, e."createdAt", COALESCE(e."lastSeenAt", e."updatedAt")
  FROM sentinel.entities e
 WHERE e.deleted = FALSE AND e."sourceEntityId" ~ '^ICAO-[0-9A-Fa-f]{6}$'
 ORDER BY upper(substr(e."sourceEntityId", 6)), e."lastSeenAt" DESC NULLS LAST
ON CONFLICT DO NOTHING;

INSERT INTO sentinel.entity_identities ("identityType", "identityValue", "entityId", "firstSeenAt", "lastSeenAt")
SELECT DISTINCT ON (substr(e."sourceEntityId", 6))
       'MMSI', substr(e."sourceEntityId", 6), e.id, e."createdAt", COALESCE(e."lastSeenAt", e."updatedAt")
  FROM sentinel.entities e
 WHERE e.deleted = FALSE AND e."sourceEntityId" ~ '^MMSI-[0-9]{7,9}$'
 ORDER BY substr(e."sourceEntityId", 6), e."lastSeenAt" DESC NULLS LAST
ON CONFLICT DO NOTHING;
