# Emplacements

An emplacement is a place observations come from: a radar site, a mast, a ship's radar, a field observer. It is a resource of the connection it is bound to, and **its heartbeat is that connection's heartbeat**. A sender heartbeats each of its emplacements with an `EmplacementState`. The connection reports each one as a SENSOR entity, refuses the fixes of a silent one (`no_heartbeat`), and is as healthy as its emplacements: green while all heartbeat, amber while some do, red while none does. This page covers the emplacement's fields and limits, the management API, heartbeats, the site entity, the client, and how the senders of a `grpc` connection report with emplacements.

## Fixed and mobile

| Mobility | Position | Heartbeat |
|----------|----------|-----------|
| `FIXED` | Required. A radar site, a mast. Set by an operator or the sender's `UpsertEmplacement`. | An `EmplacementState` with only `emplacement_id` and `heartbeat_interval_sec`; its position and time are ignored. |
| `MOBILE` | Optional. A ship's radar, a vehicle, a person. Its own `EmplacementState` reports move it. | Its position reports (`EmplacementState` with a position), which also extend its track. |

## Fields

| Field | Meaning and limits |
|-------|--------------------|
| `id` | UUID, assigned on create. |
| `name` | Required, at most 255 bytes. Unique among live emplacements, whatever its case (`Lizard` and `lizard` collide). The name of a deleted emplacement can be used again. |
| `description` | Free text. |
| `mobility` | `FIXED` or `MOBILE`. |
| `sensorType` | `RADAR`, `EO_IR`, `RF`, `ACOUSTIC`, `AIS_RECEIVER`, `ADSB_RECEIVER`, `HUMAN` or `OTHER`. |
| `position` | `{lat, lon}`, latitude -90 to 90, longitude -180 to 180. Required when `FIXED`. |
| `altitude` | Metres above the WGS84 ellipsoid. Optional. |
| `heightAboveGroundM` | How far above the ground the sensor is mounted (a mast, a roof), 0 to 1000 m. Optional. A mounting height, not a position. A sensor with no `altitude` is at the terrain height under it plus this, which is where a camera's direction-only detections are followed from (`docs/feeds.md`, "Terrain"). A camera with neither reports every detection as a `no_origin_altitude` bearing. With an `altitude` it is not used. A sender sets it with `Emplacement.height_above_ground_m`; `UpsertEmplacement` keeps the stored value when the message carries none. |
| `boresightDeg` | The centre of the coverage sector, in degrees. Any finite number; it is stored normalised into [0, 360). Measured from `azimuthReference`. |
| `azimuthReference` | `TRUE_NORTH` (default) or `PLATFORM` (relative to the platform's heading: the client adds the live heading of a mobile emplacement's entity to the boresight when it draws the sector). |
| `coverage` | `{minRangeM, maxRangeM, azimuthWidthDeg, elevationMinDeg?, elevationMaxDeg?}`. See the coverage rule below. |
| `defaultSigma` | `{rangeM?, azimuthDeg?, elevationDeg?}`: one standard deviation of each polar measurement, used by reports that carry none of their own. Each is more than 0 when set. |
| `feedId` | The connection (a feed that takes data in) the emplacement is bound to; see the binding rules below. `""` in a PATCH unbinds it, with `active: false`. Deleting the connection soft-deletes the emplacements its senders made over gRPC and unbinds and deactivates the others (see "Deleting a connection"). |
| `classification` | `UNCLASSIFIED` (default), `CONFIDENTIAL`, `SECRET` or `TOP_SECRET`. |
| `exercise` | Marks the emplacement as part of an exercise. A report bound to the emplacement over gRPC takes the flag, and so does the emplacement's own SENSOR observation. An emplacement bound to an exercise connection (`config.exercise`) is exercise whatever its row says: its SENSOR observation is marked, `GetSession` and `ListEmplacements` report it as exercise, and an `UpsertEmplacement` over that connection stores the flag set. The row of an emplacement an operator made keeps its own value in REST and on the Connections page; only the entity and what senders are told are marked. |
| `active` | Default true. An inactive emplacement is kept but not reported, and an active one needs a connection. |
| `heartbeatIntervalSec`, `lastHeartbeatAt` | The interval the emplacement last declared and when its last heartbeat arrived (server time); see "Heartbeats". Read-only. |
| `lastReportAt` | When the emplacement last reported over its `grpc` connection (see "Reporting over gRPC"); empty until it has. Written at most every 10 s, without a version change or a notice. Read-only. |
| `createdBy`, `createdAt`, `updatedAt` | Read-only. `createdBy` is `ingest-token:<token id>` for an emplacement a sender made over gRPC. |
| `version` | Starts at 1 and rises on every write. It is the ETag (see below). |

