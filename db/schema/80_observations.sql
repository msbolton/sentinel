-- =============================================================================
-- SENTINEL schema: observations
-- =============================================================================
-- Part of db/schema/, applied in file-name order by the db-schema compose
-- service and `make db-schema`. Every statement is idempotent: re-applying
-- the directory after an edit is the way the schema changes before the first
-- release. Column names are camelCase (quoted) where the Go stores use
-- camelCase. Extensions (postgis, timescaledb, age) are enabled by
-- config/postgres/init-extensions.sql at database initialisation.
-- =============================================================================

-- -----------------------------------------------------------------------------
-- observations  (apps/entity-service/internal/store/observation_store.go)
-- One row per report from any feed, accepted or not, keyed by the ingest
-- service's deterministic observation id so redelivery is idempotent.
-- -----------------------------------------------------------------------------
-- Development databases may still carry the NestJS-era observations table
-- (TypeORM shape: "id", "timestamp", "rawData"). No Go service ever wrote to
-- it, so it is replaced rather than migrated. The check is the absence of
-- "observedAt", which every version of this file has defined.
DO $$
DECLARE n BIGINT;
BEGIN
  IF EXISTS (SELECT 1 FROM information_schema.tables
              WHERE table_schema = 'sentinel' AND table_name = 'observations')
     AND NOT EXISTS (SELECT 1 FROM information_schema.columns
                      WHERE table_schema = 'sentinel' AND table_name = 'observations' AND column_name = 'observedAt') THEN
    EXECUTE 'SELECT count(*) FROM sentinel.observations' INTO n;
    RAISE NOTICE 'dropping legacy sentinel.observations (% rows, pre-refactor shape)', n;
    DROP TABLE sentinel.observations;
  END IF;
END
$$;

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
  "rejectReason"     VARCHAR(30),                   -- DUPLICATE | STALE_FIX | INVALID_POSITION | ENTITY_DELETED
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
