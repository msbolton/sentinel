# Development Guide

## Prerequisites

- **Node.js** 22+ (Angular tooling)
- **npm** (comes with Node.js)
- **Docker** & **Docker Compose** (for infrastructure services)
- **Go 1.25+** (API gateway and all backend services)

## First-Time Setup

```bash
# 1. Clone and install
cd sentinel
npm install

# 2. Copy environment file
cp .env.example .env

# 3. Start infrastructure (PostgreSQL, Kafka, OpenSearch, Keycloak, etc.)
make docker-infra

# 4. Wait ~15 seconds for services to be healthy, then create the schema and seed it
make db-schema
make db-seed

# 5. Start the API gateway and web UI
make dev
```

Seeded entities are queryable through the gateway as soon as the entity-service is up; it reads PostgreSQL directly (PostGIS index for spatial queries).

## Makefile Targets

### Development

| Target | Command | Description |
|--------|---------|-------------|
| `make dev` | Starts infra + client + every Go service | Full development environment |
| `make dev-client` | `npx nx serve client` | Angular dev server on `:4200` |
| `make dev-api` | `npx nx serve api-gateway` | Go API gateway on `:3000` (auth bypass on) |
| `make dev-ingest` | `go run ./cmd/server` | Go ingest service on `:4000` |
| `make dev-analytics` | `nx serve analytics-service` | Go analytics on `:5000` |

### Build

| Target | Command | Description |
|--------|---------|-------------|
| `make build` | `npx nx run-many --target=build` | Build all apps |
| `make build-client` | `npx nx build client --production` | Angular production build |
| `make build-api` | `npx nx build api-gateway` | Go API gateway binary |

### Docker

| Target | Description |
|--------|-------------|
| `make docker-infra` | Start infrastructure only (recommended for local dev) |
| `make docker-up` | Start full stack in containers |
| `make docker-down` | Stop all containers |
| `make docker-clean` | Stop and **remove all volumes** (destructive) |
| `make docker-logs` | Tail all container logs |
| `make docker-build` | Build all Docker images |

### Testing & Linting

| Target | Description |
|--------|-------------|
| `make test` | Run all tests across all apps |
| `make test-client` | Angular unit tests |
| `make test-api` | API gateway Go tests |
| `make test-go` | All Go service tests (vet + race) |
| `scripts/smoke.sh` | Exercise every gateway route, the nginx proxy, and the WebSocket against a running stack |
| `make lint` | Lint all code |
| `make lint-fix` | Auto-fix linting issues |

### Database

| Target | Description |
|--------|-------------|
| `make db-schema` | Apply `db/schema/*.sql` in name order to the running database; idempotent, re-run after editing a file |
| `make db-seed` | Load `db/seed/seed.sql`: 13 entities, a geofence rule, 10 locations |

### Protobuf

| Target | Description |
|--------|-------------|
| `make proto` | Generate all protobuf types (TS, Go, Python) |
| `make proto-ts` | Generate TypeScript types only |
| `make proto-lint` | Lint protobuf definitions |

## npm Scripts

```bash
npm start           # Start client + api-gateway
npm run start:client   # Angular dev server
npm run start:api   # Go API gateway (nx serve)
npm run build       # Build all
npm test            # Test all
npm run lint        # Lint all
npm run docker:infra # Start infrastructure
npm run docker:all   # Start everything
npm run docker:down  # Stop everything
```

## Ports Reference

| Service | Port | Protocol |
|---------|------|----------|
| Web UI | 4200 | HTTP |
| API Gateway | 3000 | HTTP + WebSocket |
| Entity Service | 3001 | HTTP |
| Track Service | 3002 | HTTP |
| Search Service | 3003 | HTTP |
| Link Analysis | 3004 | HTTP |
| Alert Service | 3005 | HTTP |
| Ingest Service | 4000 | HTTP |
| Analytics Service | 5000 | HTTP |
| PostgreSQL | 5432 | TCP |
| Kafka (external) | 9092 | TCP |
| Kafka (internal) | 9093 | TCP |
| OpenSearch | 9200 | HTTP |
| Keycloak | 8080 | HTTP |
| GeoServer | 8600 | HTTP |
| MinIO API | 9000 | HTTP |
| MinIO Console | 9001 | HTTP |