**Coverage rule.** All zeros (or no `coverage`) means no coverage is known: nothing is drawn or checked. Otherwise `0 <= minRangeM < maxRangeM <= 2,000,000` (2000 km), `0 < azimuthWidthDeg <= 360` (360 is omnidirectional), each elevation between -90 and 90, and `elevationMinDeg` below `elevationMaxDeg` when both are given. A PATCH is validated as the whole emplacement it would leave, so a change that is valid field by field but leaves the emplacement invalid is refused.

## API

Served by the ingest service on `HTTP_ADDR` (`/emplacements`) and proxied by the gateway under `/api/v1/emplacements`.

| Method | Path | Description |
|--------|------|-------------|
| GET | `/emplacements?feedId=&mobility=&active=` | The emplacements the caller may see (array). `feedId` filters on the bound connection, `mobility` is `FIXED` or `MOBILE`, `active=true` lists the active ones; any other non-empty value lists the inactive ones. Each row also carries `heartbeatIntervalSec` and `lastHeartbeatAt` (what the row last recorded, throttled), plus the computed `heartbeat` (`online`, `offline`, `never` or `pending`), and `silentSince` while offline. These are present only for an active emplacement bound to a `grpc` connection. |
| POST | `/emplacements` | Create. The body takes the fields above, without the read-only ones. Answers 201 with the emplacement and an `ETag`. |
| GET | `/emplacements/{id}` | One emplacement, with `ETag: "<version>"`. |
| PATCH | `/emplacements/{id}` | Merge patch: a field that is absent is left alone. Takes `If-Match: "<version>"` (the client always sends it); without the header the write is unconditional. Answers 200 with the new `ETag`. |
| DELETE | `/emplacements/{id}` | Soft delete. Answers 204. |

**Heartbeat on the API.** `heartbeat` and `silentSince` come from the status the replica that runs the connection writes every 5 s (`detail.emplacements`), so they lag by up to 5 s. When no replica reports the connection (it never ran, is stopped, or its replica went silent for longer than the stale window), they come from the row's `lastHeartbeatAt` and `heartbeatIntervalSec`, which lag by up to an interval. A client may send them back in a PATCH: they are ignored.

**Binding rules.** An active emplacement needs a connection (422 at `/feedId`; the database holds to it with a CHECK). A PATCH that unbinds one (`feedId: ""`) must set `active: false` in the same PATCH. An emplacement can be bound only to a `grpc` connection, the one driver that heartbeats its emplacements: binding to another driver is 422. Rows bound to one before this rule can still be edited. A PATCH that changes `feedId` clears `lastHeartbeatAt` and `heartbeatIntervalSec`.

**Optimistic writes.** Every response carries `ETag: "<version>"`. A PATCH sends it back in `If-Match` (`"3"`, `3` and `W/"3"` are all read as version 3); a stale value answers 412 and changes nothing, so two editors cannot overwrite each other. `GET` only returns the `ETag`; it does not read `If-Match`. The Connections page sends the version the editor opened, even when the map's 30 s refresh has since loaded a newer one. On a 412 it tells the second editor that the emplacement was changed by someone else, loads the latest and keeps the editor's changes for the next save, which claims the latest version. An emplacement deleted since it was opened is reported as deleted by someone else, and nothing is sent.

**Statuses.**

