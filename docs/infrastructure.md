# Infrastructure

## Docker Compose Services

SENTINEL uses two Compose files:

- **`docker-compose.infra.yml`** — Infrastructure only (databases, messaging, auth)
- **`docker-compose.yml`** — Full stack including application services

### Service Map

| Service | Image | Host Port | Container Port | Purpose |
|---------|-------|-----------|----------------|---------|
| PostgreSQL | `postgis/postgis:16-3.4` | 5432 | 5432 | Primary database with PostGIS, TimescaleDB, Apache AGE |
| Redis | `redis:7-alpine` | 6379 | 6379 | Geospatial cache, session store |
| Kafka | `bitnami/kafka:3.7` | 9092, 9093 | 9092, 9093 | Event streaming (KRaft mode, no Zookeeper) |
| OpenSearch | `opensearchproject/opensearch:2.17.1` | 9200 | 9200 | Full-text + geospatial search |
| Keycloak | `quay.io/keycloak/keycloak:24.0` | 8080 | 8080 | Identity provider (OIDC/JWT) |
| Keycloak DB | `postgres:16-alpine` | — | 5432 | Dedicated Keycloak database |
| GeoServer | `kartoza/geoserver:2.25.2` | 8600 | 8600 | OGC WMS/WFS geospatial services |
| MinIO | `minio/minio:latest` | 9000, 9001 | 9000, 9001 | S3-compatible object storage |

### Application Services (full compose only)

| Service | Port | Stack |
|---------|------|-------|
| API Gateway | 3000 | NestJS |
| Entity Service | 3001 | NestJS |
| Track Service | 3002 | NestJS |
| Search Service | 3003 | NestJS |
| Link Analysis | 3004 | NestJS |
| Alert Service | 3005 | NestJS |
| Ingest Service | 4000 | Go |
| Analytics Service | 5000 | Python |
| Web UI | 4200 | Angular |

## Volumes

| Volume | Mounted To | Purpose |
|--------|-----------|---------|
| `sentinel-postgres-data` | `/var/lib/postgresql/data` | PostgreSQL persistence |
| `sentinel-keycloak-db-data` | `/var/lib/postgresql/data` | Keycloak DB persistence |
| `sentinel-redis-data` | `/data` | Redis AOF + RDB persistence |
| `sentinel-kafka-data` | `/bitnami/kafka` | Kafka log segments |
| `sentinel-opensearch-data` | `/usr/share/opensearch/data` | OpenSearch indices |
| `sentinel-geoserver-data` | `/opt/geoserver/data_dir` | GeoServer configuration |
| `sentinel-minio-data` | `/data` | S3 object storage |

## Network

All services communicate over `sentinel-network` (bridge mode, subnet `172.28.0.0/16`).

Internal service discovery uses Docker Compose service names as hostnames:
- `postgres:5432` (not `localhost`)
- `redis:6379`
- `kafka:9093` (internal listener)
- `opensearch:9200`
- `keycloak:8080`

## PostgreSQL Configuration

- **Image**: `postgis/postgis:16-3.4` (PostgreSQL 16 with PostGIS 3.4)
- **Extensions**: PostGIS (spatial), TimescaleDB (time-series, optional), Apache AGE (graph, optional)
- **Initialization**: `config/postgres/init-extensions.sql` runs on first start to enable extensions
- **Credentials**: `sentinel` / `sentinel_dev` (database: `sentinel`)
- **Schema**: `sentinel` (created by seed script)

## Redis Configuration

- **Persistence**: AOF (appendonly, everysec fsync) + RDB snapshots (900/1, 300/10, 60/10000)
- **Eviction**: `allkeys-lru` when memory limit reached
- **Key patterns**:
  - `sentinel:entities:geo` — Geospatial sorted set
  - `sentinel:entities:cache:{id}` — Entity JSON (TTL: 300s)
  - `sentinel:positions`, `sentinel:viewports`, `sentinel:sessions`

## Kafka Configuration

- **Mode**: KRaft (no Zookeeper dependency)
- **Listeners**:
  - `EXTERNAL` on port 9092 — for host-side clients
  - `INTERNAL` on port 9093 — for container-to-container
  - `CONTROLLER` on port 9094 — for KRaft controller
- **Auto-topic creation**: Disabled (topics created by `config/kafka/create-topics.sh`)
- **Default partitions**: 6 (position topics: 12)
- **Log retention**: 7 days (604,800,000 ms)
- **Max message size**: 10 MB

### Kafka Topics

| Topic | Partitions | Retention | Purpose |
|-------|-----------|-----------|---------|
| `events.entity.position` | 12 | 7 days | Real-time position updates |
| `events.entity.created` | 6 | 30 days | Entity creation events |
| `events.entity.updated` | 6 | 30 days | Entity update events |
| `events.track.point` | 12 | 7 days | Track point events |
| `alerts.geofence` | 3 | 30 days | Geofence breach alerts |
| `alerts.anomaly` | 3 | 30 days | Anomaly detection alerts |
| `analytics.pattern` | 3 | 30 days | Pattern analysis results |
| `ingest.raw` | 12 | 7 days | Raw ingested data |

## Keycloak Configuration

- **Dev mode**: Starts with `start-dev` (HTTP, no TLS)
- **Realm**: `sentinel` (imported from `config/keycloak/sentinel-realm.json`)
- **Clients**:
  - `sentinel-web` — Angular frontend (public, PKCE)
  - `sentinel-api` — API gateway (confidential, bearer-only)
- **Roles**: `analyst`, `operator`, `admin`
- **Custom claim**: `classification_level` (added to JWT via protocol mapper)

## Environment Variables

See `.env.example` for the full list. Key variables:

```bash
# Database
DATABASE_HOST=localhost
DATABASE_PORT=5432
DATABASE_NAME=sentinel
DATABASE_USER=sentinel
DATABASE_PASSWORD=sentinel_dev

# Redis
REDIS_HOST=localhost
REDIS_PORT=6379

# Kafka
KAFKA_BROKERS=localhost:9092

# Keycloak
KEYCLOAK_URL=http://localhost:8080
KEYCLOAK_REALM=sentinel

# Service ports
API_GATEWAY_PORT=3000
WEB_PORT=4200
```

When running inside Docker Compose, services use internal hostnames and the `KAFKA_BROKER=kafka:9093` internal listener.
