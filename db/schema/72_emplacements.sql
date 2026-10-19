-- =============================================================================
-- SENTINEL schema: emplacements
-- =============================================================================
-- Part of db/schema/, applied in file-name order by the db-schema compose
-- service and `make db-schema`. Every statement is idempotent: re-applying
-- the directory after an edit is the way the schema changes before the first
-- release. Column names are camelCase (quoted) where the Go stores use
-- camelCase.
--
-- An emplacement is a place observations come from: a radar site, a mast, a
-- ship's radar, a field observer. Users manage them through the ingest
-- management API (/api/v1/emplacements); every write sends a
-- sentinel_emplacements notification so each ingest replica's cache follows.
-- The built-in `emplacements` feed reports each one as a SENSOR entity.
-- =============================================================================

CREATE TABLE IF NOT EXISTS sentinel.emplacements (
  id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  name               VARCHAR(255) NOT NULL,
  description        TEXT NOT NULL DEFAULT '',
  mobility           VARCHAR(10) NOT NULL CHECK (mobility IN ('FIXED', 'MOBILE')),
  "sensorType"       VARCHAR(20) NOT NULL CHECK ("sensorType" IN ('RADAR', 'EO_IR', 'RF', 'ACOUSTIC', 'AIS_RECEIVER', 'ADSB_RECEIVER', 'HUMAN', 'OTHER')),
  position           geometry(Point, 4326),
  altitude           DOUBLE PRECISION,                         -- metres above the WGS84 ellipsoid
  "boresightDeg"     DOUBLE PRECISION NOT NULL DEFAULT 0,      -- [0, 360), from azimuthReference
  "azimuthReference" VARCHAR(12) NOT NULL DEFAULT 'TRUE_NORTH' CHECK ("azimuthReference" IN ('TRUE_NORTH', 'PLATFORM')),
  coverage           JSONB NOT NULL DEFAULT '{}'::jsonb,       -- {minRangeM, maxRangeM, azimuthWidthDeg, elevationMinDeg?, elevationMaxDeg?}
  "defaultSigma"     JSONB NOT NULL DEFAULT '{}'::jsonb,       -- {rangeM?, azimuthDeg?, elevationDeg?}
  "feedId"           VARCHAR(255) REFERENCES sentinel.feeds(id) ON DELETE SET NULL,
  classification     VARCHAR(20) NOT NULL DEFAULT 'UNCLASSIFIED' CHECK (classification IN ('UNCLASSIFIED', 'CONFIDENTIAL', 'SECRET', 'TOP_SECRET')),
  exercise           BOOLEAN NOT NULL DEFAULT FALSE,
  active             BOOLEAN NOT NULL DEFAULT TRUE,
  "lastReportAt"     TIMESTAMPTZ,
  "createdBy"        VARCHAR(255),
  "createdAt"        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  "updatedAt"        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  "deletedAt"        TIMESTAMPTZ,
  version            INTEGER NOT NULL DEFAULT 1,
  CONSTRAINT emplacements_fixed_has_position CHECK (mobility <> 'FIXED' OR position IS NOT NULL)
);

-- A name is unique among live emplacements, whatever its case; a deleted
-- one's name can be used again.
CREATE UNIQUE INDEX IF NOT EXISTS idx_emplacements_name_live
  ON sentinel.emplacements (lower(name)) WHERE "deletedAt" IS NULL;
CREATE INDEX IF NOT EXISTS idx_emplacements_position
  ON sentinel.emplacements USING GIST (position);
CREATE INDEX IF NOT EXISTS idx_emplacements_feed
  ON sentinel.emplacements ("feedId") WHERE "deletedAt" IS NULL;

-- The built-in emplacements feed reports fixed emplacements every 60 s, so
-- their entities go stale after three missed heartbeats and age out after
-- ten. One row per source the mirror writes (sensor type -> EntitySource);
-- the age-out service matches feed and source together.
INSERT INTO sentinel.feed_ageout_config ("feedId", "sourceType", "staleThresholdMs", "ageoutThresholdMs")
VALUES
  ('emplacements', 'RADAR', 180000, 600000),
  ('emplacements', 'AIS', 180000, 600000),
  ('emplacements', 'ADS_B', 180000, 600000),
  ('emplacements', 'HUMINT', 180000, 600000),
  ('emplacements', 'MASINT', 180000, 600000)
ON CONFLICT ("feedId", "sourceType") WHERE "feedId" IS NOT NULL AND "sourceType" IS NOT NULL DO NOTHING;
