# Entity & Track Ageout for Stale Feeds — Design Spec

## Goal

Detect when data feeds stop updating, transition entities through a tiered staleness lifecycle (LIVE → STALE → AGED_OUT), and remove aged-out entities from active views. Operators can override ageout thresholds per feed at runtime. The frontend provides immediate visual feedback via client-side staleness calculation between backend polling cycles.

## Ageout State Lifecycle

```
Fresh data arrives     Fresh data arrives
       │                      │
       ▼                      ▼
    ┌──────┐  stale threshold  ┌───────┐  ageout threshold  ┌──────────┐
    │ LIVE │ ───────────────▶  │ STALE │ ────────────────▶  │ AGED_OUT │
    └──────┘                   └───────┘                    └──────────┘
       ▲                          │                              │
       └──────────────────────────┘──────────────────────────────┘
                    Restored when fresh data arrives
```

- **LIVE** — entity is receiving updates within expected cadence
- **STALE** — `lastSeenAt` exceeded the stale threshold; entity rendered dimmed on map but still included in queries
- **AGED_OUT** — `lastSeenAt` exceeded the ageout threshold; entity excluded from active queries and map rendering

## Data Model

All tables live in the `sentinel` schema, consistent with the existing `entities` table (`@Entity('...', { schema: 'sentinel' })`). Column names use camelCase (TypeORM default) — raw SQL must quote them (e.g., `"ageoutState"`).

### New Table: `feed_ageout_config`

| Column | Type | Notes |
|--------|------|-------|
| `id` | UUID (PK) | Auto-generated |
| `feedId` | UUID, nullable | Null = default for this source type |
| `sourceType` | varchar, nullable | EntitySource value (ADS_B, AIS, etc.). Null = global default |
| `staleThresholdMs` | integer | Milliseconds after `lastSeenAt` before marking STALE |
| `ageoutThresholdMs` | integer | Milliseconds after `lastSeenAt` before marking AGED_OUT |
| `createdAt` | timestamptz | |
| `updatedAt` | timestamptz | |

Uniqueness enforced via partial indexes to handle NULL semantics:
- `CREATE UNIQUE INDEX ON feed_ageout_config ("feedId", "sourceType") WHERE "feedId" IS NOT NULL AND "sourceType" IS NOT NULL` — feed-specific overrides
- `CREATE UNIQUE INDEX ON feed_ageout_config ("sourceType") WHERE "feedId" IS NULL AND "sourceType" IS NOT NULL` — source-type defaults
- `CREATE UNIQUE INDEX ON feed_ageout_config ((1)) WHERE "feedId" IS NULL AND "sourceType" IS NULL` — single global default row

`sourceType` is varchar (not the PostgreSQL `entity_source` enum) because the global default row uses `NULL` and should not pollute the EntitySource enum with a non-source value.

**Threshold resolution order:** feed-specific config (matching `feedId` + `sourceType`) → source-type default (`feedId IS NULL` + matching `sourceType`) → global default (`feedId IS NULL` + `sourceType IS NULL`).

### New Column on `entities` table

| Column | Type | Default |
|--------|------|---------|
| `ageoutState` | varchar | `'LIVE'` |

Indexed for the polling query. Uses varchar (consistent with `trackProcessingState`, `operationalStatus`, etc.) with a TypeScript `AgeoutState` enum for type safety.

### Default Threshold Seeds

| Source | Stale | Aged Out |
|--------|-------|----------|
| ADS_B | 60s | 5min |
| OPENSKY | 60s | 5min |
| ADSB_LOL | 60s | 5min |
| AIS | 10min | 30min |
| CELESTRAK | 24h | 7d |
| LINK16 | 30s | 2min |
| RADAR | 30s | 2min |
| *(global default, sourceType=NULL)* | 5min | 30min |

### Migration Strategy

TypeORM synchronize handles schema changes in development. For production, generate a TypeORM migration that:
1. Adds `"ageoutState"` column to `sentinel.entities` with default `'LIVE'`
2. Creates `sentinel.feed_ageout_config` table
3. Seeds default threshold rows

## Backend: Ageout Polling Service

New `AgeoutService` in `apps/entity-service/src/entities/ageout.service.ts`.

**Dependencies:** `@nestjs/schedule` (new dependency — `ScheduleModule.forRoot()` in `app.module.ts` only, not in `entities.module.ts`).

**Polling cycle** (runs every 15 seconds via `@Interval(15000)`):

1. Guard against concurrent execution with an `isRunning` boolean — skip if previous cycle is still in progress
2. Load all rows from `feed_ageout_config` into a map keyed by `(feedId, sourceType)`
3. For each source type with a configured threshold:
   - Query entities where `ageoutState = 'LIVE'` and `lastSeenAt < NOW() - staleThresholdMs` (joined/grouped by source and feedId for per-feed thresholds)
   - Bulk update matching entities to `ageoutState = 'STALE'` (limit 1000 per cycle to avoid backpressure)
   - Emit `events.entity.stale` for each transitioned entity
