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
-- ship's radar, a field observer. It is a resource of the connection it is
-- bound to ("feedId"), and its heartbeat is that connection's: the connection
-- reports it as a SENSOR entity (docs/emplacements.md). Users manage them
-- through the ingest management API (/api/v1/emplacements); every write sends
-- a sentinel_emplacements notification so each ingest replica's cache follows.
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
  -- RESTRICT: registry.Delete lets a connection's emplacements go first.
  "feedId"           VARCHAR(255) REFERENCES sentinel.feeds(id) ON DELETE RESTRICT,
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

-- 1. The built-in `emplacements` mirror feed is gone: each connection reports
--    its own emplacements. Rows bound to it (none should be) are let go and
--    deactivated, then the feed (its status, area, lease and tokens cascade)
--    and its age-out rows are deleted. Each changed row is announced so the
--    running caches follow.
WITH released AS (
  UPDATE sentinel.emplacements e
     SET "feedId" = NULL, active = FALSE, version = e.version + 1, "updatedAt" = NOW()
    FROM sentinel.feeds f
   WHERE f.id = e."feedId" AND f.id = 'emplacements' AND f.driver = 'emplacement'
  RETURNING e.id
)
SELECT pg_notify('sentinel_emplacements', json_build_object('id', id::text, 'op', 'upsert')::text) FROM released;
DELETE FROM sentinel.feeds WHERE id = 'emplacements' AND driver = 'emplacement';
DELETE FROM sentinel.feed_ageout_config WHERE "feedId" = 'emplacements';

-- 2. What the row last recorded of the emplacement's heartbeats: the interval
--    it declared and when the last one arrived (server time), written by the
--    grpc driver at most every interval and every 10 s, with no version change
--    and no notice. The owning replica's memory is the truth; these seed it
--    on a restart and answer the API when no replica reports the connection.
ALTER TABLE sentinel.emplacements
  ADD COLUMN IF NOT EXISTS "heartbeatIntervalSec" INTEGER,
  ADD COLUMN IF NOT EXISTS "lastHeartbeatAt" TIMESTAMPTZ;

-- 2b. How far above the ground the sensor is mounted, metres (a mast, a roof).
--     A camera with no altitude gets its origin altitude from the terrain
--     plus this. Not a position: a sender-managed mobile emplacement keeps it.
ALTER TABLE sentinel.emplacements ADD COLUMN IF NOT EXISTS "heightAboveGroundM" DOUBLE PRECISION;

-- 3. Every live emplacement needs a connection: an active one with none is
--    deactivated, and the Connections page offers to assign or delete it.
WITH parked AS (
  UPDATE sentinel.emplacements
     SET active = FALSE, version = version + 1, "updatedAt" = NOW()
   WHERE "feedId" IS NULL AND "deletedAt" IS NULL AND active
  RETURNING id
)
SELECT pg_notify('sentinel_emplacements', json_build_object('id', id::text, 'op', 'upsert')::text) FROM parked;

-- 4. ... and the database holds to it. Postgres 16 has no ADD CONSTRAINT IF
--    NOT EXISTS, so each change checks pg_constraint first. The connection
--    foreign key becomes RESTRICT (it was SET NULL): a connection's delete
--    lets its emplacements go first (registry.Delete), so a stray delete
--    cannot leave an active emplacement with no connection.
DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint
     WHERE conname = 'emplacements_active_needs_feed' AND conrelid = 'sentinel.emplacements'::regclass
  ) THEN
    ALTER TABLE sentinel.emplacements ADD CONSTRAINT emplacements_active_needs_feed
      CHECK (NOT active OR "deletedAt" IS NOT NULL OR "feedId" IS NOT NULL);
  END IF;
END $$;

DO $$
DECLARE fk text;
BEGIN
  FOR fk IN
    SELECT conname FROM pg_constraint
     WHERE conrelid = 'sentinel.emplacements'::regclass AND contype = 'f'
       AND confrelid = 'sentinel.feeds'::regclass AND confdeltype <> 'r'
  LOOP
    EXECUTE format('ALTER TABLE sentinel.emplacements DROP CONSTRAINT %I', fk);
  END LOOP;
  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint
     WHERE conrelid = 'sentinel.emplacements'::regclass AND contype = 'f' AND confrelid = 'sentinel.feeds'::regclass
  ) THEN
    ALTER TABLE sentinel.emplacements ADD CONSTRAINT "emplacements_feedId_fkey"
      FOREIGN KEY ("feedId") REFERENCES sentinel.feeds(id) ON DELETE RESTRICT;
  END IF;
END $$;

-- 5. No age-out rows replace the mirror's. A row with no feed is the global
--    default for its source (idx_ageout_config_source_default, thresholdSQL),
--    so one keyed for site entities would retime every target of that
--    source. A connection publishes each of its sites at least every
--    min(interval, 60 s), and every applied update moves lastSeenAt, so a
--    running connection's sites never age out under the defaults.

-- 6. Entities the mirror made name a feed that no longer exists. entity_sources
--    is created by 85_entity_identities.sql, after this file, so its cleanup
--    waits for the table to exist.
UPDATE sentinel.entities SET "feedId" = NULL WHERE "feedId" = 'emplacements';
DO $$
BEGIN
  IF to_regclass('sentinel.entity_sources') IS NOT NULL THEN
    DELETE FROM sentinel.entity_sources WHERE "feedId" = 'emplacements';
  END IF;
END $$;
