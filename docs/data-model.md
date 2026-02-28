# Data Model

## PostgreSQL Schema

All tables live under the `sentinel` schema. PostGIS provides the `geometry` type for spatial columns.

### sentinel.entities

The core entity table stores all tracked objects.

| Column | Type | Default | Description |
|--------|------|---------|-------------|
| `id` | `UUID` | `gen_random_uuid()` | Primary key |
| `entity_type` | `VARCHAR(50)` | — | Entity category (see enum below) |
| `name` | `VARCHAR(255)` | — | Display name |
| `description` | `TEXT` | `NULL` | Optional description |
| `source` | `VARCHAR(50)` | — | Intelligence source (see enum below) |
| `classification` | `VARCHAR(50)` | `'UNCLASSIFIED'` | Security classification |
| `position` | `geometry(Point, 4326)` | `NULL` | WGS84 lat/lng point |
| `heading` | `FLOAT` | `NULL` | Heading in degrees (0–360) |
| `speed_knots` | `FLOAT` | `NULL` | Speed in knots |
| `course` | `FLOAT` | `NULL` | Course over ground (degrees) |
| `mil_std_2525d_symbol` | `VARCHAR(50)` | `NULL` | MIL-STD-2525D symbol code |
| `metadata` | `JSONB` | `'{}'` | Arbitrary key-value metadata |
| `affiliations` | `TEXT[]` | `'{}'` | Affiliation tags |
| `created_at` | `TIMESTAMPTZ` | `NOW()` | Creation timestamp |
| `updated_at` | `TIMESTAMPTZ` | `NOW()` | Last update timestamp |
| `last_seen_at` | `TIMESTAMPTZ` | `NOW()` | Last observed timestamp |

**Indexes**:
- `idx_entities_position` — GIST spatial index on `position`
- `idx_entities_entity_type` — B-tree on `entity_type`
- `idx_entities_source` — B-tree on `source`
- `idx_entities_classification` — B-tree on `classification`
- `idx_entities_last_seen_at` — B-tree on `last_seen_at`

### sentinel.track_points

Time-series track data. Configured as a TimescaleDB hypertable (partitioned on `timestamp`) when TimescaleDB is available.

| Column | Type | Description |
|--------|------|-------------|
| `id` | `UUID` | Default `gen_random_uuid()` |
| `entity_id` | `UUID` | FK to entities |
| `position` | `geometry(Point, 4326)` | Track position |
| `heading` | `FLOAT` | Heading |
| `speed_knots` | `FLOAT` | Speed |
| `course` | `FLOAT` | Course |
| `source` | `VARCHAR(50)` | Data source |
| `timestamp` | `TIMESTAMPTZ` | Observation time (hypertable partition key) |

### sentinel.alerts

| Column | Type | Description |
|--------|------|-------------|
| `id` | `UUID` | Primary key |
| `alert_type` | `VARCHAR(50)` | Alert category (GEOFENCE_BREACH, ANOMALY, etc.) |
| `severity` | `VARCHAR(20)` | LOW, MEDIUM, HIGH, CRITICAL |
| `title` | `VARCHAR(500)` | Alert title |
| `description` | `TEXT` | Detailed description |
| `entity_id` | `UUID` | Related entity |
| `related_entity_ids` | `UUID[]` | Additional related entities |
| `position` | `geometry(Point, 4326)` | Alert location |
| `rule_id` | `UUID` | FK to alert_rules |
| `metadata` | `JSONB` | Additional data |
| `created_at` | `TIMESTAMPTZ` | When alert was raised |
| `acknowledged_at` | `TIMESTAMPTZ` | When acknowledged |
| `acknowledged_by` | `VARCHAR(255)` | Who acknowledged |
| `resolved_at` | `TIMESTAMPTZ` | When resolved |

### sentinel.alert_rules

