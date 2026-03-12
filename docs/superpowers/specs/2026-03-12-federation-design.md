# Federation — Design Spec

## Goal

Enable cross-instance collaborative situational awareness by federating entity data and user presence between independently deployed Sentinel instances. Instances discover each other automatically (LAN-first, internet-capable), authenticate, and stream entities and user viewport positions in near real-time (< 2s). Sharing is gated by classification level and admin-configured allowlists.

## Approach

Direct WebSocket peering between instances. Each Sentinel API Gateway gains a Federation Module that manages peer connections, discovery, sharing policy enforcement, and data exchange. No shared infrastructure required — each instance is self-contained.

## Scale

5–20 peer instances. At 20 nodes the full mesh is 190 connections — manageable for WebSocket with heartbeats.

## Data Streams

### Entity Stream

The existing Kafka Consumer (`apps/api-gateway/src/modules/gateway/kafka-consumer.service.ts`) subscribes to `events.entity.created`, `events.entity.updated`, `events.entity.position`, and `events.entity.deleted` topics. When it processes these events, it additionally publishes them to a new internal topic `federation.entity.outbound`. The Federation Module consumes this topic, runs each event through the Sharing Policy filter, and forwards qualifying entities to all connected peers.

On the receiving side, inbound entities are tagged with `sourceInstanceId` and `sourceInstanceName`, then injected into the Entity Gateway's broadcast pipeline. They flow to connected browsers like local entities but carry federation metadata. The Entity Gateway (`apps/api-gateway/src/modules/gateway/entity.gateway.ts`) broadcasts these to browser clients via the existing `entity:batch` Socket.IO event on the `/entities` namespace — the same channel used for local entities.

Federated entities are never re-shared to other peers (no circular forwarding). The outbound filter skips any entity where `sourceInstanceId` is set.

### Presence Stream

Each instance sends a lightweight presence message per connected user every 1–2 seconds:

```json
{
  "userId": "uuid",
  "displayName": "j.smith",
  "cameraCenter": { "lat": 34.05, "lon": -118.25 },
  "zoom": 8,
  "timestamp": 1710000000
}
```

This is the user's map camera position, not their physical location. Presence entries expire after 5 seconds of no updates.

### Wire Protocol

JSON over WebSocket with envelope:

```json
{
  "type": "fed:entity:batch | fed:presence:update | fed:presence:remove | fed:handshake | fed:heartbeat",
  "sourceInstanceId": "sentinel-alpha",
  "classificationLevel": "classification-s",
  "payload": {}
}
```

The `fed:handshake` message is exchanged on connection. Peers share: instance ID, display name, classification level, and protocol version. If protocol versions don't match, the connection is rejected with a `version-mismatch` close reason — no graceful degradation in v1.

All federation wire protocol message types use the `fed:` prefix to distinguish them from gateway-to-browser Socket.IO events (`entity:batch`, `entity:created`, etc.) which operate on a different channel (port 3000 `/entities` namespace vs. federation port 3100).

## Discovery

### mDNS (LAN)

The Federation Module advertises `_sentinel-fed._tcp` via mDNS, announcing instance ID, display name, federation port, and protocol version. The federation port defaults to `3100` (configurable via `FEDERATION_PORT` env var) — a dedicated port separate from the API Gateway's port 3000. This keeps federation traffic isolated from client-facing HTTP/WebSocket traffic. When a new peer appears, the Peer Manager initiates a connection.

### Seed List (WAN)

For non-LAN peers, admins configure a list of peer URLs (via Settings UI or environment variables). The Peer Manager periodically attempts to connect to seed list entries that aren't already connected.

Both discovery methods feed into the same Peer Manager connection lifecycle.

## Connection Lifecycle

1. **Discovery** — mDNS announcement or seed list entry
2. **Connect** — WebSocket connection to peer's federation port
3. **Handshake** — Exchange instance identity, classification level, protocol version
4. **Policy Check** — Sharing Policy evaluates whether this peer is allowed
5. **Streaming** — Bidirectional entity + presence flow begins
6. **Heartbeat** — Every 10s. Three missed heartbeats = stale, trigger reconnect
7. **Disconnect** — Graceful (peer shutting down) or ungraceful (network loss)

**Reconnection:** Exponential backoff starting at 2s, max 60s. On reconnect, full state sync replays current entities (not historical).

**Disconnect behavior:** Remote entities marked stale after 30s, removed after 60s. Presence markers removed immediately.

## Authentication

- **Production:** mTLS — instances exchange certificates. Peer identity verified against certificate CN/SAN. Certificates are stored on the filesystem and referenced via environment variables: `FEDERATION_TLS_CERT`, `FEDERATION_TLS_KEY`, `FEDERATION_TLS_CA`. The CA model is organization-managed — each deployment's ops team provisions certs signed by a shared CA. Certificate rotation is handled outside Sentinel (standard cert renewal; the federation server reloads certs on SIGHUP).
- **Development:** Pre-shared keys — configured via environment variable `FEDERATION_PSK`. All dev instances sharing the same PSK can peer freely.

Peers must authenticate before any data flows.

## Sharing Policy

### Classification Ceiling

Each instance has an instance-level classification ceiling, configured via the `FEDERATION_CLASSIFICATION` environment variable (one of `classification-u`, `classification-s`, `classification-ts`) and stored in the `federation_config` table. This is distinct from per-user classification roles — it represents the maximum classification level the entire instance is authorized to handle. During handshake, peers exchange their instance classification levels. The connection's ceiling is the lower of the two. Entities classified above the ceiling are never sent.

### Admin Allowlists

Configured in the Settings page under a new **Federation** tab (admin-only). Admins can:

