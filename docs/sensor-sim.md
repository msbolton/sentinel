# sensor-sim

`sensor-sim` is a command-line simulator of three kinds of sensor, one per process:

- **`--kind radar`**, a ground surveillance radar, and **`--kind eoir`**, a daylight/thermal (EO/IR) camera. Each sits at a fixed site, watches a scenario of moving vehicles, people and drones over real terrain, and reports what it detects as polar measurements. The radar reports range, bearing and elevation. The camera reports only a direction, and Sentinel works out where that direction meets the ground.
- **`--kind esm`**, an electronic-support (ESM) sensor. It sits at an emplacement, fixed or on a moving platform, watches a set of moving RF emitters (search and fire-control radars, SATCOM terminals, datalinks, jammers) and reports each detection as a direction-finding fix with an error ellipse. It needs no terrain and no scenario.

Every kind speaks Sentinel's gRPC ingest contract. It is the quickest way to put radar, camera and signals intelligence on the map without real sensors, and a worked example for anyone writing a sender of their own. This page covers what each kind does, how to set it up and run it, its flags and configuration files, how it behaves when the connection or the server misbehaves, and what you should see in Sentinel.

It lives in `simulators/sensor-sim`. It is its own Go module, built without CGO, and one Nx project (`sensor-sim`). The ingest contract, the rejection reasons and the connection settings are in `docs/feeds.md` (gRPC connections). How a report is bound to an emplacement, how a polar report is placed from it and how a mobile emplacement reports its position are in `docs/emplacements.md` ("Reporting over gRPC"). The ESM kind replaces the separate `ew-sensor` program; "Moving from ew-sensor" below maps its flags.

## What it does

### Radar and camera

To have both, run two processes. They agree on the targets without talking to each other, because a target's position depends only on the scenario and the wall clock. Every target starts its route at the scenario's `epoch` and moves along its waypoints at its speed from then on. Two processes that load the same scenario file therefore see every target in the same place at the same moment.

Each scan (default 1 per second) the simulator:

1. works out where every target is now: on the route, at the terrain height there plus the target's height above ground;
2. for each target inside the sensor's coverage (range, azimuth sector and elevation window), rolls against the probability of detection;
3. for the camera, also checks the line of sight over the terrain. A target behind a hill, or behind trees and buildings (the terrain is a surface model), is not seen;
4. sends each detection as an `Observation` with no position, only a polar measurement:
   - The **radar** sends range, azimuth, elevation and range rate, each with its 1-sigma, plus a radar detail: RCS, SNR, mode `TRACK`, track quality, a classification (vehicle, person or drone; right 90% of the time, otherwise unknown) and its confidence, and a stable radar track id.
   - The **camera** sends azimuth and elevation, marked `direction_only`, plus an EO/IR detail: the band, a classification and its confidence, the field of view, a bounding box and a stable camera track id.

Every report names its emplacement in `Report.emplacement_id`, and the server turns the polar measurement into a position from that emplacement. Identities are stable per sensor and target: `RADAR-<sensor name>-<target id>` and `EOIR-<sensor name>-<target id>`. The radar and the camera therefore make separate entities for the same target; Sentinel does not fuse them.

### ESM

Each tick (default 1 per second) the simulator:

1. moves every emitter (static, a random walk, or a loop over waypoints);
2. for a mobile sensor, moves the ownship along its path;
3. for each emitter inside the sensor's coverage (range between the minimum and maximum, azimuth within half the width of the boresight), rolls against the probability of detection;
4. for each detection, draws a fix around the true position and sends it as an `Observation` with the entity type `SIGNAL`, the identity `EMITTER-<id>`, a position, an uncertainty ellipse and the RF parameters of the emitter (frequency, bandwidth, emitter type, modulation, PRI, pulse width, scan type, power).

Every report names its emplacement in `Report.emplacement_id`. A fixed seed gives the same fixes: the ESM sensor's clock is the sum of the tick intervals, not the wall clock.

## The demo scenario (radar and camera)

`examples/radar.yaml` and `examples/camera.yaml` put a radar and a camera on Latrigg (54.6133 N, 3.1153 W), the 368 m fell just north of Keswick in the English Lake District. They share `examples/scenario.yaml`. The area is steep, has real roads, and lies in Copernicus GLO-30 coverage: one 1° tile, N54 W004, about 15 MB.

- **Radar:** "Latrigg radar", 360°, out to 10 km, 10 m up a mast.
- **Camera:** "Latrigg camera", 6 m up a mast beside the radar. It looks south-south-west (boresight 210°, 60° wide, out to 8 km) down Derwentwater towards Borrowdale.
- **Targets:** five ground targets and two drones, all looping:
  - a bus on the B5289 down the east shore of Derwentwater to Rosthwaite;
  - a lorry on the A66 from Threlkeld past the foot of Latrigg to Braithwaite;
  - a car on the A591 over Castlerigg to Thirlmere, outside the camera's view;
  - a van round the lake by Portinscale and the lane under Cat Bells;
  - a walker on the Cat Bells ridge;
  - a drone crossing the lake 100 m above the water, right across the camera's view, with the slopes of Cat Bells and Borrowdale behind it;
  - a drone circling Keswick 120 m above the rooftops.

