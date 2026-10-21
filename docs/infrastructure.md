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
| Keycloak | `quay.io/keycloak/keycloak:26.8.1` | 8080 | 8080 | Identity provider (OIDC/JWT) |
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
| Ingest Listener | 4010 (loopback only), plus every port a TCP, UDP or gRPC connection listens on, on the host's own network | Go, profile `inbound`, one replica (below) |
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

## Ingest Listener (TCP, UDP and gRPC)

External systems that send to Sentinel (the `tcp` driver in listen mode, the `udp` driver and the `grpc` driver, see `docs/feeds.md`) connect to a port chosen in the Connections page, anywhere in 1024-65535. A container with published ports cannot offer that, so those connections run on a separate *listener replica* that shares the host's network. Ordinary ingest replicas never bind them and the listener replica by default runs nothing else (feeds pinned to it excepted), and with no listener replica running an enabled listening connection shows `unassigned` with the error `no listener replica`. A dedicated listener also never takes over the ordinary feeds: with only the listener running, every other feed that is not pinned stays `unassigned` (`no ingest replica for this feed: the only live replicas are listeners, ...`) instead of moving onto it, so losing the ordinary replicas cannot overload the replica that holds the senders' ports. `INGEST_LISTENER_SHARED=true` lets the listener share the ordinary feeds and fail over for them; the gauge `sentinel_ingest_feeds_unplaced` counts feeds with nowhere to run (`docs/feeds.md`, Listeners).

### Docker Compose

- The service is `ingest-listener` in the `inbound` profile, so `docker compose up -d` leaves it out: `docker compose --profile inbound up -d ingest-listener`. It runs with `network_mode: host`, which Compose does not combine with `networks:` or published ports, so it reaches Kafka and Postgres through the host ports they publish (`localhost:9092`, `localhost:5432`), serves its health and management API on `127.0.0.1:4010` only (the healthcheck runs on the host and the gateway never calls it), and cannot be scaled.
- **Docker Desktop (Mac, Windows)** needs host networking turned on: Settings › Resources › Network › Enable host networking (Docker Desktop 4.34 or later). **Linux** needs nothing.
- `INGEST_LISTENER_HOST` (default `localhost`) is the address senders use, shown in each connection's send guide; set it to the machine's name or IP when senders are on other machines. `INGEST_RESERVED_PORTS` (blank means the default list) names the ports a connection may not take; `INGEST_SERVICE_TOKEN` guards the management API. `INGEST_LISTENER_SHARED` (default `false`) also lets the listener run a share of the ordinary feeds and take them over when `ingest-service` is down; `ingest-listener` has the same `OPENSKY_*` and `AISSTREAM_API_KEY` settings as `ingest-service` for that. A feed pinned to `ingest-listener` runs there either way. Do not put `INGEST_LISTENER_SHARED` in `.env`: `nx serve` loads that file into the host-run ingest service, which refuses the setting without `INGEST_LISTENER=true` and does not start; pass it on the command line instead.
- **Secrets.** Both ingest services run as user and group 10001 and mount `./secrets/tak` at `/etc/sentinel/tak` and `./secrets/ingest` at `/etc/sentinel/secrets`, read-only. On a Linux host the folders must be traversable and the files readable by gid 10001 (`sudo chgrp -R 10001 secrets/ingest secrets/tak` and `sudo chmod -R u=rwX,g=rX,o= secrets/ingest secrets/tak`; `docs/development.md` has the details and a certificate recipe). Variables that secret references name (`env:SENTINEL_SECRET_...`) come from `./secrets/ingest.env`, an optional `env_file` of both services (Compose 2.24 or later). What a reference may name is decided by `INGEST_SECRET_ENV_ALLOW` and `INGEST_SECRET_FILE_DIRS`; the defaults (`OPENSKY_*,AISSTREAM_*,SENTINEL_SECRET_*` and `/etc/sentinel`) fit these mounts, and `INGEST_SECRET_ENV_ALLOW=*` restores the earlier any-variable behaviour (`docs/feeds.md`).
- Every port a connection listens on is open on every interface of the machine. Use the host firewall and each connection's allowlist. The listener's management API is not among them: it binds loopback only, and is guarded by `INGEST_SERVICE_TOKEN` as well (default `dev-ingest-token`, change it outside development).

