# Frontend

The web UI (`apps/client/`) is an Angular 19 application with CesiumJS 3D globe rendering, real-time WebSocket updates, and a sidebar-based navigation layout.

## Running

```bash
npx nx serve client    # Development server on :4200
npx nx build client    # Production build
npx nx test client     # Run tests
```

The dev server proxies `/api` and `/ws` requests to the gateway on `localhost:3000`, and `/auth` to Keycloak, via `proxy.conf.json`.

## Application Structure

```
apps/client/src/
├── main.ts                         # Angular bootstrap
├── index.html                      # Shell HTML
├── styles.scss                     # Global styles
├── app/
│   ├── app.component.ts            # Root: sidebar nav, status bar, map background
│   ├── app.config.ts               # Angular providers config
│   ├── app.routes.ts               # Lazy-loaded feature routes
│   ├── core/
│   │   └── services/
│   │       ├── auth.service.ts     # Keycloak auth + dev-mode fallback
│   │       ├── entity.service.ts   # REST + WebSocket entity state
│   │       ├── websocket.service.ts # Native WebSocket stream (/ws/entities)
│   │       └── alert.service.ts    # Alert notifications
│   ├── features/
│   │   ├── map/
│   │   │   ├── map.component.ts    # CesiumJS 3D globe
│   │   │   └── cesium-config.ts    # Cesium Ion token, asset config
│   │   ├── search/
│   │   │   └── search.component.ts # Entity search panel
│   │   ├── alerts/
│   │   │   └── alerts.component.ts # Alert dashboard
│   │   ├── link-graph/
│   │   │   └── link-graph.component.ts  # vis-network graph view
│   │   └── timeline/
│   │       └── timeline.component.ts    # Track history timeline
│   └── shared/
│       └── models/
│           ├── entity.model.ts     # Entity interfaces & enums
│           ├── alert.model.ts      # Alert interfaces
│           ├── link.model.ts       # Link/relationship interfaces
│           └── track.model.ts      # Track point interfaces
└── assets/                         # Static assets, Cesium workers
```

## Routing

All routes are lazy-loaded. The CesiumJS map renders as a persistent background layer; feature panels overlay on top.

| Route | Component | Description |
|-------|-----------|-------------|
| `/map` (default) | `MapComponent` | 3D globe with entity markers |
| `/search` | `SearchComponent` | Entity search and filter panel |
| `/alerts` | `AlertsComponent` | Alert list with acknowledgment |
| `/findings` | `FindingsComponent` | Departures from entity baselines: expected vs seen, confirm/dismiss, show on map |
| `/link-graph` | `LinkGraphComponent` | Entity relationship graph (vis-network) |
| `/timeline` | `TimelineComponent` | Historical track replay |

## Core Services

### AuthService (`core/services/auth.service.ts`)

Handles Keycloak OIDC authentication with graceful degradation:

1. Dynamically imports `keycloak-js`
2. Initializes with `check-sso` + PKCE
3. **If Keycloak unavailable**: falls back to a dev profile (`dev-operator`, roles: `analyst, operator`, classification: `UNCLASSIFIED`)
4. Provides `getToken()` for the HTTP interceptor to attach `Authorization: Bearer` headers
5. Auto-refreshes tokens every 60 seconds

### EntityService (`core/services/entity.service.ts`)

Manages entity state from both REST and WebSocket sources:

- `getEntities(query)` — REST call to `GET /api/v1/entities` with bounding box and filter params
- `getEntity(id)` — REST call to `GET /api/v1/entities/:id`
- `createEntity(dto)` — REST call to `POST /api/v1/entities`
- `currentEntities$` — `BehaviorSubject<Map<string, Entity>>` merged from REST results + WebSocket events
- `entityUpdates$` — Forwarded from WebSocket service

### WebSocketService (`core/services/websocket.service.ts`)

Native `WebSocket` to the gateway's `/ws/entities` endpoint (JSON envelopes `{ type, data, ts }`; the bearer token travels as `?token=`):

- Reconnects with exponential backoff (1s–10s plus jitter) and replays the last viewport and entity subscriptions after every connect
- Sends `viewport:update` when the map camera moves, `entity:subscribe` / `entity:unsubscribe` for pinned entities
- Receives `entity:batch` (fanned out to `entityUpdates$`), `entity:deleted`, `events.entity.stale|agedout|restored` (`ageoutEvents$`), and `alert:new` (`alertStream$`)
- Exposes `connectionStatus$` (connected/disconnected/reconnecting) for the status bar