The routes follow the roads as traced from memory of the map and then checked against the terrain, so every waypoint is on land at road height. They are close, but not surveyed: a vehicle may cut a corner or run a few tens of metres off the tarmac.

From the camera, the bus and the van are in sight a little over half the time they are in its field of view. The shore woods and the slopes hide them for the rest. The lake drone is in sight almost all the time. The radar loses the A66 lorry where the road runs under Latrigg's own brow.

## The ESM examples

- `examples/esm-coastal.yaml`: "Coastal ESM Site", a fixed site on the Monterey coast looking west to sea (boresight 270°, 120° wide, 2 to 150 km) with five emitters: a surface search radar on a patrolling vessel, a fire-control radar, a SATCOM terminal, an inbound aircraft's datalink and a noise jammer.
- `examples/esm-mobile.yaml`: "Patrol Ship ESM", a ship-borne sensor on a 12-knot patrol box. Its boresight and 200° sector are measured from the bow (`azimuthReference: PLATFORM`), so the coverage turns with the ship, and it reports its own position at 1 Hz.

Both choose plaintext (`connection.insecure: true`), as the dev listener speaks.

## Set up

You need the stack running with the listener replica (the `inbound` profile), which hosts the port your connection listens on. `docs/development.md` and `docs/feeds.md` ("Listeners") cover starting it, and Docker Desktop's *Enable host networking* setting. For the camera, the ingest service needs terrain on (`INGEST_TERRAIN_CACHE_DIR`) to place its detections. Without it they arrive as bearings with the reason `terrain_unavailable`, and the radar's ground targets are not clamped to the ground. The ESM kind needs no terrain.

1. **Create a `grpc` connection** in Connections (or `POST /api/v1/feeds` with `"driver":"grpc"`), on a free port from the port hint, with these settings:
   - `senderManagedEmplacements`: on. The simulator creates its own emplacement with `UpsertEmplacement`, by name, from its configuration. A radar or camera requires it and refuses to run on a connection that does not allow it. An ESM sensor can instead report as an emplacement you create and bind to the connection yourself: leave the setting off and, if more than one is bound, pass `--emplacement-id`.
   - `exercise`: on. Everything the connection brings in is marked EXERCISE, and outbound connections refuse it unless their policy allows it.
   - `military`: on for an ESM sensor ("Military connection: draw its entities with MIL-STD-2525D symbols"), so its signals get SIGINT symbols.

   For example:

   ```json
   {
     "id": "sensor-sim", "name": "Sensor sim", "driver": "grpc", "enabled": true,
     "config": {
       "port": 6201,
       "exercise": true,
       "military": true,
       "senderManagedEmplacements": true
     }
   }
   ```

   One connection can carry several sensors: each creates its own emplacement ("Latrigg radar", "Latrigg camera", "Coastal ESM Site"). Raise `maxEmplacements` (default 16) or `maxMessagesPerSec` (default 1000) only if your run needs it.

2. **Issue a token.** Open the connection's row and issue a token under **Tokens**. The secret (`si_...`) is shown once. A token without a classification ceiling is limited to the connection's own level.

3. **Give the token to the CLI.** Put it in `secrets/sensor-sim.env`, which is gitignored and which the Compose services read when it exists:

   ```
   SENSOR_SIM_TOKEN=si_...
   # optional:
   SENSOR_SIM_ADDR=localhost:6201
   ```

   Outside Compose, `export SENSOR_SIM_TOKEN=si_...`, or `SENTINEL_INGEST_TOKEN`, which the connection's send guide uses. An existing `secrets/ew-sensor.env` with `EW_SENSOR_TOKEN` and `EW_SENSOR_ADDR` still works for `esm-sim` (see "Moving from ew-sensor").

## Run it

With Compose, the `sim` profile has a service for each kind: `radar-sim`, `eoir-sim` and `esm-sim`. Each runs the CLI in a container on the host's network with a bundled example. The radar and the camera keep their terrain tiles in the `sentinel-terrain-cache` volume. Pass the address on the `run` line, one terminal per sensor:

```
docker compose -p sentinel --profile inbound --profile sim run --rm radar-sim \
  --addr localhost:6201
docker compose -p sentinel --profile inbound --profile sim run --rm eoir-sim \
  --addr localhost:6201
docker compose -p sentinel --profile inbound --profile sim run --rm esm-sim \
  --addr localhost:6201
```

The services run:

- `radar-sim`: `sensor-sim --kind radar --config examples/radar.yaml --terrain-cache /var/cache/sentinel/terrain`;
- `eoir-sim`: `sensor-sim --kind eoir --config examples/camera.yaml --terrain-cache /var/cache/sentinel/terrain`;
- `esm-sim`: `sensor-sim --kind esm --config examples/esm-coastal.yaml`.

Flags on the `run` line are added after these, and a flag given twice takes its last value: `run --rm esm-sim --addr localhost:6201 --config examples/esm-mobile.yaml` runs the ship-borne sensor.