## Auth in Development

The API Gateway skips token verification when `AUTH_DEV_BYPASS=true` (set by `nx serve api-gateway` and by `docker-compose.yml`; refused when `APP_ENV=production`). No Keycloak configuration is needed. The bypass injects a synthetic user:

- **Username**: `dev-operator`
- **Roles**: `sentinel-analyst`, `sentinel-operator`, `sentinel-admin`
- **Classification**: `TOP_SECRET`

The Angular frontend similarly falls back to an unauthenticated dev profile when Keycloak is unavailable.

## Live Aircraft (OpenSky)

OpenSky is polled only for the area connected map clients can see, so nothing is fetched until a browser has the map open. To watch it work:

1. Put your OpenSky API client credentials in `.env` (`OPENSKY_CLIENT_ID`, `OPENSKY_CLIENT_SECRET`). `nx serve` loads `.env`; a bare `go run` does not.
2. Start the infrastructure and the services (ingest-service, entity-service, api-gateway, client) and enable the feed once: `curl -X POST localhost:3000/api/v1/feeds/opensky/enable`. The gateway needs no token with `AUTH_DEV_BYPASS=true` (the Compose and `nx serve` default); otherwise add `-H "Authorization: Bearer $TOKEN"` from an operator or admin login. The enabled state is persisted in the `feeds` table; `FEEDS_BOOTSTRAP_ENABLED=opensky` does the same on first start.
3. The ingest log shows `opensky idle: waiting for an active viewport`; `curl -s localhost:3000/api/v1/feeds/opensky/area` reports `active: false`. Direct calls on `localhost:4000` (`/feeds/opensky/enable`, `/feeds/opensky/area`) work only when the ingest service runs on the host (`make dev-ingest`); Compose does not publish it. With `INGEST_SERVICE_TOKEN` set, a direct call needs `-H 'x-sentinel-service-token: <token>'`.
4. Open http://localhost:4200 and zoom to a region. The gateway logs `viewport sync push`, the ingest service logs `opensky viewport updated` and then `opensky poll complete` every 10 s, and the viewport endpoint shows the box, `effectiveIntervalSec: 10` and `creditsRemaining`.
5. Aircraft glide between fixes (dead reckoning on the client). Pan a little and nothing is pushed; pan far or zoom in by half and one push plus an immediate poll follows. Zoom out to the globe and the interval stretches to 60 s.
6. Close the tab: the gateway pushes `active: false` and the ingest service idles. Kill and restart the ingest service: polling resumes within the 30 s heartbeat.

### Satellites (CelesTrak)

The built-in `celestrak` feed is seeded disabled. Watch the space stations first (a dozen objects, no policy risk), or pin the ISS by catalog number:

```bash
curl -s -X PATCH http://localhost:3000/api/v1/feeds/celestrak -H 'content-type: application/json' \
  -d '{"config": {"groups": ["stations"], "catalogNumbers": [25544], "refreshHours": 2, "propagationIntervalSec": 60}}'
curl -s -X POST http://localhost:3000/api/v1/feeds/celestrak/enable
curl -s http://localhost:3000/api/v1/feeds/celestrak | jq '.status.state, .status.detail'
```

`detail` shows `satellites`, `pinned`, `fetchedAt` and `nextFetchAt`. The state is `idle` until a map client is connected: satellites are emitted only over the clients' viewports, except pinned objects, which are emitted always. Open the map, pan to the Atlantic, and the ISS moves smoothly (the client propagates it between the server's once-a-minute fixes); click it for its orbit path, ground track and footprint. CelesTrak needs no key but enforces a usage policy; keep `refreshHours` at 2 and prefer a group over many catalog numbers (`docs/feeds.md`).

### Military aircraft (IntelSky)

The built-in `intelsky` feed is seeded disabled and needs no key:

```bash
curl -s -X POST http://localhost:3000/api/v1/feeds/intelsky/enable
curl -s http://localhost:3000/api/v1/feeds/intelsky | jq '.status.state, .status.detail'
```