- Enable/disable federation globally
- Accept or block specific peers by instance ID
- Filter by entity type (e.g., share aircraft and ships but not ground vehicles)
- Filter by geographic bounds (bounding box, using existing `@sentinel/common` geo utilities)

### Filtering Pipeline

**Outbound:** Local entity update → classification check → entity type check → geo bounds check → send to peer. All filters must pass.

**Inbound:** Same checks in reverse (defense in depth). The receiving instance validates against its own policy before injecting into the Entity Gateway.

### Storage

Sharing policies stored in PostgreSQL (`federation_peers` and `federation_policies` tables). Redis caches the active policy per peer for fast filtering.

## Map Rendering

The map component (`apps/web/src/app/features/map/map.component.ts`) is already ~946 lines. Federation rendering is decomposed into separate units to avoid bloating it further:

- **`federation-overlay.service.ts`** — Manages all CesiumJS primitives for federated data: entity rings, source badges, presence markers, viewport cones. The map component calls this service when federation data arrives, but doesn't know the rendering details.
- **`federation-status.component.ts`** — Standalone component for the status badge and source legend, overlaid on the map via absolute positioning. Receives federation state from `federation.service.ts`.

### Federated Entities

- Colored ring around the entity icon (rendered as an additional billboard in the existing BillboardCollection)
- Small source badge on the label (e.g., "HAWK-9 `BRAVO`")
- Each peer instance auto-assigned a unique color from a palette
- Visually distinct from local entities at a glance

### Presence Markers

- Colored dot with username and instance name label
- Translucent viewport cone indicating approximate view area
- Color matches the peer instance's assigned color
- Fade out after 5s of no updates

### Federation Status Badge

Top-right corner of the map (standalone component) showing:
- Connected peers with green/yellow/red status dot
- User count and entity count per peer

### Layer Toggles

The existing layer toggle system extends to include one toggle per peer instance, so users can show/hide federated data from specific peers.

### Source Legend

Bottom-left legend mapping colors to instance names (local + each connected peer).

## Backend Changes

### New Module: `apps/api-gateway/src/modules/federation/`

| File | Purpose |
|---|---|
| `federation.module.ts` | NestJS module wiring |
| `federation.gateway.ts` | WebSocket server for incoming peer connections |
| `peer-manager.service.ts` | Outbound connections, reconnection, heartbeats |
| `discovery.service.ts` | mDNS advertisement + listening, seed list polling |
| `sharing-policy.service.ts` | Classification + allowlist filtering (inbound/outbound) |
| `federation.controller.ts` | REST endpoints for admin config (peer list, policies) |
| `federation.entity.ts` | TypeORM entities for `federation_peers` and `federation_policies` |

### Modified Backend Files

| File | Change |
|---|---|
| `apps/api-gateway/src/modules/gateway/entity.gateway.ts` | Accept federated entities from PeerManager, broadcast with source metadata |
| `apps/api-gateway/src/modules/gateway/kafka-consumer.service.ts` | Forward local entity events to `federation.entity.outbound` topic |

### New Kafka Topic

`federation.entity.outbound` — internal topic decoupling federation from the existing entity pipeline.

### Database Migration

**`federation_peers`:** `instance_id` (PK), `display_name`, `url`, `classification_level`, `status`, `last_seen`, `color` (auto-assigned)

**`federation_policies`:** `id` (PK), `peer_instance_id` (FK), `entity_types_allowed` (array), `geo_bounds` (JSON), `enabled` (boolean)

**`federation_config`:** `id` (PK, singleton row), `instance_id` (UUID, generated on first boot), `display_name` (string, admin-configurable), `classification_level` (string, from `FEDERATION_CLASSIFICATION` env var), `federation_enabled` (boolean)

## Frontend Changes

### New Frontend Files

| File | Purpose |
|---|---|
| `core/services/federation.service.ts` | Federation status, peer list, policy CRUD, presence state |
| `features/map/federation-overlay.service.ts` | CesiumJS rendering for federated entities and presence markers |
| `features/map/federation-status.component.ts` | Status badge + source legend overlay component |

### Modified Frontend Files

| File | Change |
|---|---|
| `shared/models/entity.model.ts` | Add optional `sourceInstanceId`, `sourceInstanceName` fields |
| `features/map/map.component.ts` | Integrate `federation-overlay.service.ts`, host `federation-status.component.ts` |
| `core/services/websocket.service.ts` | Handle `federation:status` and `presence:update` events |
| `features/settings/settings.component.ts` | Add Federation tab (admin-only, `activeTab` union type expanded to include `'federation'`) for peer management and policy config |

## Instance Identity

Each instance generates a stable UUID on first boot, stored in the `federation_config` database table alongside the admin-configurable display name. Used in all federation messages.

## Edge Cases

- **Duplicate entities:** Same real-world entity tracked by both instances appears as separate markers. Deduplication is out of scope for v1 — hard correlation problem. Users identify duplicates visually.
- **Network partition:** Entities marked stale (dimmed) after 30s, removed after 60s. Presence removed immediately. Status badge shows yellow then red. Full state sync on reconnect.
- **Classification change mid-session:** Existing connections re-evaluated. Connections that no longer satisfy the ceiling are gracefully terminated with `policy-violation` close reason.
- **High entity volume:** Existing entity eviction (max 5000, LRU) applies globally to local + federated. Federated entities have lower eviction priority than local entities — locals are never evicted to make room for federated data. Layer toggles per peer manage visual clutter.
- **Circular forwarding:** Federated entities (`sourceInstanceId` set) are never re-shared. Outbound filter skips them.
- **Clock skew:** Presence expiry and entity staleness use relative durations, not absolute timestamps.