The examples choose plaintext HTTP/2 (`connection.insecure: true`), which is what the listener speaks unless you enabled `tls` on the connection, so no transport flag is needed. With `SENSOR_SIM_ADDR` in `secrets/sensor-sim.env` the services also start in the background, with no `run` line at all: `docker compose -p sentinel --profile inbound --profile sim up -d radar-sim eoir-sim esm-sim`. For TLS, add `--tls --ca /ca.pem` to the `run` line, which replaces the examples' choice, and mount the CA file, for example with `-v "$PWD/secrets/ca.pem:/ca.pem:ro"`:

```
docker compose -p sentinel --profile inbound --profile sim run --rm \
  -v "$PWD/secrets/ca.pem:/ca.pem:ro" esm-sim \
  --addr localhost:6201 --tls --ca /ca.pem
```

Stop a `run` with Ctrl-C.

The radar's or camera's first run downloads the terrain tile before streaming and logs `looking up the ground height at the site`. Later runs, and the other sensor, find it in the cache.

The connection's send guide prints the same commands for a binary on your path, one per kind:

```
sensor-sim --kind esm --addr localhost:6201 --token "$SENTINEL_INGEST_TOKEN" --insecure --config examples/esm-coastal.yaml
```

Its `--insecure` repeats the examples' choice; a guide for a TLS connection prints `--tls`, which replaces it.

To run from source, from the repository root:

```
export SENSOR_SIM_TOKEN=si_...
(cd simulators/sensor-sim && go run ./cmd/sensor-sim --kind radar --addr localhost:6201 --insecure \
  --config examples/radar.yaml --terrain-cache ~/.cache/sentinel/terrain)
(cd simulators/sensor-sim && go run ./cmd/sensor-sim --kind esm --addr localhost:6201 --insecure \
  --config examples/esm-coastal.yaml)
```

The radar and camera examples set `terrain.cacheDir` to the container's `/var/cache/sentinel/terrain`. Outside the container, pass `--terrain-cache` with a folder you can write to. If the folder cannot be written, the simulator warns and keeps tiles in memory only, and downloads them again on every run. An ESM sensor ignores any terrain setting. `nx build sensor-sim` puts a binary in `dist/simulators/sensor-sim/`. Add `--verbose` to log every report as JSON as it is sent, and each ack.

Without a `--config` file you must give `--kind`, `--lat` and `--lon` (and `--name` if you do not want the default); an ESM sensor makes up five emitters inside its coverage when no list is given. You can turn any ESM configuration into a mobile one with flags: `--mobile --path circle --speed-kts 12` (a circle of 5 km radius around `--lat`/`--lon`), or `--path waypoints` with waypoints in the YAML.

## Flags and configuration

Flags override the configuration file, which overrides the defaults; the defaults depend on the kind. Most YAML keys under `sensor` have a flag in kebab case: `rangeKm` is `--range-km`, `sigmaAzDeg` is `--sigma-az`, `sigmaM` is `--sigma-m`. The scenario, the ESM emitters and the ownship's waypoints and radius are set in YAML only (`--emitters N` sets only how many emitters), as are `sensor.azimuthReference` and `sensor.classification`. `sensor.mobility` has just `--mobile` (`--mobile=false` forces `FIXED`). The connection address and token also come from the environment. The Kinds column says which kinds a flag applies to; a setting only an ESM sensor uses is refused on a radar or camera, not ignored.