### AnalyticsService (`core/services/analytics.service.ts`)

Findings and baselines from the analytics service: `getFindings(query)`, `getFinding(id)`, `reviewFinding(id, 'CONFIRMED' | 'DISMISSED')`, `getEntityFindings(entityId)`, `getBaseline(entityId)` (a 404 resolves to `null`: no baseline yet) and `getStats()`. The `openCount` signal feeds the sidebar badge; it refreshes on start, every minute, after a review and whenever an alert carrying `metadata.findingId` arrives on the WebSocket. The entity detail panel's **Behaviour** section uses it to show the baseline (places named after nearby saved locations, range, speed, reporting rhythm, a 7×24 activity grid in the entity's local time) and the entity's open findings.

### AlertService (`core/services/alert.service.ts`)

Tracks unacknowledged alerts for the badge counter on the sidebar.

## Layout

The app uses a fixed layout with three zones:

1. **Sidebar** (left, 48px) — Navigation buttons, settings, user profile
2. **Main content** (center) — CesiumJS map as persistent background + feature panels as overlays
3. **Status bar** (bottom, 28px) — WebSocket connection status, entity count, classification level, username, UTC clock

## Proxy Configuration

`apps/client/proxy.conf.json` forwards API and WebSocket traffic to the API Gateway:

```json
{
  "/api": {
    "target": "http://localhost:3000",
    "secure": false
  },
  "/ws": {
    "target": "http://localhost:3000",
    "secure": false,
    "ws": true
  },
  "/auth": {
    "target": "http://localhost:8080",
    "secure": false,
    "changeOrigin": true,
    "pathRewrite": { "^/auth": "" }
  }
}
```

## CesiumJS

- **Version**: 1.120
- **Ion token**: Configured via `CESIUM_ION_ACCESS_TOKEN` environment variable
- **Build setup**: Cesium workers, assets, and widgets are copied into the build output via Angular build configuration
- **Globe**: 3D terrain with entity billboards/points positioned using WGS84 coordinates
- **Motion between fixes**: aircraft and moving vessels are dead-reckoned (`features/map/dead-reckoning.ts`); satellites are propagated with SGP4 from the elements they carry (`features/map/satellite-tracker.ts`, `satellite.js` 6.0.2), all of them four times a second and the selected one every frame
- **Orbit overlays**: the selected satellite gets its next-revolution path at altitude, its ground track and its horizon footprint (`features/map/orbit-geometry.ts`), toggled from the detail panel through `OrbitOverlayService`
- **satellite.js is pinned to 6.0.2**: 7.x re-exports a WASM bulk propagator that imports `node:worker_threads` and `node:module`, which breaks the production bundle while `tsc` and Jest still pass; 6.0.2 has the same API, ships CommonJS with types and needs no Jest mapping
- **Vessels**: a hull seen from above, coloured by AIS ship type category (`shared/vessel-format.ts`, `features/map/vessel-style.ts`) and turned to the heading while moving or when a true heading is reported; Vessel Details opens by default with the navigational status in words and the yearless AIS ETA as `17 Oct 22:00 UTC`
- **Flight paths**: selecting an aircraft with an ICAO address loads its past path through `FlightPathService` (`GET /api/v1/aircraft/{hex}/path`), today by default for IntelSky aircraft and off for others; the map draws it as a dashed green line at altitude (`features/map/flight-path.ts`) and Aircraft Details has the Off / Today / 3 d / 7 d control. Times are read from the points' `ts`; IntelSky's own date strings are UTC+2
- **Wide views**: while the view is wider than the gateway's detail cap (`connection:ack` `detailMaxAreaSqDeg`, 2 500 sq deg) no live positions arrive, so the map fetches `GET /api/v1/entities/density` for the view (on every camera move and every 20 s) and draws a translucent disc per type and cell in the type's colour (`features/map/density-overlay.ts`); individual entities are hidden except the selected one, and a click on a disc flies in
- **Routes**: selecting an aircraft with a callsign, or any IntelSky aircraft, loads its route through `RouteService` (`GET /api/v1/aircraft/{hex}/route`); Aircraft Details shows Departure, Arrival and Airline, and the map draws a thick purple line along the great circle from the departure (on the ground) up to the aircraft, following it as it moves
- **Military aircraft** (IntelSky) are drawn in green; their flight path is a dashed green line
- **Cluster discs** carry no trails: a clustered entity's trail polyline follows its billboard's `clusterShow` after every frame (`syncTrailClusterShow`)
