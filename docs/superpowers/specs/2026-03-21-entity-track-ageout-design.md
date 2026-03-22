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

### New Table: `feed_ageout_config`

| Column | Type | Notes |
|--------|------|-------|
| `id` | UUID (PK) | Auto-generated |
| `feedId` | UUID, nullable | Null = default for this source type |
| `sourceType` | `entity_source` enum | ADS_B, AIS, TLE, LINK16, COT, etc. |
| `staleThresholdMs` | integer | Milliseconds after `lastSeenAt` before marking STALE |
| `ageoutThresholdMs` | integer | Milliseconds after `lastSeenAt` before marking AGED_OUT |
| `createdAt` | timestamptz | |
| `updatedAt` | timestamptz | |

Unique constraint on `(feedId, sourceType)`.

**Threshold resolution order:** feed-specific config (matching `feedId` + `sourceType`) → source-type default (`feedId IS NULL` + matching `sourceType`) → global default (`feedId IS NULL` + `sourceType = 'DEFAULT'`).

### New Column on `entity_record`

| Column | Type | Default |
|--------|------|---------|
| `ageoutState` | enum (`LIVE`, `STALE`, `AGED_OUT`) | `LIVE` |

Indexed for the polling query.

### Default Threshold Seeds

| Source | Stale | Aged Out |
|--------|-------|----------|
| ADS_B | 60s | 5min |
| AIS | 10min | 30min |
| TLE | 24h | 7d |
| LINK16 | 30s | 2min |
| COT | 30s | 2min |
| DEFAULT | 5min | 30min |

## Backend: Ageout Polling Service

New `AgeoutService` in `apps/entity-service/src/entities/ageout.service.ts`.

**Dependencies:** `@nestjs/schedule` (new dependency — `ScheduleModule.forRoot()` in app module).

**Polling cycle** (runs every 15 seconds via `@Interval(15000)`):

1. Load all rows from `feed_ageout_config` into a map keyed by `(feedId, sourceType)`
2. For each source type with a configured threshold:
   - Query entities where `ageoutState = 'LIVE'` and `lastSeenAt < NOW() - staleThresholdMs` (joined/grouped by source and feedId for per-feed thresholds)
   - Bulk update matching entities to `ageoutState = 'STALE'`
   - Emit `events.entity.stale` for each transitioned entity
3. Same pattern for `STALE` → `AGED_OUT` using `ageoutThresholdMs`
4. All updates use bulk SQL (consistent with existing `bulkUpdatePositions` pattern)

**SQL strategy for per-feed thresholds:** Use a CTE that joins `entity_record` to `feed_ageout_config` with COALESCE fallback:

```sql
WITH thresholds AS (
  SELECT e.id, e.last_seen_at,
    COALESCE(feed_cfg.stale_threshold_ms, source_cfg.stale_threshold_ms, default_cfg.stale_threshold_ms) AS stale_ms
  FROM entity_record e
  LEFT JOIN feed_ageout_config feed_cfg ON feed_cfg.feed_id = e.feed_id AND feed_cfg.source_type = e.source
  LEFT JOIN feed_ageout_config source_cfg ON source_cfg.feed_id IS NULL AND source_cfg.source_type = e.source
  LEFT JOIN feed_ageout_config default_cfg ON default_cfg.feed_id IS NULL AND default_cfg.source_type = 'DEFAULT'
  WHERE e.ageout_state = 'LIVE' AND e.deleted = false
)
UPDATE entity_record SET ageout_state = 'STALE'
FROM thresholds t
WHERE entity_record.id = t.id
  AND EXTRACT(EPOCH FROM (NOW() - t.last_seen_at)) * 1000 > t.stale_ms
RETURNING entity_record.id, entity_record.entity_type, entity_record.source, entity_record.feed_id, entity_record.last_seen_at;
```

Similar query for STALE → AGED_OUT transition.

## Backend: Restoration on Ingest

In `IngestConsumer` flush logic (`ingest.consumer.ts`), during the existing bulk position update:

- The `bulkUpdatePositions` SQL already updates `lastSeenAt`. Add `ageout_state = 'LIVE'` to the SET clause.
- After the bulk update, check which entities had a previous `ageoutState != 'LIVE'` (from the pre-update lookup) and emit `events.entity.restored` for those.

This requires no new queries — it piggybacks on the existing ingest flow.

## Backend: Query Filtering

Modify existing repository methods:

- `findWithinBoundingBox()` — add `WHERE ageout_state != 'AGED_OUT'` (default), with optional `includeAgedOut` param
- `findNearby()` — same filter
- `findActiveById()` — same filter (already filters `deleted = false`)

Stale entities remain in query results — they're just rendered differently by the frontend.

## Backend: Ageout Config API

New endpoints on the entity controller (or a dedicated `AgeoutConfigController`):

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/api/v1/entities/ageout-config` | List all configs |
| `GET` | `/api/v1/entities/ageout-config/:sourceType` | Get config for source type, optional `?feedId=` query param |
| `PUT` | `/api/v1/entities/ageout-config` | Create or update config (upsert on feedId + sourceType) |
| `DELETE` | `/api/v1/entities/ageout-config/:id` | Remove override, falls back to default |

**DTOs:**

- `AgeoutConfigDto`: `feedId?` (UUID), `sourceType` (EntitySource), `staleThresholdMs` (positive int), `ageoutThresholdMs` (positive int, must be > staleThresholdMs)
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
- `events.entity.restored` → update local entity `ageoutState` to LIVE, render normally

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
| `apps/entity-service/src/entities/entities.module.ts` | Register new service, controller, entity, ScheduleModule |
| `apps/entity-service/src/app.module.ts` | Import `ScheduleModule.forRoot()` |
| `libs/common/src/kafka-topics.ts` | Add three new topic constants |
| `apps/web/src/app/...` (entity rendering) | Staleness visual treatment, config fetch, WebSocket listeners |

## Testing Strategy

- **Unit tests** for `AgeoutService`: mock repository, verify state transitions and event emission for each threshold crossing
- **Unit tests** for `AgeoutConfigController`: CRUD operations, validation (ageoutThresholdMs > staleThresholdMs)
- **Unit tests** for restoration logic in `IngestConsumer`: verify `ageoutState` reset and restored event emission
- **Unit tests** for threshold resolution: feed-specific → source-type → global fallback
- **Frontend**: test staleness rendering logic with mocked time values
