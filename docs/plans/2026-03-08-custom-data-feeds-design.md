# Custom Data Feeds Design

**Date:** 2026-03-08
**Status:** Approved

## Overview

Allow operators to configure and add new feed instances (MQTT, STOMP, TCP) through the Sentinel UI without code changes or service restarts. Custom feeds are dynamically started/stopped by the ingest-service and are indistinguishable from built-in feeds in the UI.

Credentials are out of scope for the initial implementation.

## Data Model

### `custom_feeds` table (Postgres, owned by ingest-service)

| Column | Type | Notes |
|---|---|---|
| `id` | UUID | Primary key; used as `feed_id` throughout the pipeline |
| `name` | VARCHAR | User-defined label |
| `connector_type` | ENUM | `mqtt`, `stomp`, `tcp` |
| `format` | ENUM | `json`, `nmea`, `cot`, `ais`, `adsb`, `link16` |
| `config` | JSONB | Connector-specific parameters |
| `enabled` | BOOL | Default true |
| `created_at` | TIMESTAMPTZ | |
| `updated_at` | TIMESTAMPTZ | |

**Connector-specific `config` shapes:**
- MQTT: `{ "broker_url": "...", "topics": ["..."], "qos": 0|1|2 }`
- STOMP: `{ "broker_url": "...", "queue": "..." }`
- TCP: `{ "address": "host:port" }`

### `EntityPosition` (ingest pipeline struct)

Add `FeedID uuid.UUID` field populated by each connector at message production time. Built-in feeds (OpenSky, adsb.lol, CelesTrak, and the existing generic MQTT/STOMP/TCP) get static well-known UUIDs defined as constants in the ingest-service.

### Entity/Track storage (entity-service)

Add indexed `feed_id UUID` column to entity and track tables. Enables filtering entities by originating feed.

## API Design

### Ingest-service HTTP endpoints (new)

| Method | Path | Description |
|---|---|---|
| `POST` | `/feeds` | Create and immediately start a custom feed |
| `DELETE` | `/feeds/:id` | Stop and remove a custom feed |
| `PATCH` | `/feeds/:id` | Update config; restarts the connector |

Existing `GET /feeds` and `PUT /feeds/:id` (toggle enable/disable) are unchanged. Custom feeds appear in the same list as built-ins.

### `POST /feeds` request body

```json
{
  "name": "ActiveMQ Source A",
  "connector_type": "stomp",
  "format": "cot",
  "config": {
    "broker_url": "tcp://host:61613",
    "queue": "/queue/tracks"
  }
}
```

### `GET /feeds` response (extended)

Custom feeds include `"custom": true`; built-ins include `"custom": false`.

```json
{
  "id": "uuid",
  "name": "ActiveMQ Source A",
  "connector_type": "stomp",
  "format": "cot",
  "custom": true,
  "enabled": true,
  "health": {
    "status": "healthy",
    "lastSuccessAt": "2026-03-08T10:30:00Z",
    "entitiesCount": 42,
    "errorCount": 0
  }
}
```

### API Gateway

The existing feeds module in api-gateway proxies the new endpoints at `/api/v1/feeds`. No structural changes needed.

## Ingest-Service Feed Registry

The ingest-service feed registry (currently manages built-in connectors) is extended to:

1. On startup: load all `custom_feeds` rows from Postgres and start enabled connectors
2. On `POST /feeds`: persist to Postgres, start connector goroutine, register in health tracker
3. On `DELETE /feeds/:id`: stop goroutine, deregister from health tracker, delete from Postgres
4. On `PATCH /feeds/:id`: stop existing goroutine, update Postgres row, start new goroutine
5. On `PUT /feeds/:id` (toggle): update `enabled` in Postgres, start or stop goroutine accordingly

Each connector goroutine tags every produced `EntityPosition` with its feed's UUID as `FeedID`.

## Frontend Design

The existing data feeds panel (`data-feeds.component.ts`) receives two additions:

### "Add Feed" button
Opens a slide-in form with:
- **Name** — text input
- **Connector type** — select: MQTT / STOMP / TCP
- **Format** — select: JSON / NMEA / CoT / AIS / ADS-B / Link 16
- **Connection fields** — conditional on connector type:
  - MQTT: Broker URL, Topics (tag input), QoS (0 / 1 / 2)
  - STOMP: Broker URL, Queue
  - TCP: Address (host:port)

Submit calls `POST /feeds`. The new feed appears in the list immediately.

### Delete action
Custom feeds show a delete icon in their list row. Built-in feeds do not. Delete calls `DELETE /feeds/:id` and removes the row.

All other feed behaviors (health indicator, toggle, 30s refresh) are identical between custom and built-in feeds.

## Out of Scope

- Credential/authentication configuration for feed connections
- Custom feed types beyond MQTT, STOMP, and TCP
- Feed-level data transformation rules
