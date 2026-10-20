# ew-sensor

`ew-sensor` is a command-line simulator of an electronic-support (ESM) sensor. It sits at an emplacement, watches a set of moving RF emitters (search and fire-control radars, SATCOM terminals, datalinks, jammers) and sends each detection to Sentinel as a direction-finding fix with an error ellipse, over the gRPC ingest contract. It is the quickest way to put exercise-grade signals intelligence on the map without a real sensor, and a worked example for anyone writing a sender of their own. This page covers what it does, how to set it up and run it, its flags and configuration file, how it behaves when the connection or the server misbehaves, and what you should see in Sentinel.

It lives in `apps/ew-sensor` (its own Go module, no CGO). The ingest contract it speaks, the rejection reasons and the connection settings are in `docs/feeds.md` (gRPC connections); how a report is bound to an emplacement and how a mobile emplacement reports its position are in `docs/emplacements.md` ("Reporting over gRPC").

## What it does

Each tick (default 1 per second) the simulator:

1. moves every emitter (static, a random walk, or a loop over waypoints);
2. for a mobile sensor, moves the ownship along its path;
3. for each emitter inside the sensor's coverage (range between the minimum and maximum, azimuth within half the width of the boresight), rolls against the probability of detection;
4. for each detection, draws a fix around the true position and sends it as an `Observation` with the entity type `SIGNAL`, the identity `EMITTER-<id>`, an uncertainty ellipse and the RF parameters of the emitter (frequency, bandwidth, emitter type, modulation, PRI, pulse width, scan type, power).

Every report names its emplacement in `Report.emplacement_id`. A fixed seed gives the same fixes: the simulation clock is the sum of the tick intervals, not the wall clock.

## Set up

You need the stack running with the listener replica (the `inbound` profile), which hosts the port your connection listens on. `docs/development.md` and `docs/feeds.md` ("Listeners") cover starting it, and Docker Desktop's *Enable host networking* setting.

1. **Create a `grpc` connection** in Connections (or `POST /api/v1/feeds` with `"driver":"grpc"`), on a free port from the port hint, with these settings:
   - `exercise`: on. Everything the connection brings in is marked EXERCISE, and outbound connections refuse it unless their policy allows it.
   - `military`: on ("Military connection: draw its entities with MIL-STD-2525D symbols").
   - `senderManagedEmplacements`: on. The sensor creates its own emplacement with `UpsertEmplacement`, so you do not have to make one by hand. Leave it off if you create the emplacement yourself, bind it to the connection and pass `--emplacement-id`.

   For example:

   ```json
   {
     "id": "ew-sim", "name": "EW sim", "driver": "grpc", "enabled": true,
     "config": {
       "port": 6200,
       "exercise": true,
       "military": true,
       "senderManagedEmplacements": true
     }
   }
   ```

   Raise `maxEmplacements` (default 16) or `maxMessagesPerSec` (default 1000) only if your run needs it.

2. **Issue a token.** Open the connection's row and issue a token under **Tokens**. The secret (`si_...`) is shown once. A token without a classification ceiling is limited to the connection's own level.

