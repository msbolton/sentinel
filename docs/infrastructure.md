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
| Ingest Listener | 4010, plus every port a TCP/UDP connection listens on, on the host's own network | Go, profile `inbound`, one replica (below) |
| Analytics Service | 5000 | Go |
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

The one exception is the ingest listener (below), which uses the host's network instead.

## Ingest Listener (TCP and UDP)

External systems that send to Sentinel (the `tcp` driver in listen mode and the `udp` driver, see `docs/feeds.md`) connect to a port chosen in the Connections page, anywhere in 1024-65535. A container with published ports cannot offer that, so those connections run on a separate *listener replica* that shares the host's network. Ordinary ingest replicas never bind them, and with no listener replica running an enabled listening connection shows `unassigned` with the error `no listener replica`.

### Docker Compose

- The service is `ingest-listener` in the `inbound` profile, so `docker compose up -d` leaves it out: `docker compose --profile inbound up -d ingest-listener`. It runs with `network_mode: host`, which Compose does not combine with `networks:` or published ports, so it reaches Kafka and Postgres through the host ports they publish (`localhost:9092`, `localhost:5432`), serves its health and management API on `:4010`, and cannot be scaled.
- **Docker Desktop (Mac, Windows)** needs host networking turned on: Settings › Resources › Network › Enable host networking (Docker Desktop 4.34 or later). **Linux** needs nothing.
- `INGEST_LISTENER_HOST` (default `localhost`) is the address senders use, shown in each connection's send guide; set it to the machine's name or IP when senders are on other machines. `INGEST_RESERVED_PORTS` (blank means the default list) names the ports a connection may not take; `INGEST_SERVICE_TOKEN` guards the management API.
- Every port a connection listens on is open on every interface of the machine. Use the host firewall and each connection's allowlist, and keep `:4010` closed to the network: it is guarded only by `INGEST_SERVICE_TOKEN` (default `dev-ingest-token`, change it outside development) and the gateway does not need it.

### Kubernetes (Helm)

The chart's `ingestListener` values:

| Value | Default | Purpose |
|-------|---------|---------|
| `ingestListener.enabled` | `true` (chart), `false` (`k8s/overlays/dev/values.yaml`) | Render the listener Deployment |
| `ingestListener.host` | empty | The address senders use; empty means the node's IP (`status.hostIP`). Set a DNS name or a stable address when nodes are replaced |
| `ingestListener.nodeSelector` | `sentinel.io/ingest-listener: "true"` | Which node runs it |
| `ingestListener.resources` | 250m / 128Mi requested, 2000m / 512Mi limit | |

The Deployment `<release>-ingest-listener` has one replica, `hostNetwork: true` (with `dnsPolicy: ClusterFirstWithHostNet`) and health on `:4010`. It reads the same Secret as the ingest service for `INGEST_SERVICE_TOKEN` and takes `ingestService.env`, `ingestService.lease` and the TAK certificate mount from the ingest values.

- **Label a node first**, or the Pod stays Pending: `kubectl label node <node> sentinel.io/ingest-listener=true`. Choose a node whose address senders can reach and that you will keep; senders use the node address (or `ingestListener.host`), there is no Service or load balancer in front of it.
- **Rollouts drop inbound traffic.** The Deployment uses `strategy: Recreate`: a hostNetwork pod's ports count as taken on its node, so a surge pod could never schedule beside the old one. Each rollout therefore briefly stops inbound TCP and UDP; TCP senders reconnect, and datagrams sent in the gap are lost. Roll it out in a quiet window.
- **The dev overlay leaves it off.** The listener only schedules on a labelled node, and a dev cluster seldom has one, so `k8s/overlays/dev/values.yaml` sets `ingestListener.enabled: false`. To turn it on, label a node and install with `--set ingestListener.enabled=true` (or a later `-f` file that sets it): `helm upgrade --install sentinel-dev ./k8s/charts/sentinel -f k8s/overlays/dev/values.yaml --set ingestListener.enabled=true -n sentinel-dev`.
- **Firewalls and security groups.** Allow inbound TCP and UDP on the ports your connections use to the labelled node, from the senders' networks only: the host firewall, the cloud security group and any network ACL. A port chosen later in Connections is closed until you open it, so open a range you control and narrow it with each connection's `allow`. Never open `:4010`.
- **Node ports.** The default `INGEST_RESERVED_PORTS` covers Sentinel's own ports, not the node's (the kubelet's 10250, node exporters, ...). Add them with `ingestService.env.INGEST_RESERVED_PORTS`, which reaches the ingest and listener Deployments alike; the value replaces the default list, so repeat what you still want, and keep it the same everywhere because the replica that answers a request validates the port.
- **Render the chart before a release.** The listener templates need a real `helm template` pass, with the chart's dependencies built and a service token (the chart refuses to render without one):

  ```bash
  helm dependency build k8s/charts/sentinel
  helm template t k8s/charts/sentinel --set ingestService.serviceToken=x
  helm template t k8s/charts/sentinel -f k8s/overlays/dev/values.yaml --set ingestService.serviceToken=x
  ```

  The first output contains the `t-sentinel-ingest-listener` Deployment (`hostNetwork: true`, `strategy: Recreate`, the node selector); the dev overlay's output does not.
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

# Ingest listener (profile inbound): the address senders use, and the ports
# connections may not take (blank means the default list)
INGEST_LISTENER_HOST=localhost
INGEST_RESERVED_PORTS=

# Service ports
API_GATEWAY_PORT=3000
WEB_PORT=4200
```

GeoServer provisioning takes `GIS_DB_PASSWORD` and `GIS_USER_PASSWORD_U/_C/_S/_TS` (dev defaults in `.env.example`); `ANALYTICS_SERVICE_PORT` moves the analytics host port off 5000 (macOS AirPlay). Certificates and other files a connection's `secretRefs` name as `file:/etc/sentinel/tak/...` live in the gitignored `./secrets/tak` folder, mounted read-only into the ingest container.

Ingest replica settings (`LEASE_TTL_SEC`, `LEASE_TICK_SEC`, `LEASE_REBALANCE_SEC`, `FEED_STALE_*`, `FEED_RETRY_*`, `SHUTDOWN_TIMEOUT_SEC`, buffers) are listed in `docs/feeds.md`. When running inside Docker Compose, services use internal hostnames and the `KAFKA_BROKERS=kafka:9093` internal listener.
