# Emplacements

An emplacement is a place observations come from: a radar site, a mast, a ship's radar, a field observer. Sentinel keeps them as records of their own, apart from the entities they observe, so that a report can later say which emplacement made it and where the observer stood. This page covers what an emplacement is, its fields and limits, the management API, the built-in `emplacements` feed that puts each one on the map and in search, the client, and what is still to come.

## Fixed and mobile

| Mobility | Position | Reported |
|----------|----------|----------|
| `FIXED` | Required. A radar site, a mast. | On every heartbeat of the `emplacements` feed, and on every change. |
| `MOBILE` | Optional. A ship's radar, a vehicle, a person. A mobile emplacement with no position is stored but not reported. | On every change (its position is edited, or later reported by its platform), not on the heartbeat. |

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
| `feedId` | Optional binding to a connection (a feed that takes data in). `""` in a PATCH unbinds it. A bound feed that is deleted leaves the emplacement unbound. |
| `classification` | `UNCLASSIFIED` (default), `CONFIDENTIAL`, `SECRET` or `TOP_SECRET`. |
| `exercise` | Marks the emplacement as part of an exercise. Stored now; used by the gRPC reporting work (see What comes next). |
| `active` | Default true. An inactive emplacement is kept but not reported. |
| `lastReportAt` | Reserved for the gRPC reporting path (see What comes next); nothing sets it yet. Read-only. |
| `createdBy`, `createdAt`, `updatedAt` | Read-only. |
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

**Optimistic writes.** Every response carries `ETag: "<version>"`. A PATCH sends it back in `If-Match` (`"3"`, `3` and `W/"3"` are all read as version 3); a stale value answers 412 and changes nothing, so two editors cannot overwrite each other. `GET` only returns the `ETag`; it does not read `If-Match`. The Emplacements panel handles this by telling the second editor that the emplacement was changed by someone else.

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

The `emplacements` feed (driver `emplacement`) reports each emplacement as an entity, so it is in search and can anchor a geofence ring. A mobile emplacement's entity does not yet follow a live position: see the age-out note below. It is built in and enabled by default, because it needs no credentials. The driver is internal: no connection can be created on it, and the new-connection picker hides it. The Connections page still shows its row, where it can be disabled and its heartbeat changed.

| Setting | Default | Meaning |
|---------|---------|---------|
| `heartbeatSec` | 60 (minimum 10) | How often fixed emplacements are reported again so their entities stay live. |

What it reports:

- Every active emplacement that has a position, once at start and again on every change.
- Fixed emplacements again on every heartbeat. Mobile ones are reported only when the feed starts and when they change, never on the heartbeat, so until their own position reports arrive over gRPC (see What comes next) a mobile emplacement's entity goes stale and ages out unless it is edited (see Age-out).
- Each as an entity of type `SENSOR`, with a source entity id equal to the emplacement id, the emplacement's name, classification and exercise flag, and its boresight as the heading. A fixed emplacement is a ground entity.
- Under the canonical `EMPLACEMENT` identity, so the entity service always fuses an emplacement's reports into the same entity.

The sensor type picks the source label:

| Sensor type | Source |
|-------------|--------|
| `RADAR` | `RADAR` |
| `AIS_RECEIVER` | `AIS` |
| `ADSB_RECEIVER` | `ADS_B` |
| `HUMAN` | `HUMINT` |
| anything else | `MASINT` |

**Age-out.** `db/schema/72_emplacements.sql` seeds a stale and age-out row for each of these sources on the `emplacements` feed: an entity goes stale after 3 minutes without a report and ages out after 10, which is three and ten missed heartbeats at the default for a fixed emplacement. The thresholds make no exception for mobility: because a mobile emplacement is reported only at start and on change, its SENSOR entity goes stale after 3 minutes and ages out after 10 unless it is edited, until its own position reports over gRPC arrive (sub-project 2). **Deactivating or deleting** an emplacement stops its reports, so its entity goes stale and then ages out on that schedule; it does not disappear at once. Editing it again, or setting it active, reports it at once.

## The client

- **Panel.** The Emplacements panel (nav key `e`, route `#/emplacements`) lists emplacements and edits one at a time. **Pick on map** lets you click the map for the position, and a preview of the footprint follows the form's boresight and range as you type. Saving keeps it. A conflict (412) is reported in the form and the emplacement can be reloaded.
- **Coverage.** A **Coverage** checkbox in the panel shows or hides the footprints. The choice is remembered per browser. A fixed emplacement is drawn with a marker and its coverage as a sector (a sector across the antimeridian is one wedge). A mobile one is an outlined (hollow) marker, and it and its footprint follow its live position. An omnidirectional coverage (360°) is a circle, or a ring when `minRangeM` is above 0. Inactive emplacements are not drawn unless selected.
- **Layer.** The built-in feed's row in the layers pill shows or hides the whole layer (markers and footprints). A layer that was hidden stays hidden when the map loads.
- **Entity detail.** The detail panel of a sensor entity that comes from an emplacement shows **Emplacement: <name>**, which opens the panel with it selected.

## What comes next

Sub-project 2 of this work, with the gRPC connection: a sender reporting as an emplacement over gRPC (the `IngestService` in `proto/`), conversion of polar measurements (range, azimuth, elevation from the emplacement) to positions using the boresight, reference and default sigma above, reports from a platform's own position (ownship) that move a mobile emplacement, and the `exercise` flag keeping exercise data apart. The proto contract (`Observation` emplacement fields, the `EMPLACEMENT` identity) and the fields above are already in place for it.
