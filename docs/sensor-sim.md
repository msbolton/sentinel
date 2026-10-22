# sensor-sim

`sensor-sim` is a command-line simulator of a ground surveillance radar or a daylight/thermal (EO/IR) camera. It sits at a fixed site, watches a scenario of moving vehicles, people and drones over real terrain, and sends what it detects to Sentinel over the gRPC ingest contract. The radar reports range, bearing and elevation. The camera reports only a direction, and Sentinel works out where that direction meets the ground. It is the quickest way to see Sentinel's radar and camera handling without real sensors. It is also a worked example for anyone writing a polar-measurement sender. This page covers what it does, how to set it up and run it, its flags and configuration files, how it behaves when the connection or the server misbehaves, and what you should see in Sentinel.

It lives in `apps/sensor-sim`. It is its own Go module, built without CGO, and its gRPC client is a copy of `ew-sensor`'s (see `docs/ew-sensor.md`). The ingest contract, the rejection reasons and the connection settings are in `docs/feeds.md` (gRPC connections). How a polar report is bound to an emplacement and placed from it is in `docs/emplacements.md` ("Reporting over gRPC").

## What it does

One process is one sensor: `--kind radar` or `--kind eoir`. To have both, run two processes. They agree on the targets without talking to each other, because a target's position depends only on the scenario and the wall clock. Every target starts its route at the scenario's `epoch` and moves along its waypoints at its speed from then on. Two processes that load the same scenario file therefore see every target in the same place at the same moment.

Each scan (default 1 per second) the simulator:

1. works out where every target is now: on the route, at the terrain height there plus the target's height above ground;
2. for each target inside the sensor's coverage (range, azimuth sector and elevation window), rolls against the probability of detection;
3. for the camera, also checks the line of sight over the terrain. A target behind a hill, or behind trees and buildings (the terrain is a surface model), is not seen;
4. sends each detection as an `Observation` with no position, only a polar measurement:
   - The **radar** sends range, azimuth, elevation and range rate, each with its 1-sigma, plus a radar detail: RCS, SNR, mode `TRACK`, track quality, a classification (vehicle, person or drone; right 90% of the time, otherwise unknown) and its confidence, and a stable radar track id.
   - The **camera** sends azimuth and elevation, marked `direction_only`, plus an EO/IR detail: the band, a classification and its confidence, the field of view, a bounding box and a stable camera track id.

Every report names its emplacement in `Report.emplacement_id`, and the server turns the polar measurement into a position from that emplacement. Identities are stable per sensor and target: `RADAR-<sensor name>-<target id>` and `EOIR-<sensor name>-<target id>`. The radar and the camera therefore make separate entities for the same target; Sentinel does not fuse them.

## The demo scenario

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

## Set up

You need the stack running with the listener replica (the `inbound` profile), which hosts the port your connection listens on. `docs/development.md` and `docs/feeds.md` ("Listeners") cover starting it, and Docker Desktop's *Enable host networking* setting. The ingest service needs terrain on (`INGEST_TERRAIN_CACHE_DIR`) to place the camera's detections. Without it they arrive as bearings with the reason `terrain_unavailable`, and the radar's ground targets are not clamped to the ground.

1. **Create a `grpc` connection** in Connections (or `POST /api/v1/feeds` with `"driver":"grpc"`), on a free port from the port hint, with these settings:
   - `senderManagedEmplacements`: on. This is required: the simulator creates its own emplacement with `UpsertEmplacement`, by name, from its configuration, and refuses to run on a connection that does not allow that.
   - `exercise`: on. Everything the connection brings in is marked EXERCISE, and outbound connections refuse it unless their policy allows it.

   For example:

   ```json
   {
     "id": "radar-eoir-sim", "name": "Radar and camera sim", "driver": "grpc", "enabled": true,
     "config": {
       "port": 6201,
       "exercise": true,
       "senderManagedEmplacements": true
     }
   }
   ```

   One connection can carry both sensors: each creates its own emplacement ("Latrigg radar", "Latrigg camera").

2. **Issue a token.** Open the connection's row and issue a token under **Tokens**. The secret (`si_...`) is shown once.

