# API Gateway

The API Gateway (`apps/api-gateway/`) is the single backend entry point for the Angular client. It is a Go service that:

- authenticates requests against Keycloak and enforces role and clearance policies,
- reverse-proxies domain REST calls to the Go services with the caller's identity attached,
- owns the saved camera locations table,
- runs the self-service registration and user administration flow (Keycloak Admin API + SMTP),
- streams entity, ageout, and alert events to browsers over a native WebSocket fed from Kafka,
- aggregates what connected clients are looking at and pushes that area to the ingest feeds that poll one.

## Running

```bash
npx nx serve api-gateway   # go run with AUTH_DEV_BYPASS=true
npx nx build api-gateway   # dist/apps/api-gateway/server
npx nx test api-gateway    # go vet + go test -race
```

Listens on port **3000** (`PORT`).

### Configuration

| Variable | Default | Purpose |
|----------|---------|---------|
| `APP_ENV` | `development` | `production` enables HSTS and refuses `AUTH_DEV_BYPASS` |
| `AUTH_DEV_BYPASS` | `false` | Treat every request as the built-in dev user; never in production |
| `KEYCLOAK_URL` | `http://localhost:8080` | Keycloak base URL (JWKS and Admin API) |
| `KEYCLOAK_REALM` | `sentinel` | Realm name |
| `KEYCLOAK_ISSUERS` | `{KEYCLOAK_URL}/realms/{realm}` | Comma-separated accepted `iss` values; tokens minted through the browser-facing hostname carry that hostname |
| `KEYCLOAK_AUDIENCE` | `sentinel-api` | Required `aud` claim; empty disables the check |
| `KEYCLOAK_CLIENT_ID` | `sentinel-api` | Client whose `resource_access` roles are merged into the user's roles |
| `KEYCLOAK_SERVICE_CLIENT_ID` / `_SECRET` | `sentinel-service` | Service account used for the Admin API |
| `MAIL_HOST` / `MAIL_PORT` / `MAIL_FROM` | `localhost:1025` | SMTP relay for registration notices (mailhog in dev) |
| `CORS_ORIGIN` | `http://localhost:4200` | Allowed origin for REST and WebSocket |
| `KAFKA_BROKERS` | `localhost:9092` | Brokers for the WebSocket bridge |
| `KAFKA_WS_CONSUMER_GROUP` | `sentinel-gateway-ws-{hostname}` | Unique per instance so every gateway sees every event |
| `VIEWPORT_SYNC_ENABLED` | `true` | Push the area clients are looking at to ingest-service (see Viewport-driven ingestion) |
| `VIEWPORT_SYNC_HEARTBEAT_SEC` | `30` | Re-push the viewport while clients are connected so the ingest lease stays fresh |
| `VIEWPORT_SYNC_DEBOUNCE_MS` | `1000` | Trailing debounce after viewport changes before pushing |
| `ENTITY_SERVICE_URL`, `TRACK_SERVICE_URL`, `SEARCH_SERVICE_URL`, `LINK_ANALYSIS_SERVICE_URL`, `ALERT_SERVICE_URL`, `INGEST_SERVICE_URL` | `http://localhost:300x` | Upstreams |
| `INGEST_SERVICE_TOKEN` | | Shared secret presented to the ingest management API as `X-Sentinel-Service-Token` on every proxied feed request and area push; required when the ingest service runs in production |
| `INTELSKY_BASE_URL` | `https://intelsky.org` | Where flight paths come from |
| `DB_HOST` … or `DATABASE_URL` | | PostgreSQL for the locations table |

## REST API

Base path: `/api/v1`. All responses are JSON. Errors use `{ "statusCode": n, "message": "..." }`.

### Health

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| GET | `/api/v1/health`, `/health` | No | Liveness |
| GET | `/api/v1/health/ready`, `/ready` | No | Readiness with dependency checks (`database`) |

### Proxied routes

Everything below requires a bearer token (or the dev bypass). The gateway forwards the request unchanged, adding `x-sentinel-user-id`, `x-sentinel-user-roles`, and `x-sentinel-classification`; any copies of those headers sent by the caller are stripped. An unreachable upstream yields `503 {"message": "<service> unavailable"}`.

