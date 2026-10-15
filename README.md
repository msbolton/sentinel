# SENTINEL

Geospatial intelligence platform for real-time entity tracking, link analysis, and alerting. Built as an Nx monorepo with Go microservices behind a Go API gateway, an Angular 19 frontend with CesiumJS 3D mapping, and a Python analytics service.

## Quickstart

### Prerequisites

- Node.js 22+ (frontend tooling)
- Go 1.25+
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
make dev-api   # Go API gateway on :3000
make dev-client   # Angular on :4200
```

### 5. Verify

```bash
curl http://localhost:3000/api/v1/health          # Health check
curl http://localhost:3000/api/v1/entities         # List entities
open http://localhost:4200                         # Web UI
```

## Architecture

```
Angular 19 UI (:4200)
        |
  API Gateway (Go :3000) ── WebSocket /ws/entities
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

**9 services**: API Gateway (Go :3000), Entity Service (Go :3001), Track Service (Go :3002), Search Service (Go :3003), Link Analysis Service (Go :3004), Alert Service (Go :3005), Ingest Service (Go :4000), Analytics Service (Python :5000), Web UI (Angular :4200)

**Infrastructure**: PostgreSQL 16 + PostGIS + TimescaleDB, Redis 7, Kafka 3.7 (KRaft), OpenSearch 2.17, Keycloak 24, GeoServer 2.25, MinIO

## Project Structure

```
sentinel/
├── apps/
│   ├── client/               # Angular 19 frontend
│   ├── api-gateway/          # Go API gateway: auth, proxy, WebSocket (:3000)
│   ├── entity-service/       # Entity CRUD microservice (:3001)
│   ├── track-service/        # Track history microservice (:3002)
│   ├── search-service/       # OpenSearch integration (:3003)
│   ├── link-analysis-service/# Link/graph analysis (:3004)
│   ├── alert-service/        # Alerting engine (:3005)
│   ├── ingest-service/       # Go data ingest (:4000)
│   └── analytics-service/    # Python analytics (:5000)
├── libs/
│   ├── common-go/            # Shared Go: config, Kafka, health, auth headers, models
│   └── proto-gen/            # Protobuf generated code (TS for the client, Go, Python)
├── proto/                    # Protobuf definitions
├── db/                       # SQL migrations (golang-migrate) and seed data
├── config/                   # Docker init scripts, Kafka topics, Keycloak realm
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
| `make dev` | Start infra + api-gateway + client |
| `make docker-infra` | Start infrastructure only |
| `make db-migrate` | Apply SQL migrations |
| `make db-seed` | Seed database with sample data |
| `make build` | Build all applications |
| `make test` | Run all tests |
| `make lint` | Lint all code |
| `make docker-clean` | Stop all and remove volumes |

## Tech Stack

- **Frontend**: Angular 19, CesiumJS 1.120, vis-network
- **Backend**: Go 1.24 (net/http, pgx, confluent-kafka-go, golang-jwt), native WebSocket
- **Data**: PostgreSQL 16 + PostGIS, Redis 7, Kafka 3.7, OpenSearch 2.17
- **Auth**: Keycloak 24 (OIDC/JWT), RBAC + classification-based access
- **Analytics**: Python 3.12, FastAPI
- **DevOps**: Docker Compose, Kubernetes, Nx monorepo
