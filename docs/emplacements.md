# Emplacements

An emplacement is a place observations come from: a radar site, a mast, a ship's radar, a field observer. Sentinel keeps them as records of their own, apart from the entities they observe, so that a report can say which emplacement made it and where the observer stood. This page covers what an emplacement is, its fields and limits, the management API, the built-in `emplacements` feed that puts each one on the map and in search, the client, and how senders of a `grpc` connection use emplacements: reports bound to one, polar measurements placed from it, a mobile one's own position, and emplacements the senders manage themselves.

## Fixed and mobile

| Mobility | Position | Reported |
|----------|----------|----------|
| `FIXED` | Required. A radar site, a mast. | On every heartbeat of the `emplacements` feed, and on every change. |
| `MOBILE` | Optional. A ship's radar, a vehicle, a person. A mobile emplacement with no position is stored but not reported. | On every change (its position is edited), not on the heartbeat. A mobile emplacement bound to a connection is not reported by this feed at all: see "The built-in `emplacements` feed". |

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
| `boresightDeg` | The centre of the coverage sector, in degrees. Any finite number; it is stored normalised into [0, 360). Measured from `azimuthReference`. |
| `azimuthReference` | `TRUE_NORTH` (default) or `PLATFORM` (relative to the platform's heading: the client adds the live heading of a mobile emplacement's entity to the boresight when it draws the sector). |
| `coverage` | `{minRangeM, maxRangeM, azimuthWidthDeg, elevationMinDeg?, elevationMaxDeg?}`. See the coverage rule below. |
| `defaultSigma` | `{rangeM?, azimuthDeg?, elevationDeg?}`: one standard deviation of each polar measurement, used by reports that carry none of their own. Each is more than 0 when set. |
| `feedId` | Optional binding to a connection (a feed that takes data in). `""` in a PATCH unbinds it. Deleting the connection soft-deletes the emplacements its senders made over gRPC and unbinds the others (see "Deleting a connection"). |
| `classification` | `UNCLASSIFIED` (default), `CONFIDENTIAL`, `SECRET` or `TOP_SECRET`. |
| `exercise` | Marks the emplacement as part of an exercise. A report bound to the emplacement over gRPC takes the flag, and so does the emplacement's own SENSOR observation. An emplacement bound to an exercise connection (`config.exercise`) is exercise whatever its row says: its SENSOR observation is marked, and an `UpsertEmplacement` over that connection stores the flag set. |
| `active` | Default true. An inactive emplacement is kept but not reported. |
| `lastReportAt` | When the emplacement last reported over its `grpc` connection (see "Reporting over gRPC"); empty until it has. Written at most every 10 s, without a version change or a notice. Read-only. |
| `createdBy`, `createdAt`, `updatedAt` | Read-only. `createdBy` is `ingest-token:<token id>` for an emplacement a sender made over gRPC. |
| `version` | Starts at 1 and rises on every write. It is the ETag (see below). |

**Coverage rule.** All zeros (or no `coverage`) means no coverage is known: nothing is drawn or checked. Otherwise `0 <= minRangeM < maxRangeM <= 2,000,000` (2000 km), `0 < azimuthWidthDeg <= 360` (360 is omnidirectional), each elevation between -90 and 90, and `elevationMinDeg` below `elevationMaxDeg` when both are given. A PATCH is validated as the whole emplacement it would leave, so a change that is valid field by field but leaves the emplacement invalid is refused.

## API

Served by the ingest service on `HTTP_ADDR` (`/emplacements`) and proxied by the gateway under `/api/v1/emplacements`.

| Method | Path | Description |
|--------|------|-------------|
| GET | `/emplacements?feedId=&mobility=&active=` | The emplacements the caller may see (array). `feedId` filters on the bound connection, `mobility` is `FIXED` or `MOBILE`, `active=true` lists the active ones; any other non-empty value lists the inactive ones. |
| POST | `/emplacements` | Create. The body takes the fields above, without the read-only ones. Answers 201 with the emplacement and an `ETag`. |
| GET | `/emplacements/{id}` | One emplacement, with `ETag: "<version>"`. |
| PATCH | `/emplacements/{id}` | Merge patch: a field that is absent is left alone. Takes `If-Match: "<version>"` (the client always sends it); without the header the write is unconditional. Answers 200 with the new `ETag`. |
| DELETE | `/emplacements/{id}` | Soft delete. Answers 204. |

**Optimistic writes.** Every response carries `ETag: "<version>"`. A PATCH sends it back in `If-Match` (`"3"`, `3` and `W/"3"` are all read as version 3); a stale value answers 412 and changes nothing, so two editors cannot overwrite each other. `GET` only returns the `ETag`; it does not read `If-Match`. The Emplacements panel sends the version the editor opened, even when the map's 30 s refresh has since loaded a newer one. On a 412 it tells the second editor that the emplacement was changed by someone else, loads the latest and keeps the editor's changes for the next save, which claims the latest version. An emplacement deleted since it was opened is reported as deleted by someone else, and nothing is sent.

