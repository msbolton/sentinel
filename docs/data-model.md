# Data Model

## PostgreSQL Schema

### Schema management

The schema is a directory of idempotent SQL files, `db/schema/`, one per area (`10_entities.sql`, `20_track_points.sql`, `70_feeds.sql`, `80_observations.sql`, …), applied in file-name order with psql. The `db-schema` compose service applies them before any application service starts, `make db-schema` applies them to the running `sentinel-postgres` container, and the Helm chart's `dbSchema` hook Job applies them after a first install and before every upgrade (`docs/infrastructure.md`). Re-applying after an edit is how the schema changes before the first release: every file is written so that running it again on a database that already has the objects is a no-op (`CREATE … IF NOT EXISTS`, `ADD COLUMN IF NOT EXISTS`, guarded `DO` blocks), and the files also carry the repairs that bring a database created by the former TypeORM schema in line. Versioned migrations start at the first release. Extensions (`postgis`, `timescaledb`, `age`) are created once by `config/postgres/init-extensions.sql` at database initialisation.

Column names are camelCase and must be quoted in SQL (for example `"entityType"`, `"lastSeenAt"`). Enum-like columns are `VARCHAR`; the Go stores bind plain strings. Tables with an `"updatedAt"` column keep it current through the `sentinel.set_updated_at()` trigger.

To change the schema, edit the area's file (or add a new `NN_<area>.sql`; a table that references another must sort after it) and run `make db-schema`. The ingest service checks at start that the columns it needs exist and exits with `database schema is out of date` when they do not (`docs/development.md`); a column added to `70_feeds.sql` with `ALTER` must also be listed in `RequiredColumns` in `apps/ingest-service/internal/registry/schema.go`.


All tables live under the `sentinel` schema. PostGIS provides the `geometry` type for spatial columns.

### sentinel.entities

The fused track: one row per object the platform knows about. Feeds update it through observations; operators own `name` (once set through the REST API), `affiliations`, `description` and the symbol, which feeds never overwrite. `classification` is a high-water mark: a new entity takes the level of the observation that created it (each feed stamps its configured `classification` on every observation), a later report can only raise it (`sentinel.classification_max`), and an operator may raise or lower it through `PATCH /api/v1/entities/{id}` until a higher-configured feed reports again. Alerts copy the entity's level when they fire. `db/schema/05_classification.sql` defines the ordering (`UNCLASSIFIED < CONFIDENTIAL < SECRET < TOP_SECRET`) every read path uses as the caller's ceiling.

| Column | Type | Default | Description |
|--------|------|---------|-------------|
| `id` | `UUID` | `gen_random_uuid()` | Primary key |
| `"entityType"` | `VARCHAR(50)` | — | AIRCRAFT, VESSEL, VEHICLE, PERSON, FACILITY, SATELLITE, … |
| `name` | `VARCHAR(255)` | — | Display name |
| `source` | `VARCHAR(50)` | — | EntitySource of the last accepted observation (OPENSKY, ADS_B, AIS, MANUAL, …) |
| `"feedId"` / `"lastFeedId"` | `VARCHAR` | `NULL` | Feed of the last accepted observation |
| `"sourceEntityId"` | `VARCHAR(255)` | `NULL` | The source's own id for the object |
| `"primaryIdentityType"` / `"primaryIdentityValue"` | `VARCHAR` | `NULL` | The canonical identity the entity was created from (ICAO24, MMSI, NORAD_ID, COT_UID, FEED_LOCAL) |
| `classification` | `VARCHAR(50)` | `'UNCLASSIFIED'` | Security classification |
| `exercise` | `BOOLEAN` | `FALSE` | Synthetic (exercise or simulation) mark; sticky (`e.exercise OR b.exercise`), also set when a duplicate or stale exercise report resolves to the entity, and never cleared |
| `position` | `geometry(Point, 4326)` | `NULL` | WGS84 point |
| `altitude`, `heading`, `"speedKnots"`, `course`, `pitch`, `roll`, `"circularError"` | `DOUBLE PRECISION` | `NULL` | Last accepted kinematics and 95th-percentile horizontal circular error radius (`"circularError"`, metres; derived from the 95% error ellipse or 2×2 North-East covariance via exact bivariate-normal $R_{95}$ when not given directly) |
| `kinematics` | `JSONB` | `{}` | Velocity, acceleration and `positionCovariance` (`pnPn`, `pnPe`, `pnPu`, `pePe`, `pePu`, `puPu` in m²) of the last fix; cleared to `{}` when a new fix arrives without them |
| `"platformData"` | `JSONB` | `{}` | Platform detail (ADS-B, AIS, TLE, CoT, Link 16, UAV, RF) |
| `"trackEnvironment"`, `affiliation`, `"operationalStatus"`, `"countryOfOrigin"` | `VARCHAR` | | Classification attributes |
| `"ageoutState"` | `VARCHAR(20)` | `'LIVE'` | LIVE, STALE or AGED_OUT |
| `"lastObservedAt"` | `TIMESTAMPTZ` | `NULL` | Source timestamp of the last accepted observation; a fix older than it is rejected as `STALE_FIX` |
| `"lastSeenAt"` | `TIMESTAMPTZ` | `NULL` | When the last report arrived |
| `"createdAt"`, `"updatedAt"` | `TIMESTAMPTZ` | `NOW()` | Row timestamps |
| `deleted`, `"deletedAt"` | | | Soft delete |