| Prefix | Upstream | Write roles |
|--------|----------|-------------|
| `/api/v1/entities/**` (incl. `/entities/ageout-config`) | entity-service | POST/PATCH/PUT: `sentinel-analyst`, `sentinel-admin`. DELETE: `sentinel-admin` with SECRET clearance. Ageout config PUT/DELETE: `sentinel-operator`, `sentinel-admin` |
| `/api/v1/tracks/**` (incl. `/tracks/timeline`) | track-service | read only |
| `/api/v1/alerts/**` | alert-service | PATCH acknowledge/resolve and rule writes: `sentinel-analyst`, `sentinel-admin` |
| `/api/v1/links/**` | link-analysis-service | POST/DELETE: `sentinel-analyst`, `sentinel-admin` |
| `/api/v1/search/**` | search-service | read only |
| `/api/v1/feeds/**` → `/feeds/**` (incl. `GET /feeds/ports`, port hints for a listening connection, and `GET /feeds/{id}/deadletters`, a connection's newest dead letters, and `/feeds/{id}/tokens`, a gRPC connection's ingest tokens) | ingest-service | POST/PUT/PATCH/DELETE: `sentinel-operator`, `sentinel-admin`, which covers creating and revoking tokens. `GET /feeds/{id}/deadletters` and `GET /feeds/{id}/tokens` also need one of those roles and a clearance at or above the feed's, which the ingest service checks from the propagated identity (see `docs/feeds.md`) |
| `/api/v1/drivers/**` → `/drivers/**` | ingest-service | read only: the driver catalog with config schemas |
| `/api/v1/emplacements/**` → `/emplacements/**` | ingest-service | `GET /emplacements` (query `feedId`, `mobility`, `active`), `POST /emplacements`, `GET`/`PATCH`/`DELETE /emplacements/{id}`. POST/PATCH: `sentinel-operator`, `sentinel-admin`; DELETE: `sentinel-admin`. PATCH takes `If-Match: "<version>"` and returns 412 on a stale version; responses carry `ETag: "<version>"`. A row carries `heartbeat` and `silentSince` for an active emplacement bound to a `grpc` connection. Reads hide emplacements above the caller's clearance (404), and writes cannot set a classification above it (422) |
| `/api/v1/analytics/**` | analytics-service | PATCH findings (confirm/dismiss): `sentinel-analyst`, `sentinel-admin` |

The role table lives in `internal/server/policy.go`. Note that `GET /api/v1/links/communities` returns an empty list: the Apache AGE community detection was not carried over to Go.

### Behavioural analysis (analytics-service)