**Statuses.**

| Status | Meaning |
|--------|---------|
| 400 | A bad body, a bad id, or an `If-Match` that is not a version (not a number, or below 1). |
| 404 | No such emplacement, it was deleted, or it is above the caller's clearance. A PATCH on an emplacement above the caller's clearance answers 404 whatever its `If-Match`, so a stale or absent header cannot tell whether it exists. |
| 409 | The name is taken among live emplacements (case-insensitive). |
| 412 | `If-Match` is stale. |
| 422 | `{error, details[{path, message}]}`, one entry per invalid field (`path` is a JSON pointer such as `/coverage/azimuthWidthDeg`). Also answered for a connection binding that names a feed that does not take data in, or a classification above the caller's own. |

**Roles.** Any authenticated user may read. `sentinel-operator`, `sentinel-analyst` or `sentinel-admin` may create and edit; delete needs `sentinel-admin`. The gateway enforces this in its policy table and the ingest service enforces it again on the forwarded roles.

**Clearance.** An emplacement above the caller's clearance is hidden: it is left out of lists and answers 404. A caller cannot write a classification above their own (422).

**Storage and replicas.** The table is `sentinel.emplacements` (`docs/data-model.md`). Each ingest replica keeps every emplacement in memory. Every write sends a `sentinel_emplacements` notification (`{"id", "op"}`), and each replica applies it to its cache. If a refresh fails, the replica reconnects and reloads the whole cache rather than keep a partial one.

## The built-in `emplacements` feed

The `emplacements` feed (driver `emplacement`) reports each emplacement as an entity, so it is in search and can anchor a geofence ring. A mobile emplacement bound to a connection is left to that connection's reports, which move its entity (see "Reporting over gRPC"). It is built in and enabled by default, because it needs no credentials. The driver is internal: no connection can be created on it, and the new-connection picker hides it. The Connections page still shows its row, where it can be disabled and its heartbeat changed.

| Setting | Default | Meaning |
|---------|---------|---------|
| `heartbeatSec` | 60 (minimum 10) | How often fixed emplacements are reported again so their entities stay live. |

What it reports:

- Every active emplacement that has a position, once at start and again on every change.
- Fixed emplacements again on every heartbeat. Mobile ones are reported only when the feed starts and when they change, never on the heartbeat.
- **Not a mobile emplacement bound to any connection.** The mirror skips it, at start and on every change. Only a `grpc` connection reports positions today (its `EmplacementState` reports, below), so a mobile emplacement bound to a connection of another driver is off the map until something reports it: its entity is not refreshed, goes stale and ages out (see Age-out). Unbind it (`feedId` `""`) and the mirror reports its stored position again.
- Each as an entity of type `SENSOR`, with a source entity id equal to the emplacement id, the emplacement's name, classification and exercise flag (also set when the emplacement is bound to an exercise connection; marking or unmarking the connection re-reports its emplacements within a few seconds, without waiting for the heartbeat), and, when the boresight is measured from `TRUE_NORTH`, the boresight as the heading. A `PLATFORM` boresight is relative to the platform's own heading, so it is not reported as one. A fixed emplacement is a ground entity.
- Under the canonical `EMPLACEMENT` identity, so the entity service always fuses an emplacement's reports into the same entity. Only the service gives a report that identity: the feed runner drops every `EMPLACEMENT` identity a sender supplies, so no other report can join an emplacement's entity or hide behind one.
- Never suppressed as a repeat. The runner's repeat suppression drops a fix that repeats the last position within a second; the `emplacement` driver turns it off, so an edit that keeps the position (a new name, a raised classification) always reaches the entity.

The sensor type picks the source label:

| Sensor type | Source |
|-------------|--------|
| `RADAR` | `RADAR` |
| `AIS_RECEIVER` | `AIS` |
| `ADSB_RECEIVER` | `ADS_B` |
| `HUMAN` | `HUMINT` |
| `RF` | `SIGINT` |
| anything else | `MASINT` |

**Age-out.** `db/schema/72_emplacements.sql` seeds a stale and age-out row for each of these sources on the `emplacements` feed: an entity goes stale after 3 minutes without a report and ages out after 10, which is three and ten missed heartbeats at the default for a fixed emplacement. The thresholds make no exception for mobility: because a mobile emplacement is reported only at start and on change, an unbound one's SENSOR entity goes stale after 3 minutes and ages out after 10 unless it is edited. A bound one's entity is refreshed by its own position reports over gRPC, and goes stale and ages out on the same schedule when they stop. **Deactivating or deleting** an emplacement stops its reports, so its entity goes stale and then ages out on that schedule; it does not disappear at once. Editing it again, or setting it active, reports it at once.