`detail` shows `aircraft` (about 75 worldwide), `lastStatus` 200, `fetchedAt` and `nextFetchAt`; the feed polls every 10 s whether or not a map is open. Military aircraft appear in orange. Select one: Aircraft Details opens with today's path drawn as a dashed orange line, and the Flight path control switches to 3 or 7 days or off (`GET /api/v1/aircraft/{hex}/path?days=`). To check against the live service without the stack: `cd apps/ingest-service && go test -tags live -run TestLivePoll -v ./internal/source/driver/intelsky/` and `cd apps/api-gateway && go test -tags live -run TestLivePaths -v ./internal/flightpath/`.

### Ships (aisstream.io)

The built-in `aisstream` feed is seeded disabled and reads its key from `AISSTREAM_API_KEY` (a free account at aisstream.io). Compose passes the variable to the ingest service:

```bash
echo 'AISSTREAM_API_KEY=<your key>' >> .env
docker compose up -d ingest-service
curl -s -X POST http://localhost:3000/api/v1/feeds/aisstream/enable
curl -s http://localhost:3000/api/v1/feeds/aisstream | jq '.status.state, .status.detail'
```

The state is `idle` until a map client is connected (or the watchlist has entries). Open the map over the English Channel: vessels appear coloured by ship type, turned to their heading and gliding between fixes; `detail.mode` is `detail`, both `confirmed` and `compression` are true on `detail.connections.area`, and `emitted` stays below `messages` as positions are thinned. Zoom out to Europe and discs with counts replace the vessels: `detail.mode` is `density` and `detail.density` reports the last grid's cells and vessels. Track a vessel while nobody looks:

```bash
curl -s -X PATCH http://localhost:3000/api/v1/feeds/aisstream -H 'content-type: application/json' \
  -d '{"config": {"watchlistMmsi": ["235100247"], "maxAreaSqDeg": 2500, "movingIntervalSec": 15, "stationaryIntervalSec": 180}}'
```

To check the driver against the live service without the stack: `cd apps/ingest-service && AISSTREAM_API_KEY=<key> go test -tags live -run TestLiveStream -v ./internal/source/driver/aisstream/` (about 35 s).

### Local receivers and other sources

Every source is a feed created through `POST /api/v1/feeds` (or the Feeds page). The built-in drivers are `opensky`, `intelsky`, `celestrak`, `aisstream`, `httpjson` (any JSON API, field-mapped), `stream` (a transport, a framer and a decoder), and `tcp` and `udp` (external systems that send to Sentinel, below). Pull from site receivers (`tcpclient`) where you can, so any replica can own the feed; a connection that opens a port runs on the listener replica (see "External systems sending to Sentinel"), and `pinnedReplica` is optional.

```jsonc
// readsb / dump1090 SBS (port 30003): the simplest receiver feed
{"id":"roof-sbs","name":"Roof receiver (SBS)","driver":"stream","enabled":true,
 "config":{"transport":{"kind":"tcpclient","address":"readsb.local:30003"},"framing":"line","decoder":{"name":"sbs"}}}

// readsb Beast (port 30005): raw Mode S with CPR position decoding
{"id":"roof-beast","name":"Roof receiver (Beast)","driver":"stream","enabled":true,
 "config":{"transport":{"kind":"tcpclient","address":"readsb.local:30005"},"framing":"beast","decoder":{"name":"beast"}}}

// AIS-catcher sending NMEA over UDP to the listener replica (AIS-catcher -u <listener-host> 10110)
{"id":"pier-ais","name":"Pier AIS","driver":"udp","enabled":true,
 "config":{"port":10110,"format":"nmea-ais"}}

// TAK server CoT stream over TLS (port 8089, client certificate). The PEMs go in ./secrets/tak (see
// "TAK connection" below); for a two-way session, where Sentinel sends as well, use the tak driver
{"id":"tak","name":"TAK server","driver":"stream","enabled":true,
 "config":{"transport":{"kind":"tcpclient","address":"tak.example.org:8089","tls":{"enabled":true}},
           "framing":"xmlevent","decoder":{"name":"cot"}},
 "secretRefs":{"tlsCert":"file:/etc/sentinel/tak/client.pem","tlsKey":"file:/etc/sentinel/tak/client.key","serverCa":"file:/etc/sentinel/tak/ca.pem"}}

// Any JSON API, polled for the clients' viewport, fields mapped onto observations
{"id":"adsb-fi","name":"adsb.fi","driver":"httpjson","enabled":true,
 "config":{"url":"https://opendata.adsb.fi/api/v2/lat/{lat}/lon/{lon}/dist/{radius_nm}","intervalSec":15,"requireArea":true,
           "decoder":{"options":{"records":"ac","identityType":"ICAO24","entityType":"aircraft","source":"ADS_B","altitudeUnit":"ft",
                      "fields":{"id":"hex","latitude":"lat","longitude":"lon","altitude":"alt_baro","speed":"gs","heading":"track","callsign":"flight"}}}}}
```