Every read applies the caller's clearance as a ceiling: an entity or finding above it does not exist for them.

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/v1/analytics/findings` | Findings, newest evidence first. Filters `status` (OPEN, CONFIRMED, DISMISSED, CLOSED), `kinds` (comma-separated: UNUSUAL_PLACE, UNUSUAL_TIME, UNUSUAL_MOVEMENT, WENT_DARK, REAPPEARED), `severity`, `entityId`, `since` (RFC 3339), `page`, `pageSize` |
| GET | `/api/v1/analytics/findings/{id}` | One finding with its expected and actual values, thresholds, evidence window, hits and review |
| PATCH | `/api/v1/analytics/findings/{id}` | `{"status": "CONFIRMED" \| "DISMISSED"}`; confirming publishes the finding as an alert if it has not been. 400 for another status, 409 when the finding is already closed or dismissed |
| GET | `/api/v1/analytics/entities/{id}/baseline` | What is normal for the entity: fixes and days, maturity, places, range, speed and altitude statistics, reporting gaps, the 168-slot hour-of-week histogram in local solar time. 404 while there is none |
| GET | `/api/v1/analytics/entities/{id}/findings` | The entity's findings, same filters as the list |
| GET | `/api/v1/analytics/stats` | Counters: positions judged, findings opened, alerts published, baselines, open findings by severity and kind |

A finding that reaches HIGH, or is confirmed, becomes an alert on `alerts.events` with the finding's id as the alert id and `metadata.findingId`, `kind`, `score`, `hits`; it then appears in `/api/v1/alerts`, on the `alert:new` WebSocket frame and on outbound connections like any other alert.

### Locations (owned by the gateway)

| Method | Path | Roles | Description |
|--------|------|-------|-------------|
| GET | `/api/v1/locations?category=&search=` | any | List, name sorted; `search` is a case-insensitive substring |
| GET | `/api/v1/locations/:id` | any | One location (400 on a non-UUID id, 404 when missing) |
| POST | `/api/v1/locations` | `sentinel-analyst`, `sentinel-admin` | Create (201); `name`, `latitude`, `longitude` required; defaults altitude 1000, heading 0, pitch -45, range 2000, category `CUSTOM` |
| PATCH | `/api/v1/locations/:id` | `sentinel-analyst`, `sentinel-admin` | Partial update |
| DELETE | `/api/v1/locations/:id` | `sentinel-admin` | Delete (204) |

Unknown body fields are rejected with 400, matching the former NestJS validation pipe.

### Flight paths (IntelSky, owned by the gateway)

| Method | Path | Roles | Description |
|--------|------|-------|-------------|
| GET | `/api/v1/aircraft/{hex}/path?days=1\|3\|7` | any | Where a military aircraft has flown: today (`days=1`, the default) from IntelSky's `stats/map.php?ajax_history=1`, or a range of UTC days from `stats/stats.php?ajax_track=1`. Answers `{hex, source: "INTELSKY", days, points: [{lat, lon, altFt, ts}] (oldest first), meta?}`, `meta` (callsign, registration, type, operator, description, firstSeen, lastSeen in IntelSky's local time) for ranges only. 400 for a hex that is not six hex digits or days outside 1 to 7; 503 with `Retry-After` when IntelSky or the gateway's own budget (100 a minute, half of IntelSky's limit) says wait; 502 when IntelSky fails; 200 with `points: []` when it knows nothing |

Answers are cached per aircraft and range (a minute for today, ten minutes for ranges, 500 entries) and concurrent misses share one request.

### Registration and user administration

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| POST | `/api/v1/auth/register` | none, 5 per 15 min per IP | Creates a disabled Keycloak user with `organization`, `justification`, `registrationDate` attributes. 409 when the username or email exists |
| GET | `/api/v1/auth/pending-registrations` | `sentinel-admin` | Disabled users that carry `registrationDate` |
| POST | `/api/v1/auth/approve-registration/:userId` | `sentinel-admin` | Body `{ classificationLevel? }` (default `classification-u`). Enables the user, assigns `sentinel-viewer` plus the clearance role, clears `registrationDate`, triggers the verification email. If role assignment fails the user is re-disabled and stays pending |
| POST | `/api/v1/auth/reject-registration/:userId` | `sentinel-admin` | Deletes the user and emails them; a mail failure is logged, not surfaced |
| GET | `/api/v1/auth/users` | `sentinel-admin` | Enabled users (service accounts excluded) with roles and highest clearance |
| PUT | `/api/v1/auth/users/:userId/classification` | `sentinel-admin` | Body `{ classificationLevel }` in `classification-u`, `classification-c`, `classification-s`, `classification-ts` |

## WebSocket

Endpoint: `GET /ws/entities`, same auth as REST. Browsers cannot set headers on a WebSocket, so the client passes the token as `?token=`. The Angular `WebSocketService` connects to `ws(s)://{host}/ws/entities`, reconnects with exponential backoff (1 s doubling to 10 s plus jitter) and replays its last viewport and entity subscriptions after every connect.

Every frame is a JSON envelope: `{ "type": string, "data": object, "ts": RFC3339 }`.

### Client to server

| Type | Data | Description |
|------|------|-------------|
| `viewport:update` | `{ north, south, east, west }` | Only entities inside these bounds are streamed. Bounds with `west > east` cross the antimeridian. The client sends it after every camera settle, including the whole globe (`{90, -90, 180, -180}`), and once right after the map initialises |
| `entity:subscribe` | `{ entityId }` | Always receive this entity, regardless of viewport |
| `entity:unsubscribe` | `{ entityId }` | Drop the subscription |
| `ping` | | Answered with `pong` |

### Server to client

| Type | Data | Description |
|------|------|-------------|
| `connection:ack` | `{ clientId, viewport, serverTime }` | Sent on connect; the initial viewport is the whole globe |
| `viewport:updated`, `entity:subscribed`, `entity:unsubscribed` | | Acknowledgements |
| `entity:batch` | `EntityEvent[]` | Created, updated and position events coalesced over 200 ms, filtered per client; every entity carries `feedId`, `ageoutState`, `identities` and `lastObservedAt` (`exercise: true` when the entity is exercise data, omitted when false) |
| `entity:deleted` | `EntityEvent` | Broadcast to all clients; `bulk` and `count` are set for a clear-all |
| `events.entity.stale`, `events.entity.agedout`, `events.entity.restored` | `{ id, feedId, ageoutState, lastSeenAt }[]` | Broadcast to all clients, coalesced into one array frame per type per 200 ms flush (the ageout pass can emit a thousand per cycle) |
| `alert:new` | `Alert` | New alert from `alerts.events`, whichever service produced it |
| `error` | `{ code, message }` | Rejected inbound frame |