Indexes: GiST on `position`; B-tree on `"entityType"`, `source`, `classification`, `"lastSeenAt"`, `"feedId"`, `("ageoutState", "lastSeenAt")`; partial on `"sourceEntityId"` for live rows.

### sentinel.entity_identities / sentinel.entity_sources

`entity_identities` maps a natural identifier (`"identityType"`, `"identityValue"`, primary key) to an entity, with first and last seen times. An observation whose canonical identity matches joins that entity regardless of the feed it came from; a secondary identity (callsign, registration, IMO) that would merge two entities is counted, not applied. `entity_sources` keeps one row per entity and feed with the source's own id, the last observed and received times and an observation count; the ageout threshold of an entity is the largest threshold over its sources.

### sentinel.observations

One row per report from any feed, accepted or not; a TimescaleDB hypertable on `"observedAt"` (6-hour chunks, compressed after a day, dropped after seven) when the extension is present.

| Column | Description |
|--------|-------------|
| `"observationId"`, `"observedAt"` | Primary key. The id is a UUIDv5 over feed id, source entity id, source timestamp and decoder, so a redelivered report is a no-op |
| `"receivedAt"`, `"ingestedAt"` | When the ingest replica received it and when the entity-service stored it |
| `"entityId"`, `"feedId"`, `driver`, `decoder`, `"sourceEntityId"`, `source`, `"sensorId"` | Provenance |
| `accepted`, `"rejectReason"` | `DUPLICATE`, `STALE_FIX` or `INVALID_POSITION` when not accepted |
| `position`, `altitude`, `heading`, `"speedKnots"`, `course`, velocities, accelerations, errors, covariances | The fix as reported |
| `identities` | JSON array of `{type, value}` |
| `platform` | Platform detail as JSON |
| `"rawContentType"`, `raw`, `"rawTruncated"` | The original payload, bounded by the feed's `rawPayload` policy |

Indexes on `("entityId", "observedAt" DESC)` and `("feedId", "sourceEntityId", "observedAt" DESC)`.

### sentinel.track_points

Accepted fixes as a time series, written by the track service from `entities.events`; a TimescaleDB hypertable on `"timestamp"` when available, with no primary key (the service never looks points up by id).

| Column | Description |
|--------|-------------|
| `id`, `"entityId"`, `"entityType"` | Entity the point belongs to |
| `position`, `altitude`, `heading`, `"speedKnots"`, `course`, `"velocityNorth"/East/Up`, `"accelNorth"/East/Up`, covariances, `"circularError"`, `"altitudeError"` | The fix |
| `source`, `"feedId"`, `"sourceEntityId"`, `"sensorId"`, `"observationId"` | Provenance back to the observation row |
| `"timestamp"` | Source timestamp (partition key) |

### sentinel.feeds, feed_status, feed_areas, feed_leases, ingest_replicas