3. **Give the token to the CLI.** Put it in `secrets/sensor-sim.env`, which is gitignored and which the Compose services read when it exists:

   ```
   SENSOR_SIM_TOKEN=si_...
   # optional:
   SENSOR_SIM_ADDR=localhost:6201
   ```

   Outside Compose, `export SENSOR_SIM_TOKEN=si_...`, or `SENTINEL_INGEST_TOKEN`, which the connection's send guide uses.

## Run it

With Compose, the `sim` profile has a service for each sensor. Each runs the CLI in a container on the host's network with the bundled example, and keeps its terrain tiles in the `sentinel-terrain-cache` volume. Pass the address on the `run` line, in two terminals:

```
docker compose -p sentinel --profile inbound --profile sim run --rm radar-sim \
  --addr localhost:6201
docker compose -p sentinel --profile inbound --profile sim run --rm eoir-sim \
  --addr localhost:6201
```

Each service is `sensor-sim --kind radar --config examples/radar.yaml` (or `--kind eoir --config examples/camera.yaml`) with `--terrain-cache /var/cache/sentinel/terrain`. Flags on the `run` line are added after these, and a flag given twice takes its last value.

The examples choose plaintext HTTP/2 (`connection.insecure: true`), which is what the listener speaks unless you enabled `tls` on the connection, so no transport flag is needed. With `SENSOR_SIM_ADDR` in `secrets/sensor-sim.env` the services also start in the background, with no `run` line at all: `docker compose -p sentinel --profile inbound --profile sim up -d radar-sim eoir-sim`. For TLS, add `--tls --ca /ca.pem` to the `run` line, which replaces the examples' choice, and mount the CA file, for example with `-v "$PWD/secrets/ca.pem:/ca.pem:ro"`. Stop a `run` with Ctrl-C.

The first run downloads the terrain tile before streaming and logs `looking up the ground height at the site`. Later runs, and the other sensor, find it in the cache.

The connection's send guide prints the same command for a binary on your path:

```
sensor-sim --kind eoir --addr localhost:6201 --token "$SENTINEL_INGEST_TOKEN" --insecure --config examples/camera.yaml
```

Its `--insecure` repeats the examples' choice; a guide for a TLS connection prints `--tls`, which replaces it.

To run from source, from the repository root:

```
export SENSOR_SIM_TOKEN=si_...
(cd apps/sensor-sim && go run ./cmd/sensor-sim --kind radar --addr localhost:6201 --insecure \
  --config examples/radar.yaml --terrain-cache ~/.cache/sentinel/terrain)
```

The examples set `terrain.cacheDir` to the container's `/var/cache/sentinel/terrain`. Outside the container, pass `--terrain-cache` with a folder you can write to. If the folder cannot be written, the simulator warns and keeps tiles in memory only, and downloads them again on every run. `nx build sensor-sim` puts a binary in `dist/apps/sensor-sim/`. Add `--verbose` to log every report as JSON as it is sent, and each ack.

## Flags and configuration

Flags override the configuration file, which overrides the defaults. Most YAML keys under `sensor` have a flag in kebab case: `rangeKm` is `--range-km`, `sigmaAzDeg` is `--sigma-az`. The scenario is set in YAML only. The connection address and token also come from the environment.

