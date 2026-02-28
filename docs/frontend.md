# Frontend

The web UI (`apps/web/`) is an Angular 19 application with CesiumJS 3D globe rendering, real-time WebSocket updates, and a sidebar-based navigation layout.

## Running

```bash
npx nx serve web    # Development server on :4200
npx nx build web    # Production build
npx nx test web     # Run tests
```

The dev server proxies `/api` and `/socket.io` requests to `localhost:3000` via `proxy.conf.json`.

## Application Structure

```
apps/web/src/
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
│   │       ├── websocket.service.ts # Socket.io connection management
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

Socket.io client connecting to the `/entities` namespace:

- Auto-reconnects with exponential backoff (1s–10s)
- Emits `viewport:update` when the map camera moves
- Listens for `entity:created`, `entity:updated`, `entity:deleted`, `alert:new`
- Exposes `connectionStatus$` (connected/disconnected/reconnecting) for the status bar

### AlertService (`core/services/alert.service.ts`)

Tracks unacknowledged alerts for the badge counter on the sidebar.

## Layout

The app uses a fixed layout with three zones:

1. **Sidebar** (left, 48px) — Navigation buttons, settings, user profile
2. **Main content** (center) — CesiumJS map as persistent background + feature panels as overlays
3. **Status bar** (bottom, 28px) — WebSocket connection status, entity count, classification level, username, UTC clock

## Proxy Configuration

`apps/web/proxy.conf.json` forwards API and WebSocket traffic to the API Gateway:

```json
{
  "/api": {
    "target": "http://localhost:3000",
    "secure": false
  },
  "/socket.io": {
    "target": "http://localhost:3000",
    "secure": false,
    "ws": true
  }
}
```

## CesiumJS

- **Version**: 1.120
- **Ion token**: Configured via `CESIUM_ION_ACCESS_TOKEN` environment variable
- **Build setup**: Cesium workers, assets, and widgets are copied into the build output via Angular build configuration
- **Globe**: 3D terrain with entity billboards/points positioned using WGS84 coordinates
