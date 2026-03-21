# Track Service — Gap Fixes & Frontend Integration

**Linear Issue:** SEN-80
**Date:** 2026-03-21

## Overview

Complete the track-service backend (missing kinematic fields, health endpoint, SSE replay) and build Angular frontend components for track visualization and replay on the CesiumJS globe.

## 1. Backend Gap Fixes

### 1.1 Batch Service — Complete Kinematic Fields

The `BufferedPoint` interface and bulk INSERT SQL in `track-batch.service.ts` already include `altitude`, `velocityNorth`, `velocityEast`, `velocityUp`, and `circularError`. The following 10 fields are present in the `TrackPoint` entity but missing from `BufferedPoint` and the INSERT:

- `feedId`, `trackProcessingState`
- `accelNorth`, `accelEast`, `accelUp`
- `posCovariance`, `posVelCovariance`, `velCovariance` (float arrays)
- `altitudeError`, `sensorId`

Changes required:
- Extend `BufferedPoint` interface with these 10 fields
- Add columns to the raw INSERT SQL (note: array fields use `$N::float[]` casting)
- Update `TrackService.recordPoint()` signature to accept the new fields
- Update `TrackService.handlePositionEvent()` payload type to include them

### 1.2 Health Controller

New `HealthController` at `/health` returning `{ status: 'ok' }`. Simple implementation — no dependency checks. Register in `AppModule`. Docker-compose health check does `GET http://localhost:3002/health`.

### 1.3 Kafka Handler Alignment

The `@MessagePattern('events.entity.position')` payload type in `TrackController` is narrower than what the service supports. Update it to include all kinematic fields and forward them to `handlePositionEvent()`.

### 1.4 TrackPointResult — Include Altitude

The `TrackPointResult` interface and all SELECT queries in `TrackService` omit `altitude`. Add `altitude: number | null` to `TrackPointResult` and include `tp.altitude AS altitude` in `getHistory()`, `getSimplifiedTrack()`, and `getLatestPositions()` queries.

## 2. SSE Track Replay

### 2.1 Endpoint

`GET /tracks/:entityId/replay-stream` with query params: `startTime`, `endTime`, `speedMultiplier`.

Returns `Content-Type: text/event-stream` via NestJS `@Sse()` decorator. Remove the existing stub `POST /tracks/:entityId/replay` and `ReplayTrackDto`.

New `ReplayStreamDto` with the same fields validates query params.

### 2.2 Flow

1. Query full track history for the entity within the time window
2. If no points found, emit a single `complete` event and close
3. Iterate through points, emitting each as a `point` event
4. Delay between emissions = `(point[n+1].timestamp - point[n].timestamp) / speedMultiplier`, capped at a maximum of 5 seconds to avoid long silences on sparse tracks
5. Emit a final `complete` event and close the stream

### 2.3 Event Format

```
event: point
data: {"entityId":"...","latitude":34.05,"longitude":-118.24,"altitude":1200,"heading":90,"speedKnots":12,"timestamp":"..."}

event: complete
data: {"entityId":"...","totalPoints":142}
```

### 2.4 Cancellation

Use `new Observable(subscriber => { ... return () => cleanup(); })` pattern. The teardown function explicitly clears any pending `setTimeout` handles. NestJS `@Sse()` triggers unsubscription when the client disconnects.

### 2.5 API Gateway — Tracks Proxy Module

All track requests route through the API gateway (port 3000) to preserve auth (JWT validation, role checks, header propagation). No changes to `apps/web/proxy.conf.json` — the existing `/api` catch-all already routes to the gateway.

Add a `TracksProxyModule` in the API gateway that:
- Registers routes under `/api/tracks/**`
- Forwards to `http://track-service:3002/tracks` (using `TRACK_SERVICE_URL` env var)
- Uses `HttpModule` with `httpService.get/post` for standard endpoints
- For the SSE endpoint, pipes the upstream response directly to the client response with `Content-Type: text/event-stream` and no response buffering

### 2.6 SSE Auth — Token via Query Parameter

`EventSource` does not support custom HTTP headers. For the SSE replay endpoint:
- The Angular client appends `?token=<jwt>` to the SSE URL
- The API gateway's tracks proxy extracts the token from the query param and validates it using the same JWT strategy
- The query param is stripped before forwarding to the track service

This is a standard pattern for SSE auth. The token is not logged or cached.

## 3. Frontend — Track Polyline & Context Menu

### 3.1 Angular TrackApiService

New `TrackApiService` in `app/core/services/` (named to avoid confusion with the backend `TrackService`):

- `getHistory(entityId, params?)` — `GET /api/tracks/:entityId`, returns track points
- `getLatestPosition(entityId)` — `GET /api/tracks/:entityId/latest`
- `replayStream(entityId, params)` — creates `EventSource` to `/api/tracks/:entityId/replay-stream?token=<jwt>&startTime=...`, returns `Observable<TrackPoint>` that emits each SSE point event and completes on the `complete` event. Gets the JWT from `AuthService`.

### 3.2 Context Menu

Right-click on an entity billboard opens a context menu with "Show Track History":