### External systems sending to Sentinel

The `tcp` and `udp` drivers open a port that another system sends to; `tcp` can also dial out to the sender (`mode: connect`). The default format is `sentinel.v1.Observation` (binary protobuf, or its JSON form with `encoding: json`), and the device formats (`nmea-ais`, `cot`, `sbs`, ...) work through `format`. Listening connections run on the *listener replica*, a second ingest container on the host's network so that any port you pick is reachable without editing Compose. Create a UDP connection, send it one JSON observation with netcat, and find the entity:

```bash
# Docker Desktop (Mac, Windows): Settings > Resources > Network > Enable host networking (4.34+).
# Linux needs nothing.
docker compose --profile inbound up -d ingest-listener
curl -s -X POST http://localhost:3000/api/v1/feeds -H 'content-type: application/json' \
  -d '{"id":"test-udp","name":"Test UDP","driver":"udp","enabled":true,"config":{"port":6001,"encoding":"json"}}'
sleep 6   # the listener picks the connection up at once; its status row is written every 5 s
curl -s http://localhost:3000/api/v1/feeds/test-udp | jq '{state: .status.state, listen: .status.detail.listen}'
echo '{"sourceEntityId":"test-1","entityType":"ENTITY_TYPE_VEHICLE","position":{"latitude":51.5,"longitude":-0.12}}' | nc -u -w1 localhost 6001
sleep 5   # the observation reaches the search index through Kafka and the entity service
curl -s 'http://localhost:3000/api/v1/search?q=test-1' | jq '.hits[0].name'
curl -s -X DELETE http://localhost:3000/api/v1/feeds/test-udp
```

The calls go through the gateway like the rest of this page: with `AUTH_DEV_BYPASS=true` (the Compose and `nx serve` default) they need nothing more, otherwise add `-H "Authorization: Bearer $TOKEN"` from an operator or admin login. The listener's address is `INGEST_LISTENER_HOST` (default `localhost`, set it to the machine's name or IP when senders are elsewhere); `listen` shows the host and port senders use once the connection runs. The same connection is on the Connections page, whose send guide shows the endpoint, a test command (netcat, or OpenSSL when the connection uses TLS), a JSON sample, Go and Python senders written for the connection's TLS setting, a Copy button on each snippet, the `.proto` downloads, a table of who is sending and the counters; operators and admins also get a Recent rejects panel with the connection's latest dead letters. It warns when the TLS certificate expires in under 14 days.

Binary protobuf over TCP is a stream of length-delimited messages (a varint length, then the message), which `nc` cannot produce: use the Go snippet in the connection's send guide (`protodelim.MarshalTo`), or the Python one. UDP binary needs no framing, one message per datagram.

With `encoding: json` a TCP stream or a UDP datagram carries one JSON object per line, or a line that is a JSON array of up to 100 observations (`[{...},{...}]`, all on one line). An array is accepted or refused whole; a bad element is reported by its number counted from 0, and a 101st is refused as `batch_too_large`. A batch counts once against `maxMessagesPerSec`, so a listener can take that limit times 100 observations a second.

Rules and safeguards, in short (details and the counters are in `docs/feeds.md`):