### Kubernetes (Helm)

The chart is self-contained: it has no subcharts, so there is no `helm dependency build`, and the Bitnami and MinIO values are gone (the data tier is added as chart templates in later changes). The `global` values apply to every workload:

| Value | Default | Purpose |
|-------|---------|---------|
| `global.host` | empty | The public hostname, without a scheme; `https://<host>` is what the chart derives CORS origins, token issuers and the Ingress from |
| `global.imageRegistry` | empty | Private registry that holds the Sentinel images: `<imageRegistry>/sentinel/<name>:<tag>`, the layout `make images-push REGISTRY=...` pushes. PostgreSQL (the schema Job), GeoServer and curl are third-party images and keep their own registries |
| `global.imageTag` | empty | Tag of every Sentinel image whose own `image.tag` is empty (all of them by default); empty falls back to the chart's `appVersion`, so pass the tag you pushed: `--set global.imageTag=$(git rev-parse --short HEAD)` |
| `global.imagePullSecrets` | `[]` | `[{name: ...}]`, added to every pod |

Resources carry `helm.sh/chart`, `app.kubernetes.io/part-of`, `/version` and `/managed-by` beside the `app.kubernetes.io/name` and `/instance` labels the selectors match. The chart reaches the data tier by name: `<fullname>-postgresql` (host, and the Secret with the `username`, `password` and `database-url` keys), `<fullname>-kafka:9092` and `http://<fullname>-keycloak:8080/auth`, where `<fullname>` is the one described below.

The chart's `ingestListener` values:

| Value | Default | Purpose |
|-------|---------|---------|
| `ingestListener.enabled` | `true` (chart), `false` (`k8s/overlays/dev/values.yaml`) | Render the listener Deployment |
| `ingestListener.host` | empty | The address senders use; empty means the node's IP (`status.hostIP`). Set a DNS name or a stable address when nodes are replaced |
| `ingestListener.nodeSelector` | `sentinel.io/ingest-listener: "true"` | Which node runs it |
| `ingestListener.shared` | `false` | Sets `INGEST_LISTENER_SHARED`: the listener also takes its share of the ordinary feeds and runs them when no ordinary replica is live. Off, it runs listening connections and pinned feeds only |
| `ingestListener.resources` | 250m / 128Mi requested, 2000m / 512Mi limit | |