| Flag | Default | Kinds | Meaning |
|------|---------|-------|---------|
| `--kind` | none (required) | all | `radar`, `eoir` or `esm`. |
| `--addr host:port` | none (required) | all | Ingest listener. Env `SENSOR_SIM_ADDR`. |
| `--token` | none (required) | all | Ingest token. Env `SENSOR_SIM_TOKEN`, then `SENTINEL_INGEST_TOKEN`. |
| `--insecure` | off | all | Plaintext h2c. The token travels in clear. Exactly one of `--insecure` and `--tls`. |
| `--tls` | off | all | TLS with the system roots. |
| `--ca file` | none | all | Extra root CA (needs `--tls`). |
| `--cert file`, `--key file` | none | all | Client certificate for a connection that requires one (need `--tls`, and both together). |
| `--config file` | none | all | YAML file; see below. |
| `--emplacement-id` | none | esm | Report as this existing emplacement, with the server's geometry; otherwise one is created from the flags below. |
| `--name` | `sensor-sim`; esm `esm-sim` | all | Emplacement name, unique across Sentinel. |
| `--lat`, `--lon` | none | all | Site position, degrees (0,0 counts as unset). An ESM sensor needs them only without `--emplacement-id`; a mobile one starts its path there. |
| `--alt` | none (0 m for esm) | all | Site altitude, metres above the WGS84 ellipsoid. |
| `--height-above-ground` | none | radar, eoir | Mast height, metres (0 to 1000). The site altitude is the terrain there plus this. Needs terrain. Not with `--alt`. |
| `--boresight` | 0 | all | Boresight azimuth, degrees from true north (an ESM sensor with `azimuthReference: PLATFORM`: from the bow). |
| `--width` | 90; esm 120 | all | Azimuth coverage, degrees (360 is all round). |
| `--range-km` | 10; esm 120 | all | Maximum range. |
| `--min-range-km` | 0 | all | Minimum range (a blind zone). |
| `--elev-min`, `--elev-max` | camera −15 and +75; radar unlimited | radar, eoir | Elevation coverage, degrees. |
| `--rate hz` | 1 | all | Scans (ESM: ticks) per second, above 0 and at most 100. |
| `--pd` | 0.9 | all | Probability of detection per target or emitter per scan, in (0, 1]. The radar scales it down with range and target size. |
| `--sigma-az`, `--sigma-el` | 0.5 | radar, eoir | Azimuth and elevation noise, 1 sigma, degrees. |
| `--sigma-range-m` | 10 | radar | Range noise, 1 sigma, metres. |
| `--sigma-m` | 250 | esm | Fix position error, 1 sigma, at the 10 km reference range, metres. |
| `--band` | `VISIBLE` | eoir | Camera band: `VISIBLE`, `SWIR`, `MWIR` or `LWIR`. |
| `--emitters N` | the YAML list, else 5 | esm | Number of emitters, at most 1000. Extra ones are synthesised (`SYN-01`, ...); surplus YAML ones are dropped. |
| `--mobile` | off | esm | The sensor moves along an ownship path and reports its position. |
| `--path` | `circle` | esm | Ownship path: `circle` or `waypoints`. |
| `--speed-kts` | 12 | esm | Ownship speed. |
| `--send-timestamps` | off | esm | Set source timestamps (see "Limits and gotchas"). |
| `--terrain-cache dir` | none (terrain off) | radar, eoir | Where Copernicus GLO-30 tiles are cached. |
| `--terrain-url` | the Copernicus bucket | radar, eoir | Tile source. |
| `--seed` | from the clock | all | Random seed; the log shows it. |
| `--duration` | 0 | all | Run time, e.g. `5m`; 0 runs until signalled. |
| `--heartbeat-interval` | `5s` | all | How often the emplacement sends its `EmplacementState` heartbeat; whole seconds from 1s to 300s. The server calls the site offline after three missed. |
| `--verbose` | off | all | Log every report as JSON as it is sent, and each ack (YAML: `verbose: true`). |

**Rules the configuration must meet.** The simulator checks these at start and exits 1 with every problem listed.

- **A camera needs terrain**, which decides what it can see. Without it the camera would see through hills, and its targets would sit at 0 m instead of on the ground. A camera also needs `--alt` or `--height-above-ground`.
- **A radar with terrain needs `--alt` or `--height-above-ground`.** Without either, the site would be at 0 m, below targets that stand on the real ground, and every elevation would be wrong. A radar without terrain may leave both unset: its targets then move over flat ground at 0 m, and so does its site.
- **The elevation window must not be empty.** It is checked after the defaults are filled in, so `--elev-min 80` alone on a camera is refused (its default maximum is 75), as is `--elev-max -20` alone.
- **An ESM sensor observes emitters, not a scenario.** It needs at least one emitter (listed or `--emitters`), takes `--alt` rather than `--height-above-ground`, has no elevation limits, and refuses a `scenario` or `scenarioFile`. `azimuthReference: PLATFORM` needs a mobile sensor, and a mobile sensor needs an ownship path (a circle with a radius, or at least two waypoints) and a speed.
- **ESM-only settings on a radar or camera are refused**: `--emplacement-id`, `--mobile`, `--emitters` or an `emitters` list, `--send-timestamps` and `sensor.classification`.

**Precedence.** A flag set on the command line beats the environment, which beats the YAML, which beats the default. An explicit `--rate 1` beats a YAML `rate: 5`. The token is `--token`, then `SENSOR_SIM_TOKEN`, then `SENTINEL_INGEST_TOKEN`, then the deprecated `EW_SENSOR_TOKEN`, then the YAML; the address is `--addr`, then `SENSOR_SIM_ADDR`, then the deprecated `EW_SENSOR_ADDR`, then the YAML. A transport flag, `--insecure` or `--tls`, replaces the YAML's transport, so `--tls --ca ca.pem` works with the bundled examples although they set `insecure: true`; `--insecure` also drops the YAML's `ca`, `cert` and `key`. `--kind` beats the YAML's `sensor.kind`, and the kind chosen picks the defaults. Keep the token out of YAML files: they are easy to commit by mistake.

**YAML.** Unknown keys are an error. The top-level keys are:

- `connection`: `addr`, `token`, `insecure`, `tls`, `ca`, `cert`, `key`.
- `sensor`, the site and its detection model:
  - for every kind: `kind`, `name`, `lat`, `lon`, `alt`, `boresight`, `width`, `rangeKm`, `minRangeKm`, `rate`, `pd`, `heartbeatInterval`;
  - radar and camera: `heightAboveGroundM`, `elevationMinDeg`, `elevationMaxDeg`, `sigmaAzDeg`, `sigmaElDeg`, `sigmaRangeM`, and the camera's `band`;
  - ESM: `emplacementId`, `mobility` (`FIXED` or `MOBILE`), `azimuthReference` (`TRUE_NORTH`, or `PLATFORM`, which needs `MOBILE`: the boresight is then measured from the platform's heading and turns with it), `sigmaM`, `classification` (optional: empty takes the connection's level; when set it is on the emplacement and stamped on every observation), `sendTimestamps`.
