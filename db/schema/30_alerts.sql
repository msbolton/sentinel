-- =============================================================================
-- SENTINEL schema: alerts and alert rules
-- =============================================================================
-- Part of db/schema/, applied in file-name order by the db-schema compose
-- service and `make db-schema`. Every statement is idempotent: re-applying
-- the directory after an edit is the way the schema changes before the first
-- release. Column names are camelCase (quoted) where the Go stores use
-- camelCase. Extensions (postgis, timescaledb, age) are enabled by
-- config/postgres/init-extensions.sql at database initialisation.
-- =============================================================================

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

-- Inherited from the primary entity when the alert fired; reads apply the
-- caller's clearance as a ceiling (see db/schema/05_classification.sql).
ALTER TABLE sentinel.alerts ADD COLUMN IF NOT EXISTS classification VARCHAR(50) NOT NULL DEFAULT 'UNCLASSIFIED';
CREATE INDEX IF NOT EXISTS idx_alerts_classification ON sentinel.alerts (classification);

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