- **Ports** must be 1024-65535, unique per protocol, and not Sentinel's own (`INGEST_RESERVED_PORTS`, blank means the default list of the stack's published ports); the API answers 422 otherwise. `GET /api/v1/feeds/ports` lists the reserved ranges, the ports other connections hold and a free port for each protocol, which is what the form's port hint offers (`ports` is therefore not a usable feed id). Check the port is free on the host, since the listener shares its network.
- **Allowlist.** `allow` takes IPs and CIDRs. An empty list is open to anyone who can reach the port. UDP source addresses can be spoofed, so on an untrusted network use TCP with mutual TLS:

  ```bash
  curl -s -X POST http://localhost:3000/api/v1/feeds -H 'content-type: application/json' -d '{
    "id":"site-tcp","name":"Site gateway","driver":"tcp","enabled":true,
    "config":{"mode":"listen","port":6002,"encoding":"json","allow":["10.20.0.0/16","127.0.0.1","::1"],"tls":{"enabled":true,"requireClientCert":true}},
    "secretRefs":{"tlsCert":"file:/etc/sentinel/secrets/server.pem","tlsKey":"file:/etc/sentinel/secrets/server.key","clientCa":"file:/etc/sentinel/secrets/clients-ca.pem"}}'
  ```

  The example allows `10.20.0.0/16` for the real senders, and `127.0.0.1` and `::1` for the test below (`localhost` can resolve to either); set `allow` to the addresses your senders (and your test) come from. The certificate files go in `./secrets/ingest` (gitignored), which Compose mounts read-only at `/etc/sentinel/secrets` in both ingest containers (`ingest-service` and `ingest-listener`); the TAK ones stay in `./secrets/tak`. Create both folders before the first `docker compose up`: on Linux Docker creates a missing one owned by root. A throwaway CA and certificates for the connection above (use your own PKI outside development; one CA signs the server and the sender, and `clients-ca.pem` is the file the connection trusts):

  ```bash
  mkdir -p secrets/ingest secrets/tak
  SECRETS=$PWD/secrets/ingest; mkdir -p /tmp/sentinel-pki && cd /tmp/sentinel-pki
  openssl req -x509 -newkey rsa:2048 -nodes -days 365 -subj '/CN=Sentinel test CA' -keyout ca.key -out clients-ca.pem
  openssl req -newkey rsa:2048 -nodes -subj '/CN=localhost' -keyout server.key -out server.csr
  openssl x509 -req -in server.csr -CA clients-ca.pem -CAkey ca.key -CAcreateserial -days 90 -out server.pem \
    -extfile <(printf 'subjectAltName=DNS:localhost,IP:127.0.0.1')
  openssl req -newkey rsa:2048 -nodes -subj '/CN=sender-1' -keyout client.key -out client.csr
  openssl x509 -req -in client.csr -CA clients-ca.pem -CAkey ca.key -CAcreateserial -days 90 -out client.pem
  cp server.pem server.key clients-ca.pem "$SECRETS"/    # the CA key and the sender's files stay out of the mount
  ```

  The containers run as user and group 10001 and read the files with the group's rights, and `openssl` writes keys readable by their owner only, so on a Linux host give gid 10001 access once the files are in place (run these in the repository root; they are for Linux hosts):

  ```bash
  sudo chgrp -R 10001 secrets/ingest secrets/tak
  sudo chmod -R u=rwX,g=rX,o= secrets/ingest secrets/tak   # files 0640, folders 0750; you stay the owner
  ```

  To hand the folders over completely instead, `sudo chown -R 10001:10001 secrets/ingest secrets/tak` and `sudo chmod -R u=rX,g=rX,o= secrets/ingest secrets/tak` (files 0440). Rootless Docker and Podman remap ids, where `chgrp 10001` does not apply. A file the container cannot read fails the connection at start with `permission denied` as its last error. With the connection created and running, send one observation as the certificate holder (from `/tmp/sentinel-pki`, where the sender's files are):

  ```bash
  echo '{"sourceEntityId":"mtls-1","entityType":"ENTITY_TYPE_VEHICLE","position":{"latitude":51.5,"longitude":-0.12}}' |
    openssl s_client -quiet -no_ign_eof -verify_return_error -connect 127.0.0.1:6002 -servername localhost \
      -CAfile clients-ca.pem -cert client.pem -key client.key
  ```

  The connection's send guide shows the same command with the listener's host (`localhost` by default, `INGEST_LISTENER_HOST`) in place of `127.0.0.1`, a `-verify_hostname` check (`-verify_ip` for an address) and `ca.pem` in place of `clients-ca.pem`; run from the folder that holds the files, with `localhost` it needs both `127.0.0.1` and `::1` in `allow`. Change the server certificate files in `./secrets/ingest` and the listener serves the new certificate within `INGEST_TLS_RELOAD_SEC` (60 s) without dropping connections; `detail.tlsReloads` counts it, `detail.tlsCertNotAfter` shows the expiry, and a failed reload (`tlsReloadFailed`) keeps the old one.
- **Secrets in variables.** A `secretRefs` entry of the form `env:NAME` may name only `SENTINEL_SECRET_*`, `OPENSKY_*` and `AISSTREAM_*` variables, and a `file:` entry only files under `/etc/sentinel`; anything else is a 422 (`INGEST_SECRET_ENV_ALLOW`, `INGEST_SECRET_FILE_DIRS`, and `INGEST_SECRET_ENV_ALLOW=*` for the old behaviour, in `docs/feeds.md`). Compose reads your own variables from `./secrets/ingest.env` (`NAME=value` lines; gitignored; optional) into both ingest containers: put `SENTINEL_SECRET_SITE_TOKEN=...` there and reference it as `env:SENTINEL_SECRET_SITE_TOKEN`, then `docker compose up -d` to recreate the containers (a changed file is not re-read by a running one). It needs Compose 2.24 or later, and the file must not hold anything but secrets, since every variable in it reaches the service.
- **Limits** per connection: `maxConnections` (50), `maxMessagesPerSec` (1000, shared by all senders), `maxMessagesPerSecPerSender` (0, none: a limit for each sender IP inside the shared one), `maxMessageBytes` (64 KiB for TCP, 65507 for UDP) and, for UDP, `receiveBufferBytes` (4 MiB, the socket buffer; the kernel grants at most `net.core.rmem_max` of the listener host, so raise that with `sysctl` for more, and `kernelDropped` counts what it still loses). What they refuse is counted in the connection's status and in the `sentinel_ingest_listener_*` metrics (`docs/feeds.md`).
- **Rejected messages.** A message the decoder refuses goes to `observations.dlq`; `GET /api/v1/feeds/test-udp/deadletters?limit=5&payload=true` lists the newest with the reason, error, sender (`ip:port`) and the first 1 KiB of the payload. It needs an operator or admin role and a clearance at or above the connection's classification (`AUTH_DEV_BYPASS` has both), and answers 501 when the ingest service runs with `DLQ_PEEK_MAX_SCAN=0`. The same list is the Recent rejects panel on the Connections page.
- **The listener's own port.** The listener's management API binds loopback only (`127.0.0.1:4010`): its healthcheck uses it and nothing else needs to, since the gateway talks to `ingest-service` on `:4000`. It is guarded by `INGEST_SERVICE_TOKEN` too (default `dev-ingest-token`, change it anywhere but a laptop).
- With no listener running, an enabled listening connection shows `unassigned` with the error `no listener replica`; start the profile above.

### TAK connection

A TAK connection needs a server that speaks Cursor on Target on a streaming port: a TAK Server (`tak.gov`, needs an account), FreeTAKServer (`ghcr.io/freetakteam/freetakserver`), or OpenTAKServer. None is bundled with the stack, and the `tak` driver has not yet been exercised against a live server; treat it as implemented but unverified until it has been.

Create the connection through the Connections page or the API. Against a plain TCP port no certificates are needed:

```bash
curl -s -X POST http://localhost:3000/api/v1/feeds -H 'content-type: application/json' -d '{
  "id": "tak-dev", "name": "Dev TAK", "driver": "tak", "enabled": true,
  "config": {
    "address": "host.docker.internal:8087", "siteId": "dev",
    "policy": { "entityTypes": ["AIRCRAFT"], "feeds": ["opensky"],
                "boxes": [{"north": 42.5, "south": 41.5, "east": -87, "west": -88.5}],
                "classificationCeiling": "UNCLASSIFIED" }
  }
}'
```

With OpenSky enabled and the map open over that box, `GET /api/v1/feeds/tak-dev` shows `detail.outbound.delivered` rising and `detail.remotePeer`. Point ATAK, WinTAK or iTAK at the same server to see the aircraft as air tracks with callsigns; drop a marker on the device and it appears on the Sentinel map under the connection's layer. For a TLS port set `tls.enabled` and give the enrolment certificate as `file:` secret refs mounted under `./secrets/tak` (gitignored, mounted read-only at `/etc/sentinel/tak` in both ingest containers; on Linux give gid 10001 read access as above):

```bash
openssl pkcs12 -in client.p12 -nodes -nocerts -out secrets/tak/client.key
openssl pkcs12 -in client.p12 -nokeys -clcerts -out secrets/tak/client.pem
openssl pkcs12 -in truststore.p12 -nokeys -cacerts -out secrets/tak/ca.pem
```

and `"secretRefs": {"clientCert": "file:/etc/sentinel/tak/client.pem", "clientKey": "file:/etc/sentinel/tak/client.key", "caCert": "file:/etc/sentinel/tak/ca.pem"}`.

### Behavioural analysis

The analytics service judges every fix against the entity's baseline. Baselines need a few days of history, but the impossible-move detector does not, so the quickest check is a teleport:

```bash
ID=$(curl -s -X POST http://localhost:3000/api/v1/entities -H 'content-type: application/json' \
  -d '{"entityType":"VEHICLE","name":"teleport","source":"MANUAL","position":{"lat":40,"lng":-74}}' | jq -r .id)
curl -s -o /dev/null -X PATCH http://localhost:3000/api/v1/entities/$ID/position -H 'content-type: application/json' -d '{"lat":40,"lng":-74}'
sleep 6   # updates closer than ANALYTICS_MOVE_MIN_GAP_SEC (5) are not judged
curl -s -o /dev/null -X PATCH http://localhost:3000/api/v1/entities/$ID/position -H 'content-type: application/json' -d '{"lat":40.9,"lng":-74}'
sleep 3
curl -s http://localhost:3000/api/v1/analytics/entities/$ID/findings | jq '.data[] | {kind, severity, status, alertId}'
curl -s "http://localhost:3000/api/v1/alerts?entityId=$ID" | jq '.data[] | {alertId, alertType, severity}'
```

The finding appears in the Findings panel and in the entity's Behaviour section; confirm or dismiss it there, or with `PATCH /api/v1/analytics/findings/{id} {"status":"DISMISSED"}`. Thresholds are `ANALYTICS_*` environment variables (see `apps/analytics-service/internal/config/config.go`); `GET /api/v1/analytics/stats` shows what the service has judged. Baselines are recomputed hourly for entities seen in the last day (`ANALYTICS_BASELINE_INTERVAL_MIN`); `GET /api/v1/analytics/entities/{id}/baseline` returns one once it exists.

### Several ingest replicas

Every enabled feed runs on exactly one replica: replicas heartbeat `sentinel.ingest_replicas`, a feed's preferred owner is chosen by rendezvous hashing over the live ordinary replicas (or `config.pinnedReplica`; connections that open a port go to the listener replicas, which run nothing else), and the owner holds a `sentinel.feed_leases` row it renews every `LEASE_TICK_SEC` (2 s) with a `LEASE_TTL_SEC` (15 s) expiry. `docker compose up -d --scale ingest-service=3` starts three; `GET /api/v1/feeds` shows each feed's `status.owner`, a replica that stops reporting renders its feeds as `unassigned` until another one takes them (within one TTL), and every observation carries the lease's fencing token in the `x-fence` header. Connections that bind a port (`tcp` in listen mode, `udp`, `stream` with a `tcpserver` or `udp` transport) run on a listener replica (`INGEST_LISTENER=true`, the `ingest-listener` service described above) and need no `pinnedReplica`; with none live they render as `unassigned`. The listener takes nothing else unless a feed is pinned to it or `INGEST_LISTENER_SHARED=true` is set: with only the listener live, every other feed that is not pinned renders as `unassigned` (`no ingest replica for this feed: ...`) and `sentinel_ingest_feeds_unplaced` counts it (`docs/feeds.md`, Listeners). `LEASE_ENABLED=false` runs every feed on one replica (local development with a single process). After pulling a change, build the image once (`docker compose build ingest-service`; both services use `sentinel/ingest-service:dev`) and restart `ingest-service` before `ingest-listener`, or both with one `docker compose --profile inbound up -d`: a new dedicated listener beside an old `ingest-service` leaves ordinary feeds flapping (about 30 s on, 15 s off). Build one image at a time on a small Docker VM. Keep `INGEST_LISTENER_SHARED` out of `.env`: `nx serve` loads that file into the host-run ingest service, which refuses it without `INGEST_LISTENER=true`; pass it on the command line (`INGEST_LISTENER_SHARED=true docker compose --profile inbound up -d ingest-listener`).

See `docs/architecture.md` (OpenSky viewport-driven polling) for the contract, the credit budget, and the environment variables.

## Debugging Tips

### API Gateway won't start

1. Check infrastructure is running: `docker ps`
2. Verify PostgreSQL is accessible: `psql -h localhost -U sentinel -d sentinel`
4. Check Kafka: `docker logs sentinel-kafka`

### Entities endpoint returns empty `[]`

The gateway proxies `/api/v1/entities` to the entity-service, which reads PostgreSQL. If the list is empty:

1. Run `make db-schema` then `make db-seed` to populate PostgreSQL
3. Check `curl localhost:3001/api/v1/entities` directly to rule out the gateway

### WebSocket not connecting

1. Verify the API Gateway is running on `:3000`
2. Check the Angular proxy config (`proxy.conf.json`) forwards `/ws` to `:3000` with `ws: true`
3. Check browser dev tools Network tab for WebSocket connection attempts

### Kafka consumer lag

If events aren't flowing through:

1. Check Kafka topics exist: `docker exec sentinel-kafka /opt/kafka/bin/kafka-topics.sh --bootstrap-server localhost:9093 --list`
2. Check consumer groups: `docker exec sentinel-kafka /opt/kafka/bin/kafka-consumer-groups.sh --bootstrap-server localhost:9093 --list`
3. Verify `KAFKA_BROKER` env var matches the expected listener (use `localhost:9092` from host, `kafka:9093` from containers)

### Database connection issues

Default credentials (from `.env.example`):
- Host: `localhost`
- Port: `5432`
- Database: `sentinel`
- User: `sentinel`
- Password: `sentinel_dev`

The API Gateway uses `DB_HOST`, `DB_PORT`, `DB_USERNAME`, `DB_PASSWORD`, `DB_DATABASE` env vars (see `app.module.ts`).

### Ingest service exits with `database schema is out of date`

At start, right after it opens the feed registry, the ingest service checks that the tables and columns it uses exist, and refuses to run when any is missing: against an older schema it would misplace feeds without saying so. The fatal log line names the missing columns and the file that adds them, for example `sentinel.ingest_replicas.listener` and `db/schema/70_feeds.sql`. The database predates the code, so apply the schema. Every file in `db/schema` is idempotent, so applying the whole directory is safe:

- **Compose:** `docker compose up db-schema`. Application services wait for that job, so you meet the error only with `up --no-deps`, or after pulling a change onto an older database volume. The ingest container, which Docker keeps restarting, comes up on its next try.
- **Local stack:** `make db-schema` (it applies the files to the `sentinel-postgres` container).
- **Helm:** `helm upgrade` runs the `dbSchema` Job, which applies the files before the new pods start (`dbSchema.enabled`, on by default; `docs/infrastructure.md`). You meet the error only when the Job is off or failed.
- **Any other database:** `psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -f <file>` for each file in `db/schema`, in name order.

The line is one JSON record, for example `database schema is older than this ingest-service: missing sentinel.ingest_replicas.listener (db/schema/70_feeds.sql). Apply db/schema in name order, every file is idempotent: ...`. If the fatal message is `checking the database schema failed` instead, the check itself could not run (the database is unreachable, or the connection was refused or timed out): applying the schema will not help, so look at `DATABASE_URL`, the credentials and the database first.

### Resetting everything

```bash
make docker-clean   # Removes all containers AND volumes
make docker-infra   # Restart infrastructure
sleep 15            # Wait for services
make db-seed        # Re-seed database
make dev            # Start development
```