4. Same pattern for `STALE` → `AGED_OUT` using `ageoutThresholdMs` (also limited to 1000)
5. All updates use bulk SQL (consistent with existing `bulkUpdatePositions` pattern)

**Note:** The 15-second polling interval introduces up to 15s of latency for backend state transitions. The frontend compensates with client-side staleness calculation for immediate visual feedback. If the batch limit is reached, remaining entities are processed in subsequent cycles.

**SQL strategy for per-feed thresholds:** Use a CTE that joins `sentinel.entities` to `sentinel.feed_ageout_config` with COALESCE fallback:

```sql
WITH candidates AS (
  SELECT e.id,
    COALESCE(feed_cfg."staleThresholdMs", source_cfg."staleThresholdMs", default_cfg."staleThresholdMs") AS stale_ms
  FROM sentinel.entities e
  LEFT JOIN sentinel.feed_ageout_config feed_cfg
    ON feed_cfg."feedId" = e."feedId" AND feed_cfg."sourceType" = e.source::varchar
  LEFT JOIN sentinel.feed_ageout_config source_cfg
    ON source_cfg."feedId" IS NULL AND source_cfg."sourceType" = e.source::varchar
  LEFT JOIN sentinel.feed_ageout_config default_cfg
    ON default_cfg."feedId" IS NULL AND default_cfg."sourceType" IS NULL
  WHERE e."ageoutState" = 'LIVE' AND e.deleted = false
    AND EXTRACT(EPOCH FROM (NOW() - e."lastSeenAt")) * 1000 >
      COALESCE(feed_cfg."staleThresholdMs", source_cfg."staleThresholdMs", default_cfg."staleThresholdMs")
  LIMIT 1000
)
UPDATE sentinel.entities SET "ageoutState" = 'STALE'
FROM candidates c
WHERE sentinel.entities.id = c.id
RETURNING sentinel.entities.id, sentinel.entities."entityType", sentinel.entities.source,
  sentinel.entities."feedId", sentinel.entities."lastSeenAt";
```

Note: `e.source::varchar` casts the PostgreSQL `entity_source` enum to varchar for comparison with the varchar `sourceType` column in `feed_ageout_config`.

Similar query for STALE → AGED_OUT transition.

## Backend: Restoration on Ingest

In `IngestConsumer` flush logic (`ingest.consumer.ts`), during the existing bulk position update:

- The `bulkUpdatePositions` SQL already updates `lastSeenAt`. Add `"ageoutState" = 'LIVE'` to the SET clause.
- The `findBySourceEntityIds` method and its `ExistingEntityInfo` interface must be updated to also select and return `ageoutState`.
- After the bulk update, check which entities had a previous `ageoutState != 'LIVE'` (from the pre-update lookup) and emit `events.entity.restored` for those.

This requires no new queries — it piggybacks on the existing ingest flow.

## Backend: Query Filtering

Modify existing repository methods:

- `findWithinBoundingBox()` — add `WHERE "ageoutState" != 'AGED_OUT'` (default), with optional `includeAgedOut` query param
- `findNearby()` — same filter
- `findActiveById()` — no ageout filter. Direct by-ID lookups return entities regardless of ageout state so operators can still inspect them.

Stale entities remain in list/spatial query results — they're just rendered differently by the frontend.

## Backend: Ageout Config API

