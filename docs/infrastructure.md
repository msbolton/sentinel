# Infrastructure

## Docker Compose Services

SENTINEL uses two Compose files:

- **`docker-compose.infra.yml`** — Infrastructure only (databases, messaging, auth)
- **`docker-compose.yml`** — Full stack including application services

### Service Map

| Service | Image | Host Port | Container Port | Purpose |
|---------|-------|-----------|----------------|---------|
| PostgreSQL | `postgis/postgis:16-3.4` | 5432 | 5432 | Primary database with PostGIS, TimescaleDB, Apache AGE |
| Kafka | `apache/kafka:3.7.0` | 9092, 9093 | 9092, 9093 | Event streaming (KRaft mode, no Zookeeper) |
| OpenSearch | `opensearchproject/opensearch:2.17.1` | 9200 | 9200 | Full-text + geospatial search |
| Keycloak | `quay.io/keycloak/keycloak:24.0` | 8080 | 8080 | Identity provider (OIDC/JWT) |
| Keycloak DB | `postgres:16-alpine` | — | 5432 | Dedicated Keycloak database |
| GeoServer | `kartoza/geoserver:2.25.2` | 8600 | 8600 | OGC WMS/WFS layers over the PostGIS views `entities_live_{u,c,s,ts}` and `locations_geo`, provisioned by the one-shot `geoserver-init` job (`config/geoserver/provision.sh`); accounts `gis-u` … `gis-ts` per clearance level |
| MinIO | `minio/minio:latest` | 9000, 9001 | 9000, 9001 | S3-compatible object storage |

### Application Services (full compose only)

| Service | Port | Stack |
|---------|------|-------|
| API Gateway | 3000 | Go |
| Entity Service | 3001 | Go |
| Track Service | 3002 | Go |
| Search Service | 3003 | Go |
| Link Analysis | 3004 | Go |
| Alert Service | 3005 | Go |
| Ingest Service | 4000 (not published; reached through the gateway) | Go, `--scale ingest-service=N` |
| Analytics Service | 5000 | Python |
| Web UI | 4200 | Angular |

## Volumes

| Volume | Mounted To | Purpose |
|--------|-----------|---------|
| `sentinel-postgres-data` | `/var/lib/postgresql/data` | PostgreSQL persistence |
| `sentinel-keycloak-db-data` | `/var/lib/postgresql/data` | Keycloak DB persistence |
| `sentinel-kafka-data` | `/var/lib/kafka` | Kafka log segments |
| `sentinel-opensearch-data` | `/usr/share/opensearch/data` | OpenSearch indices |
| `sentinel-geoserver-data` | `/opt/geoserver/data_dir` | GeoServer configuration |
| `sentinel-minio-data` | `/data` | S3 object storage |

## Network

All services communicate over `sentinel-network` (bridge mode, subnet `172.28.0.0/16`).

Internal service discovery uses Docker Compose service names as hostnames:
- `postgres:5432` (not `localhost`)
- `kafka:9093` (internal listener)
- `opensearch:9200`
- `keycloak:8080`

## PostgreSQL Configuration

- **Image**: `postgis/postgis:16-3.4` (PostgreSQL 16 with PostGIS 3.4)
- **Extensions**: PostGIS (spatial), TimescaleDB (time-series, optional), Apache AGE (graph, optional)
- **Initialization**: `config/postgres/init-extensions.sql` runs on first start to enable extensions
- **Credentials**: `sentinel` / `sentinel_dev` (database: `sentinel`)
- **Schema**: `sentinel`, applied from `db/schema/*.sql` by the `db-schema` compose job (or `make db-schema`); see `docs/data-model.md`
- **GIS role**: `sentinel_gis` (password `sentinel_gis_dev` in development, `GIS_DB_PASSWORD` for the provisioning job) may read only the GeoServer views, never the entity tables

## Kafka Configuration

- **Mode**: KRaft (no Zookeeper dependency)
- **Listeners**:
  - `EXTERNAL` on port 9092 — for host-side clients
  - `INTERNAL` on port 9093 — for container-to-container
  - `CONTROLLER` on port 9094 — for KRaft controller
- **Auto-topic creation**: Disabled (topics created by `config/kafka/create-topics.sh`)
- **Max message size**: 10 MB

### Kafka Topics

| Topic | Partitions | Retention | Purpose |
|-------|-----------|-----------|---------|
| `observations.raw` | 24 | 3 days | Every report from every feed (protobuf), keyed by identity |
| `observations.dlq` | 3 | 30 days | Payloads nothing could decode, with reason headers |
| `entities.events` | 12 | 7 days | Typed entity events with full snapshots, keyed by entity id |
| `alerts.events` | 3 | 30 days | Alerts from every producer |

Messages are protobuf (`proto/sentinel/v1`); see the topic table in `docs/data-model.md` for keys, headers and consumers.

## Keycloak Configuration

- **Dev mode**: Starts with `start-dev` (HTTP, no TLS)
- **Realm**: `sentinel` (imported from `config/keycloak/sentinel-realm.json`)
- **Clients**:
  - `sentinel-web` — Angular frontend (public, PKCE)
  - `sentinel-api` — API gateway (confidential, bearer-only)
- **Roles**: `sentinel-viewer`, `sentinel-analyst`, `sentinel-operator`, `sentinel-admin`, plus `classification-u|c|s|ts`
- **Custom claim**: `classification_level` (added to JWT via protocol mapper)

## Environment Variables

See `.env.example` for the full list. Key variables:

```bash
# Database (DB_* for the services, DATABASE_URL for the ingest registry)
DB_HOST=localhost
DB_PORT=5432
DB_DATABASE=sentinel
DB_USERNAME=sentinel
DB_PASSWORD=sentinel_dev
DATABASE_URL=postgres://sentinel:sentinel_dev@localhost:5432/sentinel?sslmode=disable

# Kafka
KAFKA_BROKERS=localhost:9092

# Keycloak
KEYCLOAK_URL=http://localhost:8080
KEYCLOAK_REALM=sentinel

# Ingest: gateway → ingest service token, feeds to enable on first start, OpenSky credentials
INGEST_SERVICE_TOKEN=dev-ingest-token
FEEDS_BOOTSTRAP_ENABLED=
OPENSKY_CLIENT_ID=
OPENSKY_CLIENT_SECRET=

# Service ports
API_GATEWAY_PORT=3000
WEB_PORT=4200
```

GeoServer provisioning takes `GIS_DB_PASSWORD` and `GIS_USER_PASSWORD_U/_C/_S/_TS` (dev defaults in `.env.example`); `ANALYTICS_SERVICE_PORT` moves the analytics host port off 5000 (macOS AirPlay). Certificates and other files a connection's `secretRefs` name as `file:/etc/sentinel/tak/...` live in the gitignored `./secrets/tak` folder, mounted read-only into the ingest container.

Ingest replica settings (`LEASE_TTL_SEC`, `LEASE_TICK_SEC`, `LEASE_REBALANCE_SEC`, `FEED_STALE_*`, `FEED_RETRY_*`, `SHUTDOWN_TIMEOUT_SEC`, buffers) are listed in `docs/feeds.md`. When running inside Docker Compose, services use internal hostnames and the `KAFKA_BROKERS=kafka:9093` internal listener.
