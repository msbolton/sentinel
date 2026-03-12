# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

**Sentinel** is an enterprise-grade geospatial intelligence platform — an Nx monorepo with an Angular 19 frontend, multiple NestJS microservices, a Go ingest service, and a Python analytics service.

## Common Commands

### Development
```bash
make dev                    # Start infra + api-gateway + web (preferred for full dev)
npm run docker:infra        # Start infrastructure services only (Postgres, Redis, Kafka, etc.)
npm run start:web           # Angular frontend on :4200
npm run start:api           # NestJS API gateway on :3000
```

### Build & Test
```bash
npm run build               # Build all apps (Nx parallel)
npm run build:web           # Build Angular app only
npm test                    # Run all tests
npm run lint                # Lint all projects
nx test web                 # Run tests for a single project
nx test entity-service      # Run tests for a specific service
nx test web --testFile=path/to/spec.ts   # Run a single test file
```

### Protobuf
```bash
make proto                  # Regenerate protobuf (TS, Go, Python) from proto/ definitions
```

### Database
```bash
make db-seed                # Seed with 13 sample entities
make db-migrate             # Run migrations
```

### Docker
```bash
make docker-clean           # Stop all + remove volumes (destructive)
docker-compose up -d        # Full stack
docker-compose -f docker-compose.infra.yml up -d  # Infrastructure only
```

## Architecture

### Service Map
| Service | Port | Stack | Role |
|---|---|---|---|
| web | 4200 | Angular 19 + CesiumJS | Geospatial SPA |
| api-gateway | 3000 | NestJS | Single entry point; routes HTTP + WebSocket |
| entity-service | 3001 | NestJS + TypeORM | Entity CRUD |
| track-service | 3002 | NestJS + TypeORM | Position history |
| search-service | 3003 | NestJS + OpenSearch | Full-text search |
| link-analysis-service | 3004 | NestJS | Graph/relationship analysis |
| alert-service | 3005 | NestJS + Redis | Alerting engine |
| ingest-service | 4000 | Go | External data ingestion via Kafka |
| analytics-service | 5000 | Python + FastAPI | ML pattern analysis |

### Communication Pattern
- **HTTP/WebSocket**: Angular UI → API Gateway (proxy via `proxy.conf.json`)
- **Inter-service**: Apache Kafka (KRaft mode); topics defined centrally in `libs/common/src/kafka-topics.ts`
- **Real-time push**: Socket.io from API Gateway to frontend

### Shared Libraries (`libs/`)
- `@sentinel/shared-models` — TypeScript interfaces (Entity, Track, Alert, Link, Common)
- `@sentinel/common` — Kafka topics, geo utilities (Haversine, bearing, bounding box), constants
- `@sentinel/proto-gen` — Generated protobuf code (polyglot: TS/Go/Python)

Path aliases in `tsconfig.base.json` map `@sentinel/*` → `libs/*/src/index.ts`.

### Frontend Structure (`apps/web/src/app/`)
- Angular 19 standalone components, **zoneless change detection**
- Routes (lazy-loaded): `/map` (CesiumJS), `/search`, `/alerts`, `/link-graph`, `/timeline`, `/locations`
- Service-based state management (no NgRx)
- Auth interceptor in `app.config.ts`; hash location strategy

### Backend Pattern (NestJS services)
- Each service follows CQRS pattern (`@nestjs/cqrs`)
- TypeORM entities with PostgreSQL 16 + PostGIS + TimescaleDB
- Kafka consumer + producer in every service
- Swagger docs auto-generated at `/api/docs` (api-gateway)
- Auth via Keycloak 24 (OIDC/JWT) + Passport JWT

## Infrastructure (Docker Compose)
- **PostgreSQL 16 + PostGIS** — primary store with geospatial support
- **Redis 7** — caching + geospatial ops
- **Kafka 3.7 (KRaft)** — event streaming
- **OpenSearch 2.17** — full-text + analytics
- **Keycloak 24** — OIDC/IAM
- **GeoServer 2.25** — geospatial tiles
- **MinIO** — S3-compatible object storage

Environment variables: copy `.env.example` → `.env` and fill in values.

## Key Conventions
- Nx caching is enabled for `build` and `test` — run `nx reset` if you hit stale cache issues
- CesiumJS assets are explicitly copied during Angular build (configured in `apps/web/project.json`)
- Angular bundle budgets: 2 MB warning, 5 MB error
- Go service lives entirely in `apps/ingest-service/` with standard Go module layout (`cmd/`, `internal/`)
- Python analytics service uses FastAPI; entry is `sentinel_analytics/main.py`
- K8s manifests in `k8s/overlays/dev|staging|prod` use Kustomize