The feed registry (see `docs/feeds.md`).

| Table | Content |
|-------|---------|
| `feeds` | Desired state: `id` (slug, primary key), `name`, `description`, `driver`, `config` (JSONB, validated against the driver's schema), `secret_refs` (JSONB of `env:` or `file:` references, never values; which variables and folders a reference may name is the ingest service's secret policy, `docs/feeds.md`), `enabled`, `builtin`, `version`, `created_by`, timestamps |
| `feed_status` | Observed state written by the owning replica: `owner`, `state`, `health`, `last_error`, `last_message_at`, `last_success_at`, `messages_total`, `entities_last`, `errors_total`, `applied_version`, `detail` (JSONB: a listening connection reports `listen`, the `senders` table, its counters and TLS reload state there, see `docs/feeds.md`), `updated_at` |
| `feed_areas` | The area of interest pushed by the gateway: `active`, `clients`, `boxes` (JSONB), `expires_at` |
| `feed_leases` | Which replica owns the feed: `owner_id`, `fencing_token`, `acquired_at`, `heartbeat_at`, `expires_at` |
| `ingest_replicas` | Live replicas by heartbeat: `replica_id`, `started_at`, `heartbeat_at`, `expires_at`, `listener` (takes the connections that bind a port), `listener_shared` (a listener that also runs ordinary feeds; default false) and `listener_host` (the address senders use) |

### sentinel.feed_ageout_config

Stale and age-out thresholds in milliseconds, per feed (`"feedId"`), per source type (`"sourceType"`) or global, with partial unique indexes for each level. The entity-service seeds the defaults.

### sentinel.emplacements (`db/schema/72_emplacements.sql`)

The places observations come from (see `docs/emplacements.md`). One row per emplacement; a delete sets `"deletedAt"` (soft delete). `db/seed/seed.sql` adds one demo emplacement, `Lizard Point radar` (fixed, no connection), under a fixed id, so re-running the seed neither duplicates it nor brings it back once deleted.

| Column | Content |
|--------|---------|
| `id` | UUID primary key, generated |
| `name` | Up to 255 bytes |
| `description` | Free text, default empty |
| `mobility` | `FIXED` or `MOBILE`; a CHECK requires a `position` when `FIXED` |
| `"sensorType"` | `RADAR`, `EO_IR`, `RF`, `ACOUSTIC`, `AIS_RECEIVER`, `ADSB_RECEIVER`, `HUMAN` or `OTHER` |
| `position` | `geometry(Point, 4326)`, null for a mobile emplacement with no fix. A mobile emplacement's reports over a `grpc` connection write it, at most every 10 s, with no version change |
| `altitude` | Metres above the WGS84 ellipsoid |
| `"boresightDeg"` | Centre of the coverage sector in [0, 360), measured from `"azimuthReference"` |
| `"azimuthReference"` | `TRUE_NORTH` (default) or `PLATFORM` |
| `coverage` | JSONB `{minRangeM, maxRangeM, azimuthWidthDeg, elevationMinDeg?, elevationMaxDeg?}`; `{}` when unknown |
| `"defaultSigma"` | JSONB `{rangeM?, azimuthDeg?, elevationDeg?}`, the standard deviations used for reports that carry none |
| `"feedId"` | The connection (`feeds.id`) the emplacement is bound to; `ON DELETE SET NULL`. Deleting the connection first soft-deletes the emplacements its senders made (`"createdBy"` starts `ingest-token:`) and unbinds the others, in the same transaction, raising `version` and sending a NOTIFY for each |
| `classification` | `UNCLASSIFIED` (default) to `TOP_SECRET`; callers above it neither see nor edit the row |
| `exercise` | Whether the emplacement belongs to an exercise, default false |
| `active` | Whether it is reported, default true |
| `"lastReportAt"` | When it last reported over its `grpc` connection (the earlier of the report's time and the server's), written at most every 10 s with no version change and no NOTIFY; null until it has |
| `"createdBy"`, `"createdAt"`, `"updatedAt"` | Provenance and timestamps; `"createdBy"` is `ingest-token:<token id>` for an emplacement a sender made over gRPC |
| `"deletedAt"` | Set by a delete |
| `version` | Starts at 1 and rises on every write; the API's ETag and `If-Match` |

