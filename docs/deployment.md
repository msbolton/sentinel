# Deploying to production

This runbook takes one Linux machine to a running Sentinel at `https://<host>`. You get real logins, a Let's Encrypt certificate, NetworkPolicies and nightly database backups. It follows a single path:

- k3s on one node;
- the Helm chart in `k8s/charts/sentinel`, with the values in `k8s/overlays/prod/values.yaml`;
- images from your private registry.

`make images-push` builds and pushes the images, and `make deploy` installs or upgrades the release. Each step links to `docs/infrastructure.md`, which describes every part of the chart in detail. A [local variant on k3d](#local-cluster-on-k3d) at the end runs the same steps on a laptop.

The local path is what has been run end to end. Several parts of the production path have not yet run on a real node; [Open risks](#open-risks-not-yet-exercised-on-a-real-node) lists them with what to check for each, so check them on the first install.

## What you need

- **The node.** One amd64 Linux machine with at least 4 vCPU, 16 GB of memory and a 150 GB SSD, and a public IPv4 address.
  - The prod values request about 3.2 CPU and 7.2 GiB. The rest is for k3s, Traefik, cert-manager, the hook Jobs and rolling updates.
  - The prod values' claims come to 75 GiB (PostgreSQL 30, Kafka 20, OpenSearch 10, backups 15). With the OS, k3s and the images (about 10 GB), that sits near the 85% line on a 100 GB disk, which is the bare minimum: on one, lower the claims in the site file and watch Kafka, whose topics are capped by time, not size.
  - Keep the disk below 85% full (see [Disk](#disk)).
- **A DNS name** for it, such as `sentinel.example.com`, with an A record you can set.
- **A private OCI registry** that the node can pull from (Harbor, GHCR, ECR, `registry:2` behind TLS), and an account on it that can push.
- **An SMTP relay** for registration e-mails: host, port, user and password.
- **Off-node storage for the backups**, such as an S3 bucket, B2 or SFTP: anything `rclone` can write to.
- **A workstation** with this repository checked out, plus:
  - `git`, `make`, `kubectl`, `jq` and `openssl`;
  - Helm 3.8 or later (the commands here are tested with Helm 4);
  - Docker with `buildx`. On Apple silicon, the amd64 builds run under emulation, so they are slow.

Every command below runs on the workstation, from the repository's root, unless it says "on the node".

**Keep site files out of the checkout.** The site values file, `rclone.conf` and the Secret exports below live in `~/sentinel/` (`mkdir -m 700 ~/sentinel`), never in the repository. `.gitignore` lists their names as a backstop, but a file outside the checkout cannot be committed at all.

## 1. Prepare the node

**k3s.** Install it with secrets encrypted at rest and a kubeconfig only root can read. Pin the version; v1.35.5+k3s1 is the tested one. `tls-san` adds the address you will reach the API server at to its certificate:

```bash
# on the node
sudo mkdir -p /etc/rancher/k3s
sudo tee /etc/rancher/k3s/config.yaml >/dev/null <<'EOF'
write-kubeconfig-mode: "0600"
secrets-encryption: true
tls-san:
  - 203.0.113.10            # the node's public IP, or a DNS name for it
EOF
curl -sfL https://get.k3s.io | sudo INSTALL_K3S_VERSION=v1.35.5+k3s1 sh -
sudo k3s kubectl get nodes
```

k3s puts the addresses of the node's own interfaces in that certificate. On a cloud VM behind 1:1 NAT (AWS, GCP, Azure), the public IP is not on an interface, so without `tls-san` kubectl from the workstation fails with `x509: certificate is valid for ..., not 203.0.113.10`. Providers that put the public IP on the interface (most bare-metal and many VPS hosts) work either way. To add a name later, edit the file and `sudo systemctl restart k3s`.

k3s brings Traefik as the ingress controller, flannel, its NetworkPolicy controller and the `local-path` storage class. The chart relies on all four, so turn none of them off. Volumes live under `/var/lib/rancher/k3s/storage`, so put `/var/lib/rancher` on the SSD.

**kubectl from the workstation.** The kubeconfig is readable by root only, so copy it through `sudo`, then point it at the address in `tls-san`:

```bash
mkdir -p ~/.kube
ssh <admin>@203.0.113.10 sudo cat /etc/rancher/k3s/k3s.yaml > ~/.kube/sentinel.yaml
chmod 600 ~/.kube/sentinel.yaml
sed -i.bak 's#https://127.0.0.1:6443#https://203.0.113.10:6443#' ~/.kube/sentinel.yaml && rm ~/.kube/sentinel.yaml.bak
export KUBECONFIG=~/.kube/sentinel.yaml
kubectl get nodes
```

The file holds the cluster's admin credentials, so keep it as private as an SSH key.

**Kernel settings.**
- `vm.max_map_count` is what OpenSearch expects. Single-node discovery only warns without it.
- `net.core.rmem_max` lets UDP listener connections get the receive buffer they ask for (`docs/feeds.md`).

```bash
# on the node
sudo tee /etc/sysctl.d/90-sentinel.conf >/dev/null <<'EOF'
vm.max_map_count = 262144
net.core.rmem_max = 8388608
EOF
sudo sysctl --system
```

**A storage class that keeps data.** k3s's `local-path` class deletes a volume's directory when its claim is deleted. The prod values use `local-path-retain` instead (`global.storageClass`), a copy with `reclaimPolicy: Retain`. It must exist before the first install, because a StatefulSet's storage class cannot change later:

```bash
kubectl apply -f k8s/cluster/local-path-retain.yaml
```

**The ingest listener's node.** The listener takes the TCP, UDP and gRPC connections that external systems send to. It runs on the host network of the node labelled for it, and the prod values turn it on:

```bash
kubectl label node "$(kubectl get nodes -o jsonpath='{.items[0].metadata.name}')" sentinel.io/ingest-listener=true
```

**NetworkPolicies and the listener.** The listener's connections to PostgreSQL and Kafka come from the node's `cni0` address. The prod values allow `10.42.0.1/32` (`networkPolicy.hostNetworkCIDRs`), which is that address on the first k3s node. Check it before the install. If it differs, put the address in your site file (step 5):

```bash
# on the node
ip -4 addr show cni0      # inet 10.42.0.1/24: matches; the interface appears once the first pod runs
```

**Firewall.** Open only what the stack needs, at the cloud security group and on the host:

| Port | From | For |
|------|------|-----|
| 22/tcp | your admin addresses | SSH |
| 6443/tcp | your admin addresses | `kubectl` and `helm` |
| 80/tcp, 443/tcp | anywhere | the site. Let's Encrypt validates from addresses it does not publish, so 80 cannot be narrowed |
| the ports your listener connections use (tcp and udp) | the senders' networks | ingest listener (`docs/infrastructure.md`, Ingest Listener) |
| everything else | nobody | Keep these closed: the listener's management port 4010, the kubelet (10250) and the NodePort range (30000-32767). kube-proxy forwards NodePorts before some host firewalls' input rules see them, so close them at the security group as well |

With `ufw` on the node, k3s needs the pod and service networks allowed as well:

```bash
# on the node
sudo ufw default deny incoming
sudo ufw allow from <admin cidr> to any port 22,6443 proto tcp
sudo ufw allow 80,443/tcp
sudo ufw allow from 10.42.0.0/16 to any    # pods
sudo ufw allow from 10.43.0.0/16 to any    # services
sudo ufw allow from <sender cidr> to any port 7000:7099 proto tcp   # your listener range, likewise udp
sudo ufw enable
```

**The namespace and its Pod Security level.**
- The listener uses the host network, which only the `privileged` level allows, so the namespace enforces `privileged`.
- Every other pod meets `restricted`, so warn and audit at that level: a pod that drifts from it shows up as a warning on every deploy.
- The listener itself always warns: `would violate PodSecurity "restricted:latest": host namespaces (hostNetwork=true)`.

```bash
kubectl create namespace sentinel
kubectl label namespace sentinel \
  pod-security.kubernetes.io/enforce=privileged \
  pod-security.kubernetes.io/warn=restricted \
  pod-security.kubernetes.io/audit=restricted
```

## 2. TLS

**DNS.** Point an A record for your host at the node's public address, and wait until `dig +short sentinel.example.com` returns it. Ports 80 and 443 must reach the node from the internet.

**cert-manager** (pinned; the chart's issuers are tested with this version):

```bash
helm repo add jetstack https://charts.jetstack.io --force-update
helm upgrade --install cert-manager jetstack/cert-manager --version v1.21.2 \
  -n cert-manager --create-namespace --set crds.enabled=true --wait
```

**The issuers and Traefik.**
- `letsencrypt-issuers.yaml` holds the `letsencrypt-staging` and `letsencrypt-prod` ClusterIssuers. Let's Encrypt sends expiry notices to the e-mail address in it, so put yours in.
- `traefik-config.yaml` redirects HTTP to HTTPS with a 308. It also keeps the client's address for the gateway's rate limiter (`externalTrafficPolicy: Local`).

```bash
sed 's/admin@example.com/ops@example.com/' k8s/cluster/letsencrypt-issuers.yaml | kubectl apply -f -
kubectl apply -f k8s/cluster/traefik-config.yaml
kubectl get clusterissuer                     # both READY True
curl -sI http://sentinel.example.com/ | head -1      # HTTP/1.1 308 Permanent Redirect, once Traefik has restarted
```

`docs/infrastructure.md` (Keycloak, Ingress and TLS) explains each manifest, and the controller settings for a cluster that does not run k3s's Traefik.

## 3. Images

**Build and push.** Log in to the registry, then build the nine images for amd64 and push them as `<registry>/sentinel/<name>:<tag>`. The tag defaults to the short hash of the checked-out commit. Commit first: uncommitted changes are built, but the tag does not name them.

```bash
docker login registry.example.com
make images-push REGISTRY=registry.example.com
```

`make deploy` derives the same tag from the same checkout, so run both from one commit, or pass `TAG=<tag>` to both.

**Pull credentials.** The node needs a read-only pull token for the registry. Choose one of these:

- **An image pull secret** in the namespace, named in your site file (`global.imagePullSecrets`). It is added to every pod of the chart:
  ```bash
  kubectl -n sentinel create secret docker-registry registry-credentials \
    --docker-server=registry.example.com --docker-username=<user> --docker-password=<pull token>
  ```
- **k3s's own registry configuration**, on the node. It covers every namespace, and the site file then names no pull secret:
  ```bash
  # on the node
  sudo tee /etc/rancher/k3s/registries.yaml >/dev/null <<'EOF'
  configs:
    "registry.example.com":
      auth:
        username: <user>
        password: <pull token>
  EOF
  sudo chmod 600 /etc/rancher/k3s/registries.yaml
  sudo systemctl restart k3s
  ```

## 4. The Secrets you provide

The chart generates the database, Keycloak and service-client passwords itself (step 7 exports them). You supply the rest as Secrets in the namespace, and the site file names them. That way no secret goes through a values file or Helm's release history.

```bash
# The token the gateway presents to the ingest service (required). Optional
# keys opensky-client-id and opensky-client-secret for the OpenSky feed.
kubectl -n sentinel create secret generic sentinel-ingest-token \
  --from-literal=service-token="$(openssl rand -hex 32)"

# The SMTP relay's password.
read -rsp 'SMTP password: ' pw; echo
kubectl -n sentinel create secret generic sentinel-mail --from-literal=password="$pw"; unset pw

# The map keys the web client reads (either may be left out; its layer is then off).
kubectl -n sentinel create secret generic sentinel-map-keys \
  --from-literal=google-maps-api-key=<key> --from-literal=cesium-ion-access-token=<token>

# Off-node backup copies: an rclone.conf with one remote, written outside the checkout
# (rclone config --config ~/sentinel/rclone.conf).
kubectl -n sentinel create secret generic sentinel-backup-rclone --from-file=rclone.conf=$HOME/sentinel/rclone.conf
```

**Map keys.** `apps/client/src/environments/environment.ts` has a Google Maps key and a Cesium ion token committed to the repository. Do not use them in production:
- create keys of your own;
- restrict each to your site: in Google Cloud, an HTTP-referrer restriction on `https://sentinel.example.com/*`; in Cesium ion, the token's allowed URLs;
- rotate or restrict the committed ones too.

## 5. The site file

Your deployment's own settings go in a values file kept outside the repository, `~/sentinel/sentinel-site.yaml`. `make deploy` layers it over the prod values. It names the Secrets above and holds no secret itself:

```yaml
# ~/sentinel/sentinel-site.yaml
global:
  host: sentinel.example.com
  imagePullSecrets: [{name: registry-credentials}]   # only with an image pull secret (step 3)
ingress:
  clusterIssuer: letsencrypt-staging                 # letsencrypt-prod once staging works (step 6)
ingestService:
  existingSecret: sentinel-ingest-token
mail:
  host: smtp.example.com
  port: 587
  tls: starttls                                      # tls for implicit TLS (465)
  username: sentinel@example.com
  from: "SENTINEL <sentinel@example.com>"
  existingSecret: sentinel-mail
keycloak:
  bootstrapUser:
    email: ops@example.com
web:
  mapKeys:
    existingSecret: sentinel-map-keys
backup:
  timeZone: Etc/UTC
  rclone:
    enabled: true
    existingSecret: sentinel-backup-rclone
    remote: offsite:sentinel-backups                 # <remote name in rclone.conf>:<path>
# networkPolicy:
#   hostNetworkCIDRs: ["10.42.0.1/32"]               # only if cni0 differs (step 1)
# ingestListener:
#   host: ingest.example.com                         # the name senders use; default: the node's IP
```

**Get these right before the first install.** Keycloak reads the following when it imports its realms, which it does on its first start only:
- `global.host`;
- `mail`;
- `keycloak.bootstrapUser`;
- `keycloak.adminUrl`.

After that, change them in the admin console as well (`docs/infrastructure.md`, The realms are imported on the first start only).

**Mail must be configured.** With no `mail.host`, registration e-mails fail: the gateway's rejection notices, and Keycloak's verification and password-reset messages. An empty host does not turn mail off; it just has nowhere to send. The other `mail` settings are in `docs/infrastructure.md`, Registration mail.

## 6. First install

```bash
make deploy REGISTRY=registry.example.com VALUES=$HOME/sentinel/sentinel-site.yaml
```

This runs:

```
helm upgrade --install sentinel k8s/charts/sentinel -n sentinel --create-namespace \
  -f k8s/overlays/prod/values.yaml -f $HOME/sentinel/sentinel-site.yaml \
  --set global.imageRegistry=registry.example.com --set global.imageTag=<commit> --timeout 20m
```

**What to expect.**

Helm creates the release, then runs its post-install hooks:
- the Kafka topics Job;
- the schema Job, which applies `db/schema`.

Until both are done, the services and the ingest pods crash-loop on the missing topics and tables; that is expected. Keycloak takes a minute or two on its first start, while it builds and imports the realms.

Helm waits for the hooks only, up to `--timeout 20m`, and returns once they succeed. On k3d, with the images already on the node, `make deploy` returned after about a minute and every pod was ready within two. Pulling the images from your registry and the internet adds to that.

**Do not add `--wait` or `--rollback-on-failure` (`--atomic` in Helm 3) to a first install.** With `--wait`, Helm waits for every pod to be ready before it runs the post-install hooks, but the services cannot be ready until those hooks have created the topics and the schema: the install times out and is marked failed. `--rollback-on-failure` then also uninstalls it.

```bash
kubectl -n sentinel get pods -w        # until every pod is Running and 1/1 Ready
kubectl -n sentinel get jobs           # the hook Jobs are deleted when they succeed
```

If a hook fails, Helm marks the release failed and keeps the Job for its logs:
1. Read them with `kubectl -n sentinel logs job/sentinel-db-schema` (or `job/sentinel-kafka-topics`).
2. Fix the cause.
3. Run the same `make deploy` again. The upgrade runs the hooks again, as pre-upgrade hooks.

**The certificate, on staging first.** cert-manager requests it as soon as the Ingress exists. Staging certificates are not trusted by browsers, but staging's rate limits forgive mistakes.

```bash
kubectl -n sentinel get certificate                         # sentinel-tls READY True, within a minute or two
curl -skv https://sentinel.example.com/ 2>&1 | grep 'issuer:'   # ... (STAGING) ...
```

Then switch to production. In the site file, set `ingress.clusterIssuer: letsencrypt-prod`, and deploy again:

```bash
make deploy REGISTRY=registry.example.com VALUES=$HOME/sentinel/sentinel-site.yaml
curl -sI https://sentinel.example.com/ | head -1           # HTTP/2 200, no -k
```

cert-manager reissues the certificate when the issuer changes. If it still shows the staging issuer after a few minutes, delete the Secret and it is issued again: `kubectl -n sentinel delete secret sentinel-tls`.

## 7. Check it and hand it over

**Sign in.** The bootstrap user `sentinel-admin` has the roles `sentinel-admin`, `sentinel-analyst` and `classification-ts`. Its password is in the Secret the chart generated:

```bash
kubectl -n sentinel get secret sentinel-keycloak -o jsonpath='{.data.sentinel-admin-password}' | base64 -d; echo
```

Sign in at `https://sentinel.example.com`. Change the password in the account console (`https://sentinel.example.com/auth/realms/sentinel/account`). The Secret holds the first-start value only, and stops matching once you change it.

**Smoke test.** `scripts/smoke.sh` calls every gateway route the client uses. When `node` is installed, it also opens the WebSocket:

```bash
pw=$(kubectl -n sentinel get secret sentinel-keycloak -o jsonpath='{.data.sentinel-admin-password}' | base64 -d)
export SENTINEL_TOKEN=$(curl -s https://sentinel.example.com/auth/realms/sentinel/protocol/openid-connect/token \
  -d grant_type=password -d client_id=sentinel-web -d username=sentinel-admin \
  --data-urlencode "password=$pw" | jq -r .access_token); unset pw
scripts/smoke.sh https://sentinel.example.com https://sentinel.example.com     # SMOKE PASSED
```

It skips the viewer check unless you also set `SENTINEL_VIEWER_TOKEN`, and the observations check while there are no entities. It deletes what it creates.

**The Keycloak admin console** is not on the Ingress. Forward it, keeping local port 8080 free, then open `http://localhost:8080/auth/admin/`:

```bash
kubectl -n sentinel port-forward svc/sentinel-keycloak 8080
kubectl -n sentinel get secret sentinel-keycloak -o jsonpath='{.data.admin-password}' | base64 -d; echo
```

Sign in as `admin`. Keycloak 26 marks this bootstrap admin as temporary, so replace it:
1. Create a permanent admin in the master realm: Users, then Add user. Set a password under Credentials, and give it the `admin` role under Role mapping.
2. Sign in as the new admin.
3. Delete `admin`.

**The listener** answers on the node and has registered. The database sees its connections from the address you allowed:

```bash
# on the node
curl -s http://127.0.0.1:4010/ready        # {"status":"ready",...}
# from the workstation
kubectl -n sentinel exec sentinel-postgresql-0 -- psql -U postgres -d sentinel -c \
  'SELECT replica_id, listener, listener_host FROM sentinel.ingest_replicas'   # sentinel-ingest-listener | t | <node IP>
kubectl -n sentinel exec sentinel-postgresql-0 -- psql -U postgres -Atc \
  "SELECT client_addr, count(*) FROM pg_stat_activity WHERE client_addr IS NOT NULL GROUP BY 1"
```

The pods connect from their own addresses (`10.42.0.x`). The listener connects from the node's: on one k3s node that is `10.42.0.1`, which must be in `networkPolicy.hostNetworkCIDRs`.

**Export the generated Secrets now** (see [Keep the Secrets off the node](#keep-the-secrets-off-the-node)), and take the first backup: `make backup-now`.

## Day two

### Upgrades

```bash
git pull
make images-push REGISTRY=registry.example.com
make deploy REGISTRY=registry.example.com VALUES=$HOME/sentinel/sentinel-site.yaml HELM_ARGS=--wait
```

**Hooks.** The upgrade runs its pre-upgrade hooks before any Deployment or StatefulSet changes:
1. First, a backup (`backup.preUpgrade`, on in the prod values). It writes to the claim, and to the rclone remote when that is on.
2. Then the schema and topics Jobs.

A failing hook stops the upgrade there, before the workloads change.

**Waiting and rolling back.**
- `HELM_ARGS=--wait` makes Helm wait for the new pods, up to the 20-minute timeout.
- `HELM_ARGS="--wait --rollback-on-failure"` (`--atomic` in Helm 3) also rolls the Kubernetes objects back if the rollout fails.
- A rollback does not undo the schema. The files only add, and every one is idempotent. The backup taken just before is there if you need it (`docs/infrastructure.md`, Restoring for real).

**Escape hatches, one upgrade at a time.**
- `--set kafka.topics.enabled=false` when Kafka itself is what the upgrade repairs. The topics hook would otherwise wait for the broker until its 10-minute deadline.
- `--set dbSchema.enabled=false` when you apply the schema yourself.
- `--set backup.preUpgrade.enabled=false` when the rclone remote is down.

Pass them through `HELM_ARGS`.

**Helm 4** applies changes server-side. A field you changed by hand with `kubectl edit` or `kubectl patch` that the chart also sets makes the upgrade fail with a conflict. Put the change in the site file instead, or pass `HELM_ARGS=--force-conflicts` to take the field back.

**Keycloak** applies realm changes on its first start only: after an upgrade, make them in the admin console.

**Kubernetes itself.** Before upgrading k3s, take a backup (`make backup-now`), then follow the k3s upgrade instructions for one minor version at a time.

### Keep the Secrets off the node

The chart generates three Secrets, and their only copy is in the cluster:
- `sentinel-postgresql` holds the database passwords;
- `sentinel-keycloak` holds the bootstrap passwords;
- `sentinel-keycloak-clients` holds the service client secret.

The database and Keycloak's realm hold the matching passwords. A new cluster restored from backups needs these Secrets, or the services cannot log in.

Export them, with the ones you created, to an encrypted file kept off the node and outside the checkout, then into a password manager or a vault. Repeat the export after every change. Add `registry-credentials` to the list when you use an image pull secret (step 3), and leave out any Secret you did not create (kubectl reports a missing one and exports the rest):

```bash
kubectl -n sentinel get secret sentinel-postgresql sentinel-keycloak sentinel-keycloak-clients \
  sentinel-ingest-token sentinel-mail sentinel-map-keys sentinel-backup-rclone registry-credentials -o yaml \
  | grep -vE '^ +(resourceVersion|uid|creationTimestamp):' \
  | gpg --symmetric --cipher-algo AES256 -o ~/sentinel/sentinel-secrets-$(date +%F).yaml.gpg
```

To restore them on a new cluster:
1. Create the namespace (step 1).
2. Run `gpg -d ~/sentinel/sentinel-secrets-<date>.yaml.gpg | kubectl create -f -`.
3. Run `make deploy`.

The chart adopts its three Secrets, since they still carry the release's annotations, and keeps their values.

### Backups and restores

The CronJob `sentinel-backup` runs nightly at 02:30, in `backup.timeZone`:
- it writes `pg_dump`s of the `sentinel` and `keycloak` databases, plus the roles, to the claim `sentinel-backups`;
- it keeps them for 7 days;
- with rclone on, it copies each run off the node.

`docs/infrastructure.md` (NetworkPolicies and backups) has the details: retention, failure alerts, the restore drill and the full restore.

```bash
make backup-now                                         # runs one now, waits, prints its log
kubectl -n sentinel get cronjob sentinel-backup         # LAST SCHEDULE; alert on .status.lastSuccessfulTime
kubectl -n sentinel exec sentinel-postgresql-0 -- ls -l /backups
```

**Off-node copies.** The claim is on the node's own disk. It protects against a bad upgrade or a dropped table, not against losing the node: keep rclone on.

**Retention on the remote.** `rclone copy` never deletes, so expire old copies with the storage's lifecycle rules.

**Drill.** Run the restore drill in `docs/infrastructure.md` every month or so. It restores into a scratch database and compares row counts.

### What cannot be recovered

Only PostgreSQL is backed up. It holds the entities, tracks, alerts, feeds, connections, users and Keycloak.

- **Kafka.**
  - Lost: events in flight, and the history still in the topics, up to their retention (3 days for observations, 7 for entity events, 30 for alerts and dead letters).
  - The data they produced is in PostgreSQL already.
  - After a loss, the next `make deploy` recreates the topics (the topics hook), and the services start reading them afresh.
- **OpenSearch**, the search index.
  - It is rebuilt only from new entity events, so after a loss, an entity is searchable again once it changes.
  - If Kafka survived, you can replay the entity events Kafka still holds (7 days) into a fresh index by rewinding search-service's consumer group:
    ```bash
    kubectl -n sentinel scale deploy sentinel-search-service --replicas=0
    kubectl -n sentinel exec sentinel-kafka-0 -- /opt/kafka/bin/kafka-consumer-groups.sh \
      --bootstrap-server localhost:9092 --group sentinel-search-service \
      --topic entities.events --reset-offsets --to-earliest --execute
    kubectl -n sentinel scale deploy sentinel-search-service --replicas=1
    ```
    search-service creates the index again when it starts, and fills it as it reads.
- **Files on the node** outside the claims, such as the k3s state. Rebuild those by following this runbook again.

### Coming from Docker Compose

Nothing moves over by itself: the chart starts with empty volumes and its own passwords.

- **Sentinel's data.** `pg_dump --format=custom` the Compose database, then restore it into the chart's, as in `docs/infrastructure.md` (Restoring for real).
- **Keycloak.** The chart imports a fresh production realm with only `sentinel-admin`. Users registered under Compose must register again.
- **A Compose Keycloak 24 volume** that Keycloak 26 has started on is migrated in place, one way, and keeps its old realm: the realm import is skipped for a realm that exists (`docs/infrastructure.md`, Keycloak Configuration).

### Disk

OpenSearch stops accepting writes once the node's disk passes its high watermark (90%). Search then fails and search-service crash-loops. Under the same pressure, k3s deletes unused images.

- The claims' sizes are nominal: `local-path` does not enforce them, and every volume shares the node's disk.
- Watch it with `df -h /var/lib/rancher` on the node. Keep it below 85%.
- OpenSearch lifts the block by itself once space is freed.

### Terrain

Radar and camera detections are placed with Copernicus GLO-30 terrain heights (`docs/feeds.md`, Terrain). The ingest pods download 1 degree tiles on first use and keep them in a cache folder, on by default (`ingestService.terrain.enabled`).

- **Network.** The pods need outbound HTTPS to `https://copernicus-dem-30m.s3.amazonaws.com` (`ingestService.terrain.sourceUrl`). The chart restricts ingress only, so nothing to open on the cluster; a firewall or proxy in front of the node must allow it. Where it cannot be reached, detections that need terrain become bearings (`terrain_unavailable`) and `sentinel_ingest_terrain_failures_total` rises. Set `ingestService.terrain.enabled: false` to run without it.
- **Disk.** Each tile is 15 to 50 MB, a few GB for a region. By default the cache is an `emptyDir` of 4Gi per ingest pod, on the node's disk beside everything else (see Disk above); it is lost when the pod is replaced and fetched again as detections arrive. `cacheMaxMB` (3072) makes the cache delete its oldest tiles before `cacheSizeLimit` (4Gi) would evict the pod. To keep it across restarts, create a PersistentVolumeClaim writable by group 10001 and name it in `ingestService.terrain.existingClaim`; the listener and the ingest pods then share it only if the volume is ReadWriteMany.
- **Memory.** A decoded tile is up to about 52 MB (3600 × 3600 heights below 50° of latitude). Each pod keeps `memTiles` of them (4, about 210 MB), and a download holds the file and its raster, about 100 MB, while it decodes; at most two download at once. That is why the ingest and listener pods' memory limit is 1Gi, with `GOMEMLIMIT` at 80% of it so the Go heap collects before the kubelet kills the pod. Sites spread over more than four tiles re-decode tiles as detections move between them (0.4 to 1 s each, within the 2 s wait), and some detections become `terrain_unavailable` bearings: raise `ingestService.terrain.memTiles` by the number of tiles the sites span, and the memory limits (`ingestService.resources`, `ingestListener.resources`) by about 60 MB per extra tile. With terrain off the old 512Mi is enough.
- **Preload.** Fill the cache for a site before its sensors report, so the first detections are not bearings. The command refuses a box whose tiles (50 MB each) exceed `--cache-max-mb`, so pass the chart's cap:

  ```bash
  kubectl exec -n sentinel deploy/sentinel-ingest-service -- /app/terrain-preload \
    --cache-dir /var/cache/sentinel/terrain --cache-max-mb 3072 --bbox 54.5,-3.3,54.7,-3.0
  ```

  `--bbox` is `south,west,north,east`. Each `emptyDir` is its own pod's: run it in every ingest pod, and in `deploy/sentinel-ingest-listener` (the gRPC connections' pod), which does the placing for the connections it runs. With a shared claim once is enough.
- **Attribution.** Copernicus requires this notice wherever the data, or anything made from it, is shown: "Produced using Copernicus WorldDEM-30 © DLR e.V. 2010-2014 and © Airbus Defence and Space GmbH 2014-2018 provided under COPERNICUS by the European Union and ESA; all rights reserved."

### Keep tokens out of the logs

Two kinds of credential travel in query strings:
- the WebSocket's access token (`/ws/entities?token=...`);
- an export link's API key (`?api_key=...`).

k3s's Traefik writes no access log by default; keep it that way, or drop the request path from it. The path field includes the query, and only the JSON format honours field filters. In `k8s/cluster/traefik-config.yaml`, add under `valuesContent`:

```yaml
    logs:
      access:
        enabled: true
        format: json
        fields:
          general:
            names:
              RequestPath: drop
```

With that, a request's query no longer appears in the log. Request headers, including `Authorization`, are dropped by default. The same applies to any proxy or load balancer in front of the node. The gateway does not log query strings.

## Open risks: not yet exercised on a real node

Everything else in this runbook ran on k3d. These parts did not, because they need a real node, a public name or a real registry. Check each on the first install.

| Part | The risk | Check |
|------|----------|-------|
| k3s `secrets-encryption: true` | It is a documented k3s option but untested here; a typo in `config.yaml` stops k3s from starting | `sudo k3s secrets-encrypt status` shows `Encryption Status: Enabled`; `journalctl -u k3s` if k3s does not start |
| `tls-san` and the kubeconfig | The certificate lacks the address you connect to | `kubectl get nodes` from the workstation, without `--insecure-skip-tls-verify` |
| ufw beside k3s | k3s's service load balancer (servicelb) publishes 80 and 443 through iptables rules of its own, and flannel forwards pod traffic: ufw may block them, or fail to block NodePorts | From outside: `curl -sI http://<host>/` gives 308 and `https://` 200; `nc -vz <node> 30000-30010` and `nc -vz <node> 4010` are refused. On the node: pods reach each other (`kubectl -n sentinel get pods` all Ready after `ufw enable`) |
| `/etc/rancher/k3s/registries.yaml` | Wrong host key or credentials: pulls fail | `kubectl -n sentinel get pods` shows no `ImagePullBackOff`; `sudo k3s crictl pull registry.example.com/sentinel/web:<tag>` on the node |
| `make images-push` to a real registry | Push permissions, registry TLS, and slow amd64 builds under emulation on Apple silicon | Each of the nine pushes ends without error; `docker buildx imagetools inspect registry.example.com/sentinel/web:<tag>` lists `linux/amd64` |
| Client IPs through servicelb | `k8s/cluster/traefik-config.yaml` sets `externalTrafficPolicy: Local` so the gateway's registration rate limiter (keyed by the leftmost `X-Forwarded-For`) sees real client IPs. Whether k3s's servicelb keeps the source address on a real node is unverified; on k3d the gateway sees the Docker bridge address, so that run says nothing | From a machine outside the node: `curl -s -o /dev/null https://<host>/api/v1/entities` (401), then `kubectl -n sentinel logs deploy/<fullname>-api-gateway --tail=20 \| grep ACCESS_DENIED`: `"ip"` (the same address the limiter keys on) must be that machine's public IP, not the node's or a `10.42.x.x` address. (Traefik writes no access log by default; if you turned it on, its `ClientHost` shows the same.) If it is not, every visitor shares one rate-limit key: 5 registrations per 15 minutes for the whole site |
| ACME HTTP-01 behind the redirect and the NetworkPolicies | Let's Encrypt is expected to follow the 308 to HTTPS and ignore Traefik's default certificate, and the `sentinel-acme-solver` policy is expected to admit Traefik to the solver; neither has been seen working | On `letsencrypt-staging` first: `kubectl -n sentinel get certificate` READY True within minutes; otherwise [Certificates do not arrive](#certificates-do-not-arrive) |
| Sizing | The requests (3.2 CPU, 7.2 GiB) come from the values, not from a loaded system; busy feeds can need more CPU for ingest and entity-service, and more Kafka disk | `kubectl top pods -n sentinel` and `kubectl top node` after a day of real feeds; `df -h /var/lib/rancher`; raise requests or claims in the site file |
| Rollout headroom | On 4 vCPU the requests leave about 0.8 CPU. An upgrade surges one extra pod per Deployment, so some new pods may wait `Pending` until older ones finish | `make deploy ... HELM_ARGS=--wait` completes within the 20 minutes; `kubectl -n sentinel get events --field-selector reason=FailedScheduling` during an upgrade. Persistent `Insufficient cpu`: a bigger node, or lower requests |

## Troubleshooting

### Certificates do not arrive

`kubectl -n sentinel get certificate` stays `False`.

**Tested and untested.** The HTTP-01 challenge has been checked on paper against the redirect and the NetworkPolicies, but not yet against the real Let's Encrypt ([Open risks](#open-risks-not-yet-exercised-on-a-real-node)):
- the redirect: Let's Encrypt follows it to HTTPS and ignores the certificate it finds there;
- the NetworkPolicies: the chart's `sentinel-acme-solver` policy admits Traefik to the solver pod.

**Follow the chain down to the challenge:**

```bash
kubectl -n sentinel get certificate,certificaterequest,order,challenge
kubectl -n sentinel describe challenge          # the reason: DNS, connection refused, 404, timeout ...
kubectl -n cert-manager logs deploy/cert-manager --tail=100
curl -sIL http://sentinel.example.com/.well-known/acme-challenge/x   # 308 to https, then the solver's 404 while a challenge is pending
```

**Common causes:**
- the A record does not point at the node yet;
- port 80 is closed somewhere;
- `networkPolicy.ingressController` does not match your controller's pods;
- the issuer's `ingressClassName` is not your controller's class.

**If the redirect is the problem,** you can rule it out: remove the `redirections` block from `traefik-config.yaml`, apply it, and let the challenge pass. Then put the redirect back.

**Staging first.** Production's rate limits are strict (5 failed validations per hostname per hour), so debug on `letsencrypt-staging`.

### Pods

- **`Pending` with `didn't match Pod's node affinity/selector`** (the ingest listener): label the node (step 1).
- **`Pending` with `Insufficient cpu` or `memory`:** the node is smaller than the prod values expect. Lower the requests in the site file.
- **`ImagePullBackOff`:** the tag is not in the registry (run `make images-push` from the same commit, or pass `TAG`), or the pull credentials are missing (step 3).
- **search-service in `CrashLoopBackOff` with `index_create_block_exception`:** the disk is past OpenSearch's watermark ([Disk](#disk)).
- **The first sign-in after a PostgreSQL restart answers `unknown_error`:** Keycloak's connection pool still held the old connections. The next attempt works.

### Starting over

`helm uninstall sentinel -n sentinel` keeps the generated Secrets and the claims. With `local-path-retain`, deleting the claims or the namespace keeps the volumes' directories too.

To start from scratch, delete everything:
1. `kubectl delete namespace sentinel`.
2. The Released PersistentVolumes: `kubectl get pv`, then `kubectl delete pv <name>`.
3. On the node, their directories under `/var/lib/rancher/k3s/storage`.

## Local cluster on k3d

The same steps on a laptop, for trying a change before it reaches the node. k3d runs k3s in Docker.

- On Apple silicon its node is arm64, so the images are built natively and imported, not pulled.
- The certificate comes from a local CA.
- `k8s/overlays/local/values.yaml` layers over the prod values. It sets:
  - `host` to `sentinel.localhost`;
  - the local CA issuer;
  - small requests and one replica;
  - a multi-arch PostGIS image;
  - `pullPolicy: Never` for the imported images;
  - k3d's own `local-path` class instead of `local-path-retain`, so deleting the namespace deletes the volumes too and nothing piles up as Released. `k3d cluster delete sentinel` removes everything.

Stop Docker Compose (`make docker-down`) first, and anything else running in Docker: the stack needs about 5 GB of the Docker VM's memory.

**Disk.** Keep the Docker VM's disk below 85%. Above that, the k3d node deletes unused images, including the ones you imported, and OpenSearch stops at 90%. `docker builder prune -f` frees build cache.

```bash
# 1. The cluster, publishing 80 and 443 as themselves so the issuer and redirects match the browser's URL
k3d cluster create sentinel -p "80:80@loadbalancer" -p "443:443@loadbalancer"

# 2. cert-manager, the local CA and Traefik's redirect
helm repo add jetstack https://charts.jetstack.io --force-update
helm upgrade --install cert-manager jetstack/cert-manager --version v1.21.2 \
  -n cert-manager --create-namespace --set crds.enabled=true --wait
kubectl apply -f k8s/cluster/selfsigned-issuer.yaml -f k8s/cluster/traefik-config.yaml

# 3. The listener's node and the namespace
kubectl label node k3d-sentinel-server-0 sentinel.io/ingest-listener=true
kubectl create namespace sentinel
kubectl label namespace sentinel pod-security.kubernetes.io/enforce=privileged \
  pod-security.kubernetes.io/warn=restricted pod-security.kubernetes.io/audit=restricted

# 4. Images: built for this machine, one at a time, then copied into the node
make images-local
for i in api-gateway entity-service track-service search-service link-analysis-service \
         alert-service ingest-service analytics-service web; do
  k3d image import "sentinel/$i:$(git rev-parse --short HEAD)" -c sentinel
done

# 5. Install. The imported images are docker.io/sentinel/<name>:<tag>, hence REGISTRY=docker.io
make deploy REGISTRY=docker.io VALUES=k8s/overlays/local/values.yaml
```

**Using it.**
- Trust the local CA, or let curl use it:
  ```bash
  kubectl -n cert-manager get secret sentinel-local-ca -o jsonpath='{.data.ca\.crt}' | base64 -d > sentinel-local-ca.crt
  ```
  `curl --cacert sentinel-local-ca.crt https://sentinel.localhost/` then answers 200. For the smoke test, set `CURL_CA_BUNDLE` and `NODE_EXTRA_CA_CERTS` to the file and use the commands of step 7 with `https://sentinel.localhost`.
- The admin console works through the same port-forward.
- `make backup-now` runs a backup.

**Images after a change.** `make deploy` asks for every image under one tag, the new commit's. Either rebuild and import all nine, or rebuild only what changed and keep the others on the tag they were imported with:

```bash
make images-local IMAGES=api-gateway
k3d image import "sentinel/api-gateway:$(git rev-parse --short HEAD)" -c sentinel
make deploy REGISTRY=docker.io VALUES=k8s/overlays/local/values.yaml TAG=<the tag of the other images> \
  HELM_ARGS="--set apiGateway.image.tag=$(git rev-parse --short HEAD)"
```
