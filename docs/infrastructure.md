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

The chart is self-contained: it has no subcharts, so there is no `helm dependency build`, and the Bitnami, MinIO and Redis values are gone: PostgreSQL, Kafka and OpenSearch are the chart's own StatefulSets (Data tier below), and nothing in the Kubernetes deployment uses object storage or Redis. `docs/deployment.md` is the step-by-step path to a production node (`make images-push`, then `make deploy`); this section describes what the chart does. The values overlays under `k8s/overlays` are plain Helm values files, layered with `-f`: `prod/values.yaml` sizes the chart for one k3s node and turns on the listener, its node ports and its NetworkPolicy CIDR, and a pre-upgrade backup (`make deploy` always applies it, then your site file); `local/values.yaml` goes over it for k3d (`docs/deployment.md`, Local cluster on k3d); `dev/values.yaml` is a small shared dev cluster. The `global` values apply to every workload:

| Value | Default | Purpose |
|-------|---------|---------|
| `global.host` | empty | The public hostname, without a scheme; `https://<host>` is what the chart derives CORS origins, token issuers and the Ingress from |
| `global.imageRegistry` | empty | Private registry that holds the Sentinel images: `<imageRegistry>/sentinel/<name>:<tag>`, the layout `make images-push REGISTRY=...` pushes. PostgreSQL (the schema Job), GeoServer and curl are third-party images and keep their own registries |
| `global.imageTag` | empty | Tag of every Sentinel image whose own `image.tag` is empty (all of them by default); empty falls back to the chart's `appVersion`, so pass the tag you pushed: `--set global.imageTag=$(git rev-parse --short HEAD)` |
| `global.imagePullSecrets` | `[]` | `[{name: ...}]`, added to every pod |
| `global.storageClass` | empty | StorageClass of every claim the chart makes (PostgreSQL, Kafka, OpenSearch, GeoServer, the backups); empty uses the cluster's default class (`local-path` on k3s). The prod overlay uses `local-path-retain` (`k8s/cluster/local-path-retain.yaml`), which keeps a volume's data when its claim is deleted |
| `global.clusterDomain` | `cluster.local` | DNS domain of the cluster, for the fully qualified name Kafka advertises |

Resources carry `helm.sh/chart`, `app.kubernetes.io/part-of`, `/version` and `/managed-by` beside the `app.kubernetes.io/name` and `/instance` labels the selectors match. The chart reaches the data tier by name: `<fullname>-postgresql` (host, and the Secret with the `username`, `password` and `database-url` keys, or `postgresql.existingSecret`), `<fullname>-kafka:9092`, `<fullname>-opensearch:9200` and `http://<fullname>-keycloak:8080/auth`, where `<fullname>` is the one described below. Every application workload has an `enabled` flag (`true` by default: `apiGateway`, `ingestService`, `ingestListener`, `web` and the six services under Application services below), so a release can run the data tier alone. `ingestService.enabled=false` also drops the `<fullname>-ingest` Secret that the gateway and the listener read, so the chart refuses to render while either of them is on, unless `ingestService.existingSecret` names a Secret with the `service-token` key.

The chart's `ingestListener` values:

