-- =============================================================================
-- SENTINEL schema: anomalies
-- =============================================================================
-- Part of db/schema/, applied in file-name order by the db-schema compose
-- service and `make db-schema`. Every statement is idempotent: re-applying
-- the directory after an edit is the way the schema changes before the first
-- release. Column names are camelCase (quoted) where the Go stores use
-- camelCase. Extensions (postgis, timescaledb, age) are enabled by
-- config/postgres/init-extensions.sql at database initialisation.
-- =============================================================================

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
