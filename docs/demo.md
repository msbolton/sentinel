# Public demo at sentinel.msbolton.dev

A visitor clicks the button on msbolton.dev and, a few seconds later, is inside a live Sentinel as a throwaway guest: real aircraft, vessels and satellites from the live feeds, simulated radar, EO/IR and ESM activity, and a dot for every visitor exploring right now. There is no sign-up and no login form.

The demo is an ordinary production install, by `docs/deployment.md` on one k3s node, with three additions:

- the overlay `k8s/overlays/demo/values.yaml`, layered over the prod one;
- `scripts/demo-bootstrap.sh`, which creates the demo's two gRPC connections and their token Secrets;
- the steps below, which replace or add to parts of the runbook.

How the gateway runs guests, visitor sites and the status endpoint is in `docs/api-gateway.md` (Public demo). The simulators are in `docs/sensor-sim.md`.

## What the overlay turns on

| Setting | Effect |
|---------|--------|
| `ingestService.env.FEEDS_BOOTSTRAP_ENABLED: opensky,aisstream,intelsky,celestrak` | The four live feeds are seeded enabled. This applies on the first start only, when the feeds table does not hold them yet (see [Adding the demo to an existing install](#adding-the-demo-to-an-existing-install)) |
| `sensorSim.enabled: true`, `sensorSim.tokenSecret: sentinel-demo-sim-token` | Three Deployments, `sentinel-sensor-sim-radar`, `-eoir` and `-esm`, stream to the `demo-sensors` connection on port 6201 of the node |
| `apiGateway.demo.enabled: true`, `apiGateway.demo.tokenSecret: sentinel-demo-visitors-token` | The gateway runs guest sessions (25 at most, 60 minutes each) and sends each visitor's site to the `demo-visitors` connection on port 6202 |
| `apiGateway.demo.geoip.enabled: true`, `apiGateway.demo.geoip.existingSecret: sentinel-geoip` | An initContainer downloads MaxMind GeoLite2 City at every gateway start, to place visitors who deny the browser's location |

The Secrets:

| Secret | Keys | Made by |
|--------|------|---------|
| `sentinel-demo-sim-token` | `token`: the ingest token of `demo-sensors` | `scripts/demo-bootstrap.sh` |
| `sentinel-demo-visitors-token` | `token`: the ingest token of `demo-visitors` | `scripts/demo-bootstrap.sh` |
| `sentinel-geoip` | `account-id`, `license-key` | you (owner checklist) |
| `sentinel-ingest-env` (any name; `ingestService.secretEnv.existingSecret`) | `AISSTREAM_API_KEY` | you |
| `sentinel-ingest-token` (`ingestService.existingSecret`) | `service-token`, `opensky-client-id`, `opensky-client-secret` | you (`docs/deployment.md`, step 4) |
| `sentinel-map-keys` (`web.mapKeys.existingSecret`) | `google-maps-api-key`, `cesium-ion-access-token` | you (`docs/deployment.md`, step 4) |

## Owner checklist

These need your accounts or a payment, so nothing here does them for you. Do them before the install.

1. **The server.** At Hetzner Cloud, a shared-vCPU **x86** plan with 16 GB of memory and at least 150 GB of disk: at the time of writing that is the CX42 (8 vCPU, 16 GB, 160 GB SSD), about €17 a month with its IPv4 address, in an EU location (Falkenstein, Nuremberg, Helsinki). Check the current price. Not an Arm (CAX) plan: `postgis/postgis` is amd64 only. Ubuntu 24.04, a public IPv4 address, your SSH key. The CX plans have been EU-only; a US location's 16 GB x86 plan costs more.
2. **DNS.** An A record `sentinel.msbolton.dev` to the server's IPv4 address. Add no AAAA record unless the node serves the site on IPv6 as well: Let's Encrypt prefers IPv6 when it is published, and the HTTP-01 challenge then fails.
3. **MaxMind.** A free GeoLite account at maxmind.com, and a licence key (Manage licence keys). Create the Secret once the namespace exists (`docs/deployment.md`, step 1). The values go through a file descriptor, so they never appear on a command line:

   ```bash
   read -rp 'MaxMind account id: ' id; read -rsp 'MaxMind licence key: ' key; echo
   kubectl -n sentinel create secret generic sentinel-geoip \
     --from-file=account-id=<(printf %s "$id") --from-file=license-key=<(printf %s "$key"); unset id key
   ```

   The GeoLite2 licence asks for attribution where the data is used: "This product includes GeoLite2 data created by MaxMind, available from https://www.maxmind.com".
4. **Map keys.** `/config.json` hands the Cesium ion token and the Google Maps key to every visitor, and every visitor's tiles bill to your accounts. Create new ones for the demo:
   - a Cesium ion token whose allowed URLs are `https://sentinel.msbolton.dev`;
   - a Google Maps key with an HTTP-referrer restriction on `https://sentinel.msbolton.dev/*`, restricted to the Maps JavaScript and Map Tiles APIs, with a budget alert on the billing account;
   - put both in `sentinel-map-keys` (`docs/deployment.md`, step 4).

   Then revoke, or restrict to `localhost`, the two keys committed in `apps/client/src/environments/environment.ts`: they are public in the repository.
5. **Feed keys.**
   - OpenSky: the API client's id and secret, as `opensky-client-id` and `opensky-client-secret` in `sentinel-ingest-token` (`docs/deployment.md`, step 4).
   - aisstream: the API key, in a Secret of its own that the site file names in `ingestService.secretEnv.existingSecret`. Every key of that Secret becomes an environment variable of the ingest pods, so it holds this key only:

     ```bash
     read -rsp 'aisstream API key: ' k; echo
     kubectl -n sentinel create secret generic sentinel-ingest-env --from-file=AISSTREAM_API_KEY=<(printf %s "$k"); unset k
     ```

   - **A second aisstream key for local development.** An aisstream account allows 3 connections at a time and the demo can hold 2. A dev stack running on the same key at the same time pushes the account over, and one side is cut off. Put the second key in your dev `.env`.

## Install

Follow `docs/deployment.md` with the changes below. They are listed in the order of the runbook.

**The firewall (step 1).** The demo's connections listen on the node's host network on 6201 and 6202, in plaintext gRPC with a bearer token. They are for the pods on the node only and must not be reachable from the internet. The pods reach them at the node's address, from the pod network, which a host firewall sees as ordinary input.

- The Hetzner Cloud Firewall lets in 22, 80 and 443 only, and nothing else. Use `kubectl` through an SSH tunnel instead of opening 6443 (below):

  ```bash
  hcloud firewall create --name sentinel-demo
  hcloud firewall add-rule sentinel-demo --direction in --protocol tcp --port 22 --source-ips <your address>/32
  hcloud firewall add-rule sentinel-demo --direction in --protocol tcp --port 80 --source-ips 0.0.0.0/0 --source-ips ::/0
  hcloud firewall add-rule sentinel-demo --direction in --protocol tcp --port 443 --source-ips 0.0.0.0/0 --source-ips ::/0
  hcloud firewall apply-to-resource sentinel-demo --type server --server <server name>
  ```

  Or the same in the console: Firewalls, Create firewall, three inbound TCP rules, applied to the server. A firewall with rules denies every other inbound packet.
- ufw on the node, in place of the runbook's rules. The pod and service networks stay allowed, which is what lets the simulators and the gateway reach 6201 and 6202; nothing else reaches them:

  ```bash
  # on the node
  sudo ufw default deny incoming
  sudo ufw allow from <your address> to any port 22 proto tcp
  sudo ufw allow 80,443/tcp
  sudo ufw allow from 10.42.0.0/16 to any    # pods: the simulators and the gateway send to <node IP>:6201 and :6202
  sudo ufw allow from 10.43.0.0/16 to any    # services
  sudo ufw enable
  ```

- `kubectl` from the workstation: leave the kubeconfig's server at `https://127.0.0.1:6443` (skip the runbook's `sed`) and open a tunnel first, `ssh -fNL 6443:127.0.0.1:6443 <admin>@<node>`.
- Check from a machine outside: `nc -vz -w 5 sentinel.msbolton.dev 6201` and `6202` time out, `curl -sI http://sentinel.msbolton.dev/` answers 308.

**The Secrets (step 4).** Create the ones of the [owner checklist](#owner-checklist) as well: `sentinel-geoip` and the aisstream Secret.

**The site file (step 5).** Beside the runbook's settings:

```yaml
global:
  host: sentinel.msbolton.dev
ingestService:
  existingSecret: sentinel-ingest-token
  secretEnv:
    existingSecret: sentinel-ingest-env
web:
  mapKeys:
    existingSecret: sentinel-map-keys
```

Mail is optional on the demo: guests never register. Without `mail.host`, registration e-mails fail, which nobody sees.

**The images (step 3).** `make images-push` builds the ten images, `sensor-sim` included.

**The first install (step 6) runs twice.** The gateway with the demo on needs `sentinel-demo-visitors-token`, the script that creates it signs in through the gateway, and on a first install there is no gateway yet. So the first deploy holds the gateway's demo back:

1. Install with the demo overlay, the gateway's demo off. Everything else of the overlay applies, the live feeds' first-start setting included. The simulators wait in `CreateContainerConfigError` for their Secret; that is expected.

   ```bash
   make deploy REGISTRY=registry.example.com VALUES="k8s/overlays/demo/values.yaml $HOME/sentinel/sentinel-site.yaml" \
     HELM_ARGS="--set apiGateway.demo.enabled=false"
   ```

2. Once every other pod is Ready, run the bootstrap script as `sentinel-admin`. It asks for the password: the first-start one in the Secret `sentinel-keycloak` (`docs/deployment.md`, step 7), or the one you changed it to.

   ```bash
   scripts/demo-bootstrap.sh --host https://sentinel.msbolton.dev --admin-user sentinel-admin
   ```

3. Deploy again with the demo on (the overlay's own value):

   ```bash
   make deploy REGISTRY=registry.example.com VALUES="k8s/overlays/demo/values.yaml $HOME/sentinel/sentinel-site.yaml"
   ```

4. Restart the gateway and the simulators, so each runs on its token. (The simulators usually start on their own once the Secret appears; the restart makes sure.)

   ```bash
   kubectl -n sentinel rollout restart deploy/sentinel-api-gateway \
     deploy/sentinel-sensor-sim-radar deploy/sentinel-sensor-sim-eoir deploy/sentinel-sensor-sim-esm
   ```

Every later upgrade is the same `make deploy` as step 3 (`docs/deployment.md`, Upgrades).

**Check it.**

- `curl -s https://sentinel.msbolton.dev/api/v1/demo/status` answers `{"up":true,"exploring":0,"full":false}`. Right after a gateway start it says `up:false` for a few seconds, until the gateway's startup cleanup has reached Keycloak and the ingest service: that is by design, so the button never sends a visitor into a "starting" error.
- `kubectl -n sentinel logs deploy/sentinel-api-gateway | grep 'public demo ready'`, and the initContainer's line, `kubectl -n sentinel logs deploy/sentinel-api-gateway -c geoip`: `GeoLite2-City ready`, or a `WARNING` with the reason.
- `kubectl -n sentinel logs deploy/sentinel-sensor-sim-esm --tail=5` shows `session feed_id=demo-sensors`. Connections, as the admin, shows `demo-sensors` with three senders' sites and `demo-visitors`.
- Open `https://sentinel.msbolton.dev/#/demo` in a private window: the welcome card, then the map at your location.

## The bootstrap script

`scripts/demo-bootstrap.sh` needs bash, `curl` and `jq`, and `kubectl` pointed at the cluster unless `--no-kubectl`. It reads the password from `SENTINEL_ADMIN_PASSWORD`, or asks for it without echoing. It signs in by password grant on `sentinel-web`, then:

1. makes sure the grpc connection `demo-sensors` exists, is enabled and has port 6201, `exercise`, `military` and `senderManagedEmplacements` (a setting that differs is put back with a PATCH);
2. the same for `demo-visitors`: port 6202, `exercise`, `senderManagedEmplacements`, `maxEmplacements: 32` (above the 25 guests);
3. for each, when its Secret is missing, issues a token and creates the Secret with the key `token`. A Secret that exists is left alone, unless its connection has no unrevoked token left (then its token cannot work, and it is replaced). When it had to create a missing Secret, it revokes the earlier tokens it issued itself (named `demo-bootstrap <time>`): the Secret was their only copy.

A second run reports both connections and Secrets as existing and changes nothing. It never prints a token or the password, and neither reaches a command line: the password goes to curl on stdin, the access token through a curl config file, and the ingest token through a file that `kubectl` reads, each in a private temporary directory removed on exit.

| Flag | |
|------|--|
| `--host URL` | The site. Plain `http://` only for localhost |
| `--admin-user NAME` | Default `sentinel-admin`; any user with `sentinel-admin` or `sentinel-operator` |
| `--namespace NS` | Default `sentinel` |
| `--rotate` | Issue new tokens, replace both Secrets, revoke every other token of the two connections, then print the restart. Between the revoke and the restart the simulators and visitor sites fail: restart straight away |
| `--no-kubectl` | Ensure the connections only, and print the Secrets that would be made. No token is issued, since nothing could store it |
| `--dry-run` | Sign in and read, change nothing, and print each call that would change something, with secrets redacted |

After a run that created or replaced a Secret, it prints the `kubectl rollout restart` the pods need: they read the token at start.

## Adding the demo to an existing install

On a node that already runs Sentinel, the order is simpler, and two things do not happen by themselves.

1. **The realm.** Keycloak imports its realm on the first start only (`--import-realm` skips a realm that exists). A realm created before the demo lacks three things the demo needs. A fresh install has them. Without them, a guest's account cannot be made (no role), the gateway's startup cleanup retries forever with sessions closed (403 listing users), or guest tokens are refused (401 invalid audience). Add them in the admin console (`docs/deployment.md`, step 7, for the port-forward), realm `sentinel`:
   - Realm roles, Create role: `sentinel-guest`, description "Public demo guest: read-only, throwaway".
   - Clients, `sentinel-service`, Service account roles, Assign role, Filter by clients: `realm-management` `view-realm` (beside its `manage-users`).
   - Clients, `sentinel-web`, Client scopes, `sentinel-web-dedicated`, Configure a new mapper (or Add mapper, By configuration), Audience: name `sentinel-api-audience`, Included Client Audience `sentinel-api`, Add to access token on, Add to ID token off.

   Or with `kcadm.sh` in the Keycloak pod (`config credentials` asks for the master admin's password):

   ```bash
   kubectl -n sentinel exec -it deploy/sentinel-keycloak -- bash
   kc() { /opt/keycloak/bin/kcadm.sh "$@" --config /tmp/kcadm.config; }
   kc config credentials --server http://localhost:8080/auth --realm master --user <master admin>
   kc create roles -r sentinel -s name=sentinel-guest -s 'description=Public demo guest: read-only, throwaway'
   kc add-roles -r sentinel --uusername service-account-sentinel-service --cclientid realm-management --rolename view-realm
   web=$(kc get clients -r sentinel -q clientId=sentinel-web --fields id --format csv --noquotes)
   kc create "clients/$web/protocol-mappers/models" -r sentinel -s name=sentinel-api-audience -s protocol=openid-connect \
     -s protocolMapper=oidc-audience-mapper -s 'config."included.client.audience"=sentinel-api' \
     -s 'config."access.token.claim"=true' -s 'config."id.token.claim"=false'
   rm /tmp/kcadm.config; exit
   ```

2. **The bootstrap first.** The running gateway serves the script, so run it before the overlay: `scripts/demo-bootstrap.sh --host https://<host>`. Then `make deploy` with the overlay (`VALUES="k8s/overlays/demo/values.yaml <site file>"`); the gateway and the simulators start with their Secrets in place.
3. **The live feeds.** `FEEDS_BOOTSTRAP_ENABLED` never changes a feed that exists, so enable the four in Connections (or `POST /api/v1/feeds/{opensky,aisstream,intelsky,celestrak}/enable` as an operator).

## What guests can and cannot do

A guest is a Keycloak account `guest-<8 hex>` with `sentinel-viewer`, `classification-u` and `sentinel-guest`, which the gateway deletes after 60 minutes.

They can:
- see the map with the live aircraft, vessels and satellites, and the simulated sensors' detections, all at UNCLASSIFIED;
- select entities, search, open the panels a viewer has, and fly to the sensors or to the other visitors from the welcome card;
- see their own site at full precision, and every other visitor's site snapped to the centre of a 0.1° cell (about 10 km).

They cannot:
- change anything another visitor would see: every write route needs a role above viewer;
- create API keys, read a connection's tokens or dead letters, or see more of a connection than the map and the layers panel need;
- open Connections, the link graph or the admin settings;
- see anything above UNCLASSIFIED.

A visitor's precise position never leaves their browser: the server stores, logs and publishes the blurred one only. When the last tab closes, the site goes offline after 3 missed heartbeats (30 s) and is deleted 5 minutes later. At 25 guests, new visitors get "Demo full". The session route allows 5 starts per client address in 10 minutes.

## The shared polling area

OpenSky and aisstream poll one area: the gateway merges every connected viewport into it (`apps/api-gateway/internal/stream/viewport_agg.go`). Two visitors on opposite sides of the world widen it to most of the globe, and OpenSky slows to its 60 s ceiling for everyone. This is accepted: the feeds' background ingest keeps the rest of the world populated, and the upstream usage stays bounded by the one install, however many visitors there are.

## Known limits

- **GeoIP at every gateway start.** The initContainer downloads the database each time a gateway pod starts, and MaxMind limits downloads per account per day. A crash-looping gateway can use them up. The download fails open: the gateway starts without the IP fallback, and visitors who deny the browser's location get no site until the next start that succeeds.
- **One gateway replica.** Guests, presence and the polling area live in its memory. A restart ends every guest session (the client offers a fresh one) and shows `up:false` until the startup cleanup is done.
- **Headroom.** The prod requests plus the simulators and the GeoIP download leave little of a 4 vCPU node, and a 16 GB, 8 vCPU plan is the target. Check `kubectl top pods -n sentinel` after a day of real traffic (`docs/deployment.md`, Open risks).
- **Client addresses.** The session rate limit and the GeoIP fallback use the rightmost `X-Forwarded-For` entry, which is the client only if Traefik keeps the source address (`docs/deployment.md`, Open risks, Client IPs through servicelb). Check it on the first install, or every visitor shares one rate-limit bucket and is placed at the node's location.

## The portfolio button

The button lives on msbolton.dev. It polls `https://sentinel.msbolton.dev/api/v1/demo/status` every 30 s, gives each request 5 s, and shows one of three states:

- **"Live demo · N exploring now"**, a link to `https://sentinel.msbolton.dev/#/demo`;
- **"Demo full — try again in a few minutes"**;
- **"Demo offline"**, when the request fails, times out or answers `up:false`.

The status answers browsers from `https://msbolton.dev` only (`DEMO_STATUS_ORIGIN`, `apiGateway.demo.statusOrigin`); a page on another origin, `www.msbolton.dev` included, always shows offline. Drop this into the page. It needs no framework, and without JavaScript it stays a plain link:

```html
<p id="sentinel-demo" class="sentinel-demo" aria-live="polite">
  <a href="https://sentinel.msbolton.dev/#/demo">Live demo</a>
</p>
<script>
(function () {
  var SENTINEL = 'https://sentinel.msbolton.dev';
  var box = document.getElementById('sentinel-demo');

  function show(state, exploring) {
    box.textContent = '';
    box.setAttribute('data-state', state);
    if (state === 'live') {
      var link = document.createElement('a');
      link.href = SENTINEL + '/#/demo';
      link.textContent = 'Live demo · ' + exploring + ' exploring now';
      box.appendChild(link);
    } else {
      box.textContent = state === 'full' ? 'Demo full — try again in a few minutes' : 'Demo offline';
    }
  }

  function poll() {
    var ctl = new AbortController();
    var timer = setTimeout(function () { ctl.abort(); }, 5000);
    fetch(SENTINEL + '/api/v1/demo/status', { signal: ctl.signal })
      .then(function (r) { if (!r.ok) throw new Error('HTTP ' + r.status); return r.json(); })
      .then(function (s) {
        if (!s.up) show('offline');
        else if (s.full) show('full');
        else show('live', Number(s.exploring) || 0);
      })
      .catch(function () { show('offline'); })
      .finally(function () { clearTimeout(timer); });
  }

  poll();
  setInterval(poll, 30000);
})();
</script>
```

Style it through `.sentinel-demo` and `[data-state="live" | "full" | "offline"]`. The page must be served as UTF-8 for the `·` and `—`.

**Trying it locally.** Serve the snippet from `http://localhost:8000` (`python3 -m http.server 8000` in a folder holding it as `index.html`) with `SENTINEL` set to `http://localhost:3000`, and run the gateway with `DEMO_ENABLED=true` and `DEMO_STATUS_ORIGIN=http://localhost:8000` (`docs/api-gateway.md`). It shows live while an entity event has passed within 5 minutes (a simulator on a connection does it), full with `DEMO_MAX_GUESTS=1` and one session started (`curl -X POST localhost:3000/api/v1/demo/session`), and offline once the gateway stops.