`EntityEvent` is `{ type: 'created' | 'updated' | 'deleted', entity: Entity, timestamp }` with the same `Entity` shape the REST API returns. A client whose send buffer fills (256 frames) is disconnected and reconnects.

### Viewport-driven ingestion

The gateway is the only component that knows what every connected client is looking at, so it also decides which area the area-aware feeds poll (`internal/stream/viewport_agg.go`, `viewport_sync.go`, `feed_directory.go`). Every 30 s it asks ingest-service for the enabled feeds whose driver advertises the `area` capability (`GET /feeds?capability=area&enabled=true`) and pushes to each of them; a 404 or 409 evicts a feed from the set until the next refresh.

1. Every `viewport:update` and every disconnect notifies the `ViewportSync` loop. Clients that have not sent a viewport yet are ignored, so a fresh connection does not widen the area to the globe.
2. After a 1 s debounce the loop pads each client viewport by 10 % of its span (at least 0.25°), takes the smallest box covering all of them with the longitude union computed on the circle, and splits it at the antimeridian into at most two boxes. A padded extent covering 90 % of the globe becomes the global box.
3. It pushes `PUT {INGEST_SERVICE_URL}/feeds/{id}/area` with `{ active, clients, boxes }` only when the change matters: the first time, when the unpadded extent leaves the last pushed padded one, when the area shrinks below 70 % of the previous one, or when the client count crosses zero. Small pans stay inside the padding and cost nothing. Pushes are at least 2 s apart.
4. Every 30 s it re-pushes while clients are connected; the area lease stored in `feed_areas` expires after 90 s without a push, so a dead gateway stops the polling and a restarted or newly elected ingest replica recovers within one heartbeat. Shutdown pushes `active: false`.

Failed pushes retry with a 1 s doubling to 30 s backoff. `GET /api/v1/feeds/{id}/area` (proxied to ingest-service) shows the area a feed is polling, with `idle`, `effectiveIntervalSec` and, for OpenSky, `creditsRemaining`.

### Kafka bridge

`internal/stream/bridge.go` consumes the protobuf `entities.events` and `alerts.events` topics with `auto.offset.reset=latest` and is the one place the wire contract is adapted for the browser: it routes on the event oneof (created, updated and position become `entity:batch`; deleted becomes `entity:deleted` with the entity id; stale, aged out and restored become the ageout frames) and converts the `sentinel.v1.Entity` snapshot into the JSON shape the REST API returns. Viewport and subscription state is kept in memory per connection; for horizontal scaling run each instance with its own consumer group (the default) and nothing else needs to be shared.

## Authentication

### Production (Keycloak)

Tokens are RS256 JWTs validated against `{KEYCLOAK_URL}/realms/{realm}/protocol/openid-connect/certs`. The JWKS is fetched lazily on first use and cached, so the gateway starts before Keycloak is reachable. Checks: signature, `exp` (30 s leeway), `iss` in `KEYCLOAK_ISSUERS`, `aud` equal to `KEYCLOAK_AUDIENCE`, non-empty `sub`.

The authenticated user carries realm roles plus the `sentinel-api` client roles, and a clearance level. Clearance comes from the `classification_level` claim when present, otherwise from the highest `classification-*` realm role (`classification-ts` → TOP_SECRET, `-s` → SECRET, `-c` → CONFIDENTIAL, `-u` → UNCLASSIFIED). Roles are checked by name as Keycloak issues them: `sentinel-admin`, `sentinel-analyst`, `sentinel-operator`, `sentinel-viewer`.

### Export for GIS consumers and API keys