3. **Give the token to the CLI.** Put it in `secrets/ew-sensor.env`, which is gitignored and which the Compose service reads when it exists:

   ```
   EW_SENSOR_TOKEN=si_...
   # optional:
   EW_SENSOR_ADDR=localhost:6200
   ```

   Outside Compose, `export EW_SENSOR_TOKEN=si_...` (or `SENTINEL_INGEST_TOKEN`, which the connection's send guide uses).

## Run it

With Compose, the `sim` profile builds and runs the CLI in a container on the host's network:

```
docker compose -p sentinel --profile inbound --profile sim run --rm ew-sensor \
  --addr localhost:6200 --insecure --config examples/coastal-esm.yaml
```

`--insecure` is plaintext HTTP/2, which is what the listener speaks unless you enabled `tls` on the connection. `examples/coastal-esm.yaml` is a fixed coastal site looking west to sea with five emitters. The image keeps the examples in `/app/examples`, and the working directory is `/app`, so the path resolves as written. Stop it with Ctrl-C.

A ship-borne sensor that moves and reports its own position, from the mobile example. The file says `tls: true`; the `--insecure` on this command line overrides it, which suits a local plaintext listener:

```
docker compose -p sentinel --profile inbound --profile sim run --rm ew-sensor \
  --addr localhost:6200 --insecure --config examples/mobile-esm.yaml
```

For a connection with `tls` enabled, drop `--insecure` (or pass `--tls`) and add `--ca` for a private CA. Mount the CA file into the container, for example with `-v "$PWD/secrets/ca.pem:/ca.pem:ro"`:

```
docker compose -p sentinel --profile inbound --profile sim run --rm \
  -v "$PWD/secrets/ca.pem:/ca.pem:ro" ew-sensor \
  --addr localhost:6200 --tls --ca /ca.pem --config examples/mobile-esm.yaml
```

You can also turn any configuration into a mobile one with flags: `--mobile --path circle --speed-kts 12` (a circle of 5 km radius around `--lat`/`--lon`) or `--path waypoints` with waypoints in the YAML.

To run from source, from the repository root:

```
export EW_SENSOR_TOKEN=si_...
(cd apps/ew-sensor && go run ./cmd/ew-sensor --addr localhost:6200 --insecure \
  --config examples/coastal-esm.yaml)
```

or `nx build ew-sensor` for a binary in `dist/apps/ew-sensor/`. Without a `--config` file you must give `--lat` and `--lon` (and `--name` if you do not want `ew-sensor`); the examples carry them in the YAML.

## Flags and configuration

Flags override the configuration file, which overrides the defaults. Most YAML keys have a flag of the same name in kebab case (`rangeKm` is `--range-km`, `sigmaM` is `--sigma-m`). These do not, and are set in the YAML only: `emplacement.azimuthReference`, `sensor.classification`, `ownship.radiusKm`, `ownship.waypoints`, and every per-emitter field under `emitters` (`--emitters N` sets only how many). `emplacement.mobility` has just `--mobile` (`--mobile=false` forces `FIXED`). The connection address and token also come from the environment.

| Flag | Default | Meaning |
|------|---------|---------|
| `--addr host:port` | none (required) | Ingest listener. Env `EW_SENSOR_ADDR`. |
| `--token` | none (required) | Ingest token. Env `EW_SENSOR_TOKEN`, then `SENTINEL_INGEST_TOKEN`. |
| `--insecure` | off | Plaintext h2c. The token travels in clear. Exactly one of `--insecure` and `--tls`. |
| `--tls` | off | TLS with the system roots. |
| `--ca file` | none | Extra root CA (needs `--tls`). |
| `--cert file`, `--key file` | none | Client certificate for a connection that requires one (need `--tls`, and both together). |
| `--config file` | none | YAML file; see below. |
| `--emplacement-id` | none | Use this existing emplacement; otherwise one is created from the flags below. |
| `--name` | `ew-sensor` | Emplacement name. |
| `--lat`, `--lon` | none | Sensor position, degrees. Required without `--emplacement-id` (0,0 counts as unset). |
| `--alt` | 0 | Sensor altitude, metres. |
| `--boresight` | 0 | Boresight azimuth, degrees. |
| `--width` | 120 | Azimuth coverage width, degrees (360 is omnidirectional). |
| `--range-km` | 120 | Maximum range. |
| `--min-range-km` | 0 | Minimum range. |
| `--emitters N` | 5 | Number of emitters. Extra ones are synthesised (`SYN-01`, ...); surplus YAML ones are dropped. |
| `--rate hz` | 1 | Ticks per second. |
| `--pd` | 0.9 | Probability of detection per emitter per tick, in (0, 1]. |
| `--sigma-m` | 250 | Position error, 1 sigma, at the 10 km reference range, metres. |
| `--seed` | from the clock | Random seed; the log shows it. |
| `--duration` | 0 | Run time, e.g. `5m`; 0 runs until signalled. |
| `--mobile` | off | The sensor moves along an ownship path and reports its position. |
| `--path` | `circle` | Ownship path: `circle` or `waypoints`. |
| `--speed-kts` | 12 | Ownship speed. |
| `--send-timestamps` | off | Set source timestamps (see the limits below). |

**Precedence.** A flag set on the command line beats the environment, which beats the YAML, which beats the default; an explicit `--rate 1` beats a YAML `rate: 5`. The token is `--token`, then `EW_SENSOR_TOKEN`, then `SENTINEL_INGEST_TOKEN`, then the YAML. The address is `--addr`, then `EW_SENSOR_ADDR`, then the YAML. A YAML file with a token is convenient but easy to commit by mistake: prefer the environment.

**YAML.** Unknown keys are an error. The top-level keys are:

- `connection`: `addr`, `token`, `insecure`, `tls`, `ca`, `cert`, `key`.
- `emplacement`: `emplacementId`, `name`, `lat`, `lon`, `alt`, `boresight`, `width`, `rangeKm`, `minRangeKm`, `mobility` (`FIXED` or `MOBILE`), `azimuthReference` (`TRUE_NORTH`, or `PLATFORM`, which needs `MOBILE`: the boresight is then measured from the ship's bow and turns with it).
- `sensor`: `rate`, `pd`, `sigmaM`, `classification` (optional: empty takes the connection's level; when set it is stamped on every observation), `sendTimestamps`.
- `ownship` (mobile only): `path` (`circle` or `waypoints`), `speedKts`, `radiusKm` (circle, default 5), `waypoints` (a list of `{lat, lon}`, at least two, looped).
- `emitters`: a list, each with `id` (unique), `displayName`, `emitterType` (for example `RADAR_SEARCH`, `RADAR_TRACK`, `SATCOM`, `COMMS_DATALINK`, `JAMMER`), `frequencyHz` (required), `bandwidthHz`, `priUs`, `pulseWidthUs`, `modulation`, `scanType`, `powerDbm`, `affiliation`, `trackEnvironment` (`AIR`, `SEA_SURFACE`, `SUBSURFACE`, `GROUND`, `SPACE`, `UNKNOWN`; unset or `LAND` is `GROUND`), `start` (`{lat, lon}`) and `motion` (`type` `static`, `random-walk` or `waypoints`, with `speedKts`, and `waypoints` for the last).
- `duration`, `seed`.

`examples/coastal-esm.yaml` and `examples/mobile-esm.yaml` are annotated starting points.

## Behaviour

**Session and emplacement.** The CLI calls `GetSession`, then finds the emplacement it reports as:

1. with `--emplacement-id`, that emplacement from the session (ids are compared without regard to case); an id the token cannot see is an error;
2. otherwise, when the connection has `senderManagedEmplacements` on, `UpsertEmplacement` with the configured name, position, coverage, boresight and mobility. The upsert is by name, so restarting the CLI reuses the same emplacement and does not create a new one each time;
3. otherwise the connection's single bound, active emplacement;
4. otherwise it exits with an error that says what to do.

For an emplacement that already exists on the server (cases 1 and 3), the server owns the geometry: its position, boresight, azimuth reference, coverage and mobility win over the flags and YAML. A created emplacement keeps the configured geometry.

**The fix and the ellipse.** The reported position is the true one plus Gaussian noise. The along-line-of-sight sigma is `sigma-m`; the cross-range sigma grows with range, as `sigma-m * max(1, range_km / 10)`, as it does for a direction finder. The uncertainty sent is the 95% ellipse: semi-major `2.4477 * cross sigma`, semi-minor `2.4477 * along sigma`, in metres, with the orientation in degrees from north (the long axis lies across the line of sight). Fixes are therefore long thin ellipses at long range. Sentinel draws them around the entity.

**Mobile sensors.** A mobile sensor sends an `EmplacementState` (its position, heading and velocity) once a second, and always one first on every new stream, before any observation, so the server can place the measurements. The server refuses the others as `stale_ownship` until it has a position.

**Reconnecting.** `Unauthenticated` and `PermissionDenied` end the run with exit code 2, and `Unimplemented` (the address is not a Sentinel ingest service) with exit code 1. Any other end of the stream (`Unavailable`, a transport error, a clean close by the server, `ResourceExhausted`) is logged as a warning and the CLI reconnects after a jittered exponential backoff from 0.5 s to 30 s. The line carries the cause, for example `msg="stream ended; reconnecting" stream=1 code=Unavailable message="connection moving: reconnect" backoff=612ms`. The backoff starts again once a stream has had an accepted report or has stayed open for 30 s. While disconnected the simulation keeps running, and the fixes it produces with no open stream are dropped and counted in `dropped_offline`, so the sequence of fixes for a seed does not depend on the connection. Reports that were sent but not acknowledged when a stream ended are not sent again: they are lost, not replayed.

**Backpressure and limits.**
- A `rate_limited` rejection halves the tick rate for 10 s (further ones extend the hold, they do not compound), then restores it. The CLI warns at the start when `rate * (emitters + 1 if mobile)` exceeds the connection's `maxMessagesPerSec`.
- An `unknown_emplacement` rejection within 5 s of creating the emplacement is retried once after 250 ms, in case the replica that serves the stream has not seen the new row yet. Outside that window it counts as a rejection.
- A report larger than the session's `max_message_bytes` is dropped by the CLI and logged, never sent. If the server ends a stream with `ResourceExhausted` for an oversize message, the report is dropped and the CLI reconnects.
- `UpsertEmplacement` retries `Unavailable` ("saved; retry") and "rate limited" answers; the emplacement cap (`maxEmplacements`) is fatal.

## Exit codes and stats

| Code | When |
|------|------|
| 0 | `--duration` elapsed, SIGINT or SIGTERM, or `--help`. |
| 1 | A flag, YAML or validation error (the message and `Run ew-sensor --help for the flags.` go to stderr); an unreadable `--ca`, `--cert` or `--key`; an `--emplacement-id` not in the session; no bound emplacement, or more than one; an emplacement with no position; `UpsertEmplacement` `InvalidArgument`, `AlreadyExists` or `NotFound`; `Unimplemented`. |
| 2 | `Unauthenticated` or `PermissionDenied` from any call: an unknown, revoked or expired token, another connection's token, a connection that is not sender-managed, a classification above the token's ceiling; or the sender-managed emplacement cap. |

A revoked token ends a running stream with `Unauthenticated`, so the CLI exits 2 and does not reconnect.

The CLI logs to stderr (text, one line per event; the token appears only as its first 8 characters). Every 10 s, and once more at exit as `final stats`, it logs a line such as:

```
msg=stats sent=412 ok=410 rejected=map[rate_limited:2] dead_lettered=0 retried=0 reconnects=1 dropped_oversize=0 dropped_offline=3
```

`sent` counts reports written to the stream, `ok` those acknowledged `OK`, `rejected` the `REJECTED` acks by reason, `dead_lettered` the `DEAD_LETTERED` ones, `retried` the `unknown_emplacement` retries, and `reconnects` the streams opened after the first.

## Limits and gotchas

- **Timestamps.** Source timestamps are not set unless you pass `--send-timestamps`. The server refuses an `emplacement_state` whose time is more than 5 s ahead of or 60 s behind its clock (`clock_skew`), and the simulation clock can fall behind the wall clock when a slow server pushes back on the stream. Without timestamps the server uses its own arrival time, which is right for a live simulation. Turn them on only for a host with a disciplined clock, and a run that keeps up.
- **The session is read once.** `GetSession` is not repeated on reconnect. If you change the connection's emplacements (rebind, deactivate, edit the geometry of a bound one), restart the CLI.
- **`--insecure` sends the token in clear.** It exists for a local stack and logs a warning. Use `--tls` (with `--ca` for a private CA, and `--cert`/`--key` where the connection requires a client certificate) anywhere else.
- **Host networking.** The Compose service and the listener both use the host's network. On Docker Desktop (Mac and Windows) the listener's ports are on the Docker VM's network, so either turn on *Enable host networking* (Settings, Resources, Network, 4.34 and later), after which `localhost:<port>` works from your shell too, or run the CLI inside the VM as the Compose service does. `localhost` in the `--addr` of the Compose service is the VM's, not your Mac's. Linux needs nothing.
- **Service-owned fields.** The `exercise` flag, the emplacement binding and the classification floor are applied by the server. The CLI cannot set them on an observation, and an `EMPLACEMENT` identity is dropped.
- **Image size.** The image is an Alpine base with the static binary and the examples; build it on its own (`docker compose --profile sim build ew-sensor`) rather than together with the rest of the stack on a small Docker VM.

## What you should see in Sentinel

With the sensor running against the connection above:

- **The emplacement.** A violet (`#8b5cf6`) location dot at the sensor, with its coverage sector (the Emplacements panel lists it, and the built-in layer toggles it). A mobile sensor's dot has a dashed outer ring and moves with its reports, the footprint turning with the ship's heading when the azimuth reference is `PLATFORM`.
- **The signals.** Moving `SIGNAL` entities named `EMITTER-<id>`, each with a translucent uncertainty ellipse that follows it and grows when the emitter is farther out. Emitters outside the coverage do not appear; ones inside blink in and out at the probability of detection.
- **The detail panel.** An **RF** section with frequency (MHz or GHz), bandwidth, emitter type, modulation, PRI and PRF, pulse width, scan type and power, summarised like `9.41 GHz · RADAR_SEARCH`.
- **The badge.** An **EXERCISE** badge in the detail header and the list rows, and ` · EX` on the map label. The mark is sticky: an entity that has received an exercise report keeps it.
- **The symbols.** MIL-STD-2525D SIGINT symbols in the exercise context, because the connection is marked Military: the symbol set follows the track environment (50 to 54), the entity code follows the emitter type (communications, jammer, radar, or generic signal intercept), and the affiliation sets the frame. Entities from connections that are not marked Military keep their usual icons.
- **Outbound connections.** A TAK (or other outbound) connection with the default policy sends none of this: exercise entities are denied with the reason `exercise` unless the connection's `policy.allowExercise` is on. Its `denied` counters show it.