| Status | Meaning |
|--------|---------|
| 400 | A bad body, a bad id, or an `If-Match` that is not a version (not a number, or below 1). |
| 404 | No such emplacement, it was deleted, or it is above the caller's clearance. A PATCH on an emplacement above the caller's clearance answers 404 whatever its `If-Match`, so a stale or absent header cannot tell whether it exists. |
| 409 | The name is taken among live emplacements (case-insensitive). |
| 412 | `If-Match` is stale. |
| 422 | `{error, details[{path, message}]}`, one entry per invalid field (`path` is a JSON pointer such as `/coverage/azimuthWidthDeg`). Also answered for a connection binding that names a feed that does not take data in, or a classification above the caller's own. |

**Roles.** Any authenticated user may read, and every role sees sites on the map and in the entity panel. `sentinel-operator` or `sentinel-admin` may create and edit, on the Connections page; delete needs `sentinel-admin`. Analysts no longer edit emplacements. The gateway enforces this in its policy table, and the ingest service enforces it again on the forwarded roles.

**Clearance.** An emplacement above the caller's clearance is hidden: it is left out of lists and answers 404. A caller cannot write a classification above their own (422).

**Storage and replicas.** The table is `sentinel.emplacements` (`docs/data-model.md`). Each ingest replica keeps every emplacement in memory. Every write sends a `sentinel_emplacements` notification (`{"id", "op"}`), and each replica applies it to its cache. If a refresh fails, the replica reconnects and reloads the whole cache rather than keep a partial one.

## Heartbeats

A sender heartbeats each emplacement it reports as with an `EmplacementState` at least every `heartbeat_interval_sec` seconds (1 to 300; 0 means 10; above 300 is `bad_state`). Heartbeats are never rate limited. The server records each one at its own receive time, never the sender's clock. It keeps them in memory on the replica that runs the connection, and that replica is the truth.

- **States.**
  - `online`: a heartbeat within three intervals.
  - `offline`: heard from, but not since then.
  - `never`: not heard from since the connection started.
  - `pending`: not heard from yet in this run, during the startup grace, which is the larger of three intervals and 30 s after the connection starts.
- **Fixes.** A report bound to an emplacement that is not online is `REJECTED` `no_heartbeat`, before any other check of its content. A connection with no emplacements at all is not affected.
- **The row.** `lastHeartbeatAt` and `heartbeatIntervalSec` are written at most every interval (and every 10 s), with no version change and no notice. A replica that takes the connection over (after a restart or a lease move) seeds itself from the heartbeats the rows recorded. A seeded heartbeat still inside its three intervals reads online. One that has already lapsed reads pending through the startup grace, since its sender may not have reconnected to the new replica yet, and offline after it: a site heard from once never reads never. Its next real heartbeat replaces the seed. A site heard in this run that then goes silent reads offline at once, grace or not. Pending is not online, so its fixes are still refused as `no_heartbeat`.
- **Connection status.** A connection with emplacements is as healthy as their heartbeats, not as recent as its last report.
  - All online, or online and pending: healthy, running (`detail.heartbeat: "pending"` while some wait).
  - Some silent while others are online or pending: warn, degraded.
  - None online or pending: critical, degraded.

  `detail.emplacements` lists each with its state, interval and last heartbeat. An entry above the caller's clearance is left out, and its names are blanked for a caller not cleared for the connection.

## The site entity