New dedicated `AgeoutConfigController`:

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/api/v1/entities/ageout-config` | List all configs |
| `GET` | `/api/v1/entities/ageout-config/:sourceType` | Get config for source type, optional `?feedId=` query param |
| `PUT` | `/api/v1/entities/ageout-config` | Create or update config (upsert on feedId + sourceType) |
| `DELETE` | `/api/v1/entities/ageout-config/:id` | Remove override, falls back to default |

**DTOs:**

- `AgeoutConfigDto`: `feedId?` (UUID), `sourceType?` (EntitySource — optional, omit for global default), `staleThresholdMs` (positive int), `ageoutThresholdMs` (positive int, must be > staleThresholdMs)
- `AgeoutConfigResponseDto`: all fields + `id`, `createdAt`, `updatedAt`

Protected by `JwtAuthGuard` with `sentinel-operator` or `sentinel-admin` role.

## Kafka Events

### New Topics

Added to `libs/common/src/kafka-topics.ts`:

```
ENTITY_STALE:    'events.entity.stale'
ENTITY_AGED_OUT: 'events.entity.agedout'
ENTITY_RESTORED: 'events.entity.restored'
```

### Event Payload

Consistent shape for all three events:

```typescript
{
  entity_id: string;       // UUID
  entity_type: string;     // EntityType enum value
  source: string;          // EntitySource enum value
  feed_id: string;         // UUID
  ageout_state: string;    // 'STALE' | 'AGED_OUT' | 'LIVE'
  last_seen_at: string;    // ISO 8601 timestamp
  threshold_ms: number;    // the threshold that was crossed
  timestamp: string;       // event timestamp ISO 8601
}
```

## WebSocket Gateway Changes

The existing `EntityGateway` (in `apps/api-gateway/src/modules/gateway/`) currently forwards only position updates. Add subscriptions for the three new Kafka topics:

- Subscribe to `events.entity.stale`, `events.entity.agedout`, `events.entity.restored` in the gateway's Kafka consumer
- Forward each event to **all** connected WebSocket clients (bypasses viewport filtering — a stale/aged-out entity must be removed from any client that has it cached, regardless of current viewport)
- No transformation needed — the Kafka payload is forwarded as-is

## Track Data for Aged-Out Entities

Track point data in TimescaleDB is **not** purged when an entity ages out. The track history remains available for historical queries via `getHistory()` and `getSegments()`. TimescaleDB's built-in retention policies (if configured) handle long-term data lifecycle independently.

The frontend stops rendering track trails for aged-out entities because the entity itself is removed from the active view. If the entity is restored, track rendering resumes normally.

## Frontend: Client-Side Staleness Rendering

### Data Changes

- Entity query responses include the new `ageoutState` field
- Frontend fetches ageout configs on init (cached, refreshed every 60s) to know stale thresholds per source type

### Rendering Logic

On each render cycle, for each entity:

1. If `ageoutState === 'AGED_OUT'` → do not render (backend already excludes from queries, handles local cache edge case)
2. Compute `elapsed = Date.now() - lastSeenAt`. If `elapsed > staleThresholdMs` OR `ageoutState === 'STALE'` → render as stale
3. Otherwise → render as LIVE

### Visual Treatment for Stale Entities

- Billboard/label opacity reduced to ~40%
- Position marker rendered with dashed or dimmed style
- Track trail rendered with reduced opacity
- Tooltip shows "Last seen: X ago" with stale indicator text

### WebSocket Events

Listen for ageout events via existing WebSocket gateway:

- `events.entity.stale` → update local entity `ageoutState`, triggers dimmed rendering
- `events.entity.agedout` → remove entity from local state
- `events.entity.restored` → update local entity `ageoutState` to LIVE, render normally. The restored entity's position comes from the next `events.entity.position` event (which fires in the same ingest flush cycle), so there is no visible gap.

## Files to Create or Modify

### New Files

| File | Purpose |
|------|---------|
| `apps/entity-service/src/entities/ageout.service.ts` | Polling job, state transitions, event emission |
| `apps/entity-service/src/entities/ageout-config.entity.ts` | TypeORM entity for `feed_ageout_config` |
| `apps/entity-service/src/entities/ageout-config.controller.ts` | REST endpoints for config CRUD |
| `apps/entity-service/src/entities/dto/ageout-config.dto.ts` | Request/response DTOs |
| `apps/entity-service/src/entities/ageout.service.spec.ts` | Unit tests for ageout service |
| `apps/entity-service/src/entities/ageout-config.controller.spec.ts` | Unit tests for config controller |

### Modified Files

| File | Change |
|------|--------|
| `apps/entity-service/src/entities/entity.entity.ts` | Add `ageoutState` column + enum |
| `apps/entity-service/src/entities/entity.repository.ts` | Add ageout filter to queries, restoration in bulk update |
| `apps/entity-service/src/entities/ingest.consumer.ts` | Set `ageoutState = 'LIVE'` on position update, emit restored event |
| `apps/entity-service/src/entities/enums.ts` | Add `AgeoutState` enum |
| `apps/entity-service/src/entities/entities.module.ts` | Register new service, controller, entity |
| `apps/entity-service/src/app.module.ts` | Import `ScheduleModule.forRoot()` |
| `libs/common/src/kafka-topics.ts` | Add three new topic constants |
| `apps/api-gateway/src/modules/gateway/entity.gateway.ts` | Subscribe to ageout Kafka topics, forward to WebSocket clients |
| `apps/web/src/app/...` (entity rendering) | Staleness visual treatment, config fetch, WebSocket listeners |

## Testing Strategy

- **Unit tests** for `AgeoutService`: mock repository, verify state transitions and event emission for each threshold crossing
- **Unit tests** for `AgeoutConfigController`: CRUD operations, validation (ageoutThresholdMs > staleThresholdMs)
- **Unit tests** for restoration logic in `IngestConsumer`: verify `ageoutState` reset and restored event emission
- **Unit tests** for threshold resolution: feed-specific → source-type → global fallback
- **Frontend**: test staleness rendering logic with mocked time values