A partial unique index on `lower(name)` where `"deletedAt" IS NULL` keeps live names unique whatever their case and frees a deleted emplacement's name. A GIST index covers `position`, and a partial index covers `"feedId"` for live rows. Each write sends a NOTIFY on the channel `sentinel_emplacements` with the payload `{"id": "<uuid>", "op": "upsert"|"delete"}`; every ingest replica follows it to keep its cache current. The file also seeds the `feed_ageout_config` rows of the built-in `emplacements` feed (stale after 180000 ms, aged out after 600000 ms, for each of the sources `RADAR`, `AIS`, `ADS_B`, `HUMINT`, `SIGINT` and `MASINT`).

### sentinel.ingest_tokens (`db/schema/76_ingest_tokens.sql`)

The bearer tokens senders present to a gRPC connection (`si_` followed by 43 base64url characters). Only the SHA-256 of the secret is stored (`hash`, unique); the secret is shown once, in the response that creates the token, and cannot be read back.

| Column | Content |
|--------|---------|
| `id` | UUID primary key, generated |
| `"feedId"` | The connection (`feeds.id`) the token belongs to; `ON DELETE CASCADE` |
| `name` | Up to 255 bytes |
| `prefix` | The first characters of the secret, for lists |
| `hash` | SHA-256 of the secret, unique |
| `classification` | The token's ceiling, `UNCLASSIFIED` (default) to `TOP_SECRET`: no report above it is accepted |
| `"createdBy"`, `"createdAt"` | Provenance and creation time |
| `"lastUsedAt"` | When the token last authenticated |
| `"expiresAt"` | Optional expiry |
| `"revokedAt"` | Set by a revoke; revoking again keeps the first time |

An index covers `"feedId"`. Every create and revoke sends a NOTIFY on the channel `sentinel_ingest_tokens` with the payload `{"id","feedId","op"}` (`op` is `create` or `revoke`); each ingest replica's token cache re-reads that feed's tokens on it. Deleting a feed removes its tokens by cascade and sends no token notice, so the cache also follows `sentinel_feeds` and re-reads the feed's tokens on every feed notice. Writes to `"lastUsedAt"` are batched (about every 60 seconds, per replica) and send no notice. As a guard against a silent LISTEN connection, a connected cache also reloads every token every two minutes.

### sentinel.alerts and sentinel.alert_rules

| Column | Description |
|--------|-------------|
| `alerts.id`, `"alertType"`, `severity`, `title`, `description`, `"entityId"`, `"relatedEntityIds"`, `position`, `"ruleId"`, `metadata`, `classification`, `exercise`, `"createdAt"`, `"acknowledgedAt"`, `"acknowledgedBy"`, `"resolvedAt"` | An alert from any producer; analytics alerts carry `anomalyType` and `score` in `metadata` and keep the producer's id. `exercise` is stamped where the alert is made (by `alert-service` from the primary and related entities, and by `analytics-service` from an entity lookup that also raises the alert's classification to the entity's; a lookup error marks the alert `exercise` so outbound connections fail closed) |
| `alert_rules.id`, `name`, `"ruleType"`, `config`, `"monitoredEntityTypes"`, `severity`, `enabled`, timestamps | Rule definitions (geofence polygons, thresholds) |

### sentinel.links

Entity relationships for graph analysis: `"sourceEntityId"`, `"targetEntityId"`, `"linkType"`, `confidence`, `description`, `evidence`, `"firstObserved"`, `"lastObserved"`, `metadata`, `"createdAt"`.

### sentinel.locations

`locations` holds the saved camera positions the gateway serves (`name`, `latitude`, `longitude`, `altitude`, `heading`, `pitch`, `"range"`, `"has3dTiles"`, `category`).

### Behavioural baselines and findings (`db/schema/60_behavior.sql`)

What the analytics service has learned about each entity and where it departs from it.