- `scenario`, or `scenarioFile`, but not both (radar and camera). `scenarioFile` names a file holding the scenario's own keys at its top level, relative to the configuration file, so a radar and a camera can share one. The keys are:
  - `epoch`, an RFC 3339 time;
  - `targets`, a list. Each target has `id` (unique), `kind` (`vehicle`, `drone` or `person`), `displayName`, `speedKts`, `heightAboveGroundM` (0 for a ground target), `loop` and `waypoints` (a list of `{lat, lon}`). Without `loop`, a target stops at its last waypoint.
- `terrain` (radar and camera): `cacheDir`, `sourceUrl`.
- `ownship` (ESM, mobile only): `path` (`circle` or `waypoints`), `speedKts`, `radiusKm` (circle, default 5), `waypoints` (a list of `{lat, lon}`, at least two, looped).
- `emitters` (ESM): a list, each with `id` (unique), `displayName`, `emitterType` (for example `RADAR_SEARCH`, `RADAR_TRACK`, `SATCOM`, `COMMS_DATALINK`, `JAMMER`), `frequencyHz` (required), `bandwidthHz`, `priUs`, `pulseWidthUs`, `modulation`, `scanType`, `powerDbm`, `affiliation`, `trackEnvironment` (`AIR`, `SEA_SURFACE`, `SUBSURFACE`, `GROUND`, `SPACE`, `UNKNOWN`; unset or `LAND` is `GROUND`), `start` (`{lat, lon}`) and `motion` (`type` `static`, `random-walk` or `waypoints`, with `speedKts`, and `waypoints` for the last).
- `seed`, `duration`, `verbose`.

**Set the epoch** (radar and camera). Leave `epoch` unset and it is the start of the current UTC day, 00:00 UTC, which two processes started on the same day agree on. But then a route that does not loop starts at midnight UTC: by the afternoon its target has long since parked at its last waypoint. Every route restarts at 00:00 UTC the next day, mid-run. The simulator warns when the epoch is unset. Set it explicitly, as the examples do, and give every sensor the same value (one shared `scenarioFile` does that). A looping route also has no surprises, since the epoch then only decides where in the loop each target is.

### Moving from ew-sensor

The ESM kind is the former `ew-sensor` program: the same emitters, fixes, ellipses, ownship, heartbeats, emplacement resolution, retries and exit codes. What changed is its name, its configuration shape and a few names around it.

| ew-sensor | sensor-sim |
|-----------|------------|
| `ew-sensor [flags]` | `sensor-sim --kind esm [flags]` (or `kind: esm` under `sensor:` in the YAML) |
| `--addr`, `--token`, `--insecure`, `--tls`, `--ca`, `--cert`, `--key`, `--config` | unchanged |
| `--emplacement-id`, `--lat`, `--lon`, `--alt`, `--boresight`, `--width`, `--range-km`, `--min-range-km`, `--rate`, `--pd`, `--seed`, `--duration`, `--heartbeat-interval`, `--verbose` | unchanged; the ESM defaults are ew-sensor's (120° wide, 120 km) |
| `--emitters`, `--sigma-m`, `--mobile`, `--path`, `--speed-kts`, `--send-timestamps` | unchanged, ESM only |
| `--name`, default `ew-sensor` | `--name`, default `esm-sim` for the ESM kind; pass `--name ew-sensor` to keep reporting as an emplacement created with the old default |
| env `EW_SENSOR_TOKEN` | `SENSOR_SIM_TOKEN`. `EW_SENSOR_TOKEN` is still read, last, below `SENTINEL_INGEST_TOKEN`, and logs a deprecation warning |
| env `EW_SENSOR_ADDR` | `SENSOR_SIM_ADDR`. `EW_SENSOR_ADDR` is still read, below it, with a warning |
| YAML `emplacement:` (`emplacementId`, `name`, `lat`, `lon`, `alt`, `boresight`, `width`, `rangeKm`, `minRangeKm`, `mobility`, `azimuthReference`) | the same keys under `sensor:`, with `kind: esm` |
| YAML `sensor:` (`rate`, `pd`, `sigmaM`, `classification`, `sendTimestamps`, `heartbeatInterval`), `ownship:`, `emitters:`, `connection:`, `seed`, `duration`, `verbose` | unchanged |
| `examples/coastal-esm.yaml`, `examples/mobile-esm.yaml` | `examples/esm-coastal.yaml`, `examples/esm-mobile.yaml`; both default to `insecure: true` (the old mobile example said `tls: true`) |
| Compose service `ew-sensor` (no default command), `secrets/ew-sensor.env` | `esm-sim`, which runs `examples/esm-coastal.yaml`; it reads `secrets/sensor-sim.env`, and `secrets/ew-sensor.env` too when it exists |
| `nx build ew-sensor`, `dist/adapters/ew-sensor/` | `nx build sensor-sim`, `dist/simulators/sensor-sim/` |
| Emplacement description `ew-sensor simulator`; `client_ref` values `ew-1`, `ew-2`, ... | `sensor-sim ESM`; `sim-1`, `sim-2`, ... |

