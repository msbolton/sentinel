# Go API Gateway and Migration Completion — Design Spec

## Goal

Finish SEN-82 so the backend is Go only. Commit 10e7a6d ported entity, track, alert, link-analysis, and search services to Go but left `apps/api-gateway` in NestJS and left gaps that block a clean boot: no schema creation, missing gateway routes for alerts, links, search, tracks, and ageout-config, no Go tests, and compose and docs that still describe NestJS.

Success: `docker compose down -v && docker compose up --build` on a clean machine brings up a Go-only backend with every Angular client feature working, and `make test-go` runs real tests.

## Decisions

- **Native WebSocket replaces Socket.IO.** The Go gateway uses `github.com/coder/websocket`. The Angular `WebSocketService` is rewritten behind an unchanged public API so no consumer changes.
- **The gateway is a thin reverse proxy for domain REST.** It owns only auth, registration, the websocket stream, locations, and health. The NestJS gateway's duplicate Redis geo cache and unconsumed Kafka emits are dropped; entity-service already owns both.
- **Schema lives in versioned SQL migrations** under `db/migrations`, run by the official `migrate/migrate` image. Per-service startup DDL is rejected because `sentinel.entities` is shared by four services; initdb scripts are rejected because they never update an existing volume.
- **Role names use the Keycloak `sentinel-` prefix everywhere.** The NestJS controllers checked `analyst` and `admin`, which Keycloak never issues; the dev bypass hid the mismatch.
- **Delivery is phased on one branch** with one commit per phase: migrations, gateway core, registration, websocket plus client, cleanup, tests.

## Schema baseline (`db/migrations/000001_baseline`)

Column names come from the Go stores. Four tables are camelCase in Go where the old seed script was snake_case.

| Table | Source of truth | Notes |
|---|---|---|
| `sentinel.entities` | `apps/entity-service/internal/store/entity_store.go` | 36 columns incl. `kinematics`, `"platformData"` JSONB, `"ageoutState" NOT NULL DEFAULT 'LIVE'`; `"createdAt"/"updatedAt" NOT NULL DEFAULT NOW()` (scanned as text) |
| `sentinel.track_points` | `apps/track-service/internal/store/track_store.go` | camelCase; no PK (hypertable); index `("entityId", "timestamp" DESC)`; conditional `create_hypertable` |
| `sentinel.alerts`, `sentinel.alert_rules` | `apps/alert-service/internal/store/alert_store.go` | camelCase; `alerts."entityId" NOT NULL` |
| `sentinel.links` | `apps/link-analysis-service/internal/store/link_store.go` | camelCase; indexes on both entity columns |
| `sentinel.locations` | `apps/api-gateway/src/modules/locations/location.entity.ts` | seed DDL already matches |
| `sentinel.feed_ageout_config` | `apps/entity-service/internal/store/ageout_config_store.go` | three partial unique indexes required by `ON CONFLICT ... WHERE` and `SeedDefaults` |
| `sentinel.anomalies` | `apps/analytics-service/sentinel_analytics/db.py` | snake_case |

Enum columns are `VARCHAR`; the Go stores bind plain strings. `config/postgres/02-enum-updates.sql` is deleted. `ingest-service` keeps its idempotent `custom_feeds` DDL.

Bug fixed alongside: `db.py` `fetch_track_points` selects `entity_id` and `speed_knots`, which the Go track-service never writes.

The seed script becomes `db/seed/seed.sql` (inserts only) applied through `psql`.

## Gateway (Go module `github.com/sentinel/api-gateway`)

Layout mirrors `apps/entity-service`: `cmd/server/main.go`, `internal/config`, `internal/auth`, `internal/proxy`, `internal/locations`, `internal/registration`, `internal/stream`.

Routes under `/api/v1`:

| Route | Target | Auth |
|---|---|---|
| `/health`, `/health/ready`, root `/health`, `/ready` | local | none |
| `/entities/**` incl. `/entities/ageout-config/**` | entity-service | JWT; POST/PATCH `sentinel-analyst` or `sentinel-admin`; DELETE `sentinel-admin` |
| `/tracks/**` | track-service | JWT |
| `/alerts/**` | alert-service | JWT; writes `sentinel-analyst` or `sentinel-admin` |
| `/links/**` | link-analysis-service | JWT; POST/DELETE `sentinel-analyst` or `sentinel-admin` |
| `/search/**` | search-service | JWT |
| `/feeds/**` to ingest `/feeds/**` | ingest-service | JWT; mutations `sentinel-operator` or `sentinel-admin` |
| `/locations/**` | local | JWT; POST/PATCH `sentinel-analyst` or `sentinel-admin`; DELETE `sentinel-admin` |
| `/auth/**` | local | register is public and rate limited; the rest `sentinel-admin` |
| `GET /ws/entities` | local | JWT via `Sec-WebSocket-Protocol` or `?token=` |