| Flag | Default | Meaning |
|------|---------|---------|
| `--kind` | none (required) | `radar` or `eoir`. |
| `--addr host:port` | none (required) | Ingest listener. Env `SENSOR_SIM_ADDR`. |
| `--token` | none (required) | Ingest token. Env `SENSOR_SIM_TOKEN`, then `SENTINEL_INGEST_TOKEN`. |
| `--insecure` | off | Plaintext h2c. The token travels in clear. Exactly one of `--insecure` and `--tls`. |
| `--tls` | off | TLS with the system roots. |
| `--ca file` | none | Extra root CA (needs `--tls`). |
| `--cert file`, `--key file` | none | Client certificate for a connection that requires one (need `--tls`, and both together). |
| `--config file` | none | YAML file; see below. |
| `--name` | `sensor-sim` | Emplacement name, unique across Sentinel. |
| `--lat`, `--lon` | none | Site position, degrees (0,0 counts as unset). |
| `--alt` | none | Site altitude, metres above the WGS84 ellipsoid. |
| `--height-above-ground` | none | Mast height, metres (0 to 1000). The site altitude is the terrain there plus this. Needs terrain. Not with `--alt`. |
| `--boresight` | 0 | Boresight azimuth, degrees from true north. |
| `--width` | 90 | Azimuth coverage, degrees (360 is all round). |
| `--range-km` | 10 | Maximum range. |
| `--min-range-km` | 0 | Minimum range (a blind zone). |
| `--elev-min`, `--elev-max` | camera −15 and +75; radar unlimited | Elevation coverage, degrees. |
| `--rate hz` | 1 | Scans per second, above 0 and at most 100. |
| `--pd` | 0.9 | Probability of detection per target per scan, in (0, 1]. The radar scales it down with range and target size. |
| `--sigma-az`, `--sigma-el` | 0.5 | Azimuth and elevation noise, 1 sigma, degrees. |
| `--sigma-range-m` | 10 | Range noise, 1 sigma, metres (radar). |
| `--band` | `VISIBLE` | Camera band: `VISIBLE`, `SWIR`, `MWIR` or `LWIR`. |
| `--terrain-cache dir` | none (terrain off) | Where Copernicus GLO-30 tiles are cached. |
| `--terrain-url` | the Copernicus bucket | Tile source. |
| `--seed` | from the clock | Random seed; the log shows it. |
| `--duration` | 0 | Run time, e.g. `5m`; 0 runs until signalled. |
| `--heartbeat-interval` | `5s` | How often the emplacement sends its `EmplacementState` heartbeat; whole seconds from 1s to 300s. |
| `--verbose` | off | Log every report as JSON as it is sent, and each ack. |

**Rules the configuration must meet.** The simulator checks these at start and exits 1 with every problem listed.

- **A camera needs terrain**, which decides what it can see. Without it the camera would see through hills, and its targets would sit at 0 m instead of on the ground. A camera also needs `--alt` or `--height-above-ground`.
- **A radar with terrain needs `--alt` or `--height-above-ground`.** Without either, the site would be at 0 m, below targets that stand on the real ground, and every elevation would be wrong. A radar without terrain may leave both unset: its targets then move over flat ground at 0 m, and so does its site.
- **The elevation window must not be empty.** It is checked after the defaults are filled in, so `--elev-min 80` alone on a camera is refused (its default maximum is 75), as is `--elev-max -20` alone.

**Precedence.** A flag set on the command line beats the environment, which beats the YAML, which beats the default. An explicit `--rate 1` beats a YAML `rate: 5`. The token is `--token`, then `SENSOR_SIM_TOKEN`, then `SENTINEL_INGEST_TOKEN`, then the YAML; the address is `--addr`, then `SENSOR_SIM_ADDR`, then the YAML. A transport flag, `--insecure` or `--tls`, replaces the YAML's transport, so `--tls --ca ca.pem` works with the bundled examples although they set `insecure: true`; `--insecure` also drops the YAML's `ca`, `cert` and `key`. Keep the token out of YAML files: they are easy to commit by mistake.

**YAML.** Unknown keys are an error. The top-level keys are:

- `connection`: `addr`, `token`, `insecure`, `tls`, `ca`, `cert`, `key`.
- `sensor`: `kind`, `name`, `lat`, `lon`, `alt`, `heightAboveGroundM`, `boresight`, `width`, `rangeKm`, `minRangeKm`, `elevationMinDeg`, `elevationMaxDeg`, `rate`, `pd`, `sigmaAzDeg`, `sigmaElDeg`, `sigmaRangeM`, `heartbeatInterval`, `band`.
- `scenario`, or `scenarioFile`, but not both. `scenarioFile` names a file holding the scenario's own keys at its top level, relative to the configuration file, so a radar and a camera can share one. The keys are:
  - `epoch`, an RFC 3339 time;
  - `targets`, a list. Each target has `id` (unique), `kind` (`vehicle`, `drone` or `person`), `displayName`, `speedKts`, `heightAboveGroundM` (0 for a ground target), `loop` and `waypoints` (a list of `{lat, lon}`). Without `loop`, a target stops at its last waypoint.