| Column | Type | Description |
|--------|------|-------------|
| `id` | `UUID` | Primary key |
| `name` | `VARCHAR(255)` | Rule name |
| `rule_type` | `VARCHAR(50)` | GEOFENCE_BREACH, SPEED_ANOMALY, etc. |
| `config` | `JSONB` | Rule configuration (polygon coords, thresholds) |
| `monitored_entity_types` | `TEXT[]` | Entity types this rule applies to |
| `severity` | `VARCHAR(20)` | Default severity for triggered alerts |
| `enabled` | `BOOLEAN` | Whether rule is active |
| `created_at` | `TIMESTAMPTZ` | — |
| `updated_at` | `TIMESTAMPTZ` | — |

### sentinel.links

Entity relationship links for graph analysis.

| Column | Type | Description |
|--------|------|-------------|
| `id` | `UUID` | Primary key |
| `source_entity_id` | `UUID` | Source entity |
| `target_entity_id` | `UUID` | Target entity |
| `link_type` | `VARCHAR(50)` | Relationship type |
| `confidence` | `FLOAT` | Confidence score (0–1) |
| `description` | `TEXT` | Relationship description |
| `evidence` | `TEXT[]` | Supporting evidence references |
| `first_observed` | `TIMESTAMPTZ` | When first observed |
| `last_observed` | `TIMESTAMPTZ` | When last observed |
| `metadata` | `JSONB` | Additional data |
| `created_at` | `TIMESTAMPTZ` | — |

## Enums

### EntityType

```
UNKNOWN | PERSON | VEHICLE | VESSEL | AIRCRAFT | FACILITY | EQUIPMENT | UNIT | SIGNAL | CYBER
```

### EntitySource

```
HUMINT | SIGINT | GEOINT | OSINT | MASINT | CYBER | MANUAL
```

### Classification

Hierarchical from lowest to highest:

```
UNCLASSIFIED (0) < CONFIDENTIAL (1) < SECRET (2) < TOP_SECRET (3)
```

### Affiliation (seed data examples)

```
FRIENDLY | HOSTILE | NEUTRAL | UNKNOWN | ASSUMED_FRIENDLY | SUSPECT | PENDING
```

Custom tags are also used: `USN`, `FIFTH_FLEET`, `COALITION`, `HIGH_VALUE_TARGET`, `LOGISTICS`, etc.

## Kafka Topics

Events flow through typed Kafka topics. Each service consumes the topics relevant to its domain.

| Topic | Key | Value | Producers | Consumers |
|-------|-----|-------|-----------|-----------|
| `events.entity.position` | entity_id | Position update | API Gateway, Ingest | Track, Gateway (WS) |
| `events.entity.created` | entity_id | Full entity | API Gateway | Entity, Search, Alert |
| `events.entity.updated` | entity_id | Updated fields | API Gateway | Entity, Search, Alert |
| `events.track.point` | entity_id | Track point | Track Service | Analytics |
| `alerts.geofence` | alert_id | Geofence alert | Alert Service | Gateway (WS) |
| `alerts.anomaly` | alert_id | Anomaly alert | Alert Service | Gateway (WS) |
| `analytics.pattern` | pattern_id | Pattern result | Analytics | Alert Service |
| `ingest.raw` | source_id | Raw data | Ingest Service | Entity Service |

## Redis Data Structures

| Key | Type | TTL | Purpose |
|-----|------|-----|---------|
| `sentinel:entities:geo` | Sorted Set (GEOADD) | None | Geospatial index for bounding-box queries |
| `sentinel:entities:cache:{id}` | String (JSON) | 300s | Full entity data cache |
| `sentinel:positions` | Hash | None | Latest position snapshot per entity |
| `sentinel:viewports` | Hash | None | Per-user viewport state |
| `sentinel:sessions` | Hash | None | User session data |
| `sentinel:ws-registry` | Set | None | Active WebSocket connections |

## Seed Data

The seed script (`scripts/seed-data.ts`) populates 13 sample entities across the Middle East region:

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
