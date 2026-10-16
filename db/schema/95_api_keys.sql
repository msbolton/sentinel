-- =============================================================================
-- SENTINEL schema: API keys
-- =============================================================================
-- Part of db/schema/, applied in file-name order by the db-schema compose
-- service and `make db-schema`. Every statement is idempotent: re-applying
-- the directory after an edit is the way the schema changes before the first
-- release. Column names are camelCase (quoted) where the Go stores use
-- camelCase.
-- =============================================================================

-- -----------------------------------------------------------------------------
-- api_keys  (apps/api-gateway/internal/apikeys)
-- Per-user, read-only keys for GIS tools and KML network links that cannot
-- carry a bearer token. The secret is shown once and stored as a SHA-256
-- hash; roles and classification are fixed at issue time (viewer, capped at
-- the issuer's clearance), so a key never outgrows the user who made it.
-- -----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS sentinel.api_keys (
  id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  "userId"         VARCHAR(255) NOT NULL,
  username         VARCHAR(255) NOT NULL,
  name             VARCHAR(100) NOT NULL,
  prefix           VARCHAR(16)  NOT NULL,            -- first characters of the secret, for display
  "keyHash"        BYTEA        NOT NULL UNIQUE,     -- SHA-256 of the full secret
  roles            TEXT[]       NOT NULL DEFAULT ARRAY['sentinel-viewer']::text[],
  classification   VARCHAR(50)  NOT NULL DEFAULT 'UNCLASSIFIED',
  "createdAt"      TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
  "expiresAt"      TIMESTAMPTZ,
  "lastUsedAt"     TIMESTAMPTZ,
  "revokedAt"      TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_api_keys_user ON sentinel.api_keys ("userId");