An emplacement the old program created keeps working: the upsert is by name, so the ESM kind with the same `--name` updates it rather than creating a new one.

## Behaviour

**Session and emplacement.** The CLI calls `GetSession`, then finds the emplacement it reports as.

A radar or camera always calls `UpsertEmplacement` with its configuration:

- a `FIXED` emplacement of sensor type `RADAR` or `EO_IR`, with the configured name;
- the site position, with the resolved altitude (the terrain plus the mast height when you gave `--height-above-ground`);
- the mast height itself, as `height_above_ground_m`;

  The simulator resolves the altitude itself, as metres above the WGS84 ellipsoid, because it already has the terrain. A real sensor's adapter that knows only its mast height, or whose altitude is above mean sea level (most GPS receivers and survey data give that), should send `height_above_ground_m` and leave the altitude out: the server then puts the sensor at the terrain under it plus the mast, for a radar as for a camera (`docs/emplacements.md`). An altitude above mean sea level sent as is would be off by the geoid height, tens of metres, which moves a camera's ground placements by hundreds of metres;
- the boresight (true north);
- the coverage: minimum and maximum range, azimuth width and elevation limits. A camera always declares its window, defaults included; a radar declares only the limits it was given.

A connection without `senderManagedEmplacements` is refused with exit code 1: a radar or camera never reports as an emplacement someone else made, since its site and coverage come from its configuration.

An ESM sensor tries, in order:

1. with `--emplacement-id`, that emplacement from the session (ids are compared without regard to case); an id the token cannot see is an error;
2. otherwise, when the connection has `senderManagedEmplacements` on, `UpsertEmplacement` of an `RF` emplacement with the configured name, position, coverage, boresight, azimuth reference, mobility and classification;
3. otherwise the connection's single bound, active emplacement;
4. otherwise it exits with an error that says what to do.

For an emplacement that already exists on the server (cases 1 and 3), the server owns the geometry: its position, boresight, azimuth reference, coverage and mobility win over the flags and YAML. A created emplacement keeps the configured geometry. If an existing emplacement has no coverage on the server, the CLI logs a warning and simulates the configured (or default 120 degree, 120 km) sector around the server's boresight; Sentinel draws no coverage for it, so emitters outside that sector are absent without any visible reason.

The upsert is by name, so a restart reuses the emplacement. The name must be unique across all emplacements, so a second sensor needs its own `--name`, and a name an operator already used fails with `AlreadyExists` (the error suggests `--name`, and for an ESM sensor `--emplacement-id`). The emplacement inherits the connection's `exercise` flag, so on an exercise connection the sensor site itself is marked exercise and the outbound exercise policy holds it back along with the detections.

**Heartbeats.** The emplacement is its connection's heartbeat. Every stream begins with an `EmplacementState` (before any observation), and every state declares `heartbeat_interval_sec`, the `--heartbeat-interval` (default 5 s).

- A fixed site sends one every interval. It carries the site's position, which the server ignores.
- A mobile ESM sensor sends one every second, with its position, heading and velocity, so the server can place its measurements.

The server calls the emplacement offline after three missed intervals: it refuses its observations as `no_heartbeat`, draws the site grey, and turns the connection amber or red. A `no_heartbeat` ack, which a replica move can cause, makes the CLI send a heartbeat at once (at most one a second) and send the refused detection again, once. An ESM sensor sends absolute positions, never polar measurements, so a mobile sensor's ownship position matters only when the connection has `enforceCoverage` on; then the server refuses its observations as `stale_ownship` until it has one.

**Terrain** (radar and camera).

- *The source.* Heights come from Copernicus GLO-30, converted from mean sea level to the WGS84 ellipsoid, which is the datum of every altitude in Sentinel. The model is a surface model: it includes trees and buildings, which block the camera as a hill does.
- *Waiting for it.* Before streaming, the CLI looks up the ground at the site and under every target, which on a cold cache downloads their tiles. It waits up to 300 s. Each scan's terrain work is then bounded to 2 s. A scan the terrain cannot answer in time is skipped, counted in `scans_skipped` and warned about once per run of skipped scans. The download carries on in the background, and later scans use the tile once it is in.
- *Where there is none.* A tile that is missing from the source is sea, at 0 m above mean sea level. Land in a known gap in GLO-30 (Armenia and Azerbaijan) has no heights at all, so a scenario there is skipped scan after scan.

**The radar's numbers.**

- *Detection.* The detection probability is `pd × min(1, (max range / range)⁴ × size)`, where `size` is 1 for a vehicle, 0.3 for a person and 0.15 for a drone. A vehicle is detected at `pd` all the way out; a drone's chance falls off over the outer third of the range.
- *SNR.* SNR is 18 dB for a vehicle at maximum range and grows by 40 dB per decade closer. It is kept between 3 dB and 60 dB, so a target at or very near the radar reports 60 dB.
- *Range rate.* It is the true rate of change of range, with no noise; positive is receding.

