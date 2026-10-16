-- =============================================================================
-- SENTINEL schema: track_points
-- =============================================================================
-- Part of db/schema/, applied in file-name order by the db-schema compose
-- service and `make db-schema`. Every statement is idempotent: re-applying
-- the directory after an edit is the way the schema changes before the first
-- release. Column names are camelCase (quoted) where the Go stores use
-- camelCase. Extensions (postgis, timescaledb, age) are enabled by
-- config/postgres/init-extensions.sql at database initialisation.
-- =============================================================================

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

-- Columns added with the observation store; a no-op on a database this file
-- created, present for databases created before them.
ALTER TABLE sentinel.track_points
  ADD COLUMN IF NOT EXISTS "observationId"  UUID,
  ADD COLUMN IF NOT EXISTS "sourceEntityId" VARCHAR(255),
  ADD COLUMN IF NOT EXISTS "entityType"     VARCHAR(50);

-- Legacy repair: feedId was a UUID under TypeORM; feed ids are slugs.
ALTER TABLE sentinel.track_points
  ALTER COLUMN "feedId" TYPE VARCHAR(255) USING "feedId"::text;