Auth: `golang-jwt/jwt/v5` with `MicahParks/keyfunc/v3` against the Keycloak JWKS endpoint, issuer `KEYCLOAK_URL/realms/{realm}`, audience `sentinel-api`, RS256. The user struct matches `jwt.strategy.ts`. `AUTH_DEV_BYPASS=true` injects the same dev user as `jwt-auth.guard.ts`; it is set in compose and nx serve, never by default. Security headers from `security-headers.middleware.ts`; HSTS only when `APP_ENV=production`. Audit log as structured zap JSON with the same event types as `auth-audit.service.ts`.

Proxy: one `httputil.ReverseProxy` per upstream. Injects `x-sentinel-user-id`, `x-sentinel-user-roles`, `x-sentinel-classification` (constants in `libs/common-go/middleware/auth.go`), strips inbound copies, maps dial errors to 503 `{statusCode, message}`.

Registration: Keycloak Admin REST with cached client-credentials token (30 s early refresh), `net/smtp` mailer to `MAIL_HOST:MAIL_PORT` without TLS, per-IP limiter of 5 per 15 minutes on register, the seven routes the client calls, JSON shapes identical to the TS `PendingUser` and `ActiveUser` interfaces.

Track-service gains `GET /api/v1/tracks/timeline?startTime&endTime&buckets` returning `[{timestamp, count}]`, which the timeline view already requests.

## WebSocket protocol

Endpoint `GET /ws/entities`. Frames are JSON text `{"type": string, "data": object, "ts": ISO-8601}`.

Client to server: `viewport:update {north,south,east,west}`, `entity:subscribe {entityId}`, `entity:unsubscribe {entityId}`.

Server to client: `connection:ack {clientId, viewport, serverTime}`, `viewport:updated {viewport}`, `entity:batch EntityEvent[]`, `entity:deleted EntityEvent` (with `bulk` and `count` for clear-all), `events.entity.stale|agedout|restored` raw payload, `alert:new Alert` (with `acknowledged` derived from `acknowledgedAt`), `error {code, message}`.

Server: in-memory hub (viewport and subscriptions per connection; Redis dropped because the NestJS service only ever read its local cache), one read and one write goroutine per connection with a bounded send buffer, 200 ms batcher, Kafka bridge on the seven entity topics plus `alerts.geofence` and `alerts.anomaly` with `auto.offset.reset=latest`. `EntityEvent` shape is built exactly as `entity.gateway.ts` lines 274-300 so client models do not change. For horizontal scale later, each instance uses a unique consumer group.

Client: only `apps/client/src/app/core/services/websocket.service.ts` changes. Reconnect with exponential backoff 1 s to 10 s plus jitter, infinite attempts; replay the last viewport and subscriptions after each open. `proxy.conf.json` routes `/ws`; `nginx.conf` gains `/api` and `/ws` proxy blocks.

## Cleanup

Remove all NestJS, TypeORM, Passport, Socket.IO, kafkajs, ioredis, GraphQL, and mailer packages from `package.json`. Keep `libs/proto-gen` TS output for the client. Fix compose labels, healthchecks (`wget` against `/health`), build contexts, `AUTH_DEV_BYPASS`, SMTP, and `db-migrate` dependencies. Update Makefile, `.gitignore` (`apps/*/server`), docs, and the Helm gateway deployment env names and probes.

## Testing

httptest handler tests against small store interfaces with in-memory fakes; pure-logic tests for the auth middleware (test JWKS), websocket hub (viewport filter, batching, subscriptions), alert evaluator, entity batch buffer, track and link handlers. `scripts/smoke.sh` curls every gateway route after compose is up. `go vet` added to each project's test target.

## Out of scope

- `GET /api/v1/links/communities` keeps returning `[]`; the AGE implementation is not restored.
- Swagger is not restored.
- The `feature/sen-80-track-service` branch (federation, tracks proxy) must be re-ported after this lands.
