# SENTINEL

Geospatial intelligence platform for real-time entity tracking, link analysis, and alerting. Built as an Nx monorepo with NestJS microservices, an Angular 19 frontend with CesiumJS 3D mapping, and a polyglot backend (TypeScript, Go, Python).

## Quickstart

### Prerequisites

- Node.js 20+
- Docker & Docker Compose
- npm

### 1. Install dependencies

```bash
npm install
```

### 2. Start infrastructure

```bash
make docker-infra
```

This starts PostgreSQL (PostGIS), Redis, Kafka, OpenSearch, Keycloak, GeoServer, and MinIO.

### 3. Seed the database

```bash
make db-seed
```

Populates PostgreSQL with 13 sample entities (vessels, aircraft, vehicles, persons, facilities, signals, units) across the Middle East region.

### 4. Start the API gateway and frontend

```bash
make dev
```

Or individually:

```bash
make dev-api   # NestJS on :3000
make dev-web   # Angular on :4200
```

### 5. Verify

```bash
curl http://localhost:3000/api/v1/health          # Health check
curl http://localhost:3000/api/v1/entities         # List entities
open http://localhost:4200                         # Web UI
open http://localhost:3000/api/docs                # Swagger
```

## Architecture

```
Angular 19 UI (:4200)
        |
  API Gateway (NestJS :3000) ── Redis (geospatial cache)
        |                           |
     Kafka ─────────────────────────┤
        |                           |
  ┌─────┼─────────┬────────┬────────┤
  │     │         │        │        │
Entity Track   Search   Alert   Link Analysis
:3001  :3002   :3003    :3005    :3004
  │     │         │        │        │
  └─────┼─────────┴────────┘        │
        │                           │
   PostgreSQL/PostGIS ──────── OpenSearch
```

**9 microservices**: API Gateway (NestJS), Entity Service (NestJS), Track Service (NestJS), Search Service (NestJS), Link Analysis Service (NestJS), Alert Service (NestJS), Ingest Service (Go :4000), Analytics Service (Python :5000), Web UI (Angular :4200)

**Infrastructure**: PostgreSQL 16 + PostGIS + TimescaleDB, Redis 7, Kafka 3.7 (KRaft), OpenSearch 2.17, Keycloak 24, GeoServer 2.25, MinIO

## Project Structure

```
sentinel/
├── apps/
│   ├── web/                  # Angular 19 frontend
│   ├── api-gateway/          # NestJS API gateway (:3000)
│   ├── entity-service/       # Entity CRUD microservice (:3001)
│   ├── track-service/        # Track history microservice (:3002)
│   ├── search-service/       # OpenSearch integration (:3003)
│   ├── link-analysis-service/# Link/graph analysis (:3004)
│   ├── alert-service/        # Alerting engine (:3005)
│   ├── ingest-service/       # Go data ingest (:4000)
│   └── analytics-service/    # Python analytics (:5000)
├── libs/
│   ├── shared-models/        # TypeScript interfaces & enums
│   ├── common/               # Kafka topics, geo utils, constants
│   └── proto-gen/            # Protobuf generated code
├── proto/                    # Protobuf definitions
├── config/                   # Docker init scripts, Keycloak realm
├── scripts/                  # Seed data, utilities
├── k8s/                      # Kubernetes manifests
├── docs/                     # Detailed documentation
├── docker-compose.yml        # Full stack
├── docker-compose.infra.yml  # Infrastructure only
└── Makefile                  # Build, dev, test, deploy targets
```

## Documentation

| Document | Description |
|----------|-------------|
| [Architecture](docs/architecture.md) | System design, data flow, communication patterns |
| [Infrastructure](docs/infrastructure.md) | Docker services, ports, volumes, networking |
| [API Gateway](docs/api-gateway.md) | REST endpoints, WebSocket events, auth |
| [Frontend](docs/frontend.md) | Angular app structure, CesiumJS map, services |
| [Data Model](docs/data-model.md) | PostgreSQL schema, entity types, Kafka topics |
| [Development](docs/development.md) | Local setup, Makefile targets, debugging tips |

## Key Commands

| Command | Description |
|---------|-------------|
| `make dev` | Start infra + api-gateway + web |
| `make docker-infra` | Start infrastructure only |
| `make db-seed` | Seed database with sample data |
| `make build` | Build all applications |
| `make test` | Run all tests |
| `make lint` | Lint all code |
| `make docker-clean` | Stop all and remove volumes |

## Tech Stack

- **Frontend**: Angular 19, CesiumJS 1.120, vis-network
- **Backend**: NestJS 10, TypeORM, Passport JWT, Socket.io
- **Data**: PostgreSQL 16 + PostGIS, Redis 7, Kafka 3.7, OpenSearch 2.17
- **Auth**: Keycloak 24 (OIDC/JWT), RBAC + classification-based access
- **Ingest**: Go 1.22
- **Analytics**: Python 3.12, FastAPI
- **DevOps**: Docker Compose, Kubernetes, Nx monorepo