| Table | Columns | Notes |
|-------|---------|-------|
| `entity_baselines` | `"entityId"` (PK), `"entityType"`, `"computedAt"`, `"windowStart"`, `"windowEnd"`, `"pointCount"`, `days`, `mature`, `stats` JSONB, `hours` SMALLINT[168] | One row per entity, recomputed hourly from the last 30 days of `track_points`. `stats` holds range, speed, altitude and reporting-gap statistics; `hours` is the hour-of-week activity histogram in local solar time; `mature` is the gate the detectors wait for |
| `entity_places` | `id`, `"entityId"`, `"clusterIndex"`, `name`, `"locationId"`, `centroid`, `"radiusM"`, `visits`, `"medianDwellSec"`, `"pointShare"`, `"firstSeen"`, `"lastSeen"` | The baseline's dwell clusters, replaced on each recomputation; `name` comes from a nearby `locations` row |
| `findings` | `id`, `"entityId"`, `kind`, `severity`, `score`, `summary`, `expected` JSONB, `actual` JSONB, `thresholds` JSONB, `position`, `"evidenceStart"`, `"evidenceEnd"`, `status`, `"reviewedBy"`, `"reviewedAt"`, `"alertId"`, `classification`, `hits`, `"firstAt"`, `"lastAt"`, `"closedAt"` | A departure from the baseline. Kinds: `UNUSUAL_PLACE`, `UNUSUAL_TIME`, `UNUSUAL_MOVEMENT`, `WENT_DARK`, `REAPPEARED`. Status `OPEN` → `CLOSED` when evidence stops, or `CONFIRMED` / `DISMISSED` by an analyst; a partial unique index keeps one open finding per entity and kind so repeat evidence is absorbed (`hits`). A finding that reaches HIGH or is confirmed is published as an alert with the same id (`"alertId"`). `classification` is inherited from the entity |
| `coverage_cells` | `cell`, `"feedId"`, `"lastObservedAt"` | When each feed last reported in a grid cell, so an entity going dark can be told from a feed going away |

### GIS views (`db/schema/90_gis_views.sql`)

`sentinel.entities_live` presents live, non-deleted entities with a position in snake_case for GeoServer (`entity_type`, `classification`, `classification_rank`, `feed_id`, `last_feed_id`, `source`, `affiliation`, `track_environment`, `heading`, `speed_knots`, `course`, `altitude`, `sidc`, `ageout_state`, `exercise`, `last_observed_at`, `last_seen_at`, `lat`, `lon`, `geom`). `entities_live_u`, `_c`, `_s` and `_ts` filter it by rank; `locations_geo` adds a `geom` point to locations. `gt_pk_metadata` tells GeoTools the views' primary key. The role `sentinel_gis` may read the views only, never `entities`. `09_gis_views_drop.sql` drops the views at the start of an apply so column changes on the tables go through; the views are recreated at the end.

### sentinel.api_keys