**The ESM fix and its ellipse.** The reported position is the true one plus Gaussian noise. The along-line-of-sight sigma is `sigma-m`; the cross-range sigma grows with range, as `sigma-m * max(1, range_km / 10)`, as it does for a direction finder. The uncertainty sent is the 95% ellipse: semi-major `2.4477 * cross sigma`, semi-minor `2.4477 * along sigma`, in metres, with the orientation in degrees from north (the long axis lies across the line of sight, at the bearing plus 90°). Fixes are therefore long thin ellipses at long range. Sentinel draws them around the entity.

**Reconnecting.** `Unauthenticated` and `PermissionDenied` end the run with exit code 2, and `Unimplemented` (the address is not a Sentinel ingest service) with exit code 1. Any other end of the stream (`Unavailable`, a transport error, a clean close by the server, `ResourceExhausted`) is logged as a warning and the CLI reconnects after a jittered exponential backoff from 0.5 s to 30 s. The line carries the cause, for example `msg="stream ended; reconnecting" stream=1 code=Unavailable message="connection moving: reconnect" backoff=612ms`. The backoff starts again once a stream has had an accepted report or has stayed open for 30 s. While disconnected the simulation keeps running, and the detections it produces with no open stream are dropped and counted in `dropped_offline`, so for an ESM sensor a disconnect does not change the sequence of fixes for a seed. (A `rate_limited` ack does, because it changes the tick interval.) Reports that were sent but not acknowledged when a stream ended are not sent again: they are lost, not replayed.

**Backpressure and limits.**

- A `rate_limited` rejection halves the scan rate for 10 s (further ones extend the hold, they do not compound), then restores it. The CLI warns at the start when `rate × targets` (ESM: `rate × emitters`) exceeds the connection's `maxMessagesPerSec`; heartbeats are never rate limited and do not count.
- An `unknown_emplacement` rejection within 5 s of creating the emplacement is retried once after 250 ms, in case the replica that serves the stream has not seen the new row yet. Outside that window it counts as a rejection.
- A `no_heartbeat` rejection sends a heartbeat at once and retries the detection once after 250 ms; both the rejection and the retry are counted.
- A report larger than the session's `max_message_bytes` is dropped by the CLI and logged, never sent. If the server ends a stream with `ResourceExhausted` for an oversize message, the report is dropped and the CLI reconnects.
- `UpsertEmplacement` retries `Unavailable` ("saved; retry") and "rate limited" answers; the emplacement cap (`maxEmplacements`) is fatal.

## Exit codes and stats

| Code | When |
|------|------|
| 0 | `--duration` elapsed, SIGINT or SIGTERM, or `--help`. |
| 1 | A flag, YAML or validation error (the message and `Run sensor-sim --help for the flags.` go to stderr); an unreadable `--ca`, `--cert` or `--key`; no ground height for the site within 300 s; a radar or camera on a connection without `senderManagedEmplacements`; for an ESM sensor, an `--emplacement-id` not in the session, no bound emplacement or more than one, or an emplacement with no position; `UpsertEmplacement` `InvalidArgument` (including a `sensor.classification` above the token's ceiling), `AlreadyExists` or `NotFound`; `Unimplemented`. |
| 2 | `Unauthenticated` or `PermissionDenied` from any call: an unknown, revoked or expired token, another connection's token, a connection that is not sender-managed, a connection above the token's classification; or the sender-managed emplacement cap. |

Reports above the token's ceiling are rejected one by one (`above_ceiling`) and do not end the run. A revoked token ends a running stream with `Unauthenticated`, so the CLI exits 2 and does not reconnect.

The CLI logs to stderr (text, one line per event; the token appears only as a short prefix). Every 10 s, and once more at exit as `final stats`, it logs a line such as:

```
msg=stats sent=412 ok=410 rejected=map[rate_limited:2] dead_lettered=0 retried=0 reconnects=1 dropped_oversize=0 dropped_offline=3 scans_skipped=0
```

`sent` counts reports written to the stream, `ok` those acknowledged `OK`, `rejected` the `REJECTED` acks by reason, `dead_lettered` the `DEAD_LETTERED` ones, `retried` the `unknown_emplacement` and `no_heartbeat` retries, `reconnects` the stream ends and failed stream opens, each followed by a backoff and a new attempt (so a long outage counts every attempt), and `scans_skipped` the scans the terrain could not answer within 2 s.

## Limits and gotchas

- **Wall-clock truth** (radar and camera). Targets move with the wall clock, so a fixed `--seed` repeats the detection rolls and the noise, but not the positions. The seed and the logged start time together describe a run. An ESM sensor's emitters move on its own simulation clock instead, so a seed repeats its fixes.
- **No fusion.** The radar and the camera make separate entities for the same target.
- **Timestamps** (ESM). Source timestamps are not set unless you pass `--send-timestamps`. The server refuses an `emplacement_state` whose time is more than 5 s ahead of or 60 s behind its clock (`clock_skew`), and the simulation clock can fall behind the wall clock when a slow server pushes back on the stream. Without timestamps the server uses its own arrival time, which is right for a live simulation. Turn them on only for a host with a disciplined clock, and a run that keeps up. With `--send-timestamps` a fixed site's heartbeats carry simulation time too, but the server does not read the time of a fixed site's heartbeat, so only a mobile sensor's states face the `clock_skew` window. The radar and the camera never set them.
- **The session is read once.** `GetSession` is not repeated on reconnect. If you change the connection's settings or emplacements (rebind, deactivate, edit the geometry of a bound one), restart the CLI.
- **`--insecure` sends the token in clear.** It exists for a local stack and logs a warning. Use `--tls` (with `--ca` for a private CA, and `--cert`/`--key` where the connection requires a client certificate) anywhere else.
- **Host networking.** The Compose services and the listener both use the host's network. On Docker Desktop (Mac and Windows) the listener's ports are on the Docker VM's network, so either turn on *Enable host networking* (Settings, Resources, Network, 4.34 and later), after which `localhost:<port>` works from your shell too, or run the CLI inside the VM as the Compose services do. `localhost` in the `--addr` of a Compose service is the VM's, not your Mac's. Linux needs nothing.
- **Service-owned fields.** The `exercise` flag, the emplacement binding and the classification floor are applied by the server. The CLI cannot set them on an observation, and an `EMPLACEMENT` identity is dropped.
- **Image size.** The image is an Alpine base with the static binary and the examples. Build it on its own (`docker compose --profile sim build radar-sim`) rather than with the rest of the stack on a small Docker VM. `eoir-sim` and `esm-sim` use the same image.

## What you should see in Sentinel

**Radar and camera.** With both sensors running against the connection above, and terrain on in the ingest service:

- **The sites.** Two location dots on Latrigg, "Latrigg radar" and "Latrigg camera". The radar has a full circle of coverage. The camera has a 60° sector down the lake. Both are listed in the connection's **Emplacements** section with an **online** badge. Stop a CLI and its dot greys within three heartbeat intervals.
- **Vehicles placed on the ground.**
  - The radar's ground targets (`RADAR-Latrigg radar-…`) follow the A66, the A591, the B5289 and the lane round the lake. The server clamps them to the terrain, and each has a **Radar** section in the detail panel: range rate, RCS, SNR, mode, track quality, classification and radar track.
  - The camera's ground targets (`EOIR-Latrigg camera-…`) appear where the line of sight meets the ground. Each has an uncertainty ellipse that stretches along the line of sight, outlined on the ground and drawn at least 15 m wide so a shallow-angle sliver stays visible (the panel's CEP95 is the true figure), and an **EO/IR** section: band, classification, field of view, camera track, "Placed on terrain from a bearing" and CEP95.
  - Camera targets come and go as the shore woods and the hillsides hide them.