The Deployment `<fullname>-ingest-listener` (the chart's full name: `fullnameOverride` if set, else `<release>-sentinel`, or just the release name when it contains `sentinel`, so `sentinel-dev-ingest-listener` for the release `sentinel-dev`) has one replica, `hostNetwork: true` (with `dnsPolicy: ClusterFirstWithHostNet`) and health on `:4010`, bound on every interface of the node so the kubelet probes reach it (unlike Compose, which binds it to loopback). It reads the same Secret as the ingest service for `INGEST_SERVICE_TOKEN` and takes `ingestService.env`, `ingestService.lease`, the TAK certificate mount, `ingestService.secretFiles` and `ingestService.secretEnv` from the ingest values.

These `ingestService` values reach the ingest service and the listener alike:

| Value | Default | Purpose |
|-------|---------|---------|
| `ingestService.podSecurityContext` | `runAsNonRoot`, user and group `10001`, `fsGroup: 10001`, `seccompProfile: RuntimeDefault` | The ingest and listener pods run as the image's non-root user `10001`. `fsGroup` is what makes the 0440 files of `secretFiles` and the TAK mount readable, so a platform that assigns the ids itself (OpenShift, say) needs `runAsUser`, `runAsGroup` and `fsGroup` set to `null` (`--set ingestService.podSecurityContext.runAsUser=null`), and then those files may be readable by root only |
| `ingestService.containerSecurityContext` | `allowPrivilegeEscalation: false`, all capabilities dropped, `readOnlyRootFilesystem: false` | The service binds only ports from 1024 up and writes no files; `readOnlyRootFilesystem` stays off until a full run on a read-only root has been seen working |
| `ingestService.secretFiles.existingSecret` | empty | A Secret whose keys are mounted as files (mode 0440) for `file:` secret refs to name; empty mounts nothing. The Secret must exist before the pods start, and each key must be a valid file name |
| `ingestService.secretFiles.mountPath` | `/etc/sentinel/secrets` | Where the files appear |
| `ingestService.secretEnv.existingSecret` | empty | A Secret whose keys become environment variables of the pods (`envFrom`) for `env:` secret refs to name; empty adds none. Name the keys `SENTINEL_SECRET_<NAME>` (or another name `INGEST_SECRET_ENV_ALLOW` allows). The Secret must exist before the pods start (otherwise `CreateContainerConfigError`), every key reaches both pods and can set any configuration the chart leaves unset (`LEASE_ENABLED`, say), a same-named entry in `ingestService.env` wins over the Secret's key, and the variables change only when the pods restart |
| `ingestService.env` | `{}` | Plain environment values for the ingest and listener pods, such as `INGEST_RESERVED_PORTS`, `INGEST_SECRET_ENV_ALLOW`, `INGEST_SECRET_FILE_DIRS` (needed only to move a mount outside `/etc/sentinel` or to name other variables) and `INGEST_TLS_RELOAD_SEC` |
| `ingestService.serviceMonitor.enabled` | `false` | Renders a ServiceMonitor for the ingest service and, while `ingestListener.enabled` is true, a PodMonitor for the listener, which has no Service to select (needs the Prometheus Operator CRDs). `ingestService.serviceMonitor.interval` (`30s`) is the scrape interval |

- **Label a node first**, or the Pod stays Pending: `kubectl label node <node> sentinel.io/ingest-listener=true`. Choose a node whose address senders can reach and that you will keep; senders use the node address (or `ingestListener.host`), there is no Service or load balancer in front of it.
- **Secret files for mutual TLS.** A TCP connection with `tls.requireClientCert` reads its server certificate, key and client CA from `file:` secret refs. Create a Secret in the release's namespace whose keys are the file names (the chart never creates this one, so no key material passes through a values file), and install or upgrade with `--set ingestService.secretFiles.existingSecret=sentinel-ingest-files`:

  ```bash
  kubectl create secret generic sentinel-ingest-files -n sentinel-dev \
    --from-file=server.pem --from-file=server.key --from-file=clients-ca.pem
  ```

  The connection's `secretRefs` are then `"tlsCert":"file:/etc/sentinel/secrets/server.pem"`, `"tlsKey":"file:/etc/sentinel/secrets/server.key"` and `"clientCa":"file:/etc/sentinel/secrets/clients-ca.pem"`. Kubernetes refreshes the mounted files within about a minute of a change to the Secret, and a TLS connection loads a renewed certificate, key or CA from them within `INGEST_TLS_RELOAD_SEC` (60 s) more, without dropping connections: renew the Secret and no restart is needed. The exceptions are a TAK connection and an MQTT broker reached over a websocket or through `all_proxy`, which keep the TLS configuration they started with: restart those feeds after a renewal (`docs/feeds.md`). Moving `secretFiles.mountPath` or the TAK `mountPath` outside `/etc/sentinel` needs `INGEST_SECRET_FILE_DIRS` to include the new folder.
- **Rollouts drop inbound traffic.** The Deployment uses `strategy: Recreate`: a hostNetwork pod's ports count as taken on its node, so a surge pod could never schedule beside the old one. Each rollout therefore briefly stops inbound TCP and UDP; TCP senders reconnect, and datagrams sent in the gap are lost. Roll it out in a quiet window. An upgrade that changes the pod template, such as the one that introduced the non-root ids, restarts the ingest-service pods (rolling) and recreates the listener. The listener's `hostNetwork` also keeps it out of namespaces that enforce the Pod Security `baseline` or `restricted` level; the ingest-service Deployment and the schema Job are written to meet `restricted`.
- **Its replica id is the Deployment's name.** `REPLICA_ID` is `<fullname>-ingest-listener`, not the pod name, so it is the same after a rollout and a feed pinned to the listener (`config.pinnedReplica`, `docs/feeds.md`) keeps matching it. That is safe because the Deployment has one replica and, during a rollout, `Recreate` never runs two pods at once. The ingest service's pods use their pod names as ids, and those change with every rollout.
- **The schema is applied by a Job.** A new ingest pod checks the schema at start: against an older one it exits with the fatal log `database schema is out of date` (naming the missing columns and `db/schema/70_feeds.sql`) and crash-loops, and a failing check of another kind logs `checking the database schema failed`. The chart's `dbSchema` hook Job applies every file in `db/schema` (see Database schema below): before the new pods on an upgrade, but after them on a first install, where the ingest pods crash-loop until it has finished; the ingest-service Deployment's default RollingUpdate leaves the old pods serving while a new one fails, but the listener's `Recreate` stops the old pod first, so its listening connections stay down until the schema is in place. Upgrade the ingest-service and the listener in the same release, and `ingest-service` first when you roll them yourself: while old and new replicas run side by side they can place feeds differently, a new dedicated listener beside an old ingest service leaves ordinary feeds flapping (about 30 s on, 15 s off), a few feeds restart, and status rows written by an old replica do not yet hide decoder error texts from lower-cleared callers. With `dbSchema.enabled=false`, apply the files yourself first (`psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -f <file>`, in name order; every file is idempotent) and then run `helm upgrade`.
- **The dev overlay leaves it off.** The listener only schedules on a labelled node, and a dev cluster seldom has one, so `k8s/overlays/dev/values.yaml` sets `ingestListener.enabled: false`. To turn it on, label a node and install with `--set ingestListener.enabled=true` (or a later `-f` file that sets it): `helm upgrade --install sentinel-dev ./k8s/charts/sentinel -f k8s/overlays/dev/values.yaml --set ingestListener.enabled=true --set ingestService.serviceToken=<token> -n sentinel-dev` (the chart refuses to render without `ingestService.serviceToken` or `ingestService.existingSecret`).
- **Firewalls and security groups.** Allow inbound TCP and UDP (gRPC is TCP) on the ports your connections use to the labelled node, from the senders' networks only: the host firewall, the cloud security group and any network ACL. A port chosen later in Connections is closed until you open it, so open a range you control and narrow it with each connection's `allow`. Never open `:4010` to outside networks; the one exception is Prometheus inside the cluster, which has to reach it for the listener's `/metrics` when `ingestService.serviceMonitor.enabled` renders the PodMonitor. A UDP listener's receive buffer is capped by `net.core.rmem_max` of the node (`docs/feeds.md`).
- **Node ports.** The default `INGEST_RESERVED_PORTS` covers Sentinel's own ports, not the node's (the kubelet's 10250, node exporters, ...). Add them with `ingestService.env.INGEST_RESERVED_PORTS`, which reaches the ingest and listener Deployments alike; the value replaces the default list, so repeat what you still want, and keep it the same everywhere because the replica that answers a request validates the port.
- **Render and lint the chart before a release.** The listener templates and the schema Job need a real `helm template` pass with a service token (the chart refuses to render without one). `make chart-lint` needs `helm` and `kubeconform` (no dependency build). It runs `helm lint`, checks that the schema Job carries `db/schema`, checks that every hardened pod spec (both ingest Deployments and the Job) renders `runAsNonRoot: true`, and validates the rendered chart with `kubeconform -strict -ignore-missing-schemas` for the defaults, the dev overlay and a render with GeoServer and the ServiceMonitor on:

  ```bash
  make chart-lint
  helm template t k8s/charts/sentinel --set ingestService.serviceToken=x
  helm template t k8s/charts/sentinel -f k8s/overlays/dev/values.yaml --set ingestService.serviceToken=x
  ```

  The first output contains the `t-sentinel-ingest-listener` Deployment (`hostNetwork: true`, `strategy: Recreate`, the node selector); the dev overlay's output does not.

### Database schema (Helm)

With `dbSchema.enabled` (the default) the chart applies `db/schema`, linked into the chart under `files/schema`, with a Helm hook Job: after a first install has created the release's resources (post-install) and before every upgrade (pre-upgrade). The hook ConfigMap that carries the SQL has weight -10 and the Job -5, so on a first install the `sentinel_gis` role and the GIS views exist before `geoserver-init` (post-install, weight 0) runs. The Job waits for the database, then runs the files in name order with `psql -v ON_ERROR_STOP=1`; every file is idempotent, and a failing Job fails the install or upgrade. It needs Helm: `kubectl apply` of a rendered chart ignores hooks.

| Value | Default | Purpose |
|-------|---------|---------|
| `dbSchema.enabled` | `true` | Render the hook Job and its ConfigMap |
| `dbSchema.image` | `postgres:16-alpine` | Needs `psql` and `pg_isready` |
| `dbSchema.backoffLimit`, `dbSchema.activeDeadlineSeconds` | `10`, `900` | Retries, and how long the Job may run in all |
| `dbSchema.resources` | 50m / 64Mi requested, 500m / 256Mi limit | Present so that a namespace ResourceQuota demanding requests and limits cannot reject the hook pod |
| `dbSchema.podSecurityContext`, `dbSchema.containerSecurityContext` | non-root `10001`, no privilege escalation, all capabilities dropped | Same hardening as the ingest pods, without `fsGroup` (its one volume is a ConfigMap); set a key to `null` to drop it |

- **The role.** The Job connects with the `database-url` key of the `<fullname>-postgresql` Secret, which no template creates yet (the chart-owned PostgreSQL that will create it arrives with the data tier, so until then you supply it) and which must connect as the role the services and the gateway use. That role must own schema `sentinel`, and the role `sentinel_gis` must exist so that `90_gis_views.sql` needs no `CREATEROLE`. Both are one-time database setup, done on an empty data directory (`CREATE SCHEMA sentinel AUTHORIZATION <role>`, and `CREATE ROLE sentinel_gis LOGIN` without a password). A database that already holds the schema needs, once and as a superuser: `ALTER SCHEMA sentinel OWNER TO <role>;`, `ALTER TABLE sentinel.<table> OWNER TO <role>;` for each table already in the schema (or `REASSIGN OWNED BY <old owner> TO <role>;`), and `CREATE ROLE sentinel_gis LOGIN;` only if it is missing.
- **The GIS password.** `sentinel_gis` has no password, so it cannot log in until `ALTER ROLE sentinel_gis PASSWORD '<geoserver.gisDbPassword>';` sets the one GeoServer's provisioning uses. Until then `geoserver-init` fails on a first install; run it again afterwards. The comment in `db/schema/90_gis_views.sql` that says the development password is set there describes Compose only.
- **Timeouts and waiting.** Helm waits for hooks only as long as `--timeout` (5 minutes by default), while the Job may run `activeDeadlineSeconds` (15 minutes); pass `--timeout 15m` for a slow cold start. On a first install do not pass `--wait` or `--atomic`: Helm would wait for ingest pods that cannot start until the Job has run, so use `helm upgrade --install` without them. Upgrades are fine, since pre-upgrade hooks run first.
- **What stays.** Hook resources are not removed by `helm uninstall`. The ConfigMap (about 53 KB) stays until the next install or upgrade replaces it, and a failed Job stays for its logs (`kubectl logs job/<fullname>-db-schema`); delete the ConfigMap by hand after an uninstall.

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

- **Dev mode**: Starts with `start-dev` (HTTP, no TLS). The health endpoints live on the management port 9000, which compose does not publish
- **Version**: Keycloak 26. It starts with `KC_PROXY_HEADERS=xforwarded` and bootstraps the master-realm admin from `KC_BOOTSTRAP_ADMIN_USERNAME` / `KC_BOOTSTRAP_ADMIN_PASSWORD`; the `token-exchange` and `admin-fine-grained-authz` features are not enabled because nothing uses them
- **Upgrading a dev volume**: the first start of Keycloak 26 against a `sentinel-keycloak-db-data` volume written by Keycloak 24 migrates the database in place and cannot be undone. Copy the volume first if you may want Keycloak 24 back. The realm import is skipped when the realm exists, so an existing dev realm does not pick up changes to `sentinel-realm.json` (recreate the volume, or apply them in the admin console)
- **Realm**: `sentinel` (imported from `config/keycloak/sentinel-realm.json`)
- **Clients**:
  - `sentinel-web` — Angular frontend (public, PKCE)
  - `sentinel-api` — API gateway (confidential, bearer-only)
- **Roles**: `sentinel-viewer`, `sentinel-analyst`, `sentinel-operator`, `sentinel-admin`, plus `classification-u|c|s|ts`
- **Custom claim**: `classification_level` (added to JWT via protocol mapper)
- **Keycloak 26 needs two things the realm now declares**: `sentinel-web` has the `basic` client scope (since 25 it carries the `sub` claim, which the gateway requires), and the user profile sets `unmanagedAttributePolicy: ADMIN_EDIT` (otherwise Keycloak drops the `organization`, `justification` and `registrationDate` attributes the registration flow stores)

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

# Registration mail: mailhog in dev. A real relay sets MAIL_TLS (starttls|tls|none),
# MAIL_USERNAME and MAIL_PASSWORD; see docs/api-gateway.md
MAIL_HOST=localhost
MAIL_PORT=1025
MAIL_TLS=
MAIL_USERNAME=
MAIL_PASSWORD=

# Service ports
API_GATEWAY_PORT=3000
WEB_PORT=4200
```

GeoServer provisioning takes `GIS_DB_PASSWORD` and `GIS_USER_PASSWORD_U/_C/_S/_TS` (dev defaults in `.env.example`); `ANALYTICS_SERVICE_PORT` moves the analytics host port off 5000 (macOS AirPlay). Certificates and other files a connection's `secretRefs` name as `file:/etc/sentinel/...` live in gitignored folders under `./secrets`, mounted read-only into the ingest containers (`ingest-service` and `ingest-listener`, which run as 10001): `./secrets/tak` at `/etc/sentinel/tak` for TAK certificates, and `./secrets/ingest` at `/etc/sentinel/secrets` for anything else, such as the PEMs of a TCP connection with mutual TLS; `./secrets/ingest.env` supplies the `SENTINEL_SECRET_*` variables for `env:` references (`docs/development.md`, which also has the file permissions a Linux host needs). The ingest service reads `INGEST_SECRET_ENV_ALLOW`, `INGEST_SECRET_FILE_DIRS` and `INGEST_TLS_RELOAD_SEC` too (`docs/feeds.md`).

Ingest replica settings (`LEASE_TTL_SEC`, `LEASE_TICK_SEC`, `LEASE_REBALANCE_SEC`, `FEED_STALE_*`, `FEED_RETRY_*`, `SHUTDOWN_TIMEOUT_SEC`, buffers) are listed in `docs/feeds.md`. Compose gives both ingest services `stop_grace_period: 30s` (its default is 10 s) so that the 25 s `SHUTDOWN_TIMEOUT_SEC` can run before the container is killed; a Kafka that stalls can still outlast it. When running inside Docker Compose, services use internal hostnames and the `KAFKA_BROKERS=kafka:9093` internal listener.
