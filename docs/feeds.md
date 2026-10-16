# Feeds

A feed is one configured source of observations: a built-in poller such as OpenSky, a JSON API, or a byte stream from a receiver or a TAK server. This page covers the lifecycle of a feed, the management API, configuration and secrets, what each driver does, how several ingest replicas share the work, and what to write to add a driver.

## Lifecycle

```
operator / gateway ──▶ feeds (desired state, Postgres) ──LISTEN/NOTIFY──▶ every ingest replica
                                                                              │ lease: one owner per feed (feed_leases)
                                                                              ▼
                                   driver ──▶ runner (provenance, dedupe, raw bounds) ──▶ observations.raw (protobuf)
                                                                              ▼
                       entity-service: resolve identities ──▶ fuse (newest fix wins) ──▶ entities, observations, entity_sources
                                                                              ▼
                                                     entities.events ──▶ track, alert, search, analytics, gateway WebSocket
```

1. **Desired state** lives in `sentinel.feeds`: id (an immutable slug), name, driver, `config` (validated against the driver's JSON Schema plus the common options), `secret_refs`, `enabled`, `version`. Every write bumps `version` and notifies the replicas.
2. **Ownership.** With leases enabled (the default) each enabled feed runs on exactly one replica. Replicas heartbeat `ingest_replicas`; a feed's preferred owner is chosen by rendezvous hashing over the live replicas, or by `config.pinnedReplica`; the owner holds a `feed_leases` row it renews every `LEASE_TICK_SEC` (2 s) with a `LEASE_TTL_SEC` (15 s) expiry. Acquisition bumps the fencing token, which travels on every observation (`x-fence` header, `fencing_token` field) and guards status writes, so a replica that lost its lease cannot overwrite the new owner's work. A non-preferred replica steps in only after the preferred one had a TTL to claim a free feed, and hands the feed back after `LEASE_REBALANCE_SEC` (30 s) once the preferred replica is live.
3. **Running.** The supervisor builds the driver, wraps it in a runner and starts it. A driver that fails to start or exits with an error is restarted with backoff (`FEED_RETRY_MIN_SEC` to `FEED_RETRY_MAX_SEC`). A version bump restarts the feed with the new configuration.
4. **Observations.** The runner stamps provenance (feed id, driver, receive time, lease token), guarantees an identity (a canonical one such as ICAO24 or MMSI when the decoder found one, else `FEED_LOCAL` over feed id and source entity id), bounds the raw payload (`rawPayload: none|bounded|full`, bounded to `RAW_PAYLOAD_MAX_BYTES`), suppresses a fix that repeats the previous one within a second and ten metres (still forwarding one every 5 s so downstream last-seen times move), computes the deterministic observation id, and publishes to `observations.raw` keyed by identity so one object's reports stay in order. Payloads nothing could decode go to `observations.dlq` (at most `DLQ_RATE_PER_SEC` per feed) with the reason and error in headers.
5. **Status.** The owner writes `feed_status` every 5 s: state (`starting`, `running`, `idle`, `degraded`, `error`, `stopped`), health from the time since the last report against `staleWarnSec` / `staleCriticalSec`, counters, last error, applied version and driver detail (area, credits, connections, lease token). `GET /feeds` from any replica overlays the live status for feeds running there and renders a row whose replica stopped reporting as `unassigned`.
6. **Outbound.** A connection (direction `out` or `both`) also runs a pump beside its driver on the owning replica: its own Kafka consumer group `sentinel-conn-<id>` on `entities.events` (and `alerts.events` unless `policy.alerts.enabled` is false), so offsets survive restarts and handovers and a slow remote never stalls another connection. Each event is checked against `config.policy` (loop and re-export guards first, then classification ceiling, entity type, feed, boxes), coalesced per entity (only the newest snapshot waits; a removal replaces any pending snapshot and is never dropped), spaced by `outbound.perEntityMinSec`, rate-limited by `outbound.maxEventsPerSec`, and handed to the driver. `deleted` and `aged_out` events become removals for entities the remote knows; an entity that stops passing the policy is removed after `outbound.policyExitGraceSec`; events older than `outbound.maxLagSec` are dropped rather than replayed. While the driver reports it is not connected the pump pauses and keeps the queue. `feed_status.detail.outbound` carries `delivered`, `deliveryErrors`, `denied`, `coalesced`, `dropped`, `queueDepth`, `tracked` and `consumerGroup`; `messagesTotal` and `lastSuccessAt` include deliveries. Losing the lease cancels the pump, which leaves the group so the new owner resumes from the committed offset.
6. **Fusion** happens in the entity-service: observations from different feeds that share a canonical identity update one entity, the newest source timestamp wins, rejected fixes are kept as observations with a reject reason, and per-feed ageout thresholds apply. See `docs/data-model.md`.

## Management API

Served by the ingest service on `HTTP_ADDR` (`:4000`) and proxied by the gateway under `/api/v1`. The gateway presents `X-Sentinel-Service-Token` (`INGEST_SERVICE_TOKEN`); reads need any authenticated user, writes need `sentinel-operator` or `sentinel-admin`. The gateway itself may push areas with the bare service token.

| Method | Path | Description |
|--------|------|-------------|
| GET | `/feeds?driver=&enabled=&capability=&direction=` | Every feed with its status (array); `direction=in` or `out` also matches `both`, `direction=both` only `both` |
| POST | `/feeds` | Create: `{ id?, name, description?, driver, config, secretRefs?, enabled? }`; the id is slugified from the name when omitted. 422 with `details[{path,message}]` on validation failure, 409 on a duplicate id |
| GET | `/feeds/{id}` | One feed |
| PUT | `/feeds/{id}` | Replace the mutable fields (`name`, `description`, `config`, `secretRefs`, `enabled`) |
| PATCH | `/feeds/{id}` | Merge patch; `config` merges one level deep, `null` removes a key; `id`, `driver`, `builtin` are immutable |
| DELETE | `/feeds/{id}` | 403 for a built-in feed (disable it instead) |
| POST | `/feeds/{id}/enable`, `/feeds/{id}/disable` | Persisted desired state |
| GET | `/feeds/{id}/status` | The status row (live when the feed runs on the answering replica) |
| GET, PUT | `/feeds/{id}/area` | Area of interest for drivers with the `area` capability: `{ active, clients, boxes[{north,south,east,west}] }`, at most four boxes that never cross the antimeridian; the response adds `live`, `idle`, `areaSqDeg`, `effectiveIntervalSec`, `creditsRemaining`, `lowCredits`, `lastPollAt`, `nextPollInSec`, `expiresAt`. 409 for a driver without the capability |
| GET | `/drivers`, `/drivers/{id}` | The catalog: id, name, description, source, `direction`, capabilities, `configSchema`, `secretFields` |

The feed resource carries `source` (the EntitySource label its observations use), `direction` (`in`, `out` or `both`: the driver's direction, narrowed by `config.direction` when set), `capabilities` (`poll`, `area`, `listen`), `builtin`, `version` and `status`. A feed that sends (`out` or `both`) is a *connection*: it consumes `entities.events` and `alerts.events` under the same lease and status machinery and pushes what its `config.policy` allows to the remote system.

## Configuration and secrets

`config` is validated against the driver's schema (served on `/drivers/{id}`) and the common options every driver accepts:

| Key | Purpose |
|-----|---------|
| `staleWarnSec`, `staleCriticalSec` | Health thresholds (defaults `FEED_STALE_WARN_SEC` 120, `FEED_STALE_CRITICAL_SEC` 300) |
| `backpressure` | `block`, `drop` or `nack` between a transport and its decoder; the transport default applies when absent |
| `pinnedReplica` | Run only on this replica; required for listening transports (`tcpserver`, `udp`) |
| `classification` | Default level (`UNCLASSIFIED`, `CONFIDENTIAL`, `SECRET`, `TOP_SECRET`) stamped on every observation; a decoder may only raise it. Entities keep the highest level any feed reported. Link 16 feeds should set `SECRET`, since the decoder does not read the header's security field |
| `rawPayload` | `none`, `bounded` (default) or `full` raw bytes kept on each observation |
| `direction` | `in`, `out` or `both`; narrows a driver that can do both, never widens one (422 otherwise) |
| `policy` | Sharing policy of an `out` or `both` connection (422 on a driver that only receives): `entityTypes[]`, `feeds[]` (feed ids that must have contributed), `boxes[{north,south,east,west}]` (up to 16; `west > east` wraps the antimeridian), `classificationCeiling` (default `UNCLASSIFIED`), `reexport` (send entities another connection contributed to; a loop back to the same connection is always prevented), `alerts{enabled, minSeverity, types[]}`. Empty lists mean no restriction |
| `outbound` | Sink tuning: `queueMax` (10000), `perEntityMinSec` (5), `maxEventsPerSec` (200), `maxLagSec` (300), `policyExitGraceSec` (30) |

Secrets never appear in `config`. Each driver declares `secretFields`, and `secretRefs` maps them to references the owning replica resolves at start: `env:VARIABLE_NAME` (the variable must be set on every replica) or `file:/absolute/path` (the file's contents, trimmed; how mounted secrets and PEM certificates arrive). Plain values are rejected.

## Drivers

| Driver | Capabilities | Source | Configuration |
|--------|--------------|--------|---------------|
| `opensky` | poll, area | OPENSKY | `intervalSec` (10), `viewportMode` (true: idle without an active area; false: poll `bbox` or the globe continuously), `minPollSpacingSec`, `maxAreaSqDeg` (400: beyond it the interval stretches by `ceil(area/max)` up to `maxIntervalSec` 60), `creditFloor` (200: below it poll at the slowest interval), `viewportTtlSec` (90, the area lease). Secrets `clientId`, `clientSecret` (OAuth2 client credentials; anonymous without them) |
| `adsblol` | poll, area | ADSB_LOL | `intervalSec`, `scope`: `mil` (every military aircraft worldwide) or `area` (everything within the clients' viewport through `/v2/point`, `maxRadiusNm` 250, idle without an area) |
| `celestrak` | poll | CELESTRAK | `groups` (CelesTrak groups, default `active`), `tleRefreshHours` (6), `propagationIntervalSec` (60); elements are fetched inside the run with backoff |
| `httpjson` | poll, area | decoder's `source` | `url` with placeholders `{lat}`, `{lon}`, `{radius_nm}`, `{radius_km}`, `{north}`, `{south}`, `{east}`, `{west}`, `{bbox}`, `{now_unix}` (one request per box of an active area; a global box otherwise), `method` and `body`, `headers`, `intervalSec`, `requireArea` (idle without an area), `auth` (`none`, `basic`, `bearer`, `header`, `oauth2_client_credentials`; secrets `password`, `token`, `clientSecret`), `rateLimitHeader` and `creditFloor`, `decoder.options` for the `json` decoder |
| `stream` | listen when the transport binds a port | decoder's source | `transport` (`tcpclient`, `tcpserver`, `udp`, `mqtt`, `stomp`, `websocket`, with `tls`), `framing` (`line`, `lengthprefix`, `beast`, `xmlevent`, or `message` for message transports), `decoder` (`name`, `options`). Secret `password` |

Decoders for `stream`:

| Decoder | Input | Source | Notes |
|---------|-------|--------|-------|
| `json` | JSON object or array, or a batch document with `records` | `options.source` (UNSPECIFIED) | Field map (`fields.latitude`, `fields.longitude` required; id, name, altitude, speed, heading, course, timestamp, callsign, …), `identityType`, `timeFormat`, `altitudeUnit`, `speedUnit`, `entityType`. The default map is the legacy position shape |
| `sbs` | BaseStation CSV (readsb port 30003) | ADS_B | MSG 2/3 fixes; MSG 1/4/5/6/8 as positionless partial observations |
| `beast` | Mode-S Beast binary (readsb port 30005, `framing: beast`) | ADS_B | Identification, CPR airborne position (global pair within 10 s, then local), velocity, status, Mode S altitude and squawk replies for known aircraft |
| `nmea-ais` | NMEA VDM/VDO (AIS-catcher, receivers) | AIS | Types 1–3, 5, 18, 19, 21, 24, 27; fragments reassembled per sender |
| `nmea-gps` | NMEA GGA/RMC | GPS | Needs `options.sourceEntityId` (the platform the receiver reports) |
| `cot` | Cursor on Target XML (`framing: xmlevent`) | COT | Affiliation and environment from the type; tasking events skipped |
| `link16` | JREAP-C binary (`framing: lengthprefix`) | LINK16 | J-series track messages |

A frame that carries nothing (a heartbeat, a partial fragment, an unsupported message) is counted as skipped by reason; a frame that fails to decode is dead-lettered; a decoder panic is recovered and dead-lettered.

Backpressure between a transport and its decoder: `tcpclient`, `tcpserver` and `websocket` block (TCP flow control reaches the sender), `udp` drops, `stomp` nacks, `mqtt` blocks up to 2 s then drops. All of it is counted (`frames_dropped_total`, `nacked_total`, `blocked_seconds_total`).

Recipes for readsb, AIS-catcher, a TAK server and a JSON API are in `docs/development.md`.

## Replicas and operations

- Environment: `KAFKA_BROKERS`, `KAFKA_OBSERVATION_TOPIC`, `KAFKA_DEADLETTER_TOPIC`, `DATABASE_URL`, `HTTP_ADDR`, `INGEST_SERVICE_TOKEN` (required in production), `REPLICA_ID` (hostname; the pod name in Kubernetes), `FEEDS_BOOTSTRAP_ENABLED` (built-ins inserted enabled on first start), `PUBLISHER_SHARDS`, `OBS_BUFFER`, `FRAME_BUFFER`, `RAW_PAYLOAD_MAX_BYTES`, `DLQ_RATE_PER_SEC`, `FEED_STALE_*`, `FEED_RETRY_*`, `LEASE_ENABLED`, `LEASE_TTL_SEC`, `LEASE_TICK_SEC`, `LEASE_REBALANCE_SEC`, `SHUTDOWN_TIMEOUT_SEC`.
- Scaling: `docker compose up -d --scale ingest-service=3`; the Helm chart's `ingestService.replicaCount`. Site receivers should be pulled (`tcpclient`) rather than listened for, so any replica can own them.
- Shutdown: readiness turns 503, the supervisor stops every driver and writes stopped status rows, the publisher drains, the producer flushes, leases and the replica row are released, bounded by `SHUTDOWN_TIMEOUT_SEC`.
- Metrics (`/metrics`): `frames_total`, `observations_total`, `decode_errors_total`, `skipped_total`, `duplicates_total`, `feeds_running`, `feed_last_success_timestamp`, `feed_entities_count`, `feed_errors_total`, `feed_restarts_total`, `leases_lost_total`, `lease_handovers_total`, `observations_published_total`, `observations_delivery_failed_total`, `deadletter_total`, `deadletter_dropped_total`, `publisher_shard_depth`, `publisher_blocked_seconds_total`, labelled by `feed_id` and `driver`.
- Kafka: `observations.raw` (24 partitions, 3 days, key = `ICAO24:ABC123` or `feed:<id>:<sourceEntityId>`), `observations.dlq` (3, 30 days). Headers on every message: `content-type`, `schema`, `schema-version`, `producer`, `feed-id`, `x-fence`; dead letters add `driver`, `decoder`, `reason`, `error`, `received-at`.

## Adding a driver

Everything lives under `apps/ingest-service/internal/source`.

1. **Decide the shape.** A new wire format over an existing transport is a decoder; a new way to fetch (an API with its own scheduling or request shaping) is a driver; a new way to move bytes is a transport.
2. **Decoder** (`decoder/<name>/`): implement `decoder.Decoder` (`Decode(transport.Frame) ([]*obs.Observation, error)`, `Close()`), fill the object fields of `obs.Observation` (source entity id, `Source`, entity type, environment, position with `SetPosition`, kinematics with `obs.Float`, identities with `AddIdentity`, a platform block), return `decoder.Skip(reason)` for frames that carry nothing and an error for frames that are broken. Register in `init()` with `decoder.Register(decoder.Definition{Name, Description, Source, ContentType, New})`, add the name to the `decoder.name` enum in `driver/stream/schema.json`, and write table tests on real frames.
3. **Driver** (`driver/<name>/`): embed a `schema.json`, implement `driver.Driver` (`Run(ctx, sink)`, `Health()`), use `httppoll.NewScheduler` and `httppoll.NewClient` for a poller, implement `aoi.AreaAware` when the area of interest shapes requests, and register in `init()` with `driver.Register(driver.Definition{ID, Name, Description, Source, Capabilities, SecretFields, AreaTTL, ConfigSchema, New})`. Emit through the sink (`Emit`, `Succeeded(count)`, `Skipped(reason)`, `DeadLetter`); never touch Kafka or the generated protobuf code.
4. **Transport** (`transport/<kind>/`): implement `transport.Transport` (`Run(ctx, emit)`, `State()`), honour `ErrBackpressure` from the emit, set `Listens` when it binds a port, and register with `transport.Register`.
5. **Register the package** with one import line in `source/all/all.go`, add a validation case to `drivers/catalog_test.go`, and a recipe to `docs/development.md`.
6. **Try it live**: create the feed through `POST /api/v1/feeds`, watch `GET /api/v1/feeds/{id}/status` and `/metrics`, and confirm observations on `observations.raw` and nothing unexpected on `observations.dlq`.