Each active emplacement is reported as an entity of type `SENSOR` by its own connection, under the canonical `EMPLACEMENT` identity, so it is in search and can anchor a geofence ring. Its source entity id is the emplacement id, and it carries the emplacement's name, classification (raised to the connection's floor) and exercise flag (ORed with the connection's).

- **A grpc connection** publishes it at most once a second on a heartbeat, and again every `min(interval, 25 s)` whatever its state, at once when the state changes, and once more as offline when the connection stops. Each publish carries `operationalStatus` (`OPERATIONAL` online, `UNKNOWN` pending, `INACTIVE` otherwise) and a `generic` platform detail. Its fields are `heartbeat`, `heartbeatIntervalSec`, `lastHeartbeatAt` and, while silent, `silentSince`. A re-publish is stamped with the last heartbeat, never the current time, but never earlier than the connection's start on this replica: the row's heartbeat is throttled and older than what the previous replica published, which entity-service would otherwise drop as out of date.
- **Another driver's connection** (tcp, udp, tak) can hold an emplacement only as a row bound before the grpc-only rule. It reports such a fixed emplacement every 25 s, at start and on every change, so the site stays in search. This is a reduced publisher inside the connection's runner, not a feed, and its reports carry no heartbeat state. These sites stay violet, and they do not count as the connection succeeding. Rebind them to a `grpc` connection to have them heartbeat.
- A site's own report is never suppressed as a repeat.

The sensor type picks the source label:

| Sensor type | Source |
|-------------|--------|
| `RADAR` | `RADAR` |
| `AIS_RECEIVER` | `AIS` |
| `ADSB_RECEIVER` | `ADS_B` |
| `HUMAN` | `HUMINT` |
| `RF` | `SIGINT` |
| anything else | `MASINT` |

**Age-out.** Site entities use the global per-source thresholds. A running connection publishes each site at least every 25 s, inside the shortest stale threshold (RADAR's 30 s), and every applied update refreshes the entity's last-seen time, so a running connection's sites never go stale or age out, silent ones included. That holds across a restart or a lease move: the new run stamps its publishes no earlier than its own start, so they are newer than the previous run's last publish and are applied. Those of a stopped or deleted connection do age out.

## The client

- **Connections page.** An inbound connection's expanded row has an **Emplacements** section. Each emplacement is shown with its badge (**online**, **silent since 14:02Z** or **awaiting first heartbeat**), its interval, and **Edit**, **Deactivate** and, for admins, **Delete**.
  - **+ New emplacement** appears on grpc connections only.
  - The editor is locked to the connection: an exercise connection's emplacements are exercise, and no classification below the connection's is offered.
  - Position is typed; **Show on map** flies the map there.
  - The row summary reads "2 of 3 sites online", and the page's `?open=<id>` expands a row.
  - An **Unassigned emplacements** group at the top lists those with no connection, to assign to a grpc connection (which activates them) or delete. It is gone when empty.
- **Map.** Each site is drawn under its connection's row in the layers pill, as a violet (`#8b5cf6`) location dot (dashed when mobile) with its coverage. A silent site is a grey hollow dot with a dim footprint, labelled "silent since 14:02Z". "Silent" is judged in the browser against the server's clock, from the site entity's heartbeat fields, so it turns grey within seconds of the third missed heartbeat. A click selects the site's SENSOR entity.
- **Entity detail.** A site's panel shows **Heartbeat: online · every 5 s** (or offline since …), and for operators its **Emplacement** name links to `/connections?open=<connection>`.
- The Emplacements page is gone; `#/emplacements` opens the map.

## Reporting over gRPC

An emplacement bound to a `grpc` connection (`feedId`) is what that connection's senders report as. The driver's settings and the ack reasons are in `docs/feeds.md`; this section is what the emplacement does to a report. "The connection's level" below is the connection's classification, and a token's ceiling is the classification it was issued with.

**Binding.** A report names its emplacement in `Report.emplacement_id` (an `emplacement_state` body may repeat it in its own `emplacement_id`). Ids are compared without regard to case or surrounding space, so `ABC…` claims the emplacement `abc…`. An empty one means the connection's only emplacement (active, bound to this connection and not above the token's ceiling). Only a connection with **no emplacements at all** takes unbound reports: positional ones, with no emplacement. The count includes inactive emplacements and those above the token's ceiling, so an operator who deactivates or rebinds a connection's only emplacement does not turn its sender's reports into unbound ones, which would lose the emplacement's classification and `exercise` flag. Every other miss is `unknown_emplacement`: an id that does not exist, one bound to another connection, an inactive emplacement, one higher than the token's ceiling, or an empty id on a connection whose emplacements are not exactly one usable one. The answer is the same for all of them, so a sender learns nothing about emplacements it cannot use.

A token whose ceiling is below the connection's level could not hold anything it sends, so every report from it, whatever its body (including none), is `REJECTED` `above_ceiling` and never dead-lettered.

A bound report:

- carries the emplacement as its sensor (its `sensor_id` and `emplacement_id` are the emplacement's id);
- takes its source from the sensor type (the table above) when it sets none;
- takes the emplacement's classification when that is above the report's own, and the emplacement's `exercise` flag.

A target never carries the `EMPLACEMENT` identity: only an emplacement's own report is its entity, and a target that carried one would fuse into the sensor that saw it. Sentinel removes every `EMPLACEMENT` identity a target is sent before it is keyed, so a target whose only identity is `EMPLACEMENT` is `missing_identity`. A bound target whose `source_entity_id` is the emplacement's own id is `REJECTED` `bad_identity`: the emplacement's own SENSOR observation is keyed on that id, and the two would share a repeat key. On the wire an unset sub-message and an empty one are the same, and decode as an empty message, so an observation with nothing in it is an ordinary observation: `missing_identity` (dead-lettered, or `REJECTED` when the report is above the connection's level, as for any validation failure). Only a report with no body at all is `empty_report`.

**Polar measurements.** A bound report with a `polar` measurement and no position is placed from the emplacement: from a fixed one's stored place, and from a mobile one's track at the report's time (`source_timestamp`, else arrival). An unbound report with `polar` and no position is `unknown_emplacement`, as there is nothing to place it from. A report that has both a position and a `polar` keeps its position (the altitude-only form below excepted).

- **Azimuth.** The measurement's `azimuth_reference` is used, else the emplacement's. A `PLATFORM` azimuth adds the platform heading. A fixed emplacement has none, and a mobile one has one only when its fixes carried a heading; without one the report is `bad_polar`.
- **Elevation.** With an elevation, the target lies along it from the emplacement. Without one, the target is at the report's altitude when it has one, else the emplacement's (0 when a fixed one has none).
- **Direction only.** A measurement with `direction_only` (a camera) carries no usable range: `range_m` is ignored and an elevation is required (without one the report is `bad_polar`). The line from the emplacement along the azimuth and elevation is followed down to the terrain (`docs/feeds.md`, "Terrain"), and the detection is placed where it meets the ground, as an observation with a position covariance and the track environment `GROUND`. The line starts at the emplacement's `altitude`, else at the terrain height under it plus `heightAboveGroundM`, and is followed up to `coverage.maxRangeM` (else `INGEST_TERRAIN_MAX_RANGE_KM`, 30 km). The detection is instead recorded as a **bearing** on the emplacement's site entity, and acked `OK`, when the target is said to be in the air (`air_target`), the line meets no ground within range (`no_ground_intersection`), the terrain is off or unavailable (`terrain_unavailable`), or the emplacement has no origin altitude (`no_origin_altitude`). The site entity carries the lines in its generic fields `bearings` and `bearingTtlSec` (the connection's `bearingTtlSec`, default 10 s); at most 50 are kept, and the map draws each as a dashed line from the camera. `status.detail.bearings` counts every bearing received, by reason, including ones since replaced or already past their time to live.
- **Radar height.** A radar report placed from a range polar measurement also meets the terrain, when it is on: a ground target is put on the ground, and an air target gets `RadarDetail.height_above_ground_m`, its height over the terrain below it. The sender's own value of that field is always dropped. `docs/sensor-sim.md` simulates both sensors.
- **Altitude-only position.** A `position` of latitude 0 and longitude 0 with `altitude_meters` set carries only the target's altitude: it is no fix. A bound polar report in this form is converted at that altitude; an unbound one is `unknown_emplacement`.
- **Covariance.** The position's covariance comes from the measurement's sigmas, each falling back to the emplacement's `defaultSigma`. A covariance with a non-finite term is dropped and the report goes on without one.
- **Reasons.** `bad_polar` applies to any report that carries a polar measurement, including one that also has a position and one that is unbound, and is a direction-only measurement with no elevation, a range below 0 or above 2e7 m (20,000 km), an elevation outside -90 to 90, a non-finite value, a sigma that is not above 0, a `PLATFORM` azimuth with no heading, an altitude the ray never reaches, or a converted position that is not finite. `stale_ownship` is a mobile emplacement with no position for the time of the report: before the first fix its track holds, or more than `ownshipToleranceMs` (default 2000, at most 60000) past the newest. `outside_coverage` applies only with `enforceCoverage`: a bound report with a position (given or converted) is held to the emplacement's coverage as seen from where it stood (range, half the azimuth width either side of the boresight, elevation); an emplacement with no coverage holds everything. A misconfiguration to avoid: a FIXED emplacement with a `PLATFORM` azimuth reference and a sector coverage (an azimuth width under 360) cannot resolve its boresight without a platform heading, so under `enforceCoverage` it refuses every report as `outside_coverage`.

**Position reports (`EmplacementState`).** A mobile emplacement's heartbeat is its position report: an `emplacement_state` with `position`, optional `heading`, `velocity` (north, east and up in m/s) and `time` (the server's clock when unset). An unbound one is `unknown_emplacement`, and so is a body whose `emplacement_id` differs from the report's.

- **Malformed.** A missing position, a position outside WGS84 bounds, a non-finite altitude, heading or velocity, or an invalid `time` is `bad_state`. It is dead-lettered, or `REJECTED` when the emplacement is above the connection's level. An `emplacement_state` with nothing in it has no position, so it is `bad_state` (or `unknown_emplacement`, when the report names no bound mobile emplacement); on the wire it cannot be told from an unset one, so it is never `empty_report`.
- **Clock skew.** A `time` more than 5 s ahead of the server's clock, or more than 60 s behind it (the length of the track), is `REJECTED` `clock_skew`, never dead-lettered: it is a sender's clock to mend, not a malformed report, and the report is not stored. A report that is both malformed and skewed is `bad_state`.
- **Track.** The fix extends the emplacement's track, which is where its polar measurements are measured from. The track keeps the last 60 s behind its newest fix, and at most 2400 fixes: when it reaches 2400 it is cut to the newest 1200 (a 1 Hz sender holds about 60). A fix at a time the track already holds replaces it. A measurement is placed by interpolating between the fixes around its time, or, up to `ownshipToleranceMs` after the newest, by carrying the newest on along its velocity. The track lives in the memory of the replica that runs the connection; after a restart measurements are `stale_ownship` until the next fix.
- **The row.** `lastReportAt` and the stored position are written at most every 10 s for each emplacement, throttled on the server's clock and without a version change or a notice. They record the earlier of the fix time and the server's time, so a sender with a fast clock can neither stall nor flood the writes, and `lastReportAt` never moves back. Position writes and the report-only writes an accepted observation makes are throttled apart, so a sender that streams both keeps its position current. The position is written only when the emplacement is at or above the connection's level; below it, only `lastReportAt` is written, so a position reported over a more highly classified connection never lands in the row. An accepted observation from a bound emplacement, fixed or mobile, also sets `lastReportAt` (at the same 10 s interval).
- **The entity.** The emplacement's SENSOR entity moves at most once a second, to the fix's position, with its heading and velocity, at the earlier of the fix time and the server's time, marked online.

**Sender-managed emplacements (`UpsertEmplacement`).** With `senderManagedEmplacements` on, a token may create and update emplacements on its connection. Without it the call is `PermissionDenied`, and so is a token below the connection's level; a replica that cannot write answers `Unavailable` (retry).

- **Which.** An `id` names one to update: it must be bound to this connection, made by one of its senders and not above the token's ceiling, else `NotFound` (the same answer for each). An empty id, or one that names nothing, first looks for one of the senders' own emplacements with the same name (ignoring case and surrounding space) on this connection, not above the token's ceiling, and updates it, so a retried upsert is idempotent. Failing that it creates one with an id Sentinel assigns, up to `maxEmplacements` (default 16, at most 256) for the connection, else `ResourceExhausted` (an update by name is not a new row, so the limit does not apply). A name that any other emplacement holds (an operator's, another connection's, or one above the token) is `AlreadyExists`; an invalid emplacement, a missing one or a classification above the token's ceiling is `InvalidArgument`; a store failure is `Internal`.
- **Sentinel's to set.** The emplacement is bound to the connection, and `active` and `createdBy` (`ingest-token:<token id>`) are Sentinel's, never the sender's: a create is active, an update keeps `active`. The classification is raised to the connection's level when below it. On an exercise connection (`config.exercise`) the emplacement is always stored with `exercise` set, on create and on update, whatever the message says.
- **What an upsert replaces.** On every upsert the name, description and boresight are replaced, so a zero value clears the stored one. The `exercise` flag is sticky: an update sets it when the message does and never clears it, so a sender can mark an emplacement as part of an exercise but only a person (over REST) can take the mark off (on an exercise connection the next upsert sets it again). An enum left unset (mobility, sensor type, azimuth reference, classification) and a sub-message left out (position, coverage, default sigma) keep what is stored, or take the defaults on a create. A position sent without an altitude clears the stored altitude. `height_above_ground_m` is set when the message carries one and kept when it does not.
- **Cache.** The call waits up to 2 s for the emplacement cache to hold the write, so that the next report can claim the emplacement. If the cache is slower than that, the call answers `Unavailable` ("the emplacement was saved; retry once the service has caught up"): the write succeeded, and the retry, which finds the row by name, is idempotent.
- **Rate limits.** An upsert draws one token from the sender's own bucket (`maxMessagesPerSecPerSender`) and then from the connection's (`maxMessagesPerSec`), as a report does; when either is empty it is `ResourceExhausted` ("rate limited").
- **A person editing one.** A PATCH on a sender's MOBILE emplacement ignores `position` and `altitude` (not `heightAboveGroundM`, a mounting height the sender does not report) while it stays mobile, as its sender reports where it is and a position set by hand would be overwritten at the next report. A PATCH that makes it `FIXED` is a person choosing where it is, and that position applies. Its other fields apply, until the sender's next upsert replaces the name, description and boresight again. A PATCH is also how the `exercise` flag is cleared.

**Deleting a connection.** Deleting a connection soft-deletes the emplacements its senders made, and unbinds and deactivates the others, which then appear under Unassigned (`feedId` null, version raised). Rows deleted earlier are let go of the connection too. Each change sends a notice, so every replica's cache follows.

**Known differences.** The altitudes Sentinel stores and converts with, including a polar measurement's, are metres above the WGS84 ellipsoid, as in `altitude` above. The proto comment on `Coordinate.altitude_meters` says mean sea level, but Sentinel takes a sender's `altitude_meters` as an ellipsoidal height and applies no geoid correction, so a sender that measures above mean sea level is off by the geoid height (roughly 100 m either way). The terrain heights Sentinel looks up are converted the other way round: the Copernicus model is above mean sea level (EGM2008), and the ingest service adds the geoid height to give an ellipsoidal height, so a placed camera detection, a radar target clamped to the ground and a polar-converted position all use the same datum. The map's own terrain (Cesium World Terrain) is a separate surface, so a ground track and Copernicus's idea of the ground can differ by the surface model's error and cell size.

## The exercise flag

The `exercise` flag travels end to end: from a connection (`config.exercise`) or an emplacement onto observations, onto entities as a sticky mark (an exercise report marks its entity even when the report is a duplicate or loses a newer-fix check, and no path clears an entity's mark once set), into the map (` · EX`), the detail panel (`EXERCISE`), search (`EX`), exports, and alerts (stamped by both `alert-service` and `analytics-service`; if an entity lookup fails when making an alert, the alert is marked exercise so it fails closed), and outbound connections refuse exercise entities (removing a tracked entity at once) and their alerts unless `policy.allowExercise` is set. Only a `grpc` connection reports a mobile emplacement's position; a mobile emplacement bound to another driver (an older row) stays off the map (see "The site entity" above).