## The client

- **Panel.** The Emplacements panel (nav key `e`, route `#/emplacements`) lists emplacements, filtered by mobility, sensor type and state (active or inactive), and edits one at a time. **Pick on map** lets you click the map for the position, and a preview of the footprint follows the form's boresight and range as you type. Saving keeps it. A conflict (412) is reported in the form, the latest is loaded and the edits are kept for another save.
- **Coverage.** A **Coverage** checkbox in the panel shows or hides the footprints. The choice is remembered per browser. Each emplacement is drawn on the map as a violet (`#8b5cf6`) three-ring location dot (`0.7` scale, `0.9` when selected; dashed outer ring when mobile) with its coverage as a sector (a sector across the antimeridian is one wedge); a mobile emplacement's dot and footprint follow its live position. An omnidirectional coverage (360°) is a circle, or a ring when `minRangeM` is above 0. Inactive emplacements are not drawn unless selected.
- **Layer.** The built-in feed's row in the layers pill shows or hides the whole layer (markers and footprints). A layer that was hidden stays hidden when the map loads.
- **Entity detail.** The detail panel of a sensor entity that comes from an emplacement shows **Emplacement: <name>**, which opens the panel with it selected.

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
- **Altitude-only position.** A `position` of latitude 0 and longitude 0 with `altitude_meters` set carries only the target's altitude: it is no fix. A bound polar report in this form is converted at that altitude; an unbound one is `unknown_emplacement`.
- **Covariance.** The position's covariance comes from the measurement's sigmas, each falling back to the emplacement's `defaultSigma`. A covariance with a non-finite term is dropped and the report goes on without one.
- **Reasons.** `bad_polar` applies to any report that carries a polar measurement, including one that also has a position and one that is unbound, and is a range below 0 or above 2e7 m (20,000 km), an elevation outside -90 to 90, a non-finite value, a sigma that is not above 0, a `PLATFORM` azimuth with no heading, an altitude the ray never reaches, or a converted position that is not finite. `stale_ownship` is a mobile emplacement with no position for the time of the report: before the first fix its track holds, or more than `ownshipToleranceMs` (default 2000, at most 60000) past the newest. `outside_coverage` applies only with `enforceCoverage`: a bound report with a position (given or converted) is held to the emplacement's coverage as seen from where it stood (range, half the azimuth width either side of the boresight, elevation); an emplacement with no coverage holds everything. A misconfiguration to avoid: a FIXED emplacement with a `PLATFORM` azimuth reference and a sector coverage (an azimuth width under 360) cannot resolve its boresight without a platform heading, so under `enforceCoverage` it refuses every report as `outside_coverage`.