- `terrain`: `cacheDir`, `sourceUrl`.
- `seed`, `duration`, `verbose`.

**Set the epoch.** Leave `epoch` unset and it is the start of the current UTC day, 00:00 UTC, which two processes started on the same day agree on. But then a route that does not loop starts at midnight UTC: by the afternoon its target has long since parked at its last waypoint. Every route restarts at 00:00 UTC the next day, mid-run. The simulator warns when the epoch is unset. Set it explicitly, as the examples do, and give every sensor the same value (one shared `scenarioFile` does that). A looping route also has no surprises, since the epoch then only decides where in the loop each target is.

## Behaviour

**Session and emplacement.** The CLI calls `GetSession`, then `UpsertEmplacement` with its configuration:

- a `FIXED` emplacement of sensor type `RADAR` or `EO_IR`, with the configured name;
- the site position, with the resolved altitude (the terrain plus the mast height when you gave `--height-above-ground`);
- the mast height itself, as `height_above_ground_m`;
- the boresight (true north);
- the coverage: minimum and maximum range, azimuth width and elevation limits. A camera always declares its window, defaults included; a radar declares only the limits it was given.

The upsert is by name, so a restart reuses the emplacement. The name must be unique across all emplacements: a name an operator already used fails with `AlreadyExists`, and the error suggests `--name`. The emplacement inherits the connection's `exercise` flag. A connection without `senderManagedEmplacements` is refused with exit code 1. Unlike ew-sensor, this simulator never reports as an emplacement someone else made: its site and coverage come from its configuration.

**Heartbeats.** Every stream begins with the emplacement's `EmplacementState`, before any observation. Another follows every `--heartbeat-interval` (default 5 s), each declaring that interval. The server calls the emplacement offline after three missed intervals and refuses its observations as `no_heartbeat`. A `no_heartbeat` ack makes the CLI heartbeat at once (at most once a second) and send the refused detection again, once.

**Terrain.**

- *The source.* Heights come from Copernicus GLO-30, converted from mean sea level to the WGS84 ellipsoid, which is the datum of every altitude in Sentinel. The model is a surface model: it includes trees and buildings, which block the camera as a hill does.
- *Waiting for it.* Before streaming, the CLI looks up the ground at the site and under every target, which on a cold cache downloads their tiles. It waits up to 300 s. Each scan's terrain work is then bounded to 2 s. A scan the terrain cannot answer in time is skipped, counted in `scans_skipped` and warned about once per run of skipped scans. The download carries on in the background, and later scans use the tile once it is in.
- *Where there is none.* A tile that is missing from the source is sea, at 0 m above mean sea level. Land in a known gap in GLO-30 (Armenia and Azerbaijan) has no heights at all, so a scenario there is skipped scan after scan.

**The radar's numbers.**

- *Detection.* The detection probability is `pd × min(1, (max range / range)⁴ × size)`, where `size` is 1 for a vehicle, 0.3 for a person and 0.15 for a drone. A vehicle is detected at `pd` all the way out; a drone's chance falls off over the outer third of the range.
- *SNR.* SNR is 18 dB for a vehicle at maximum range and grows by 40 dB per decade closer. It is kept between 3 dB and 60 dB, so a target at or very near the radar reports 60 dB.
- *Range rate.* It is the true rate of change of range, with no noise; positive is receding.

**Reconnecting, backpressure and limits** are ew-sensor's ("Behaviour" in `docs/ew-sensor.md`):

- `Unauthenticated` and `PermissionDenied` end the run with exit code 2, and `Unimplemented` with exit code 1.
- Any other end of the stream is retried with a jittered backoff from 0.5 s to 30 s. Reports that were never acked are not resent.
- A `rate_limited` ack halves the scan rate for 10 s. The CLI warns at the start when `rate × targets` can exceed the connection's `maxMessagesPerSec`.
- An `unknown_emplacement` rejection within 5 s of the upsert is retried once.
- A report larger than the session's `max_message_bytes` is dropped and logged.