- Listen for `viewer.screenSpaceEventHandler` right-click (`RIGHT_CLICK`) events
- Pick entity under cursor via `viewer.scene.pick()`
- Render a positioned `<div>` overlay, dismiss on outside click or Escape
- On click: fetch track history, draw polyline, open bottom panel
- Signal writes from Cesium event callbacks wrapped in `NgZone.run()` for zoneless change detection

### 3.3 Track Render Service

`TrackRenderService` manages Cesium primitives. Follows the existing `BuildingsService` pattern: receives `viewer` and `Cesium` references via an `init(viewer, Cesium)` method called from `MapComponent`.

- **Two polyline entities** for replay: "played" portion (highlighted color) and "future" portion (dimmed color), each with `CallbackProperty` for dynamic position updates
- Static track view (no replay): single polyline with full history
- Start/end point markers
- Clears all track entities when panel is closed
- Animated replay marker: Cesium `Entity` with `BillboardGraphics` (reusing existing entity icon style)

### 3.4 MapComponent Integration

`MapComponent` calls `trackRenderService.init(this.viewer, this.Cesium)` after viewer creation, same as it does for `BuildingsService`.

## 4. Frontend — Bottom Track Panel & Animated Replay

### 4.1 Track Panel Component

`TrackPanelComponent` — standalone, collapsible bottom drawer (~200px expanded):

- **Header bar**: Entity name/ID, close button, collapse/expand toggle
- **Timeline scrubber**: Horizontal slider showing track time range, draggable thumb for current position
- **Playback controls**: Play/pause button, speed selector (0.5x, 1x, 2x, 5x, 10x)
- **Status line**: Current timestamp, lat/lng, altitude, speed, heading of replay cursor

Collapsed state shows just the header bar.

### 4.2 Replay Animation Flow

1. Play → `TrackApiService.replayStream()` opens SSE connection
2. Each incoming point:
   - Updates animated entity marker position (billboard)
   - Advances the "played" polyline, shrinks the "future" polyline (two separate primitives)
   - Updates timeline scrubber position
   - Updates status line
3. Pause → closes SSE, stores current timestamp
4. Resume → reopens SSE with `startTime` set to one tick after the last received point's timestamp (exclusive, avoids re-emitting the paused point)
5. Speed change → closes and reopens SSE with new `speedMultiplier` from current timestamp
6. Scrub (drag timeline) → closes SSE, jumps marker to nearest point, waits for play

### 4.3 Globe Integration

Animated marker is a Cesium `Entity` with `BillboardGraphics`. Position uses `Cartesian3.fromDegrees(lng, lat, altitude)` — altitude-aware for aircraft. On replay completion, marker stays at final position until panel is closed.

### 4.4 State Management

Signal-based `TrackPanelStore` (zoneless pattern, no NgRx):

- `selectedEntityId` — currently loaded entity
- `allPoints` — full fetched track history (used for polyline rendering and timeline range)
- `currentIndex` — index into `allPoints` for current replay position
- `isPlaying`, `currentTime`, `speedMultiplier`, `isExpanded`

Scoped to the track panel feature (provided at component level, not `providedIn: 'root'`).

## 5. Testing

### 5.1 Backend

- Update `track.controller.spec.ts` and `track-batch.service.spec.ts` for the 10 new kinematic fields
- Health controller test: `/health` returns `{ status: 'ok' }`
- SSE replay: test Observable emits points in order with correct timing, completes after last point, cleans up on unsubscribe (mock TrackService)
- Tracks proxy module: test auth token extraction from query param, forwarding

### 5.2 Frontend

- `TrackApiService`: test HTTP calls and EventSource observable wrapping
- `TrackPanelComponent`: test panel open/close/collapse, playback state transitions (play/pause/resume/speed change/scrub)
- `TrackRenderService`: test polyline entities added/removed, two-polyline replay split (mock Cesium viewer)

All tests use Jest.

## Key File Paths

### Backend (existing, modified)
- `apps/track-service/src/tracks/track-batch.service.ts` — add 10 fields to BufferedPoint + INSERT
- `apps/track-service/src/tracks/track.service.ts` — add altitude to TrackPointResult, SSE replay logic
- `apps/track-service/src/tracks/track.controller.ts` — SSE endpoint, remove POST replay stub, update Kafka handler
- `apps/track-service/src/tracks/dto/query-track.dto.ts` — remove ReplayTrackDto, add ReplayStreamDto

### Backend (new)
- `apps/track-service/src/health/health.controller.ts`
- `apps/api-gateway/src/modules/tracks/tracks-proxy.module.ts`
- `apps/api-gateway/src/modules/tracks/tracks-proxy.controller.ts`

### Frontend (new)
- `apps/web/src/app/core/services/track-api.service.ts`
- `apps/web/src/app/features/track-panel/track-panel.component.ts`
- `apps/web/src/app/features/track-panel/track-panel.store.ts`
- `apps/web/src/app/features/track-panel/track-render.service.ts`
- `apps/web/src/app/shared/components/context-menu/context-menu.component.ts`
