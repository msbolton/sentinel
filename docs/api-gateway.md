# API Gateway

The API Gateway (`apps/api-gateway/`) is the primary backend entry point. It exposes REST endpoints, hosts the WebSocket gateway for real-time updates, manages the Redis geospatial cache, and produces Kafka events.

## Running

```bash
npx nx serve api-gateway   # Development with watch mode
npx nx build api-gateway   # Production build
```

Listens on port **3000** (configurable via `PORT` env var).

## REST API

Base path: `/api/v1`

### Health

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| GET | `/api/v1/health` | No | Liveness check — returns `{ status: "ok" }` |
| GET | `/api/v1/health/ready` | No | Readiness check with dependency status |

### Entities

All entity endpoints require JWT authentication (or dev-mode bypass).

| Method | Path | Auth | Roles | Description |
|--------|------|------|-------|-------------|
| GET | `/api/v1/entities` | JWT | Any | Query entities by bounding box and filters |
| GET | `/api/v1/entities/:id` | JWT | Any | Get single entity by UUID |
| POST | `/api/v1/entities` | JWT | `analyst`, `admin` | Create a new entity |
| PUT | `/api/v1/entities/:id` | JWT | `analyst`, `admin` | Update an existing entity |
| DELETE | `/api/v1/entities/:id` | JWT | `admin` + SECRET clearance | Soft-delete an entity |

#### Query Parameters (GET /entities)

| Param | Type | Default | Description |
|-------|------|---------|-------------|
| `north` | number | — | Bounding box north latitude |
| `south` | number | — | Bounding box south latitude |
| `east` | number | — | Bounding box east longitude |
| `west` | number | — | Bounding box west longitude |
| `entityTypes` | string[] | — | Filter by entity type(s) |
| `sources` | string[] | — | Filter by source(s) |
| `classification` | string | — | Max classification level to return |
| `page` | number | 1 | Page number |
| `pageSize` | number | 50 | Results per page (max 500) |

#### Response Format

```json
{
  "data": [
    {
      "id": "uuid",
      "entityType": "VESSEL",
      "name": "USS MASON",
      "latitude": 25.551,
      "longitude": 52.887,
      "heading": 90,
      "speed": 18.0,
      "classification": "UNCLASSIFIED",
      "source": "MANUAL",
      "createdAt": "2025-01-01T00:00:00.000Z",
      "updatedAt": "2025-01-01T00:00:00.000Z"
    }
  ],
  "pagination": {
    "page": 1,
    "pageSize": 50,
    "total": 13,
    "totalPages": 1
  }
}
```

## WebSocket Events

The WebSocket gateway runs on the `/entities` namespace via Socket.io.

### Client-to-Server

| Event | Payload | Description |
|-------|---------|-------------|
| `viewport:update` | `{ north, south, east, west }` | Update client viewport for filtered updates |
| `entity:subscribe` | `{ entityId }` | Subscribe to updates for a specific entity |
| `entity:unsubscribe` | `{ entityId }` | Unsubscribe from entity updates |

### Server-to-Client

| Event | Payload | Description |
|-------|---------|-------------|
| `entity:created` | `EntityEvent` | New entity created |
| `entity:updated` | `EntityEvent` | Entity data updated |
| `entity:deleted` | `EntityEvent` | Entity deleted |
| `entity:update` | `EntityEvent` | Generic entity update (position, metadata) |
| `alert:new` | `Alert` | New alert triggered |

## Authentication

### Production (Keycloak)

JWT tokens issued by Keycloak are validated via JWKS at:

```
{KEYCLOAK_BASE_URL}/realms/sentinel/protocol/openid-connect/certs
```

The `JwtAuthGuard` (at `modules/auth/jwt-auth.guard.ts`) validates tokens, then checks:
1. **Roles** — `@Roles()` decorator requires at least one matching role
2. **Classification** — `@Classification()` decorator enforces minimum clearance (UNCLASSIFIED < CONFIDENTIAL < SECRET < TOP_SECRET)

### Development (Auth Bypass)

When `NODE_ENV !== 'production'`, the `DevAuthGuard` (`modules/auth/dev-auth.guard.ts`) replaces `JwtAuthGuard` via a provider factory in `AuthModule`. It injects a synthetic user:

```json
{
  "userId": "00000000-0000-0000-0000-000000000001",
  "username": "dev-operator",
  "roles": ["analyst", "operator", "admin"],
  "classificationLevel": "TOP_SECRET"
}
```

No Keycloak configuration is needed for local development.

## Swagger Documentation

Interactive API docs are available at:

```
http://localhost:3000/api/docs
```

Built with `@nestjs/swagger` decorators on all controllers and DTOs.

## Module Structure

```
apps/api-gateway/src/
├── main.ts                           # Bootstrap, CORS, Swagger, Kafka
├── app.module.ts                     # Root module (TypeORM, CQRS, features)
└── modules/
    ├── auth/
    │   ├── auth.module.ts            # Conditional JwtAuthGuard/DevAuthGuard
    │   ├── jwt-auth.guard.ts         # Production JWT + RBAC + classification guard
    │   ├── dev-auth.guard.ts         # Dev-mode auth bypass
    │   ├── jwt.strategy.ts           # Passport JWT strategy (Keycloak JWKS)
    │   └── decorators/
    │       ├── roles.decorator.ts    # @Roles() decorator
    │       └── classification.decorator.ts  # @Classification() decorator
    ├── entities/
    │   ├── entities.module.ts        # Kafka client, auth imports
    │   ├── entities.controller.ts    # REST CRUD endpoints
    │   ├── entities.service.ts       # Redis cache, DB fallback, Kafka events
    │   └── dto/
    │       ├── create-entity.dto.ts  # Validated creation payload
    │       ├── update-entity.dto.ts  # Partial update payload
    │       └── query-entities.dto.ts # Bounding box + filter params
    ├── gateway/
    │   ├── entity-gateway.module.ts  # WebSocket module
    │   ├── entity.gateway.ts         # Socket.io gateway (/entities namespace)
    │   ├── kafka-consumer.service.ts # Kafka → WebSocket bridge
    │   └── viewport.service.ts       # Per-client viewport tracking
    └── health/
        ├── health.module.ts
        └── health.controller.ts      # Liveness + readiness endpoints

```

## Cache Warming

On startup, `EntitiesService.onModuleInit()` queries all entities from PostgreSQL and populates:
1. Redis geospatial index (`GEOADD` to `sentinel:entities:geo`)
2. Individual entity JSON cache (`SET` to `sentinel:entities:cache:{id}` with 300s TTL)

If a `queryEntities()` call returns 0 results from Redis, the service falls back to a direct PostgreSQL query using `ST_Within` / `ST_MakeEnvelope`, then re-warms the cache with the results.
