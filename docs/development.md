# Development Guide

## Prerequisites

- **Node.js** 22+ (Angular tooling)
- **npm** (comes with Node.js)
- **Docker** & **Docker Compose** (for infrastructure services)
- **Go 1.25+** (API gateway and all backend services)
- **Python 3.12+** (for analytics-service, optional)

## First-Time Setup

```bash
# 1. Clone and install
cd sentinel
npm install

# 2. Copy environment file
cp .env.example .env

# 3. Start infrastructure (PostgreSQL, Redis, Kafka, etc.)
make docker-infra

# 4. Wait ~15 seconds for services to be healthy, then create the schema and seed it
make db-migrate
make db-seed

# 5. Start the API gateway and web UI
make dev
```

The entity-service warms its Redis geo cache from PostgreSQL on startup, so seeded entities are immediately queryable through the gateway.

## Makefile Targets

### Development

| Target | Command | Description |
|--------|---------|-------------|
| `make dev` | Starts infra + client + every Go service | Full development environment |
| `make dev-client` | `npx nx serve client` | Angular dev server on `:4200` |
| `make dev-api` | `npx nx serve api-gateway` | Go API gateway on `:3000` (auth bypass on) |
| `make dev-ingest` | `go run ./cmd/server` | Go ingest service on `:4000` |
| `make dev-analytics` | `uvicorn ... --reload` | Python analytics on `:5000` |

### Build

| Target | Command | Description |
|--------|---------|-------------|
| `make build` | `npx nx run-many --target=build` | Build all apps |
| `make build-client` | `npx nx build client --production` | Angular production build |
| `make build-api` | `npx nx build api-gateway` | Go API gateway binary |

### Docker

| Target | Description |
|--------|-------------|
| `make docker-infra` | Start infrastructure only (recommended for local dev) |
| `make docker-up` | Start full stack in containers |
| `make docker-down` | Stop all containers |
| `make docker-clean` | Stop and **remove all volumes** (destructive) |
| `make docker-logs` | Tail all container logs |
| `make docker-build` | Build all Docker images |

### Testing & Linting

| Target | Description |
|--------|-------------|
| `make test` | Run all tests across all apps |
| `make test-client` | Angular unit tests |
| `make test-api` | API gateway Go tests |
| `make test-go` | All Go service tests (vet + race) |
| `scripts/smoke.sh` | Exercise every gateway route, the nginx proxy, and the WebSocket against a running stack |
| `make lint` | Lint all code |
| `make lint-fix` | Auto-fix linting issues |

### Database

| Target | Description |
|--------|-------------|
| `make db-migrate` | Apply SQL migrations from `db/migrations` (golang-migrate) |
| `make db-migrate-down` | Roll back the most recent migration |
| `make db-migrate-version` | Show the applied migration version |
| `make db-seed` | Load `db/seed/seed.sql`: 13 entities, a geofence rule, 10 locations |

### Protobuf

| Target | Description |
|--------|-------------|
| `make proto` | Generate all protobuf types (TS, Go, Python) |
| `make proto-ts` | Generate TypeScript types only |
| `make proto-lint` | Lint protobuf definitions |

## npm Scripts

```bash
npm start           # Start client + api-gateway
npm run start:client   # Angular dev server
npm run start:api   # Go API gateway (nx serve)
npm run build       # Build all
npm test            # Test all
npm run lint        # Lint all
npm run docker:infra # Start infrastructure
npm run docker:all   # Start everything
npm run docker:down  # Stop everything
```

## Ports Reference

| Service | Port | Protocol |
|---------|------|----------|
| Web UI | 4200 | HTTP |
| API Gateway | 3000 | HTTP + WebSocket |
| Entity Service | 3001 | HTTP |
| Track Service | 3002 | HTTP |
| Search Service | 3003 | HTTP |
| Link Analysis | 3004 | HTTP |
| Alert Service | 3005 | HTTP |
| Ingest Service | 4000 | HTTP |
| Analytics Service | 5000 | HTTP |
| PostgreSQL | 5432 | TCP |
| Redis | 6379 | TCP |
| Kafka (external) | 9092 | TCP |
| Kafka (internal) | 9093 | TCP |
| OpenSearch | 9200 | HTTP |
| Keycloak | 8080 | HTTP |
| GeoServer | 8600 | HTTP |
| MinIO API | 9000 | HTTP |
| MinIO Console | 9001 | HTTP |

## Auth in Development

The API Gateway skips token verification when `AUTH_DEV_BYPASS=true` (set by `nx serve api-gateway` and by `docker-compose.yml`; refused when `APP_ENV=production`). No Keycloak configuration is needed. The bypass injects a synthetic user:

- **Username**: `dev-operator`
- **Roles**: `sentinel-analyst`, `sentinel-operator`, `sentinel-admin`
- **Classification**: `TOP_SECRET`