- **Drone heights.** The radar's drones carry **Height above ground** in their Radar section, about 100 m over the lake and 120 m over Keswick, and are drawn at that height. With the map's terrain on, heights are drawn relative to the ground.
- **Bearing lines.** The camera cannot range a drone, so it sends no position for one. Each drone the camera sees becomes a dashed bearing line from the camera, labelled with the classification (for example `drone 87%`), fading out over 10 s unless it is seen again. The lake drone sweeps its line across the camera's sector and back. The camera site's detail panel lists its current bearings and why each is a bearing ("Airborne, distance unknown" here).
- **The badge.** An **EXERCISE** badge on everything, if the connection is marked exercise.

**ESM.** With the ESM sensor running against the connection above:

- **The emplacement.** A violet location dot at the sensor, with its coverage sector, shown under the connection's own row in the map's layers pill and listed in the connection's **Emplacements** section on the Connections page with an **online** badge. The connection is green while it heartbeats. Stop the CLI and, within three heartbeat intervals, the connection turns red and the dot greys, hollow, labelled "silent since 14:02Z". A mobile sensor's dot has a dashed outer ring and moves with its reports.
- **The signals.** Moving `SIGNAL` entities named by each emitter's `displayName` (its id when none is set; the source entity id is `EMITTER-<id>`), each with a translucent uncertainty ellipse that follows it and grows when the emitter is farther out. Emitters outside the coverage do not appear; ones inside blink in and out at the probability of detection.
- **The detail panel.** An **RF** section with frequency (MHz or GHz), bandwidth, emitter type, modulation, PRI and PRF, pulse width, scan type and power, summarised like `9.41 GHz · RADAR_SEARCH`.
- **The badge.** An **EXERCISE** badge in the detail header and the list rows, and ` · EX` on the map label. The mark is sticky: an entity that has received an exercise report keeps it.
- **The symbols.** MIL-STD-2525D SIGINT symbols in the exercise context, because the connection is marked Military: the symbol set follows the track environment (50 to 54), the entity code follows the emitter type (communications, jammer, radar, or generic signal intercept), and the affiliation sets the frame. Entities from connections that are not marked Military keep their usual icons.
- **Outbound connections.** A TAK (or other outbound) connection with the default policy sends none of this: exercise entities are denied with the reason `exercise` unless the connection's `policy.allowExercise` is on. Its `denied` counters show it.

Terrain attribution: Produced using Copernicus WorldDEM-30 © DLR e.V. 2010-2014 and © Airbus Defence and Space GmbH 2014-2018 provided under COPERNICUS by the European Union and ESA; all rights reserved.
