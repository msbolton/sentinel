# Development Guide

## Prerequisites

- **Node.js** 20+ (for NestJS and Angular)
- **npm** (comes with Node.js)
- **Docker** & **Docker Compose** (for infrastructure services)
- **Go 1.22+** (for ingest-service, optional)
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

# 4. Wait ~15 seconds for services to be healthy, then seed the database
make db-seed

# 5. Start the API gateway and web UI
make dev
```

The API Gateway will warm the Redis cache from PostgreSQL on startup, so seeded entities are immediately queryable.

## Makefile Targets

### Development

| Target | Command | Description |
|--------|---------|-------------|
| `make dev` | Starts infra + web + api-gateway | Full development environment |
| `make dev-web` | `npx nx serve web` | Angular dev server on `:4200` |
| `make dev-api` | `npx nx serve api-gateway` | NestJS dev server on `:3000` |
| `make dev-ingest` | `go run ./cmd/server` | Go ingest service on `:4000` |
| `make dev-analytics` | `uvicorn ... --reload` | Python analytics on `:5000` |

### Build

| Target | Command | Description |
|--------|---------|-------------|
| `make build` | `npx nx run-many --target=build` | Build all apps |
| `make build-web` | `npx nx build web --production` | Angular production build |
| `make build-api` | `npx nx build api-gateway` | NestJS production build |

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
| `make test-web` | Angular unit tests |
| `make test-api` | NestJS unit tests |
| `make lint` | Lint all code |
| `make lint-fix` | Auto-fix linting issues |

### Database

| Target | Description |
|--------|-------------|
| `make db-migrate` | Run TypeORM migrations |
| `make db-seed` | Seed database with 13 sample entities |

### Protobuf

| Target | Description |
|--------|-------------|
| `make proto` | Generate all protobuf types (TS, Go, Python) |
| `make proto-ts` | Generate TypeScript types only |
| `make proto-lint` | Lint protobuf definitions |

## npm Scripts

```bash
npm start           # Start web + api-gateway
npm run start:web   # Angular dev server
npm run start:api   # NestJS dev server
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

The API Gateway uses a dev-mode auth bypass when `NODE_ENV` is not `production`. No Keycloak configuration is needed. The bypass injects a synthetic user:

- **Username**: `dev-operator`
- **Roles**: `analyst`, `operator`, `admin`
- **Classification**: `TOP_SECRET`

The Angular frontend similarly falls back to an unauthenticated dev profile when Keycloak is unavailable.

## Debugging Tips

### API Gateway won't start

1. Check infrastructure is running: `docker ps`
2. Verify PostgreSQL is accessible: `psql -h localhost -U sentinel -d sentinel`
3. Check Redis: `redis-cli ping`
4. Check Kafka: `docker logs sentinel-kafka-1`

### Entities endpoint returns empty `[]`

The entities endpoint reads from Redis first, then falls back to PostgreSQL. If both are empty:

1. Run `make db-seed` to populate PostgreSQL
2. Restart the API Gateway — it will warm the Redis cache on startup
3. Or just hit the endpoint — the DB fallback will re-warm the cache automatically

### WebSocket not connecting

1. Verify the API Gateway is running on `:3000`
2. Check the Angular proxy config (`proxy.conf.json`) forwards `/socket.io` to `:3000`
3. Check browser dev tools Network tab for WebSocket connection attempts

### Kafka consumer lag

If events aren't flowing through:

1. Check Kafka topics exist: `docker exec -it sentinel-kafka-1 kafka-topics.sh --bootstrap-server localhost:9092 --list`
2. Check consumer groups: `docker exec -it sentinel-kafka-1 kafka-consumer-groups.sh --bootstrap-server localhost:9092 --list`
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
