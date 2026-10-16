-- =============================================================================
-- SENTINEL schema: entity_identities and entity_sources
-- =============================================================================
-- Part of db/schema/, applied in file-name order by the db-schema compose
-- service and `make db-schema`. Every statement is idempotent: re-applying
-- the directory after an edit is the way the schema changes before the first
-- release. Column names are camelCase (quoted) where the Go stores use
-- camelCase. Extensions (postgis, timescaledb, age) are enabled by
-- config/postgres/init-extensions.sql at database initialisation.
-- =============================================================================



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