The Angular frontend similarly falls back to an unauthenticated dev profile when Keycloak is unavailable.

## Live Aircraft (OpenSky)

OpenSky is polled only for the area connected map clients can see, so nothing is fetched until a browser has the map open. To watch it work:

1. Put your OpenSky API client credentials in `.env` (`OPENSKY_CLIENT_ID`, `OPENSKY_CLIENT_SECRET`). `nx serve` loads `.env`; a bare `go run` does not.
2. Start the infrastructure and the services (ingest-service, entity-service, api-gateway, client) and enable the feed once: `curl -X POST localhost:4000/feeds/opensky/enable` (or through the gateway as an operator: `POST /api/v1/feeds/opensky/enable`). The enabled state is persisted in the `feeds` table; `FEEDS_BOOTSTRAP_ENABLED=opensky` does the same on first start.
3. The ingest log shows `opensky idle: waiting for an active viewport`; `curl -s localhost:4000/feeds/opensky/area` reports `active: false`. With `INGEST_SERVICE_TOKEN` set, add `-H 'x-sentinel-service-token: <token>'`.
4. Open http://localhost:4200 and zoom to a region. The gateway logs `viewport sync push`, the ingest service logs `opensky viewport updated` and then `opensky poll complete` every 10 s, and the viewport endpoint shows the box, `effectiveIntervalSec: 10` and `creditsRemaining`.
5. Aircraft glide between fixes (dead reckoning on the client). Pan a little and nothing is pushed; pan far or zoom in by half and one push plus an immediate poll follows. Zoom out to the globe and the interval stretches to 60 s.
6. Close the tab: the gateway pushes `active: false` and the ingest service idles. Kill and restart the ingest service: polling resumes within the 30 s heartbeat.

### Several ingest replicas

Every enabled feed runs on exactly one replica: replicas heartbeat `sentinel.ingest_replicas`, a feed's preferred owner is chosen by rendezvous hashing over the live replicas (or `config.pinnedReplica`), and the owner holds a `sentinel.feed_leases` row it renews every `LEASE_TICK_SEC` (2 s) with a `LEASE_TTL_SEC` (15 s) expiry. `docker compose up -d --scale ingest-service=3` starts three; `GET /api/v1/feeds` shows each feed's `status.owner`, a replica that stops reporting renders its feeds as `unassigned` until another one takes them (within one TTL), and every observation carries the lease's fencing token in the `x-fence` header. Feeds whose transport binds a port (`tcpserver`, `udp`) must set `pinnedReplica`. `LEASE_ENABLED=false` runs every feed on one replica (local development with a single process).

See `docs/architecture.md` (OpenSky viewport-driven polling) for the contract, the credit budget, and the environment variables.

## Debugging Tips

### API Gateway won't start

1. Check infrastructure is running: `docker ps`
2. Verify PostgreSQL is accessible: `psql -h localhost -U sentinel -d sentinel`
3. Check Redis: `redis-cli ping`
4. Check Kafka: `docker logs sentinel-kafka`

### Entities endpoint returns empty `[]`

The gateway proxies `/api/v1/entities` to the entity-service, which reads its Redis geo cache first and falls back to PostgreSQL. If the list is empty:

1. Run `make db-migrate` then `make db-seed` to populate PostgreSQL
2. Restart the entity-service so it re-warms its Redis cache, or just hit the endpoint and let the DB fallback do it
3. Check `curl localhost:3001/api/v1/entities` directly to rule out the gateway

### WebSocket not connecting

1. Verify the API Gateway is running on `:3000`
2. Check the Angular proxy config (`proxy.conf.json`) forwards `/ws` to `:3000` with `ws: true`
3. Check browser dev tools Network tab for WebSocket connection attempts

### Kafka consumer lag

If events aren't flowing through:

1. Check Kafka topics exist: `docker exec sentinel-kafka /opt/kafka/bin/kafka-topics.sh --bootstrap-server localhost:9093 --list`
2. Check consumer groups: `docker exec sentinel-kafka /opt/kafka/bin/kafka-consumer-groups.sh --bootstrap-server localhost:9093 --list`
3. Verify `KAFKA_BROKER` env var matches the expected listener (use `localhost:9092` from host, `kafka:9093` from containers)

### Database connection issues

Default credentials (from `.env.example`):
- Host: `localhost`
- Port: `5432`
- Database: `sentinel`
- User: `sentinel`
- Password: `sentinel_dev`

The API Gateway uses `DB_HOST`, `DB_PORT`, `DB_USERNAME`, `DB_PASSWORD`, `DB_DATABASE` env vars (see `app.module.ts`).

### Resetting everything

```bash
make docker-clean   # Removes all containers AND volumes
make docker-infra   # Restart infrastructure
sleep 15            # Wait for services
make db-seed        # Re-seed database
make dev            # Start development
```
