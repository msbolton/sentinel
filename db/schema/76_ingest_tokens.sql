-- =============================================================================
-- SENTINEL schema: ingest_tokens
-- =============================================================================
-- Part of db/schema/, applied in file-name order. Every statement is
-- idempotent. Column names are camelCase (quoted).
--
-- An ingest token is the bearer secret a sender presents to a gRPC
-- connection ("si_" + 43 base64url characters). Only its SHA-256 is kept;
-- prefix is what lists show. classification is the token's ceiling: no
-- report above it is accepted. Rows go when their feed is deleted. Every
-- create and revoke sends a sentinel_ingest_tokens notification
-- ({"id","feedId","op"}) so each ingest replica's token cache follows.
-- =============================================================================

CREATE TABLE IF NOT EXISTS sentinel.ingest_tokens (
  id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  "feedId"         VARCHAR(255) NOT NULL REFERENCES sentinel.feeds(id) ON DELETE CASCADE,
  name             VARCHAR(255) NOT NULL,
  prefix           VARCHAR(16) NOT NULL,
  hash             BYTEA NOT NULL UNIQUE,
  classification   VARCHAR(20) NOT NULL DEFAULT 'UNCLASSIFIED' CHECK (classification IN ('UNCLASSIFIED', 'CONFIDENTIAL', 'SECRET', 'TOP_SECRET')),
  "createdBy"      VARCHAR(255),
  "createdAt"      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  "lastUsedAt"     TIMESTAMPTZ,
  "expiresAt"      TIMESTAMPTZ,
  "revokedAt"      TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_ingest_tokens_feed ON sentinel.ingest_tokens ("feedId");