Per-user, read-only API keys for GIS tools and KML network links: `id`, `"userId"`, `username`, `name`, `prefix` (the first characters of the secret, for lists), `"keyHash"` (SHA-256 of the secret, unique), `roles` (viewer only), `classification` (capped at the issuer's clearance when issued), `"createdAt"`, `"expiresAt"`, `"lastUsedAt"`, `"revokedAt"`. The secret itself is never stored.

### Connections in `sentinel.feeds`

A feed whose driver sends (`out` or `both`, narrowable with `config.direction`) is a connection. `config.policy` holds the sharing policy, `config.outbound` the delivery tuning, `config.classification` the default level stamped on its observations; `feed_status.detail.outbound` carries the delivery counters. No new columns: `docs/feeds.md` lists the keys.

## Enums and the string convention

Enum-like columns are `VARCHAR` holding the bare protobuf enum name without its type prefix: the database, the Kafka JSON produced by `pbjson`, the REST API and the client all spell `AIRCRAFT`, `OPENSKY`, `ICAO24`, `STALE`. The protobuf definitions in `proto/sentinel/v1` are the source of truth:

- **EntityType**: `UNKNOWN | PERSON | VEHICLE | VESSEL | AIRCRAFT | FACILITY | EQUIPMENT | UNIT | SIGNAL | CYBER | SENSOR | SATELLITE | DRONE`
- **EntitySource**: `HUMINT | SIGINT | GEOINT | OSINT | MASINT | CYBER | MANUAL | AIS | ADS_B | LINK16 | GPS | RADAR | CELESTRAK | OPENSKY | ADSB_LOL | INTELSKY` (`ADSB_LOL` is legacy: the adsb.lol feed was replaced by IntelSky; `UNSPECIFIED` for a JSON feed without a configured source)
- **IdentityType**: `ICAO24 | MMSI | IMO | NORAD_ID | COT_UID | LINK16_TN | FEED_LOCAL | CALLSIGN | REGISTRATION`; the first four are canonical and drive fusion
- **AgeoutState**: `LIVE | STALE | AGED_OUT`
- **Classification**, hierarchical: `UNCLASSIFIED (0) < CONFIDENTIAL (1) < SECRET (2) < TOP_SECRET (3)`
- **Affiliation** (seed examples): `FRIENDLY | HOSTILE | NEUTRAL | UNKNOWN | ASSUMED_FRIENDLY | SUSPECT | PENDING`, plus free-form tags such as `USN`, `FIFTH_FLEET`

## Kafka Topics

| Topic | Partitions | Retention | Key | Message | Producers | Consumers |
|-------|-----------|-----------|-----|---------|-----------|-----------|
| `observations.raw` | 24 | 3 days | identity (`ICAO24:ABC123`, else `feed:<id>:<sourceEntityId>`) | `sentinel.v1.Observation` | ingest replicas | entity-service |
| `observations.dlq` | 3 | 30 days | feed id | raw payload; headers `driver`, `decoder`, `sender` (the remote address, so a sender's IP stays for the topic's 30 days), `reason`, `error`, `received-at` (when the frame arrived) | ingest replicas | operators |
| `entities.events` | 12 | 7 days | entity id | `sentinel.v1.EntityEvent`: full `Entity` snapshot plus one of created, updated, position (with an `ObservationRef`), deleted, stale, aged_out, restored; the `event-type` header names the case | entity-service | track, alert, search, analytics, gateway |
| `alerts.events` | 3 | 30 days | entity id | `sentinel.v1.AlertEvent` with `producer` | alert-service, analytics | alert-service, gateway |

Every message carries `content-type: application/x-protobuf`, `schema` (the fully qualified message name), `schema-version`, `producer`, and on observations `feed-id` and `x-fence` (the owning replica's lease token). Topics are created by `config/kafka/create-topics.sh` in compose and by the Helm chart's Kafka provisioning.

## Seed Data

`db/seed/seed.sql` (run with `make db-seed`) populates 13 sample entities across the Middle East region, one geofence alert rule, and 10 saved camera locations. Entities:

| Entity | Type | Source | Classification | Location |
|--------|------|--------|----------------|----------|
| MV ATLANTIC VOYAGER | VESSEL | GEOINT | UNCLASSIFIED | Persian Gulf |
| DHOW-7742 | VESSEL | SIGINT | SECRET | Persian Gulf |
| USS MASON (DDG-87) | VESSEL | MANUAL | UNCLASSIFIED | Persian Gulf |
| EAGLE-01 | AIRCRAFT | SIGINT | SECRET | Iraq |
| UAE-117 | AIRCRAFT | OSINT | UNCLASSIFIED | UAE |
| CONVOY-ALPHA | VEHICLE | GEOINT | SECRET | Syria |
| SUPPLY-TRUCK-22 | VEHICLE | HUMINT | CONFIDENTIAL | Syria |
| HVT-ALPHA | PERSON | HUMINT | TOP_SECRET | Damascus |
| INFORMANT-BRAVO | PERSON | HUMINT | SECRET | Beirut |
| COMPOUND-DELTA | FACILITY | GEOINT | SECRET | Syria |
| FOB LIBERTY | FACILITY | MANUAL | CONFIDENTIAL | Baghdad |
| EMITTER-7790 | SIGNAL | SIGINT | TOP_SECRET | Syria |
| 3RD BDE, 1ST AD | UNIT | MANUAL | CONFIDENTIAL | Iraq |

Plus one sample alert rule: "Persian Gulf Exclusion Zone" geofence monitoring VESSEL and AIRCRAFT types.