| Method | Path | Roles | Notes |
|--------|------|-------|-------|
| GET | `/api/v1/export/entities.geojson` | any | FeatureCollection of live entities with a position (`[lon, lat, alt]` points; properties `name`, `entityType`, `classification`, `exercise`, `feedId`, `lastFeedId`, `source`, `heading`, `speedKnots`, `course`, `altitude`, `sidc`, `affiliation`, `trackEnvironment`, `countryOfOrigin`, `ageoutState`, `lastObservedAt`). Query `bbox=west,south,east,north`, `types=`, `feeds=`, `page` (1), `limit` (1000, max 5000). `X-Total-Count` header. Reads through the entity service with the caller's identity, so the clearance ceiling applies |
| GET | `/api/v1/export/entities.kml` | any | The same as KML Placemarks (ExtendedData, `TimeStamp`, absolute altitude). With `link=1&refresh=30` it returns a `NetworkLink` document that reopens the plain URL every `refresh` seconds (min 5) and after the view stops, passing the view as `bbox`; every other parameter, including `api_key`, is kept in the link |
| GET | `/api/v1/auth/api-keys` | any user token | The caller's live keys (`id`, `name`, `prefix`, `classification`, `createdAt`, `expiresAt`, `lastUsedAt`) |
| POST | `/api/v1/auth/api-keys` | any user token | Body `{ name, classification?, expiresInDays? }`. 201 with the key plus `secret` (`sk_…`, shown once). A key carries the viewer role only and `classification = min(requested, caller's clearance)` |
| DELETE | `/api/v1/auth/api-keys/{id}` | owner or admin | Revokes; 404 when not the caller's |

Google Earth and other KML or GIS tools cannot send an `Authorization` header, so an API key may be presented as the bearer token or, on `/api/v1/export/*` only, as `?api_key=sk_…`. The middleware tells a key from a JWT by the `sk_` prefix, resolves it through `sentinel.api_keys` (SHA-256 at rest, revoked and expired keys refused, `lastUsedAt` stamped) and runs the request as the owner with viewer rights and the key's classification. A principal that authenticated with a key cannot list, make or revoke keys. With `AUTH_DEV_BYPASS` every request is the dev user and keys are never consulted.

Clearance is a ceiling on every read. The services apply `x-sentinel-classification` themselves (`libs/common-go/classification` gives them one ordering): entity lists, nearby, counts, by-id and observations; alerts; search, nearby search, suggestions and facets; track history, latest, segments and the timeline. An entity or alert above the caller's clearance answers 404, a list simply omits it, and a missing header fails closed to UNCLASSIFIED (so a service called directly, without the gateway, serves only unclassified rows). The WebSocket stream filters the same way: entity frames and `alert:new` are sent only to clients cleared to at least the row's level; id-only frames (`entity:deleted`, ageout) are not filtered. `PATCH /api/v1/entities/{id}` refuses to mark an entity above the caller's clearance (403).

Denials are logged as structured audit events (`ACCESS_DENIED`, `ROLE_CHECK_FAILED`, `CLASSIFICATION_CHECK_FAILED`) with the user, method, path, and client IP.

The realm's `sentinel-web` client has an audience mapper that adds `aud=sentinel-api` to access tokens. A Keycloak database imported before that mapper existed will not have it; either add the mapper in the admin console or set `KEYCLOAK_AUDIENCE=` to skip the check.

### Development (auth bypass)

With `AUTH_DEV_BYPASS=true` (set by `nx serve` and `docker-compose.yml`) every request, including the WebSocket, runs as:

```json
{
  "userId": "00000000-0000-0000-0000-000000000001",
  "username": "dev-operator",
  "roles": ["sentinel-analyst", "sentinel-operator", "sentinel-admin"],
  "classificationLevel": "TOP_SECRET"
}
```

The gateway refuses to start with the bypass enabled when `APP_ENV=production`.

### Security headers

Every response carries `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`, `X-XSS-Protection`, `Referrer-Policy: strict-origin-when-cross-origin`, `Cache-Control: no-store`, `Permissions-Policy`, and, in production, `Strict-Transport-Security`.

## Package layout

```
apps/api-gateway/
├── cmd/server/main.go            # Bootstrap, wiring, graceful shutdown
├── internal/
│   ├── config/                   # Environment configuration
│   ├── auth/                     # JWKS verifier, RequireAuth/RequireRoles/RequireClassification, audit log, security headers
│   ├── proxy/                    # Reverse proxy with identity header propagation
│   ├── server/                   # Router, health endpoints, role policy table
│   ├── locations/                # pgx store + handler for sentinel.locations
│   ├── flightpath/               # IntelSky flight paths: cached, rate-budgeted proxy for /api/v1/aircraft/{hex}/path
│   ├── registration/             # Keycloak Admin client, SMTP mailer, rate limiter, /auth handler
│   └── stream/                   # WebSocket hub, client, batcher, Kafka bridge, viewport aggregation and feed area sync
├── Dockerfile
└── project.json
```
