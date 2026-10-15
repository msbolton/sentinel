# API Gateway

The API Gateway (`apps/api-gateway/`) is the single backend entry point for the Angular client. It is a Go service that:

- authenticates requests against Keycloak and enforces role and clearance policies,
- reverse-proxies domain REST calls to the Go services with the caller's identity attached,
- owns the saved camera locations table,
- runs the self-service registration and user administration flow (Keycloak Admin API + SMTP),
- streams entity, ageout, and alert events to browsers over a native WebSocket fed from Kafka.

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
| `ENTITY_SERVICE_URL`, `TRACK_SERVICE_URL`, `SEARCH_SERVICE_URL`, `LINK_ANALYSIS_SERVICE_URL`, `ALERT_SERVICE_URL`, `INGEST_SERVICE_URL` | `http://localhost:300x` | Upstreams |
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
| `/api/v1/feeds/**` → `/feeds/**` | ingest-service | POST/PUT/PATCH/DELETE: `sentinel-operator`, `sentinel-admin` |

The role table lives in `internal/server/policy.go`. Note that `GET /api/v1/links/communities` returns an empty list: the Apache AGE community detection was not carried over to Go.

### Locations (owned by the gateway)

| Method | Path | Roles | Description |
|--------|------|-------|-------------|
| GET | `/api/v1/locations?category=&search=` | any | List, name sorted; `search` is a case-insensitive substring |
| GET | `/api/v1/locations/:id` | any | One location (400 on a non-UUID id, 404 when missing) |
| POST | `/api/v1/locations` | `sentinel-analyst`, `sentinel-admin` | Create (201); `name`, `latitude`, `longitude` required; defaults altitude 1000, heading 0, pitch -45, range 2000, category `CUSTOM` |
| PATCH | `/api/v1/locations/:id` | `sentinel-analyst`, `sentinel-admin` | Partial update |
| DELETE | `/api/v1/locations/:id` | `sentinel-admin` | Delete (204) |

Unknown body fields are rejected with 400, matching the former NestJS validation pipe.

### Registration and user administration

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| POST | `/api/v1/auth/register` | none, 5 per 15 min per IP | Creates a disabled Keycloak user with `organization`, `justification`, `registrationDate` attributes. 409 when the username or email exists |
| GET | `/api/v1/auth/pending-registrations` | `sentinel-admin` | Disabled users that carry `registrationDate` |
| POST | `/api/v1/auth/approve-registration/:userId` | `sentinel-admin` | Body `{ classificationLevel? }` (default `classification-u`). Enables the user, assigns `sentinel-viewer` plus the clearance role, clears `registrationDate`, triggers the verification email. If role assignment fails the user is re-disabled and stays pending |
| POST | `/api/v1/auth/reject-registration/:userId` | `sentinel-admin` | Deletes the user and emails them; a mail failure is logged, not surfaced |
| GET | `/api/v1/auth/users` | `sentinel-admin` | Enabled users (service accounts excluded) with roles and highest clearance |
| PUT | `/api/v1/auth/users/:userId/classification` | `sentinel-admin` | Body `{ classificationLevel }` in `classification-u`, `classification-s`, `classification-ts` |

## WebSocket

Endpoint: `GET /ws/entities`, same auth as REST. Browsers cannot set headers on a WebSocket, so the client passes the token as `?token=`. The Angular `WebSocketService` connects to `ws(s)://{host}/ws/entities`, reconnects with exponential backoff (1 s doubling to 10 s plus jitter) and replays its last viewport and entity subscriptions after every connect.

Every frame is a JSON envelope: `{ "type": string, "data": object, "ts": RFC3339 }`.

### Client to server

| Type | Data | Description |
|------|------|-------------|
| `viewport:update` | `{ north, south, east, west }` | Only entities inside these bounds are streamed. Bounds with `west > east` cross the antimeridian |
| `entity:subscribe` | `{ entityId }` | Always receive this entity, regardless of viewport |
| `entity:unsubscribe` | `{ entityId }` | Drop the subscription |
| `ping` | | Answered with `pong` |

### Server to client

| Type | Data | Description |
|------|------|-------------|
| `connection:ack` | `{ clientId, viewport, serverTime }` | Sent on connect; the initial viewport is the whole globe |
| `viewport:updated`, `entity:subscribed`, `entity:unsubscribed` | | Acknowledgements |
| `entity:batch` | `EntityEvent[]` | Created and updated entities coalesced over 200 ms, filtered per client |
| `entity:deleted` | `EntityEvent` | Broadcast to all clients; `bulk` and `count` are set for a clear-all |
| `events.entity.stale`, `events.entity.agedout`, `events.entity.restored` | raw Kafka payload | Broadcast to all clients |
| `alert:new` | `Alert` | New alert from `alerts.geofence` or `alerts.anomaly` |
| `error` | `{ code, message }` | Rejected inbound frame |

`EntityEvent` is `{ type: 'created' | 'updated' | 'deleted', entity: Entity, timestamp }` with the same `Entity` shape the REST API returns. A client whose send buffer fills (256 frames) is disconnected and reconnects.

### Kafka bridge

`internal/stream/bridge.go` consumes `events.entity.position`, `created`, `updated`, `deleted`, `stale`, `agedout`, `restored`, `alerts.geofence`, and `alerts.anomaly` with `auto.offset.reset=latest`. Entity payloads are accepted in both the snake_case NestJS shape (`entity_id`, `altitude_meters`) and the shape the Go entity-service emits (`id`, `altitude`). Viewport and subscription state is kept in memory per connection; for horizontal scaling run each instance with its own consumer group (the default) and nothing else needs to be shared.

## Authentication

### Production (Keycloak)

Tokens are RS256 JWTs validated against `{KEYCLOAK_URL}/realms/{realm}/protocol/openid-connect/certs`. The JWKS is fetched lazily on first use and cached, so the gateway starts before Keycloak is reachable. Checks: signature, `exp` (30 s leeway), `iss` in `KEYCLOAK_ISSUERS`, `aud` equal to `KEYCLOAK_AUDIENCE`, non-empty `sub`.

The authenticated user carries realm roles plus the `sentinel-api` client roles, and a clearance level. Clearance comes from the `classification_level` claim when present, otherwise from the highest `classification-*` realm role (`classification-ts` → TOP_SECRET, `-s` → SECRET, `-c` → CONFIDENTIAL, `-u` → UNCLASSIFIED). Roles are checked by name as Keycloak issues them: `sentinel-admin`, `sentinel-analyst`, `sentinel-operator`, `sentinel-viewer`.

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
│   ├── registration/             # Keycloak Admin client, SMTP mailer, rate limiter, /auth handler
│   └── stream/                   # WebSocket hub, client, batcher, Kafka bridge
├── Dockerfile
└── project.json
```
