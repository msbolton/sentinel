-- =============================================================================
-- SENTINEL schema: feeds, feed_status, feed_areas, ingest_replicas, feed_leases
-- =============================================================================
-- Part of db/schema/, applied in file-name order by the db-schema compose
-- service and `make db-schema`. Every statement is idempotent: re-applying
-- the directory after an edit is the way the schema changes before the first
-- release. Column names are camelCase (quoted) where the Go stores use
-- camelCase. Extensions (postgis, timescaledb, age) are enabled by
-- config/postgres/init-extensions.sql at database initialisation.
--
-- The ingest service checks at startup that the columns it depends on exist
-- and refuses to start without them: a column added below with ALTER TABLE
-- must also be listed in apps/ingest-service/internal/registry/schema.go.
-- =============================================================================

-- -----------------------------------------------------------------------------
-- feeds  (apps/ingest-service/internal/registry)
-- Desired state of every feed, built-in or user-defined. id is an immutable
-- slug that entities."feedId", track_points."feedId" and
-- feed_ageout_config."feedId" store as-is. A row is a source feed or a
-- connection that also (or only) sends: the driver's direction, narrowed by
-- config.direction, decides; config.policy and config.outbound hold the
-- sharing policy and sink tuning; feed_status.detail.outbound carries the
-- sink counters. The Kafka consumer group sentinel-conn-<id> is derived.
-- -----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS sentinel.feeds (
  id           VARCHAR(64)  PRIMARY KEY CHECK (id ~ '^[a-z0-9][a-z0-9-]{0,62}$'),
  name         VARCHAR(255) NOT NULL,
  description  TEXT         NOT NULL DEFAULT '',
  driver       VARCHAR(64)  NOT NULL,                 -- key in the ingest driver registry
  config       JSONB        NOT NULL DEFAULT '{}',    -- validated against the driver's JSON schema
  secret_refs  JSONB        NOT NULL DEFAULT '{}',    -- {"clientSecret": "env:OPENSKY_CLIENT_SECRET"}; never plaintext
  enabled      BOOLEAN      NOT NULL DEFAULT FALSE,   -- desired state
  builtin      BOOLEAN      NOT NULL DEFAULT FALSE,   -- seeded; id and driver immutable, not deletable
  version      INTEGER      NOT NULL DEFAULT 1,       -- bumped on every write; the owner restarts when behind
  created_by   VARCHAR(255),
  created_at   TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
  updated_at   TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

-- Observed state, written only by the ingest replica that owns the feed.
CREATE TABLE IF NOT EXISTS sentinel.feed_status (
  feed_id          VARCHAR(64) PRIMARY KEY REFERENCES sentinel.feeds(id) ON DELETE CASCADE,
  owner            VARCHAR(128),
  state            VARCHAR(16) NOT NULL DEFAULT 'stopped',   -- stopped|starting|running|idle|degraded|error
  health           VARCHAR(16) NOT NULL DEFAULT 'unknown',   -- healthy|warn|critical|unknown
  last_error       TEXT,
  last_message_at  TIMESTAMPTZ,
  last_success_at  TIMESTAMPTZ,
  messages_total   BIGINT  NOT NULL DEFAULT 0,
  entities_last    INTEGER NOT NULL DEFAULT 0,
  errors_total     BIGINT  NOT NULL DEFAULT 0,
  applied_version  INTEGER NOT NULL DEFAULT 0,
  detail           JSONB   NOT NULL DEFAULT '{}',            -- driver-specific: credits, effectiveIntervalSec, idle
  updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Area-of-interest lease pushed by the API gateway from the connected
-- clients' viewports; read by whichever replica owns the feed.
CREATE TABLE IF NOT EXISTS sentinel.feed_areas (
  feed_id     VARCHAR(64) PRIMARY KEY REFERENCES sentinel.feeds(id) ON DELETE CASCADE,
  active      BOOLEAN NOT NULL DEFAULT FALSE,
  clients     INTEGER NOT NULL DEFAULT 0,
  boxes       JSONB   NOT NULL DEFAULT '[]',
  expires_at  TIMESTAMPTZ,
  updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Feed ownership across ingest replicas. A feed runs on the replica holding
-- an unexpired lease; the fencing token guards against a stale owner.
CREATE TABLE IF NOT EXISTS sentinel.ingest_replicas (
  replica_id    VARCHAR(128) PRIMARY KEY,
  started_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  heartbeat_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  expires_at    TIMESTAMPTZ NOT NULL
);

-- A listener replica runs with the host's network and takes the
-- connections that bind a port (TCP/UDP listeners); listener_host is the
-- address senders use, shown in the send guide.
ALTER TABLE sentinel.ingest_replicas ADD COLUMN IF NOT EXISTS listener BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE sentinel.ingest_replicas ADD COLUMN IF NOT EXISTS listener_host TEXT;

CREATE TABLE IF NOT EXISTS sentinel.feed_leases (
  feed_id        VARCHAR(64) PRIMARY KEY REFERENCES sentinel.feeds(id) ON DELETE CASCADE,
  owner_id       VARCHAR(128) NOT NULL,
  fencing_token  BIGINT NOT NULL DEFAULT 1,
  acquired_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  heartbeat_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  expires_at     TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_feed_leases_expires ON sentinel.feed_leases (expires_at);

-- The ingest service used to create its own custom_feeds table at startup in
-- the default schema; the feeds table above replaces it.
DROP TABLE IF EXISTS public.custom_feeds;
DROP TABLE IF EXISTS sentinel.custom_feeds;