## Exit codes and stats

| Code | When |
|------|------|
| 0 | `--duration` elapsed, SIGINT or SIGTERM, or `--help`. |
| 1 | A flag, YAML or validation error (the message and `Run sensor-sim --help for the flags.` go to stderr); an unreadable `--ca`, `--cert` or `--key`; no ground height for the site within 300 s; a connection without `senderManagedEmplacements`; `UpsertEmplacement` `InvalidArgument` or `AlreadyExists`; `Unimplemented`. |
| 2 | `Unauthenticated` or `PermissionDenied` from any call: an unknown, revoked or expired token, another connection's token, a connection above the token's classification; or the sender-managed emplacement cap. |

The CLI logs to stderr (text, one line per event; the token appears only as a short prefix). Every 10 s, and once more at exit as `final stats`, it logs a line such as:

```
msg=stats sent=412 ok=410 rejected=map[rate_limited:2] dead_lettered=0 retried=0 reconnects=1 dropped_oversize=0 dropped_offline=3 scans_skipped=0
```

The counters are ew-sensor's, plus `scans_skipped`: scans the terrain could not answer within 2 s.

## Limits and gotchas

- **Wall-clock truth.** Targets move with the wall clock, so a fixed `--seed` repeats the detection rolls and the noise, but not the positions. The seed and the logged start time together describe a run.
- **No fusion.** The radar and the camera make separate entities for the same target.
- **The session is read once.** `GetSession` is not repeated on reconnect. If you change the connection's settings, restart the CLI.
- **`--insecure` sends the token in clear.** It exists for a local stack and logs a warning.
- **Host networking.** As for ew-sensor: on Docker Desktop, `localhost` in `--addr` is the Docker VM's, and the listener's port is reachable there. Turn on *Enable host networking* to reach it from your shell too.
- **Image size.** Build the image on its own (`docker compose --profile sim build radar-sim`) rather than with the rest of the stack on a small Docker VM. `eoir-sim` uses the same image.

## What you should see in Sentinel

With both sensors running against the connection above, and terrain on in the ingest service:

- **The sites.** Two location dots on Latrigg, "Latrigg radar" and "Latrigg camera". The radar has a full circle of coverage. The camera has a 60° sector down the lake. Both are listed in the connection's **Emplacements** section with an **online** badge. Stop a CLI and its dot greys within three heartbeat intervals.
- **Vehicles placed on the ground.**
  - The radar's ground targets (`RADAR-Latrigg radar-…`) follow the A66, the A591, the B5289 and the lane round the lake. The server clamps them to the terrain, and each has a **Radar** section in the detail panel: range rate, RCS, SNR, mode, track quality, classification and radar track.
  - The camera's ground targets (`EOIR-Latrigg camera-…`) appear where the line of sight meets the ground. Each has an uncertainty ellipse that stretches along the line of sight, outlined on the ground and drawn at least 15 m wide so a shallow-angle sliver stays visible (the panel's CEP95 is the true figure), and an **EO/IR** section: band, classification, field of view, camera track, "Placed on terrain from a bearing" and CEP95.
  - Camera targets come and go as the shore woods and the hillsides hide them.
- **Drone heights.** The radar's drones carry **Height above ground** in their Radar section, about 100 m over the lake and 120 m over Keswick, and are drawn at that height. With the map's terrain on, heights are drawn relative to the ground.
- **Bearing lines.** The camera cannot range a drone, so it sends no position for one. Each drone the camera sees becomes a dashed bearing line from the camera, labelled with the classification (for example `drone 87%`), fading out over 10 s unless it is seen again. The lake drone sweeps its line across the camera's sector and back. The camera site's detail panel lists its current bearings and why each is a bearing ("Airborne, distance unknown" here).
- **The badge.** An **EXERCISE** badge on everything, if the connection is marked exercise.

Terrain attribution: Produced using Copernicus WorldDEM-30 © DLR e.V. 2010-2014 and © Airbus Defence and Space GmbH 2014-2018 provided under COPERNICUS by the European Union and ESA; all rights reserved.