**Position reports (`EmplacementState`).** A mobile emplacement tells Sentinel where it is with a report whose body is an `emplacement_state` (`position`, optional `heading`, `velocity` as north, east and up in m/s, and `time`, the server's clock when unset). It is taken only for a bound MOBILE emplacement; a fixed or unbound one is `unknown_emplacement`, and so is a body whose `emplacement_id` differs from the report's.

- **Malformed.** A missing position, a position outside WGS84 bounds, a non-finite altitude, heading or velocity, or an invalid `time` is `bad_state`. It is dead-lettered, or `REJECTED` when the emplacement is above the connection's level. An `emplacement_state` with nothing in it has no position, so it is `bad_state` (or `unknown_emplacement`, when the report names no bound mobile emplacement); on the wire it cannot be told from an unset one, so it is never `empty_report`.
- **Clock skew.** A `time` more than 5 s ahead of the server's clock, or more than 60 s behind it (the length of the track), is `REJECTED` `clock_skew`, never dead-lettered: it is a sender's clock to mend, not a malformed report, and the report is not stored. A report that is both malformed and skewed is `bad_state`.
- **Track.** The fix extends the emplacement's track, which is where its polar measurements are measured from. The track keeps the last 60 s behind its newest fix, and at most 2400 fixes: when it reaches 2400 it is cut to the newest 1200 (a 1 Hz sender holds about 60). A fix at a time the track already holds replaces it. A measurement is placed by interpolating between the fixes around its time, or, up to `ownshipToleranceMs` after the newest, by carrying the newest on along its velocity. The track lives in the memory of the replica that runs the connection; after a restart measurements are `stale_ownship` until the next fix.
- **The row.** `lastReportAt` and the stored position are written at most every 10 s for each emplacement, throttled on the server's clock and without a version change or a notice. They record the earlier of the fix time and the server's time, so a sender with a fast clock can neither stall nor flood the writes, and `lastReportAt` never moves back. Position writes and the report-only writes an accepted observation makes are throttled apart, so a sender that streams both keeps its position current. The position is written only when the emplacement is at or above the connection's level; below it, only `lastReportAt` is written, so a position reported over a more highly classified connection never lands in the row. An accepted observation from a bound emplacement, fixed or mobile, also sets `lastReportAt` (at the same 10 s interval).
- **The entity.** The emplacement's SENSOR entity moves at most once a second, to the fix's position, with its heading and velocity, at the earlier of the fix time and the server's time. This is why the built-in feed does not report a bound mobile emplacement.

**Sender-managed emplacements (`UpsertEmplacement`).** With `senderManagedEmplacements` on, a token may create and update emplacements on its connection. Without it the call is `PermissionDenied`, and so is a token below the connection's level; a replica that cannot write answers `Unavailable` (retry).

- **Which.** An `id` names one to update: it must be bound to this connection, made by one of its senders and not above the token's ceiling, else `NotFound` (the same answer for each). An empty id, or one that names nothing, first looks for one of the senders' own emplacements with the same name (ignoring case and surrounding space) on this connection, not above the token's ceiling, and updates it, so a retried upsert is idempotent. Failing that it creates one with an id Sentinel assigns, up to `maxEmplacements` (default 16, at most 256) for the connection, else `ResourceExhausted` (an update by name is not a new row, so the limit does not apply). A name that any other emplacement holds (an operator's, another connection's, or one above the token) is `AlreadyExists`; an invalid emplacement, a missing one or a classification above the token's ceiling is `InvalidArgument`; a store failure is `Internal`.
- **Sentinel's to set.** The emplacement is bound to the connection, and `active` and `createdBy` (`ingest-token:<token id>`) are Sentinel's, never the sender's: a create is active, an update keeps `active`. The classification is raised to the connection's level when below it.
- **What an upsert replaces.** On every upsert the name, description and boresight are replaced, so a zero value clears the stored one. The `exercise` flag is sticky: an update sets it when the message does and never clears it, so a sender can mark an emplacement as part of an exercise but only a person (over REST) can take the mark off. An enum left unset (mobility, sensor type, azimuth reference, classification) and a sub-message left out (position, coverage, default sigma) keep what is stored, or take the defaults on a create. A position sent without an altitude clears the stored altitude.
- **Cache.** The call waits up to 2 s for the emplacement cache to hold the write, so that the next report can claim the emplacement. If the cache is slower than that, the call answers `Unavailable` ("the emplacement was saved; retry once the service has caught up"): the write succeeded, and the retry, which finds the row by name, is idempotent.
- **Rate limits.** An upsert draws one token from the sender's own bucket (`maxMessagesPerSecPerSender`) and then from the connection's (`maxMessagesPerSec`), as a report does; when either is empty it is `ResourceExhausted` ("rate limited").
- **A person editing one.** A PATCH on a sender's MOBILE emplacement ignores `position` and `altitude` while it stays mobile, as its sender reports where it is and a position set by hand would be overwritten at the next report. A PATCH that makes it `FIXED` is a person choosing where it is, and that position applies. Its other fields apply, until the sender's next upsert replaces the name, description and boresight again. A PATCH is also how the `exercise` flag is cleared.

**Deleting a connection.** Deleting a `grpc` connection soft-deletes the emplacements its senders made, and unbinds the others (`feedId` null, version raised). Each change sends a notice, so every replica's cache follows.

**Known differences.** The altitudes Sentinel stores and converts with, including a polar measurement's, are metres above the WGS84 ellipsoid, as in `altitude` above. The proto comment on `Coordinate.altitude_meters` says mean sea level, but Sentinel takes a sender's `altitude_meters` as an ellipsoidal height and applies no geoid correction, so a sender that measures above mean sea level is off by the geoid height (roughly 100 m either way).

## Not yet built

The `exercise` flag now travels end to end: from a connection (`config.exercise`) or an emplacement onto observations, onto entities as a sticky mark (an exercise report marks its entity even when the report is a duplicate or loses a newer-fix check, and no path clears an entity's mark once set), into the map (` · EX`), the detail panel (`EXERCISE`), search (`EX`), exports, and alerts (stamped by both `alert-service` and `analytics-service`; if an entity lookup fails when making an alert, the alert is marked exercise so it fails closed), and outbound connections refuse exercise entities (removing a tracked entity at once) and their alerts unless `policy.allowExercise` is set. Only a `grpc` connection reports a mobile emplacement's position; other drivers do not, so a mobile emplacement bound to one stays off the map (see the built-in feed above).