| Value | Default | Purpose |
|-------|---------|---------|
| `ingestListener.enabled` | `true` (chart and `k8s/overlays/prod/values.yaml`), `false` (`k8s/overlays/dev/values.yaml`) | Render the listener Deployment |
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
- **NetworkPolicies.** The listener is on the host network, so its connections to PostgreSQL and Kafka come from a node address: set `networkPolicy.hostNetworkCIDRs` (the prod overlay sets `10.42.0.1/32`, one k3s node's `cni0`), or while the listener is enabled those two accept every source on their port: pods in any namespace and the nodes (their Services are ClusterIP, so this is not the internet) (NetworkPolicies and backups, below).
- **Firewalls and security groups.** Allow inbound TCP and UDP (gRPC is TCP) on the ports your connections use to the labelled node, from the senders' networks only: the host firewall, the cloud security group and any network ACL. A port chosen later in Connections is closed until you open it, so open a range you control and narrow it with each connection's `allow`. Never open `:4010` to outside networks; the one exception is Prometheus inside the cluster, which has to reach it for the listener's `/metrics` when `ingestService.serviceMonitor.enabled` renders the PodMonitor. A UDP listener's receive buffer is capped by `net.core.rmem_max` of the node (`docs/feeds.md`).
- **Node ports.** The default `INGEST_RESERVED_PORTS` covers Sentinel's own ports, not the node's (the kubelet's 10250, node exporters, ...). Add them with `ingestService.env.INGEST_RESERVED_PORTS`, which reaches the ingest and listener Deployments alike; the value replaces the default list, so repeat what you still want, and keep it the same everywhere because the replica that answers a request validates the port. `k8s/overlays/prod/values.yaml` does this for a k3s node (the API server and supervisor, containerd, the kubelet and its neighbours, etcd, flannel and WireGuard, a node exporter and the NodePort range), and `make chart-lint` checks that it still holds every port of the default list.
- **Render and lint the chart before a release.** The listener templates and the schema Job need a real `helm template` pass with a service token and `global.host` (the chart refuses to render without either). `make chart-lint` needs `helm` and `kubeconform` (no dependency build). It runs `helm lint`, checks that the schema Job carries `db/schema`, checks that every hardened pod spec (the gateway, the six services, the web client, both ingest Deployments and the Job) renders `runAsNonRoot: true`, and validates the rendered chart with `kubeconform -strict -ignore-missing-schemas` for the defaults, the dev overlay, the prod overlay alone and with the local one over it, a render with GeoServer and the ingest ServiceMonitor on, a render with registration mail, map keys, the services' ServiceMonitors and the Ingress's class and annotations, the data tier alone, and the apps against an external database (`postgresql.existingSecret`, data tier off). It also checks that the topics script and the initdb SQL reach the chart through their links, and the runAsNonRoot check covers the data tier's pods (Keycloak's too) and the topics Job. Finally it checks the Keycloak realm the chart renders, with and without registration mail: its only users are `sentinel-admin` and the service account, it holds no `-dev` value and no password (the service client secret, the bootstrap password and the SMTP password are `${...}` placeholders), and `sentinel-web`'s redirect URIs and web origins are all on `https://sentinel.example.com`. The NetworkPolicies and backups are validated with their optional parts on (the listener's `hostNetworkCIDRs`, GeoServer's peers, the metrics peer, an rclone upload) and with both off; the backup CronJob is in the runAsNonRoot check, and two checks pin the listener rule: PostgreSQL's policy has no `from` while the listener has no CIDRs, and the CIDRs reach both the PostgreSQL and the Kafka policy. A render with `web.service.port=80` must leave every policy on a named port and the web policy on the container's `http` port (4200). Last, the prod overlay's `INGEST_RESERVED_PORTS`, which replaces the service's default list, must reach both ingest Deployments and still hold every entry of `DefaultReservedPorts` (`apps/ingest-service/internal/registry/ports.go`):

  ```bash
  make chart-lint
  helm template t k8s/charts/sentinel --set ingestService.serviceToken=x --set global.host=sentinel.example.com
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

- **The role.** The Job connects with the `database-url` key of the `<fullname>-postgresql` Secret (or `postgresql.existingSecret`), as the role the services and the gateway use: `sentinel`, which is not a superuser. That role must own schema `sentinel`, and the role `sentinel_gis` must exist so that `90_gis_views.sql` needs no `CREATEROLE`. The chart's PostgreSQL does both at its first start (Data tier below). Another database needs the same one-time setup, on an empty data directory (`CREATE SCHEMA sentinel AUTHORIZATION <role>`, `CREATE ROLE sentinel_gis LOGIN` without a password, and `GRANT SELECT ON public.geometry_columns, public.spatial_ref_sys TO <role> WITH GRANT OPTION` so the role can pass them on to `sentinel_gis`). A database that already holds the schema needs, once and as a superuser: `ALTER SCHEMA sentinel OWNER TO <role>;`, `ALTER TABLE sentinel.<table> OWNER TO <role>;` for each table already in the schema (or `REASSIGN OWNED BY <old owner> TO <role>;`), and `CREATE ROLE sentinel_gis LOGIN;` only if it is missing.
- **The GIS password.** `sentinel_gis` has no password, so it cannot log in until `ALTER ROLE sentinel_gis PASSWORD '<geoserver.gisDbPassword>';` sets the one GeoServer's provisioning uses. Until then `geoserver-init` fails on a first install; run it again afterwards. The comment in `db/schema/90_gis_views.sql` that says the development password is set there describes Compose only.
- **Timeouts and waiting.** Helm waits for hooks only as long as `--timeout` (5 minutes by default), while the Job may run `activeDeadlineSeconds` (15 minutes); pass `--timeout 15m` for a slow cold start. On a first install do not pass `--wait` or `--atomic`: Helm would wait for ingest pods that cannot start until the Job has run, so use `helm upgrade --install` without them. Upgrades are fine, since pre-upgrade hooks run first.
- **What stays.** Hook resources are not removed by `helm uninstall`. The ConfigMap (about 53 KB) stays until the next install or upgrade replaces it, and a failed Job stays for its logs (`kubectl logs job/<fullname>-db-schema`); delete the ConfigMap by hand after an uninstall.

### Data tier (Helm)

The chart runs PostgreSQL with PostGIS, Kafka and OpenSearch itself, each a one-replica StatefulSet with a ClusterIP Service and a ReadWriteOnce claim from `global.storageClass`, using the images Compose runs. It targets one node: there is no replication, so the nightly backups (NetworkPolicies and backups, below) are what protects the data. Each has an `enabled` flag, `resources`, `persistence.size`, an `image` (pulled from its own registry, never `global.imageRegistry`) and the restricted Pod Security settings (non-root, no privilege escalation, every capability dropped, `RuntimeDefault` seccomp) in `podSecurityContext` and `containerSecurityContext`. The namespace can enforce the `restricted` level as long as the ingest listener (host network) is off. Keycloak, the fourth part of the data tier, keeps its state in PostgreSQL; see Keycloak, Ingress and TLS below.

| Component | Image | Service | Runs as | Defaults |
|-----------|-------|---------|---------|----------|
| `postgresql` | `postgis/postgis:16-3.4` | `<fullname>-postgresql:5432` | 999, read-only root | 50Gi; 250m / 512Mi requested, 2000m / 2Gi limit; `/dev/shm` 256Mi in memory (`shmSize`) |
| `kafka` | `apache/kafka:3.7.0` | `<fullname>-kafka:9092` | 1000 | 20Gi; heap 768m (`heapOpts`); 250m / 1Gi requested, 1000m / 1536Mi limit |
| `opensearch` | `opensearchproject/opensearch:2.17.1` | `<fullname>-opensearch:9200` | 1000 | 20Gi; heap 1g (`javaOpts`); 500m / 2Gi requested, 2000m / 2Gi limit |

- **PostgreSQL.** `PGDATA` is the `pgdata` subdirectory of the claim (initdb refuses a mount point holding `lost+found`), and the grace period is 60 s so a fast shutdown can checkpoint. On its first start, against an empty volume, an initdb script (ConfigMap `<fullname>-postgresql-initdb`) runs as the superuser `postgres`, after the image's own PostGIS script, and creates: the login roles `sentinel` and `keycloak` with the passwords of the Secret; the database `sentinel`, owned by `sentinel` and copied from `template_postgis` so it has the extensions of the Compose database (`postgis`, `postgis_topology`, `fuzzystrmatch`, `postgis_tiger_geocoder`); the database `keycloak`, owned by `keycloak` (Keycloak's); schema `sentinel` owned by `sentinel`; `sentinel_gis` (LOGIN, no password); then it runs `config/postgres/init-extensions.sql` (linked into the chart as `files/postgres`) in `sentinel`, as Compose does, and grants `sentinel` the PostGIS catalogues with grant option for `90_gis_views.sql`. TimescaleDB and Apache AGE are not in the image, so that script skips them and the schema keeps plain tables. Compose differs in one way: its `sentinel` role is the superuser. The script never runs again, so a password changed in the Secret later must be changed in the database too (`ALTER ROLE ... PASSWORD ...`, as `postgres`), and the services restarted.
- **The Secret.** Unless `postgresql.existingSecret` names one, the chart generates `<fullname>-postgresql` with the keys `postgres-password` (the superuser), `username` (`sentinel`), `password` (`sentinel`'s), `keycloak-password` and `database-url` (`postgres://sentinel:<password>@<fullname>-postgresql:5432/sentinel?sslmode=disable`: the connection stays inside the cluster, behind the NetworkPolicies, so it is not encrypted). The passwords are 32 random letters and digits, created on the first install and read back from the Secret on every upgrade. The Secret carries `helm.sh/resource-policy: keep`: `helm uninstall` leaves it, as Kubernetes leaves the StatefulSets' claims, so installing the same release name into the same namespace again adopts it and the passwords still match the data. Delete both by hand to start over (`kubectl delete secret <fullname>-postgresql` and `kubectl delete pvc -l app.kubernetes.io/instance=<release>`). An `existingSecret` must have all five keys and exist before the install; the database-url must name the user `sentinel`. With `postgresql.enabled=false` (an external database) the services still read `<fullname>-postgresql` or `existingSecret`, and that database needs the setup described under The role above.
- **Kafka.** KRaft combined mode (broker and controller in one process, node id 1, the Compose `CLUSTER_ID`). Clients use the PLAINTEXT listener on 9092, advertised as `<fullname>-kafka.<namespace>.svc.<global.clusterDomain>:9092`; the controller listens on 9093 and votes for itself through `localhost`. The other settings are Compose's (`KAFKA_LOG_DIRS=/var/lib/kafka/data`, replication factor 1, no topic auto-creation, 10 MB messages, 7-day default retention). The pod has `enableServiceLinks: false`, because the image turns every `KAFKA_*` variable into a broker setting, the injected `KAFKA_PORT=tcp://...` included. The log directory is the claim's `data` subdirectory: Kafka refuses to start when the log directory holds anything but partitions (a `lost+found`, say), and the image declares `/var/lib/kafka/data` a volume, so a claim mounted above it would leave the logs on an anonymous volume that the next pod loses. Probes are TCP on 9092. `kafka.clusterId` must not change once the volume is formatted.
- **Kafka topics.** The hook Job `<fullname>-kafka-topics` (post-install and pre-upgrade, `kafka.topics.enabled`) runs `config/kafka/create-topics.sh`, linked into the chart as `files/kafka`, with `KAFKA_BROKER=<fullname>-kafka:9092` and `REPLICATION_FACTOR` from `kafka.topics.replicationFactor` (1). It waits for the broker, creates the five topics that are missing and leaves existing ones alone, so new partition counts or retentions in the script reach new topics only. Because it runs before every upgrade and waits for the broker, an upgrade meant to repair a broken Kafka stalls until `kafka.topics.activeDeadlineSeconds` (600) runs out: pass `--set kafka.topics.enabled=false` for that one upgrade.
- **OpenSearch.** Single-node discovery, which also skips the bootstrap checks, so the node needs no `vm.max_map_count` change. The security plugin is off (no TLS, no login): only the NetworkPolicies keep other pods out. `bootstrap.memory_lock` is false, since locking memory needs `IPC_LOCK`, which a restricted pod cannot have. Readiness is `/_cluster/health?local=true`; a single node reports `yellow` once an index asks for replicas.
- **Upgrading from the Bitnami subcharts.** Their StatefulSets, Secrets and claims have other names and layouts; the chart does not migrate them. Dump the old database (`pg_dump`) and restore it into the new one.

### Application services (Helm)

The gateway, the six services behind it and the web client, each a Deployment with a ClusterIP Service named `<fullname>-<name>`. All of them use the private-registry images (`global.imageRegistry`, `global.imageTag`) and `https://<global.host>` as their CORS origin, so `global.host` is required. They run non-root with the restricted Pod Security settings (no privilege escalation, every capability dropped, `RuntimeDefault` seccomp) and a read-only root filesystem.

| Values key | Workload | Port | Replicas | Reads |
|------------|----------|------|----------|-------|
| `apiGateway` | `api-gateway` | 3000 | 2 | PostgreSQL, Kafka, Keycloak, every service below |
| `entityService` | `entity-service` | 3001 | 2 | PostgreSQL, Kafka |
| `trackService` | `track-service` | 3002 | 2 | PostgreSQL, Kafka |
| `searchService` | `search-service` | 3003 | 1 | PostgreSQL, Kafka, OpenSearch |
| `linkAnalysisService` | `link-analysis-service` | 3004 | 1 | PostgreSQL |
| `alertService` | `alert-service` | 3005 | 2 | PostgreSQL, Kafka |
| `analyticsService` | `analytics-service` | 5000 | always 1 | PostgreSQL, Kafka |
| `web` | `web` | 4200 | 2 | nothing (static files) |

- **The six services** come from one template (`templates/services.yaml`). Each gets `PORT`, `DATABASE_URL` (the `database-url` key of the Postgres Secret, so `postgresql.existingSecret` reaches them too), `KAFKA_BROKERS` (not link-analysis) and `CORS_ORIGIN`; search also gets `OPENSEARCH_HOST` and `OPENSEARCH_PORT` (`searchService.opensearch.host`, empty for the chart's `<fullname>-opensearch`, and `.port`, `9200`). Per service: `enabled`, `replicaCount`, `image`, `service.port`, `resources`, `env` (plain values the chart does not set, such as `LOG_LEVEL` or the `ANALYTICS_*` thresholds; a name the chart sets is an error) and `secretEnv.existingSecret` (a Secret whose keys become variables through `envFrom`). Analytics has no `replicaCount`: its baseline job takes a Postgres advisory lock and its detector state is in process, so it always runs one replica. Probes are `/health` (liveness) and `/ready` (readiness). `appServices` holds what the six share: `podSecurityContext` and `containerSecurityContext` (user and group 10001, the images' user) and `serviceMonitor` (`enabled: false`, `interval: 30s`), which renders a ServiceMonitor per service for its `/metrics` and needs the Prometheus Operator CRDs.
- **The gateway** runs with `APP_ENV=production` and never gets `AUTH_DEV_BYPASS`. The chart sets `DATABASE_URL` (Postgres Secret), `KAFKA_BROKERS`, `CORS_ORIGIN=https://<host>`, `KEYCLOAK_URL=http://<fullname>-keycloak:8080/auth` (the signing keys are fetched inside the cluster), `KEYCLOAK_REALM=sentinel`, `KEYCLOAK_ISSUERS=https://<host>/auth/realms/sentinel` (the issuer in the tokens, which is the public address the browser used), the seven `*_SERVICE_URL`s (`http://<fullname>-<service>:<service.port>`, ingest included), `INGEST_SERVICE_TOKEN` (the ingest Secret), `KEYCLOAK_SERVICE_CLIENT_SECRET` and the `MAIL_*` below. `apiGateway.extraEnv` adds plain values the chart does not set (`VIEWPORT_SYNC_ENABLED`, `INTELSKY_BASE_URL`, `LOG_LEVEL`, ...; a name the chart sets is an error). `apiGateway.podSecurityContext` and `containerSecurityContext` default to user 10001 and a read-only root. The signing keys are fetched on the first token, so with Keycloak down `/health` and `/ready` (the database) still answer 200 and protected routes answer 401.
- **The service client secret.** The gateway creates accounts through Keycloak's `sentinel-service` client, and under `APP_ENV=production` it refuses to start when that client's secret is empty or the dev realm's `sentinel-service-secret-dev`. The chart generates the Secret `<fullname>-keycloak-clients` with the key `service-client-secret` (32 random letters and digits, read back on every upgrade, `helm.sh/resource-policy: keep` so it survives `helm uninstall` like the Postgres Secret). The realm the chart imports into Keycloak gives the `sentinel-service` client the same value (Keycloak, Ingress and TLS below). `keycloak.clientsExistingSecret` names a Secret of your own with that key instead; it must not hold the dev value.
- **Registration mail.** `mail.host` empty (the default) sets no `MAIL_*` at all, and registration e-mails fail until a relay is configured: the gateway would otherwise send to `localhost:1025`, the dev mailhog. With a host the chart sets `MAIL_HOST`, `MAIL_PORT` (`mail.port`, `587`), `MAIL_FROM` (`mail.from`, empty for `SENTINEL <sentinel@<host>>`), `MAIL_TLS` (`mail.tls`: `starttls`, the default, `tls` for implicit TLS, usually port 465, or `none`), `MAIL_USERNAME` when `mail.username` is set, and `MAIL_PASSWORD` from the key `password` of `mail.existingSecret`, or of the Secret `<fullname>-mail` that the chart renders from `mail.password` (only when it is set). The chart refuses `mail.tls=none` with a password, as the gateway would (it would send the password in cleartext), and any other `mail.tls` value.
- **The web client** is the `sentinel/web` image, unprivileged nginx (user 101) on 4200. The image's `default.conf` proxies to the Compose hosts `api-gateway` and `keycloak`, which do not resolve here, so the chart mounts a ConfigMap (`<fullname>-web-nginx`) over `/etc/nginx/conf.d` whose server block listens on 4200 and includes only the image's `static.conf`, and answers 404 under `/auth`, so the parts of Keycloak the Ingress does not route never get the client's `index.html`; the Ingress routes `/api`, `/ws` and Keycloak's public paths. A change to it rolls the pods (a checksum annotation). The root filesystem is read-only, with emptyDirs at `/tmp` (nginx's pid file and temp paths) and `/var/run/sentinel`, where the entrypoint writes `/config.json` from `GOOGLE_MAPS_API_KEY` and `CESIUM_ION_ACCESS_TOKEN`. Those come from `web.mapKeys`: `googleMapsApiKey` and `cesiumIonAccessToken`, stored in the Secret `<fullname>-web`, or `existingSecret`, a Secret with the keys `google-maps-api-key` and `cesium-ion-access-token`, either of which may be missing. Both are optional (an empty key turns off the layer that needs it) and must match `[A-Za-z0-9._-]`: the chart refuses other characters, as the container would. Probes are `/healthz`.

### Keycloak, Ingress and TLS (Helm)

Keycloak 26 (`quay.io/keycloak/keycloak:26.8.1`, `keycloak.image`) runs as a one-replica Deployment `<fullname>-keycloak` with the Service `<fullname>-keycloak:8080`, the address the gateway uses for the signing keys, the token endpoint and the admin API. It keeps its state in the `keycloak` database of the chart's PostgreSQL (user `keycloak`, the `keycloak-password` key of the Postgres Secret); an init container waits for it with `pg_isready` (the `dbSchema.image`). It starts with `start --import-realm` in production mode, served under `/auth` (`KC_HTTP_RELATIVE_PATH`) at `KC_HOSTNAME=https://<global.host>/auth`, so tokens carry the issuer `https://<host>/auth/realms/sentinel` that the gateway checks; `KC_HOSTNAME_BACKCHANNEL_DYNAMIC=true` answers the gateway's in-cluster requests at the URL they came to. TLS ends at the Ingress, which forwards plain HTTP (`KC_HTTP_ENABLED=true`) with the `X-Forwarded-*` headers (`KC_PROXY_HEADERS=xforwarded`). Health is on the management port 9000, which the Service does not expose (`/auth/health/started`, `/ready`, `/live`); the startup probe allows 5 minutes, since the first start builds Keycloak for its options and creates the schema. `KC_CACHE=local`: one replica, nothing to cluster with, and sessions live in the database. It runs as the image's user 1000 with the restricted Pod Security settings, but not on a read-only root, since that build writes under `/opt/keycloak`. `keycloak.extraEnv` adds plain values (`KC_LOG_LEVEL`, ...); `keycloak.resources` defaults to 250m / 768Mi requested, 2000m / 1536Mi limit.

- **The realm** is in a ConfigMap, `<fullname>-keycloak-realm` (beside `master-realm.json`, below), that the chart renders from `config/keycloak/sentinel-realm.json` (linked into the chart as `files/keycloak`; the dev realm itself is unchanged). The chart keeps only its service-account users (the dev `admin`, `analyst` and `viewer` go; `sentinel-service`'s keeps `manage-users` and `view-realm`, which approving a registration needs to read the realm roles) and adds `sentinel-admin`; sets `sentinel-web`'s redirect URIs (`https://<host>/*`), web origins (`https://<host>`) and post-logout redirect URIs (`https://<host>/*`); adds the `VERIFY_EMAIL` (the e-mail an approval sends) and `UPDATE_PASSWORD` required actions, which the file does not list; and replaces `smtpServer` with the `mail` values (host, port, the address and display name of `mail.from`, `starttls` or `ssl` from `mail.tls`, `auth` and `user` when `mail.username` is set), or drops it when `mail.host` is empty. The secrets are Keycloak placeholders that it resolves from its environment while importing: `${SENTINEL_SERVICE_CLIENT_SECRET}` (the `sentinel-service` client's secret, from `<fullname>-keycloak-clients` or `keycloak.clientsExistingSecret`, the value the gateway uses), `${SENTINEL_ADMIN_PASSWORD}` and `${SENTINEL_SMTP_PASSWORD}` (from the mail Secret, when there is one). The ConfigMap therefore holds no secret. Placeholders Keycloak cannot resolve, such as the user profile's `${username}` display names, stay as they are.
- **The realms are imported on the first start only.** `--import-realm` skips a realm that already exists, so a later change to the realm file, `global.host`, `mail`, `keycloak.adminUrl` or the Secrets above does not reach a running deployment: make it in the admin console as well (Realm settings, Email for the mail settings; Clients for the redirect URIs; the client's Credentials for its secret, then update `<fullname>-keycloak-clients` to match). To import again from scratch, delete the realm in the admin console and restart the Deployment; every account in it is lost.
- **Bootstrap users.** `sentinel-admin` signs in to Sentinel with the roles of the dev `admin` (`sentinel-admin`, `sentinel-analyst`, `classification-ts`), the e-mail `keycloak.bootstrapUser.email` (empty: `sentinel-admin@<host>`, marked verified) and a password that is not temporary, since the client signs in with the password grant, which cannot change it. The master realm's admin (`keycloak.adminUsername`, `admin`) is Keycloak's bootstrap admin (`KC_BOOTSTRAP_ADMIN_USERNAME` and `_PASSWORD`), which Keycloak creates on its first start and marks temporary: create a permanent admin in the console and delete it. Both passwords are in the Secret `<fullname>-keycloak` (keys `sentinel-admin-password` and `admin-password`, 32 random letters and digits, read back on every upgrade, `helm.sh/resource-policy: keep`), or in `keycloak.existingSecret`, a Secret of your own with those keys. Read them with
  ```bash
  kubectl -n <namespace> get secret <fullname>-keycloak -o jsonpath='{.data.sentinel-admin-password}' | base64 -d; echo
  kubectl -n <namespace> get secret <fullname>-keycloak -o jsonpath='{.data.admin-password}' | base64 -d; echo
  ```
  Like the realm, they are used on the first start only: after a password is changed in Keycloak the Secret no longer shows it.
- **The admin console** is not on the Ingress. Forward the Service and open `http://localhost:8080/auth/admin/`, then sign in as the master realm's admin. `keycloak.adminUrl` (`http://localhost:8080/auth`) is both the console's address (`KC_HOSTNAME_ADMIN`, for its pages and the admin API) and the master realm's frontend URL: the console signs in through the master realm, whose pages the Ingress does not route, so the chart also imports a `master-realm.json` (the file name is what makes Keycloak import it instead of creating its default master realm): `files/keycloak-master-realm.json`, Keycloak's default master realm (its display name, and a user profile that requires no e-mail or name, without which the bootstrap admin could not sign in), with that URL as its frontend URL. Master-realm tokens therefore carry the issuer `http://localhost:8080/auth/realms/master`; the sentinel realm keeps `https://<host>`. Forward to another local port and the console will not load: change `keycloak.adminUrl` before the first start, or the master realm's Frontend URL in the console afterwards. Keep local port 8080 free:
  ```bash
  kubectl -n <namespace> port-forward svc/<fullname>-keycloak 8080
  ```
- **The Ingress** (`<fullname>`, `ingress.enabled`) serves `https://<global.host>` and routes, by path prefix, `/api` and `/ws` to `<fullname>-api-gateway`, `/auth/realms/sentinel` and `/auth/resources` (the realm's login pages, tokens, keys and account console, and the files they load) to `<fullname>-keycloak`, and everything else to `<fullname>-web`. The rest of `/auth` reaches the web client, which answers 404: the admin console and admin API (`/auth/admin`), the master realm (`/auth/realms/master`, whose admin a password grant could otherwise be tried against from anywhere), and Keycloak's welcome page. Health and metrics are on the management port, which no Service exposes, so `/auth/health` and `/auth/metrics` are 404 too. `ingress.className` names the IngressClass (empty: the cluster's default, `traefik` on k3s); `ingress.annotations` adds annotations for the controller; `ingress.clusterIssuer` (`letsencrypt-prod`) becomes the `cert-manager.io/cluster-issuer` annotation, and cert-manager keeps the certificate in `ingress.tlsSecretName` (empty: `<fullname>-tls`). With `clusterIssuer` empty, create that TLS Secret yourself. The chart does not redirect plain HTTP itself; the controller must, or `http://<host>/` serves the client and its password-grant login would send credentials in cleartext. On k3s, `k8s/cluster/traefik-config.yaml` does it (below). ingress-nginx (RKE2's default controller) already answers 308 on HTTP for an Ingress that has a `tls` section (`ssl-redirect`, on by default), and leaves `/.well-known/acme-challenge/` alone; to force it, set `--set-string ingress.annotations.nginx\.ingress\.kubernetes\.io/force-ssl-redirect=true`. Another controller needs its own equivalent.
- **Cluster setup** (`k8s/cluster/`, applied once per cluster, not part of the chart). Install cert-manager with its CRDs first (`helm install cert-manager jetstack/cert-manager -n cert-manager --create-namespace --version v1.21.2 --set crds.enabled=true`). Then:
  - `letsencrypt-issuers.yaml`: the ClusterIssuers `letsencrypt-staging` and `letsencrypt-prod`, ACME with HTTP-01 through the `traefik` IngressClass. Put your e-mail address in both before applying. HTTP-01 needs the host to resolve to the node from the internet and port 80 to reach the controller. Try staging first (`--set ingress.clusterIssuer=letsencrypt-staging`): its certificates are untrusted but its rate limits are generous.
  - `selfsigned-issuer.yaml`: for a cluster with no public name (k3d). A `selfsigned` ClusterIssuer issues a CA certificate (`sentinel-local-ca` in the `cert-manager` namespace), and the ClusterIssuer `sentinel-local-ca` signs with it; install with `--set ingress.clusterIssuer=sentinel-local-ca` and trust the CA (`kubectl -n cert-manager get secret sentinel-local-ca -o jsonpath='{.data.ca\.crt}' | base64 -d`), with `curl --cacert`, `NODE_EXTRA_CA_CERTS` or the system keychain.
  - `local-path-retain.yaml` (k3s): a copy of the `local-path` StorageClass with `reclaimPolicy: Retain`, for the prod overlay's `global.storageClass`. Deleting a claim or the namespace leaves the volume's directory under `/var/lib/rancher/k3s/storage` and its PersistentVolume `Released`; it is not bound again by itself, so restore from the backups or copy the files out, and delete Released volumes by hand. Apply it before the first install: a StatefulSet's storage class cannot change later.
  - `traefik-config.yaml` (k3s): a `HelmChartConfig` for the Traefik chart k3s bundles (v39; its keys changed between chart versions, so check `kubectl -n kube-system get helmchart traefik -o jsonpath='{.spec.chart}'`). It sets Traefik's Service to `externalTrafficPolicy: Local`, so the client's address survives to Traefik's `X-Forwarded-For`, which the gateway's registration rate limiter keys on (with the default `Cluster` every visitor would share the node's address). It also redirects the `web` entrypoint (80) to `websecure` (443) with a permanent 308 (`ports.web.http.redirections.entryPoint`). cert-manager's HTTP-01 challenges still pass: Let's Encrypt follows the redirect to `https://<host>/.well-known/acme-challenge/...` without checking the certificate it finds there, and Traefik routes the solver's Ingress on 443 as well.
- **On k3d** (local checks): the issuer and the redirects must equal the browser's URL, so map the real ports (`k3d cluster edit <cluster> --port-add "443:443@loadbalancer"`, and 80) and use `global.host=sentinel.localhost`, which resolves to 127.0.0.1. Keep host port 8080 free for the admin console's port-forward.

### NetworkPolicies and backups (Helm)

**NetworkPolicies** (`templates/networkpolicies.yaml`, `networkPolicy.enabled`, on by default) restrict what reaches each pod of the release; egress is not restricted, since feeds, the mail relay and ACME are on the internet. They need a CNI that enforces them: k3s's embedded controller (kube-router, on by default), Calico or Cilium. A default deny selects every pod of the release (`app.kubernetes.io/instance` and `app.kubernetes.io/part-of: sentinel`), so a pod with no rule below (the hook Jobs, the backups) accepts nothing; pods outside the release, such as cert-manager's solver or a debug pod, are not selected by it. The rules name the containers' ports (`http`, `postgresql`, `kafka`), since a policy matches the pod's port and not the Service's: changing a `service.port` value leaves them right.

| Pods | Accept | Container port |
|------|--------|----------------|
| `web` | the ingress controller | 4200 |
| `api-gateway` | the ingress controller | 3000 |
| `keycloak` | the ingress controller and the gateway (signing keys, tokens, admin API) | 8080 |
| `entity-service` … `alert-service`, `analytics-service`, `ingest-service` | the gateway, and Prometheus while the chart renders their ServiceMonitor | `http` (3001-3005, 5000, 4000) |
| `opensearch` | `search-service` | 9200 |
| `postgresql`, `kafka` | every pod of the namespace, and `networkPolicy.hostNetworkCIDRs` | 5432, 9092 |
| `geoserver` (when enabled) | anyone, or `geoserver-init` and `networkPolicy.geoserver.from` when that is set | 8600 |
| cert-manager's HTTP-01 solver (`acme.cert-manager.io/http01-solver`) | the ingress controller | any |

| Value | Default | Purpose |
|-------|---------|---------|
| `networkPolicy.enabled` | `true` | Render the policies |
| `networkPolicy.ingressController` | `namespaceSelector` `kubernetes.io/metadata.name: kube-system`, `podSelector` `app.kubernetes.io/name: traefik` | The ingress controller's pods (both selectors must match): k3s's Traefik. RKE2's ingress-nginx runs in `kube-system` (`app.kubernetes.io/name: rke2-ingress-nginx`), the upstream ingress-nginx chart in `ingress-nginx` (`app.kubernetes.io/name: ingress-nginx`). A wrong selector takes the whole site down (every page times out at the controller), so check the labels with `kubectl get pods -A --show-labels \| grep -i ingress` |
| `networkPolicy.hostNetworkCIDRs` | `[]` | Where the ingest listener's connections to PostgreSQL and Kafka come from (below) |
| `networkPolicy.metrics` | `namespaceSelector` `kubernetes.io/metadata.name: monitoring`, `podSelector` `{}` | Prometheus, allowed to the services and the ingest service while `appServices.serviceMonitor.enabled` / `ingestService.serviceMonitor.enabled` render their monitors. The listener's PodMonitor needs nothing (a hostNetwork pod is not subject to policies); a scraper that follows the `prometheus.io/scrape` annotations needs a monitor turned on too |
| `networkPolicy.geoserver.from` | `[]` | NetworkPolicy peers (`ipBlock`, `namespaceSelector`, ...) GeoServer accepts on 8600 beside its provisioning Job; empty accepts anyone, as without the policies, since GIS clients reach it however you expose it and its own accounts guard the layers. `kubectl port-forward` is never affected by policies |

- **The ingest listener and `hostNetworkCIDRs`.** The listener runs on the host network, so policies do not apply to it, and its connections to PostgreSQL and Kafka arrive from a node address rather than a pod address: from the node's pod-network gateway when the database pod is on the same node (`cni0`, `10.42.0.1` on the first k3s node, which is what PostgreSQL's `pg_stat_activity` shows on k3d), and, when it is on another node, usually from the listener node's address on the interface the traffic leaves by (its pod-network address with some CNIs): confirm the source with `SELECT DISTINCT client_addr FROM pg_stat_activity` in PostgreSQL once the listener runs. List those addresses in `networkPolicy.hostNetworkCIDRs` (for k3s, `--set networkPolicy.hostNetworkCIDRs={10.42.0.1/32}` on one node). **With the listener enabled (the chart default) and the list empty, PostgreSQL and Kafka accept every source on their port: pods in any namespace and the nodes (their Services are ClusterIP, so this is not the internet)**, not only the namespace's pods, so the listener keeps working whatever the CNI; set the CIDRs in production. With the listener off they accept the namespace only.
- **Traffic from the node itself.** kube-router accepts any connection whose source is an address of the pod's own node, whatever the policies say ("permit the traffic to pods when source is the pod's local node"), so the kubelet's probes keep working and, on a single k3s node, so would the listener with any CIDRs. Other CNIs treat host-network traffic in their own ways, especially from another node (Cilium, for one, matches node addresses with `ipBlock` only when its `policy-cidr-match-mode` includes `nodes`): on anything but a single k3s node, check that the listener's `/ready` still answers 200 after the policies are on.
- **Only the ingress controller reaches the web client, the gateway and Keycloak.** Exposing them another way, with a `NodePort` or `LoadBalancer` Service or a second controller, is blocked by their policies until its source is admitted: point `networkPolicy.ingressController` at that controller, or add a policy of your own for the other path.
- **cert-manager's solver.** While cert-manager gets a certificate it starts a solver pod in the release's namespace, and Let's Encrypt reaches it through the ingress controller. The default deny does not select it; the chart's `<fullname>-acme-solver` policy (with an Ingress and a `clusterIssuer`) admits the ingress controller to it, so a namespace-wide default deny of your own does not break issuance or renewal.
- **Checking them.** A pod in the namespace is refused by the services and the gateway, while the gateway reaches them. The second command adds an ephemeral container, which shares the gateway pod's network and stays listed in the pod (terminated) until the pod is replaced:
  ```bash
  kubectl -n <namespace> run np-check --rm -i --restart=Never --image=curlimages/curl:8.10.1 -- \
    curl -s -m 4 -o /dev/null -w '%{http_code}\n' http://<fullname>-entity-service:3001/health   # 000 (refused)
  gw=$(kubectl -n <namespace> get pod -l app.kubernetes.io/name=api-gateway -o name | head -1)
  kubectl -n <namespace> debug "$gw" -i --profile=restricted --image=curlimages/curl:8.10.1 -- \
    curl -s -o /dev/null -w '%{http_code}\n' http://<fullname>-entity-service:3001/health          # 200
  ```

**Backups** (`templates/backup-cronjob.yaml`, `backup.enabled`, on by default, rendered with the chart's PostgreSQL only: an external database is backed up where it runs). The CronJob `<fullname>-backup` runs `pg_dumpall --globals-only` (the roles and their password hashes, `globals.sql`) and `pg_dump --format=custom` of each database (`sentinel.dump`, `keycloak.dump`) as the superuser `postgres`, into `/backups/<UTC time>/` on the claim `<fullname>-backups`. It writes into `.partial-<time>/` and renames the directory only when every dump has succeeded, so a directory named for its time always holds a whole backup. After a good run it deletes the backup directories older than `backup.retentionDays` (and unfinished ones older than a day); a failing run deletes nothing.

| Value | Default | Purpose |
|-------|---------|---------|
| `backup.enabled` | `true` | Render the CronJob and the claim |
| `backup.schedule`, `backup.timeZone` | `30 2 * * *`, empty (the controller's zone, usually UTC) | When it runs. A run missed by more than an hour (the cluster was down) is skipped; one run at a time (`concurrencyPolicy: Forbid`) |
| `backup.databases` | `[sentinel, keycloak]` | One `<db>.dump` each |
| `backup.retentionDays` | `7` | Age at which a backup directory is deleted (1 or more) |
| `backup.image` | `postgres:16-alpine` | `pg_dump` must be the server's major version or newer |
| `backup.persistence.size`, `backup.persistence.existingClaim` | `20Gi`, empty | The claim `<fullname>-backups` (from `global.storageClass`, `helm.sh/resource-policy: keep`, so it survives `helm uninstall`), or an existing one such as an NFS share |
| `backup.rclone.enabled`, `.existingSecret`, `.remote`, `.extraArgs`, `.image` | off, empty, empty, `[]`, `rclone/rclone:1.71.2` | Off-node copies, below |
| `backup.preUpgrade.enabled` | `false` | A backup before every upgrade, below |
| `backup.backoffLimit`, `backup.activeDeadlineSeconds` | `2`, `3600` | Retries, and how long a run may take |
| `backup.resources` | 50m / 128Mi requested, 1000m / 512Mi limit | |
| `backup.podSecurityContext`, `backup.containerSecurityContext` | PostgreSQL's ids (999, `fsGroup` 999), read-only root, no privilege escalation, every capability dropped | The same ids as PostgreSQL, because its pod mounts the claim too |

- **The claim is on the node too.** On a single node with `local-path` the dumps share the database's disk: they protect against a dropped table, a bad upgrade or a corrupt database, not against losing the node. Copy them off it with rclone (or point `existingClaim` at storage that is elsewhere).
- **PostgreSQL mounts the claim** read-only at `/backups`, so restores run in its pod with `kubectl exec`. It also makes the claim bind during the install: with a `WaitForFirstConsumer` class such as `local-path`, a claim only the CronJob used would stay `Pending` until the first run, and `helm --wait` would time out on it. Turning backups on or off therefore restarts PostgreSQL once, and when `existingClaim` is unavailable PostgreSQL cannot start.
- **Off-node copies with rclone.** Create a Secret with the key `rclone.conf` (an S3 bucket, a B2 or SFTP remote, ...) and name a remote and path in it:
  ```bash
  kubectl -n <namespace> create secret generic sentinel-backup-rclone --from-file=rclone.conf=$HOME/sentinel/rclone.conf   # kept outside the checkout
  helm upgrade ... --set backup.rclone.enabled=true --set backup.rclone.existingSecret=sentinel-backup-rclone \
    --set backup.rclone.remote=offsite:sentinel-backups
  ```
  The dump then runs as an init container and `rclone copy /backups <remote>` as the main one, with `backup.rclone.extraArgs` appended (`-v` lists each file). `copy` adds what the remote lacks and deletes nothing, so the remote keeps every backup: expire old ones with the storage's lifecycle rules. rclone works on a copy of the config in `/tmp`, since some remotes rewrite it (a refreshed OAuth token); a token refreshed that way is not saved back to the Secret.
- **Before upgrades.** `backup.preUpgrade.enabled` adds a pre-upgrade hook Job (weight -20, before the schema Job and its ConfigMap) that runs the same backup and upload. A failing backup fails the upgrade. It is skipped until the claim exists (the install, and the upgrade that turns backups on: its pod would wait for a claim Helm only creates after the hooks), and Helm waits for it only as long as `--timeout`. With rclone on, an unreachable remote fails the hook too, and so the upgrade; each retry (`backup.backoffLimit`) dumps again first, so a failing upload leaves an extra local backup per attempt. Turn rclone or the hook off for an upgrade that must go ahead while the remote is down.
- **Noticing failures.** A failed run leaves its Job and pods (the last three, `failedJobsHistoryLimit`) for `kubectl logs`; `kubectl -n <namespace> get cronjob <fullname>-backup` shows the last schedule, and `.status.lastSuccessfulTime` the last success, which is what to alert on (with the Prometheus Operator and kube-state-metrics, `kube_cronjob_status_last_successful_time`). Nothing else reports a failure. Retention deletes by age only, and only after a successful run, so a full claim makes every run fail and stays full: delete old directories by hand (from a pod that mounts the claim read-write, since PostgreSQL's mount is read-only) or grow the claim, and size it for `retentionDays` dumps.
- **Run one now** and look at the result. `make backup-now` (with `RELEASE` and `NAMESPACE`, both `sentinel` by default) creates the Job, waits for it and prints its log; by hand:
  ```bash
  kubectl -n <namespace> create job --from=cronjob/<fullname>-backup backup-$(date +%s)
  kubectl -n <namespace> logs -f job/backup-<that number>        # -c dump with rclone on
  kubectl -n <namespace> exec <fullname>-postgresql-0 -- ls -l /backups
  ```
- **Restore drill** (try it now and then): restore into a scratch database and compare row counts.
  ```bash
  kubectl -n <namespace> exec -it <fullname>-postgresql-0 -- sh
  ts=<a directory under /backups>
  createdb -U postgres -T template0 sentinel_restore
  pg_restore -U postgres --no-owner --exit-on-error -d sentinel_restore /backups/$ts/sentinel.dump
  psql -U postgres -d sentinel_restore -c 'SELECT count(*) FROM sentinel.entities'   # and the same in sentinel
  dropdb -U postgres sentinel_restore
  ```
- **Restoring for real.** Stop the writers and the backups, then, in the PostgreSQL pod, recreate the database and restore it, keeping its owners (the roles exist, since the chart's initdb created them):
  ```bash
  kubectl -n <namespace> scale deploy -l app.kubernetes.io/instance=<release> --replicas=0
  kubectl -n <namespace> patch cronjob <fullname>-backup --type merge -p '{"spec":{"suspend":true}}'
  # in the PostgreSQL pod:
  psql -U postgres -c 'DROP DATABASE sentinel WITH (FORCE)' -c 'CREATE DATABASE sentinel OWNER sentinel TEMPLATE template0'
  pg_restore -U postgres --exit-on-error -d sentinel /backups/$ts/sentinel.dump
  ```
  Keycloak's database the same way with `keycloak` as the owner. Then `helm upgrade` with the same values, which puts the replica counts back and runs the schema Job, and resume the CronJob (`"suspend":false`). On a new cluster whose Secret has new passwords, either restore the old Secret first (it is kept on `helm uninstall`; keep a copy elsewhere) or run `globals.sql` (`psql -U postgres -f /backups/$ts/globals.sql`; the roles that exist already report errors, and their passwords become the old ones, so put those back into the Secret).

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
- **Realm**: `sentinel` (imported from `config/keycloak/sentinel-realm.json`; the Helm chart imports a production rendering of it, see Keycloak, Ingress and TLS above)
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
