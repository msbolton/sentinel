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
│   │   ├── config/
│   │   │   └── runtime-config.ts   # Loads /config.json (map keys) before bootstrap
│   │   └── services/
│   │       ├── auth.service.ts     # Keycloak auth + dev-mode fallback (dev builds only)
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

## Environments and runtime config

| Build | Environment file | Map keys |
|-------|------------------|----------|
| `nx serve client` (dev) | `src/environments/environment.ts` | Built in, read directly |
| `nx build client` (production, the default) | `src/environments/environment.prod.ts` (`fileReplacements`) | Empty; filled at runtime |

The production bundle carries no keys. `main.ts` calls `loadRuntimeConfig()` (`core/config/runtime-config.ts`) before bootstrapping Angular. It fetches `/config.json` with `cache: 'no-store'` and copies `googleMapsApiKey` and `cesiumIonAccessToken` onto `environment`, so `Cesium.Ion.defaultAccessToken` and the 3D buildings provider see them when the map initialises. A 404 means "no config" (dev server, plain static hosting) and any other failure is logged; neither blocks startup, and empty values never overwrite what the build already holds.

In the container the file is written at start by `apps/client/docker-entrypoint.d/40-sentinel-config.sh` from the `GOOGLE_MAPS_API_KEY` and `CESIUM_ION_ACCESS_TOKEN` environment variables (Compose reads both from `.env`; empty when unset). It goes to `/var/run/sentinel/config.json`, which nginx serves at `/config.json` with `Cache-Control: no-store`. Values may contain only `[A-Za-z0-9._-]` (they are written into JSON unescaped), and the container refuses to start on anything else.

nginx is split in two files in the image: `/etc/nginx/sentinel/static.conf` (SPA fallback, asset caching, `index.html` revalidation, `/config.json`, `/healthz`) and `/etc/nginx/conf.d/default.conf` (a server block that includes it plus the Compose-only `/api`, `/ws` and `/auth/` proxy blocks). The Kubernetes chart replaces `default.conf` and includes only `static.conf`; with a read-only root filesystem, mount an `emptyDir` at `/var/run/sentinel`.

### Authentication failures

In a development build, a failed Keycloak initialisation falls back to a local `dev-operator` profile so the UI works without Keycloak. A production build never does: it stays signed out, `AuthService.initError$` carries the reason, and the login page shows it.

### Rotate the committed keys

`src/environments/environment.ts` still holds a real Google Maps API key and Cesium Ion token, and they are in the git history. They are no longer shipped in the production bundle, but anyone with repository access has them. Rotate both, or restrict them (Google Maps key by HTTP referrer and API, Cesium Ion token by allowed URL), then replace the values in `environment.ts` with your own or empty strings.

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
- **Ion token**: Configured via `CESIUM_ION_ACCESS_TOKEN` (see [Environments and runtime config](#environments-and-runtime-config))
- **Build setup**: Cesium workers, assets, and widgets are copied into the build output via Angular build configuration
- **Globe**: 3D terrain with entity billboards/points positioned using WGS84 coordinates
- **Motion between fixes**: aircraft, drones and moving vessels are dead-reckoned (`features/map/dead-reckoning.ts`, `DEAD_RECKONED_TYPES`); ground vehicles turn their icon with their heading but stay at their last fix, since a straight line between fixes would leave the road; satellites are propagated with SGP4 from the elements they carry (`features/map/satellite-tracker.ts`, `satellite.js` 6.0.2), all of them four times a second and the selected one every frame
- **Terrain and heights**: the map's terrain button (Cesium World Terrain; disabled without an ion token, remembered in localStorage) is separate from the ingest service's Copernicus tiles. `features/map/height-mode.ts` decides how an entity sits against the ground: a radar height above ground is drawn relative to the terrain only while map terrain is on, a `GROUND` track from any feed is clamped to the terrain even when it carries an altitude, an altitude above zero is absolute (ellipsoidal) and anything else is clamped. Camera bearings are dashed lines from the site (`features/map/bearing-layer.ts`), and the Copernicus attribution is registered with Cesium's credit display, which is hidden, so it is also in `docs/feeds.md` (Terrain)
- **Orbit overlays**: the selected satellite gets its next-revolution path at altitude, its ground track and its horizon footprint (`features/map/orbit-geometry.ts`), toggled from the detail panel through `OrbitOverlayService`
- **satellite.js is pinned to 6.0.2**: 7.x re-exports a WASM bulk propagator that imports `node:worker_threads` and `node:module`, which breaks the production bundle while `tsc` and Jest still pass; 6.0.2 has the same API, ships CommonJS with types and needs no Jest mapping
- **Vessels**: a hull seen from above, coloured by AIS ship type category (`shared/vessel-format.ts`, `features/map/vessel-style.ts`) and turned to the heading while moving or when a true heading is reported; Vessel Details opens by default with the navigational status in words and the yearless AIS ETA as `17 Oct 22:00 UTC`
- **Flight paths**: selecting an aircraft with an ICAO address loads its past path through `FlightPathService` (`GET /api/v1/aircraft/{hex}/path`), today by default for IntelSky aircraft and off for others; the map draws it as a dashed orange line at altitude (`features/map/flight-path.ts`) and Aircraft Details has the Off / Today / 3 d / 7 d control. Times are read from the points' `ts`; IntelSky's own date strings are UTC+2
- **Vessel density**: `density:grid` frames (`WebSocketService.density$`) are drawn as counted discs, merged to about a dozen across the view, while the view is larger than the feed's cap and the grid is under 60 s old (`features/map/density-overlay.ts`); individual vessels are hidden meanwhile and a click on a disc flies in
