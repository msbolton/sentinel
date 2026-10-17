-- =============================================================================
-- SENTINEL schema: behavioural baselines, places, findings, coverage
-- =============================================================================
-- Part of db/schema/, applied in file-name order by the db-schema compose
-- service and `make db-schema`. Every statement is idempotent: re-applying
-- the directory after an edit is the way the schema changes before the first
-- release. Column names are camelCase (quoted) where the Go stores use
-- camelCase. Extensions (postgis, timescaledb, age) are enabled by
-- config/postgres/init-extensions.sql at database initialisation.
-- =============================================================================

-- -----------------------------------------------------------------------------
-- entity_baselines  (apps/analytics-service/internal/store/baseline_store.go)
-- What is normal for one entity, recomputed hourly from its track points:
-- the hour-of-week activity histogram (168 slots, local solar time), the
-- range/speed/altitude/gap statistics as JSON, and whether there is enough
-- history to judge departures from it.
-- -----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS sentinel.entity_baselines (
  "entityId"    UUID PRIMARY KEY,
  "entityType"  VARCHAR(50) NOT NULL,
  "computedAt"  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  "windowStart" TIMESTAMPTZ NOT NULL,
  "windowEnd"   TIMESTAMPTZ NOT NULL,
  "pointCount"  INTEGER NOT NULL,
  days          INTEGER NOT NULL,
  mature        BOOLEAN NOT NULL DEFAULT FALSE,
  stats         JSONB NOT NULL DEFAULT '{}'::jsonb,
  hours         SMALLINT[] NOT NULL DEFAULT '{}'
);

-- -----------------------------------------------------------------------------
-- entity_places  (apps/analytics-service/internal/store/baseline_store.go)
-- The dwell clusters of a baseline: where the entity habitually stops.
-- Replaced wholesale whenever the baseline is recomputed.
-- -----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS sentinel.entity_places (
  id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  "entityId"       UUID NOT NULL REFERENCES sentinel.entity_baselines("entityId") ON DELETE CASCADE,
  "clusterIndex"   INTEGER NOT NULL,
  name             VARCHAR(255),
  "locationId"     UUID,
  centroid         geometry(Point, 4326) NOT NULL,
  "radiusM"        DOUBLE PRECISION NOT NULL,
  visits           INTEGER NOT NULL,
  "medianDwellSec" DOUBLE PRECISION NOT NULL,
  "pointShare"     DOUBLE PRECISION NOT NULL,
  "firstSeen"      TIMESTAMPTZ NOT NULL,
  "lastSeen"       TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_entity_places_entity ON sentinel.entity_places ("entityId");

-- -----------------------------------------------------------------------------
-- findings  (apps/analytics-service/internal/store/finding_store.go)
-- A departure from an entity's baseline, with what was expected and what was
-- seen. One OPEN finding per entity and kind absorbs repeated evidence; it is
-- CLOSED when the evidence stops, or CONFIRMED / DISMISSED by an analyst. A
-- finding that reaches HIGH or is confirmed becomes an alert with the same
-- id (alertId), stored by the alert service like any other producer's.
-- -----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS sentinel.findings (
  id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  "entityId"      UUID NOT NULL,
  kind            VARCHAR(40) NOT NULL,
  severity        VARCHAR(20) NOT NULL,
  score           DOUBLE PRECISION NOT NULL DEFAULT 0,
  summary         TEXT NOT NULL,
  expected        JSONB NOT NULL DEFAULT '{}'::jsonb,
  actual          JSONB NOT NULL DEFAULT '{}'::jsonb,
  thresholds      JSONB NOT NULL DEFAULT '{}'::jsonb,
  position        geometry(Point, 4326),
  "evidenceStart" TIMESTAMPTZ NOT NULL,
  "evidenceEnd"   TIMESTAMPTZ NOT NULL,
  status          VARCHAR(20) NOT NULL DEFAULT 'OPEN',
  "reviewedBy"    VARCHAR(255),
  "reviewedAt"    TIMESTAMPTZ,
  "alertId"       UUID,
  classification  VARCHAR(50) NOT NULL DEFAULT 'UNCLASSIFIED',
  hits            INTEGER NOT NULL DEFAULT 1,
  "firstAt"       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  "lastAt"        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  "closedAt"      TIMESTAMPTZ,
  CONSTRAINT findings_status_check CHECK (status IN ('OPEN', 'CONFIRMED', 'DISMISSED', 'CLOSED'))
);

-- The absorb upsert targets this partial index: INSERT ... ON CONFLICT
-- ("entityId", kind) WHERE status = 'OPEN'.
CREATE UNIQUE INDEX IF NOT EXISTS uq_findings_open ON sentinel.findings ("entityId", kind) WHERE status = 'OPEN';
CREATE INDEX IF NOT EXISTS idx_findings_status_last ON sentinel.findings (status, "lastAt" DESC);
CREATE INDEX IF NOT EXISTS idx_findings_entity_last ON sentinel.findings ("entityId", "lastAt" DESC);
CREATE INDEX IF NOT EXISTS idx_findings_classification ON sentinel.findings (classification);

-- -----------------------------------------------------------------------------
-- coverage_cells  (apps/analytics-service/internal/store/coverage_store.go)
-- When each feed last reported anything in a grid cell, so an entity that
-- stops reporting can be told apart from a feed that stopped covering the
-- area. Cell ids are "<floor(lat/deg)>:<floor(lon/deg)>" for the configured
-- cell size.
-- -----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS sentinel.coverage_cells (
  cell             VARCHAR(32) NOT NULL,
  "feedId"         VARCHAR(255) NOT NULL,
  "lastObservedAt" TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (cell, "feedId")
);
