# Track Service — Gap Fixes & Frontend Integration

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Complete the track-service backend (missing kinematic fields, health endpoint, SSE replay) and build Angular frontend components for CesiumJS track visualization and replay.

**Architecture:** NestJS track-service microservice (port 3002) with PostgreSQL/PostGIS/TimescaleDB. API gateway (port 3000) proxies track requests via `@nestjs/axios` HttpService. Angular 19 frontend with CesiumJS globe, standalone components, zoneless change detection, signal-based state.

**Tech Stack:** NestJS, TypeORM, PostGIS, `@nestjs/axios`, Angular 19, CesiumJS, RxJS, Jest

**Spec:** `docs/superpowers/specs/2026-03-21-track-service-design.md`

---

## File Structure

### Backend (modified)
- `apps/track-service/src/tracks/track-batch.service.ts` — Add 10 missing fields to `BufferedPoint` + bulk INSERT
- `apps/track-service/src/tracks/track.service.ts` — Add altitude to `TrackPointResult`, add `replayStream()` method
- `apps/track-service/src/tracks/track.controller.ts` — SSE endpoint, update Kafka handler, remove POST replay stub
- `apps/track-service/src/tracks/dto/query-track.dto.ts` — Replace `ReplayTrackDto` with `ReplayStreamDto`
- `apps/track-service/src/tracks/track.controller.spec.ts` — Update tests for new endpoints
- `apps/track-service/src/tracks/track-batch.service.spec.ts` — Update `makePoint` helper with new fields
- `apps/track-service/src/app.module.ts` — Import `HealthModule`

### Backend (new)
- `apps/track-service/src/health/health.controller.ts` — `/health` endpoint
- `apps/track-service/src/health/health.module.ts` — Module registration
- `apps/track-service/src/health/health.controller.spec.ts` — Test
- `apps/api-gateway/src/modules/tracks/tracks.module.ts` — Gateway module with HttpModule
- `apps/api-gateway/src/modules/tracks/tracks.controller.ts` — Proxy controller with SSE passthrough
- `apps/api-gateway/src/modules/tracks/tracks.service.ts` — HttpService wrapper for track-service
- `apps/api-gateway/src/modules/tracks/tracks.controller.spec.ts` — Test

### Frontend (new)
- `apps/web/src/app/core/services/track-api.service.ts` — HTTP + EventSource client
- `apps/web/src/app/core/services/track-api.service.spec.ts` — Test
- `apps/web/src/app/features/track-panel/track-panel.store.ts` — Signal-based state
- `apps/web/src/app/features/track-panel/track-panel.component.ts` — Bottom drawer UI
- `apps/web/src/app/features/track-panel/track-panel.component.html` — Template
- `apps/web/src/app/features/track-panel/track-panel.component.scss` — Styles
- `apps/web/src/app/features/track-panel/track-panel.component.spec.ts` — Test
- `apps/web/src/app/features/track-panel/track-render.service.ts` — Cesium polyline/marker management
- `apps/web/src/app/features/track-panel/track-render.service.spec.ts` — Test
- `apps/web/src/app/shared/components/context-menu/context-menu.component.ts` — Right-click menu

### Frontend (modified)
- `apps/web/src/app/features/map/map.component.ts` — Init TrackRenderService, add right-click handler, include TrackPanelComponent + ContextMenuComponent
- `apps/web/src/app/features/map/map.component.html` — Add track panel + context menu to template

---

## Task 1: Health Controller

**Files:**
- Create: `apps/track-service/src/health/health.controller.ts`
- Create: `apps/track-service/src/health/health.module.ts`
- Create: `apps/track-service/src/health/health.controller.spec.ts`
- Modify: `apps/track-service/src/app.module.ts:5,29`

- [ ] **Step 1: Write the health controller test**

```typescript
// apps/track-service/src/health/health.controller.spec.ts
import { Test } from '@nestjs/testing';
import { HealthController } from './health.controller';

describe('HealthController', () => {
  let controller: HealthController;

  beforeEach(async () => {
    const module = await Test.createTestingModule({
      controllers: [HealthController],
    }).compile();
    controller = module.get(HealthController);
  });

  it('should return status ok', () => {
    expect(controller.check()).toEqual({ status: 'ok' });
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `npx nx test track-service --testPathPattern=health.controller.spec`
Expected: FAIL — cannot find `./health.controller`

- [ ] **Step 3: Write health controller and module**

```typescript
// apps/track-service/src/health/health.controller.ts
import { Controller, Get } from '@nestjs/common';

@Controller('health')
export class HealthController {
  @Get()
  check() {
    return { status: 'ok' };
  }
}
```

```typescript
// apps/track-service/src/health/health.module.ts
import { Module } from '@nestjs/common';
import { HealthController } from './health.controller';

@Module({
  controllers: [HealthController],
})
export class HealthModule {}
```

- [ ] **Step 4: Register HealthModule in AppModule**

In `apps/track-service/src/app.module.ts`, add import:
```typescript
import { HealthModule } from './health/health.module';
```
Add `HealthModule` to the `imports` array after `TrackModule`.

- [ ] **Step 5: Run test to verify it passes**

Run: `npx nx test track-service --testPathPattern=health.controller.spec`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add apps/track-service/src/health/
git add apps/track-service/src/app.module.ts
git commit -m "feat(track-service): add health controller (SEN-80)"
```

---

## Task 2: Complete Kinematic Fields in Batch Service

**Files:**
- Modify: `apps/track-service/src/tracks/track-batch.service.ts:6-20,112-138`
- Modify: `apps/track-service/src/tracks/track-batch.service.spec.ts:46-76`

- [ ] **Step 1: Update `makePoint` helper in test to include new fields**

In `track-batch.service.spec.ts`, update the `makePoint` function's type and defaults to include the 10 new fields:

```typescript
function makePoint(overrides: Partial<{
  entityId: string;
  latitude: number;
  longitude: number;
  heading: number | null;
  speedKnots: number | null;
  course: number | null;
  source: string | null;
  timestamp: Date;
  altitude: number | null;
  velocityNorth: number | null;
  velocityEast: number | null;
  velocityUp: number | null;
  circularError: number | null;
  feedId: string | null;
  trackProcessingState: string | null;
  accelNorth: number | null;
  accelEast: number | null;
  accelUp: number | null;
  posCovariance: number[] | null;
  posVelCovariance: number[] | null;
  velCovariance: number[] | null;
  altitudeError: number | null;
  sensorId: string | null;
}> = {}) {
  return {
    entityId: overrides.entityId ?? '550e8400-e29b-41d4-a716-446655440000',
    latitude: overrides.latitude ?? 10.0,
    longitude: overrides.longitude ?? 20.0,
    heading: overrides.heading ?? null,
    speedKnots: overrides.speedKnots ?? null,
    course: overrides.course ?? null,
    source: overrides.source ?? 'AIS',
    timestamp: overrides.timestamp ?? new Date('2025-01-01T00:00:00Z'),
    altitude: overrides.altitude ?? null,
    velocityNorth: overrides.velocityNorth ?? null,
    velocityEast: overrides.velocityEast ?? null,
    velocityUp: overrides.velocityUp ?? null,
    circularError: overrides.circularError ?? null,
    feedId: overrides.feedId ?? null,
    trackProcessingState: overrides.trackProcessingState ?? null,
    accelNorth: overrides.accelNorth ?? null,
    accelEast: overrides.accelEast ?? null,
    accelUp: overrides.accelUp ?? null,
    posCovariance: overrides.posCovariance ?? null,
    posVelCovariance: overrides.posVelCovariance ?? null,
    velCovariance: overrides.velCovariance ?? null,
    altitudeError: overrides.altitudeError ?? null,
    sensorId: overrides.sensorId ?? null,
  };
}
```

- [ ] **Step 2: Add test for new fields in INSERT SQL**

Add a new test in the `flush` describe block:

```typescript
it('should include all kinematic fields in INSERT SQL', async () => {
  await service.addPoint(makePoint({
    feedId: 'feed-1',
    trackProcessingState: 'FUSED',
    accelNorth: 1.5,
    accelEast: -0.5,
    accelUp: 0.1,
    posCovariance: [1, 0, 0, 1, 0, 1],
    posVelCovariance: [0.1, 0.2],
    velCovariance: [0.5, 0, 0.5],
    altitudeError: 10.0,
    sensorId: 'radar-01',
  }));

  await service.flush();

  const [sql, params] = mockRepo.query.mock.calls[0];
  expect(sql).toContain('"feedId"');
  expect(sql).toContain('"trackProcessingState"');
  expect(sql).toContain('"accelNorth"');
  expect(sql).toContain('"sensorId"');
  expect(params).toContain('feed-1');
  expect(params).toContain('FUSED');
  expect(params).toContain('radar-01');
});
```

- [ ] **Step 3: Run tests to verify new test fails**

Run: `npx nx test track-service --testPathPattern=track-batch.service.spec`
Expected: FAIL — INSERT SQL doesn't contain `"feedId"`

- [ ] **Step 4: Update `BufferedPoint` interface with 10 new fields**

In `track-batch.service.ts`, add to the `BufferedPoint` interface after `circularError`:

```typescript
feedId: string | null;
trackProcessingState: string | null;
accelNorth: number | null;
accelEast: number | null;
accelUp: number | null;
posCovariance: number[] | null;
posVelCovariance: number[] | null;
velCovariance: number[] | null;
altitudeError: number | null;
sensorId: string | null;
```

- [ ] **Step 5: Update the bulk INSERT SQL in `flush()`**

Update the INSERT column list and value placeholders. Each point now has 23 parameters (was 13). The column list becomes:

```sql
INSERT INTO sentinel.track_points
  ("entityId", "source", "position", "heading", "speedKnots", "course", "timestamp",
   "altitude", "velocityNorth", "velocityEast", "velocityUp", "circularError",
   "feedId", "trackProcessingState", "accelNorth", "accelEast", "accelUp",
   "posCovariance", "posVelCovariance", "velCovariance", "altitudeError", "sensorId")
```

Update the `valuesClauses.push(...)` to include parameters `$${idx+13}` through `$${idx+22}`, with array fields cast as `$${idx+N}::float[]`.

Update the `params.push(...)` to include:
```typescript
point.feedId,
point.trackProcessingState,
point.accelNorth,
point.accelEast,
point.accelUp,
point.posCovariance,
point.posVelCovariance,
point.velCovariance,
point.altitudeError,
point.sensorId,
```

Update `paramIndex.current += 23;` (was 13). This is 23 because `position` consumes 2 SQL params (longitude + latitude) via `ST_MakePoint`, while all other columns consume 1 param each. Total: 22 columns, 23 SQL params.

- [ ] **Step 6: Run tests to verify all pass**

Run: `npx nx test track-service --testPathPattern=track-batch.service.spec`
Expected: PASS (all tests)

- [ ] **Step 7: Commit**

```bash
git add apps/track-service/src/tracks/track-batch.service.ts
git add apps/track-service/src/tracks/track-batch.service.spec.ts
git commit -m "feat(track-service): add all kinematic fields to batch INSERT (SEN-80)"
```

---

## Task 3: Add Altitude to TrackPointResult & Update Queries

**Files:**
- Modify: `apps/track-service/src/tracks/track.service.ts:13-23,82-93,152-180,238-252`

- [ ] **Step 1: Add `altitude` to `TrackPointResult` interface**

In `track.service.ts`, add `altitude: number | null;` after `course` in the `TrackPointResult` interface (around line 19).

- [ ] **Step 2: Add `altitude` to `getHistory()` SELECT**

In `getHistory()`, add `'tp.altitude AS altitude',` to the `.select([...])` array (after the `source` line, around line 91).

- [ ] **Step 3: Add `altitude` to `getSimplifiedTrack()` SELECT**

In `getSimplifiedTrack()`, add `tp.altitude,` to the final SELECT list (after `tp.source`, around line 174).

- [ ] **Step 4: Add `altitude` to `getLatestPositions()` SELECT**

In `getLatestPositions()`, add `tp.altitude,` to the SELECT list (after `tp.source`, around line 248).

- [ ] **Step 5: Run all track-service tests**

Run: `npx nx test track-service`
Expected: PASS — existing tests still pass (they don't assert on altitude)

- [ ] **Step 6: Commit**

```bash
git add apps/track-service/src/tracks/track.service.ts
git commit -m "feat(track-service): include altitude in TrackPointResult queries (SEN-80)"
```

---

## Task 4: Update Kafka Handler & recordPoint Signature

**Files:**
- Modify: `apps/track-service/src/tracks/track.service.ts:39-69,261-295`
- Modify: `apps/track-service/src/tracks/track.controller.ts:100-119`

- [ ] **Step 1: Extend `handlePositionEvent()` payload type**

In `track.service.ts`, add the 10 new optional fields to the `handlePositionEvent` payload type:

```typescript
feedId?: string;
trackProcessingState?: string;
accelNorth?: number;
accelEast?: number;
accelUp?: number;
posCovariance?: number[];
posVelCovariance?: number[];
velCovariance?: number[];
altitudeError?: number;
sensorId?: string;
```

And add them to the `batchService.addPoint()` call:

```typescript
feedId: payload.feedId ?? null,
trackProcessingState: payload.trackProcessingState ?? null,
accelNorth: payload.accelNorth ?? null,
accelEast: payload.accelEast ?? null,
accelUp: payload.accelUp ?? null,
posCovariance: payload.posCovariance ?? null,
posVelCovariance: payload.posVelCovariance ?? null,
velCovariance: payload.velCovariance ?? null,
altitudeError: payload.altitudeError ?? null,
sensorId: payload.sensorId ?? null,
```

- [ ] **Step 2: Extend `recordPoint()` signature**

Add the 10 new parameters (all with `= null` defaults) to `recordPoint()` and pass them through to `batchService.addPoint()`.

- [ ] **Step 3: Update Kafka handler payload in controller**

In `track.controller.ts`, add the new optional fields to the `@Payload()` type annotation in `handlePositionEvent()`:

```typescript
feedId?: string;
trackProcessingState?: string;
accelNorth?: number;
accelEast?: number;
accelUp?: number;
posCovariance?: number[];
posVelCovariance?: number[];
velCovariance?: number[];
altitudeError?: number;
sensorId?: string;
```

- [ ] **Step 4: Run all track-service tests**

Run: `npx nx test track-service`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add apps/track-service/src/tracks/track.service.ts
git add apps/track-service/src/tracks/track.controller.ts
git commit -m "feat(track-service): propagate all kinematic fields through Kafka handler (SEN-80)"
```

---

## Task 5: SSE Replay Endpoint

**Files:**
- Modify: `apps/track-service/src/tracks/dto/query-track.dto.ts:36-48`
- Modify: `apps/track-service/src/tracks/track.service.ts` — add `replayStream()` method
- Modify: `apps/track-service/src/tracks/track.controller.ts:76-98` — replace POST with SSE GET
- Modify: `apps/track-service/src/tracks/track.controller.spec.ts` — update tests

- [ ] **Step 1: Replace `ReplayTrackDto` with `ReplayStreamDto`**

In `dto/query-track.dto.ts`, replace `ReplayTrackDto` class with:

```typescript
export class ReplayStreamDto {
  @IsDateString()
  startTime: string;

  @IsDateString()
  endTime: string;

  @IsOptional()
  @Type(() => Number)
  @IsNumber()
  @Min(0.1)
  @Max(100)
  speedMultiplier?: number;
}
```

- [ ] **Step 2: Write test for SSE replay endpoint**

In `track.controller.spec.ts`, add `replayStream` to the mock service:

```typescript
const mockTrackService = {
  getHistory: jest.fn(),
  getLatestPositions: jest.fn(),
  getSegments: jest.fn(),
  replayStream: jest.fn(),
};
```

Add test:

```typescript
describe('replayStream', () => {
  it('should return an Observable from trackService.replayStream', () => {
    const entityId = '550e8400-e29b-41d4-a716-446655440000';
    const query = {
      startTime: '2025-01-01T00:00:00Z',
      endTime: '2025-01-02T00:00:00Z',
      speedMultiplier: 2,
    };
    const mockObservable = new Observable<any>();
    trackService.replayStream.mockReturnValue(mockObservable);

    const result = controller.replayStream(entityId, query as any);

    expect(trackService.replayStream).toHaveBeenCalledWith(
      entityId,
      new Date('2025-01-01T00:00:00Z'),
      new Date('2025-01-02T00:00:00Z'),
      2,
    );
    expect(result).toBe(mockObservable);
  });
});
```

Add `import { Observable } from 'rxjs';` at top of test file.

- [ ] **Step 3: Run test to verify it fails**

Run: `npx nx test track-service --testPathPattern=track.controller.spec`
Expected: FAIL — `replayStream` method not found on controller

- [ ] **Step 4: Implement `replayStream()` in TrackService**

In `track.service.ts`, add imports:

```typescript
import { Observable } from 'rxjs';
import { MessageEvent } from '@nestjs/common';
```

Note: `MessageEvent` must be imported from `@nestjs/common` (not the DOM type). It has the shape `{ data: string | object; type?: string; id?: string; }`.

```typescript
// ... inside TrackService class:

private static readonly MAX_REPLAY_DELAY_MS = 5000;

replayStream(
  entityId: string,
  startTime: Date,
  endTime: Date,
  speedMultiplier = 1,
): Observable<MessageEvent> {
  return new Observable<MessageEvent>((subscriber) => {
    let timeoutHandle: NodeJS.Timeout | null = null;
    let cancelled = false;

    const run = async () => {
      try {
        const points = await this.getHistory(entityId, startTime, endTime);

        if (points.length === 0) {
          subscriber.next({
            data: JSON.stringify({ entityId, totalPoints: 0 }),
            type: 'complete',
          } as MessageEvent);
          subscriber.complete();
          return;
        }

        for (let i = 0; i < points.length; i++) {
          if (cancelled) return;

          subscriber.next({
            data: JSON.stringify(points[i]),
            type: 'point',
          } as MessageEvent);

          if (i < points.length - 1) {
            const currTime = new Date(points[i].timestamp).getTime();
            const nextTime = new Date(points[i + 1].timestamp).getTime();
            const rawDelay = (nextTime - currTime) / speedMultiplier;
            const delay = Math.min(
              Math.max(rawDelay, 0),
              TrackService.MAX_REPLAY_DELAY_MS,
            );

            if (delay > 0) {
              await new Promise<void>((resolve) => {
                timeoutHandle = setTimeout(() => {
                  timeoutHandle = null;
                  resolve();
                }, delay);
              });
            }
          }
        }

        if (!cancelled) {
          subscriber.next({
            data: JSON.stringify({ entityId, totalPoints: points.length }),
            type: 'complete',
          } as MessageEvent);
          subscriber.complete();
        }
      } catch (err) {
        if (!cancelled) subscriber.error(err);
      }
    };

    run();

    return () => {
      cancelled = true;
      if (timeoutHandle) {
        clearTimeout(timeoutHandle);
        timeoutHandle = null;
      }
    };
  });
}
```

- [ ] **Step 5: Replace POST replay with SSE GET in controller**

In `track.controller.ts`:

1. Add imports: `import { Sse, MessageEvent } from '@nestjs/common';` and `import { Observable } from 'rxjs';`
2. Update import from DTO: change `ReplayTrackDto` to `ReplayStreamDto`
3. Remove the entire `replayTrack()` method (the POST stub)
4. Add the SSE endpoint:

```typescript
@Sse(':entityId/replay-stream')
replayStream(
  @Param('entityId', ParseUUIDPipe) entityId: string,
  @Query() query: ReplayStreamDto,
): Observable<MessageEvent> {
  const startTime = new Date(query.startTime);
  const endTime = new Date(query.endTime);
  const speedMultiplier = query.speedMultiplier || 1;

  return this.trackService.replayStream(
    entityId,
    startTime,
    endTime,
    speedMultiplier,
  );
}
```

**Important:** Place this method BEFORE the `@Get(':entityId')` route so NestJS matches `/replay-stream` before the `:entityId` wildcard. Or better: place it after `@Get(':entityId/latest')` and `@Get(':entityId/segments')` since it has a specific path `':entityId/replay-stream'`.

- [ ] **Step 6: Run all track-service tests**

Run: `npx nx test track-service`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add apps/track-service/src/tracks/dto/query-track.dto.ts
git add apps/track-service/src/tracks/track.service.ts
git add apps/track-service/src/tracks/track.controller.ts
git add apps/track-service/src/tracks/track.controller.spec.ts
git commit -m "feat(track-service): implement SSE replay endpoint (SEN-80)"
```

---

## Task 6: API Gateway — Tracks Module

**Files:**
- Create: `apps/api-gateway/src/modules/tracks/tracks.service.ts`
- Create: `apps/api-gateway/src/modules/tracks/tracks.controller.ts`
- Create: `apps/api-gateway/src/modules/tracks/tracks.module.ts`
- Create: `apps/api-gateway/src/modules/tracks/tracks.controller.spec.ts`
- Modify: `apps/api-gateway/src/app.module.ts:11,52`

Reference: Follow the `FeedsModule`/`FeedsService` pattern at `apps/api-gateway/src/modules/feeds/`.

- [ ] **Step 1: Write test for tracks controller**

```typescript
// apps/api-gateway/src/modules/tracks/tracks.controller.spec.ts
import { Test } from '@nestjs/testing';
import { TracksController } from './tracks.controller';
import { TracksService } from './tracks.service';

describe('TracksController', () => {
  let controller: TracksController;
  let service: jest.Mocked<TracksService>;

  beforeEach(async () => {
    const mockService = {
      getHistory: jest.fn(),
      getLatestPosition: jest.fn(),
      getSegments: jest.fn(),
    };

    const module = await Test.createTestingModule({
      controllers: [TracksController],
      providers: [{ provide: TracksService, useValue: mockService }],
    }).compile();

    controller = module.get(TracksController);
    service = module.get(TracksService);
  });

  it('should be defined', () => {
    expect(controller).toBeDefined();
  });

  describe('getHistory', () => {
    it('should delegate to service', async () => {
      const points = [{ id: '1', latitude: 10, longitude: 20 }];
      service.getHistory.mockResolvedValue(points);

      const result = await controller.getHistory('entity-1', {});
      expect(service.getHistory).toHaveBeenCalledWith('entity-1', {});
      expect(result).toEqual(points);
    });
  });

  describe('getLatestPosition', () => {
    it('should delegate to service', async () => {
      const point = { id: '1', latitude: 10, longitude: 20 };
      service.getLatestPosition.mockResolvedValue(point);

      const result = await controller.getLatestPosition('entity-1');
      expect(service.getLatestPosition).toHaveBeenCalledWith('entity-1');
      expect(result).toEqual(point);
    });
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `npx nx test api-gateway --testPathPattern=tracks.controller.spec`
Expected: FAIL — cannot find module

- [ ] **Step 3: Write TracksService**

```typescript
// apps/api-gateway/src/modules/tracks/tracks.service.ts
import { Injectable, Logger, ServiceUnavailableException } from '@nestjs/common';
import { HttpService } from '@nestjs/axios';
import { ConfigService } from '@nestjs/config';
import { firstValueFrom } from 'rxjs';

@Injectable()
export class TracksService {
  private readonly logger = new Logger(TracksService.name);
  private readonly trackServiceUrl: string;

  constructor(
    private readonly httpService: HttpService,
    private readonly configService: ConfigService,
  ) {
    this.trackServiceUrl = this.configService.get<string>(
      'TRACK_SERVICE_URL',
      'http://localhost:3002',
    );
  }

  async getHistory(entityId: string, query: Record<string, any>): Promise<any[]> {
    try {
      const { data } = await firstValueFrom(
        this.httpService.get(`${this.trackServiceUrl}/tracks/${entityId}`, {
          params: query,
        }),
      );
      return data;
    } catch (err) {
      this.logger.warn(`Track service error: ${err instanceof Error ? err.message : String(err)}`);
      throw new ServiceUnavailableException('Track service unavailable');
    }
  }

  async getLatestPosition(entityId: string): Promise<any> {
    try {
      const { data } = await firstValueFrom(
        this.httpService.get(`${this.trackServiceUrl}/tracks/${entityId}/latest`),
      );
      return data;
    } catch (err) {
      this.logger.warn(`Track service error: ${err instanceof Error ? err.message : String(err)}`);
      throw new ServiceUnavailableException('Track service unavailable');
    }
  }

  async getSegments(entityId: string, query: Record<string, any>): Promise<any[]> {
    try {
      const { data } = await firstValueFrom(
        this.httpService.get(`${this.trackServiceUrl}/tracks/${entityId}/segments`, {
          params: query,
        }),
      );
      return data;
    } catch (err) {
      this.logger.warn(`Track service error: ${err instanceof Error ? err.message : String(err)}`);
      throw new ServiceUnavailableException('Track service unavailable');
    }
  }

  getReplayStreamUrl(entityId: string): string {
    return `${this.trackServiceUrl}/tracks/${entityId}/replay-stream`;
  }
}
```

- [ ] **Step 4: Write TracksController**

```typescript
// apps/api-gateway/src/modules/tracks/tracks.controller.ts
import {
  Controller,
  Get,
  Param,
  Query,
  Req,
  Res,
  UseGuards,
  ParseUUIDPipe,
  Logger,
} from '@nestjs/common';
import { Request, Response } from 'express';
import { HttpService } from '@nestjs/axios';
import { firstValueFrom } from 'rxjs';
import { JwtAuthGuard } from '../auth/jwt-auth.guard';
import { TracksService } from './tracks.service';

@Controller('tracks')
@UseGuards(JwtAuthGuard)
export class TracksController {
  private readonly logger = new Logger(TracksController.name);

  constructor(
    private readonly tracksService: TracksService,
    private readonly httpService: HttpService,
  ) {}

  @Get(':entityId')
  async getHistory(
    @Param('entityId', ParseUUIDPipe) entityId: string,
    @Query() query: Record<string, any>,
  ) {
    return this.tracksService.getHistory(entityId, query);
  }

  @Get(':entityId/latest')
  async getLatestPosition(
    @Param('entityId', ParseUUIDPipe) entityId: string,
  ) {
    return this.tracksService.getLatestPosition(entityId);
  }

  @Get(':entityId/segments')
  async getSegments(
    @Param('entityId', ParseUUIDPipe) entityId: string,
    @Query() query: Record<string, any>,
  ) {
    return this.tracksService.getSegments(entityId, query);
  }

  /**
   * SSE replay passthrough — pipes the upstream SSE stream directly to the client.
   * Auth is handled by JwtAuthGuard on this controller.
   * For the Angular EventSource client, the JWT token is passed via ?token= query param.
   */
  @Get(':entityId/replay-stream')
  async replayStream(
    @Param('entityId', ParseUUIDPipe) entityId: string,
    @Query() query: Record<string, any>,
    @Req() req: Request,
    @Res() res: Response,
  ) {
    const url = this.tracksService.getReplayStreamUrl(entityId);

    // Strip token from query before forwarding
    const { token, ...forwardQuery } = query;

    try {
      const upstream = await firstValueFrom(
        this.httpService.get(url, {
          params: forwardQuery,
          responseType: 'stream',
          headers: { Accept: 'text/event-stream' },
        }),
      );

      res.setHeader('Content-Type', 'text/event-stream');
      res.setHeader('Cache-Control', 'no-cache');
      res.setHeader('Connection', 'keep-alive');
      res.setHeader('X-Accel-Buffering', 'no');

      // Close upstream when client disconnects to prevent dangling connections
      req.on('close', () => upstream.data.destroy());

      upstream.data.pipe(res);
    } catch (err) {
      this.logger.warn(`SSE proxy error: ${err instanceof Error ? err.message : String(err)}`);
      res.status(503).json({ message: 'Track service unavailable' });
    }
  }
}
```

- [ ] **Step 5: Write TracksModule**

```typescript
// apps/api-gateway/src/modules/tracks/tracks.module.ts
import { Module } from '@nestjs/common';
import { HttpModule } from '@nestjs/axios';
import { ConfigModule } from '@nestjs/config';
import { AuthModule } from '../auth/auth.module';
import { TracksController } from './tracks.controller';
import { TracksService } from './tracks.service';

@Module({
  imports: [HttpModule, ConfigModule, AuthModule],
  controllers: [TracksController],
  providers: [TracksService],
})
export class TracksModule {}
```

- [ ] **Step 6: Register TracksModule in API gateway AppModule**

In `apps/api-gateway/src/app.module.ts`, add:
```typescript
import { TracksModule } from './modules/tracks/tracks.module';
```
Add `TracksModule` to the `imports` array.

- [ ] **Step 7: Add JWT query param extraction for SSE auth**

In the `JwtAuthGuard` or via a custom guard, the `?token=` query param needs to be extracted for SSE endpoints. Check `apps/api-gateway/src/modules/auth/jwt.strategy.ts` — if it uses `ExtractJwt.fromAuthHeaderAsBearerToken()`, add a fallback extractor:

```typescript
jwtFromRequest: ExtractJwt.fromExtractors([
  ExtractJwt.fromAuthHeaderAsBearerToken(),
  (req) => req?.query?.token as string || null,
]),
```

This allows both `Authorization: Bearer <token>` (normal requests) and `?token=<token>` (EventSource SSE).

- [ ] **Step 8: Run tests**

Run: `npx nx test api-gateway --testPathPattern=tracks.controller.spec`
Expected: PASS

- [ ] **Step 9: Commit**

```bash
git add apps/api-gateway/src/modules/tracks/
git add apps/api-gateway/src/app.module.ts
git add apps/api-gateway/src/modules/auth/jwt.strategy.ts
git commit -m "feat(api-gateway): add tracks proxy module with SSE passthrough (SEN-80)"
```

---

## Task 7: Angular TrackApiService

**Files:**
- Create: `apps/web/src/app/core/services/track-api.service.ts`
- Create: `apps/web/src/app/core/services/track-api.service.spec.ts`

Reference: Follow pattern from `apps/web/src/app/core/services/entity.service.ts` for HTTP calls.

- [ ] **Step 1: Write tests**

```typescript
// apps/web/src/app/core/services/track-api.service.spec.ts
import { TestBed } from '@angular/core/testing';
import { HttpClientTestingModule, HttpTestingController } from '@angular/common/http/testing';
import { TrackApiService, TrackPoint } from './track-api.service';
import { AuthService } from './auth.service';

describe('TrackApiService', () => {
  let service: TrackApiService;
  let httpMock: HttpTestingController;

  beforeEach(() => {
    const authMock = { getToken: () => 'test-jwt-token' };

    TestBed.configureTestingModule({
      imports: [HttpClientTestingModule],
      providers: [
        TrackApiService,
        { provide: AuthService, useValue: authMock },
      ],
    });

    service = TestBed.inject(TrackApiService);
    httpMock = TestBed.inject(HttpTestingController);
  });

  afterEach(() => httpMock.verify());

  it('should fetch track history', () => {
    const mockPoints: TrackPoint[] = [{
      id: '1', entityId: 'e1', latitude: 10, longitude: 20,
      altitude: null, heading: null, speedKnots: null, course: null,
      source: null, timestamp: '2025-01-01T00:00:00Z',
    }];

    service.getHistory('e1').subscribe(points => {
      expect(points).toEqual(mockPoints);
    });

    const req = httpMock.expectOne('/api/tracks/e1');
    expect(req.request.method).toBe('GET');
    req.flush(mockPoints);
  });

  it('should fetch latest position', () => {
    const mockPoint: TrackPoint = {
      id: '1', entityId: 'e1', latitude: 10, longitude: 20,
      altitude: 500, heading: 90, speedKnots: 12, course: 180,
      source: 'AIS', timestamp: '2025-01-01T00:00:00Z',
    };

    service.getLatestPosition('e1').subscribe(point => {
      expect(point).toEqual(mockPoint);
    });

    const req = httpMock.expectOne('/api/tracks/e1/latest');
    req.flush(mockPoint);
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `npx nx test web --testPathPattern=track-api.service.spec`
Expected: FAIL — cannot find module

- [ ] **Step 3: Implement TrackApiService**

```typescript
// apps/web/src/app/core/services/track-api.service.ts
import { Injectable } from '@angular/core';
import { HttpClient, HttpParams } from '@angular/common/http';
import { Observable, Subscriber } from 'rxjs';
import { AuthService } from './auth.service';

export interface TrackPoint {
  id: string;
  entityId: string;
  latitude: number;
  longitude: number;
  altitude: number | null;
  heading: number | null;
  speedKnots: number | null;
  course: number | null;
  source: string | null;
  timestamp: string;
}

export interface TrackHistoryParams {
  startTime?: string;
  endTime?: string;
  maxPoints?: number;
  simplify?: number;
}

export interface ReplayParams {
  startTime: string;
  endTime: string;
  speedMultiplier?: number;
}

export interface TrackSegment {
  startTime: string;
  endTime: string;
  points: TrackPoint[];
}

@Injectable({ providedIn: 'root' })
export class TrackApiService {
  private readonly apiUrl = '/api/tracks';

  constructor(
    private readonly http: HttpClient,
    private readonly authService: AuthService,
  ) {}

  getHistory(entityId: string, params?: TrackHistoryParams): Observable<TrackPoint[]> {
    let httpParams = new HttpParams();
    if (params) {
      if (params.startTime) httpParams = httpParams.set('startTime', params.startTime);
      if (params.endTime) httpParams = httpParams.set('endTime', params.endTime);
      if (params.maxPoints) httpParams = httpParams.set('maxPoints', params.maxPoints.toString());
      if (params.simplify) httpParams = httpParams.set('simplify', params.simplify.toString());
    }
    return this.http.get<TrackPoint[]>(`${this.apiUrl}/${entityId}`, { params: httpParams });
  }

  getLatestPosition(entityId: string): Observable<TrackPoint | null> {
    return this.http.get<TrackPoint | null>(`${this.apiUrl}/${entityId}/latest`);
  }

  getSegments(entityId: string, startTime?: string, endTime?: string): Observable<TrackSegment[]> {
    let httpParams = new HttpParams();
    if (startTime) httpParams = httpParams.set('startTime', startTime);
    if (endTime) httpParams = httpParams.set('endTime', endTime);
    return this.http.get<TrackSegment[]>(`${this.apiUrl}/${entityId}/segments`, { params: httpParams });
  }

  replayStream(entityId: string, params: ReplayParams): Observable<TrackPoint> {
    return new Observable<TrackPoint>((subscriber: Subscriber<TrackPoint>) => {
      const token = this.authService.getToken();
      const queryParts = [
        `startTime=${encodeURIComponent(params.startTime)}`,
        `endTime=${encodeURIComponent(params.endTime)}`,
      ];
      if (params.speedMultiplier) {
        queryParts.push(`speedMultiplier=${params.speedMultiplier}`);
      }
      if (token) {
        queryParts.push(`token=${encodeURIComponent(token)}`);
      }

      const url = `${this.apiUrl}/${entityId}/replay-stream?${queryParts.join('&')}`;
      const eventSource = new EventSource(url);

      eventSource.addEventListener('point', (event: MessageEvent) => {
        try {
          subscriber.next(JSON.parse(event.data));
        } catch (e) {
          subscriber.error(e);
        }
      });

      eventSource.addEventListener('complete', () => {
        eventSource.close();
        subscriber.complete();
      });

      eventSource.onerror = () => {
        eventSource.close();
        subscriber.error(new Error('SSE connection error'));
      };

      return () => {
        eventSource.close();
      };
    });
  }
}
```

- [ ] **Step 4: Run tests**

Run: `npx nx test web --testPathPattern=track-api.service.spec`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add apps/web/src/app/core/services/track-api.service.ts
git add apps/web/src/app/core/services/track-api.service.spec.ts
git commit -m "feat(web): add TrackApiService with SSE replay support (SEN-80)"
```

---

## Task 8: Track Panel Store

**Files:**
- Create: `apps/web/src/app/features/track-panel/track-panel.store.ts`

- [ ] **Step 1: Write the store**

```typescript
// apps/web/src/app/features/track-panel/track-panel.store.ts
import { Injectable, signal, computed } from '@angular/core';
import { TrackPoint } from '../../core/services/track-api.service';

@Injectable()
export class TrackPanelStore {
  readonly selectedEntityId = signal<string | null>(null);
  readonly selectedEntityName = signal<string>('');
  readonly allPoints = signal<TrackPoint[]>([]);
  readonly currentIndex = signal<number>(-1);
  readonly isPlaying = signal<boolean>(false);
  readonly speedMultiplier = signal<number>(1);
  readonly isExpanded = signal<boolean>(true);

  readonly isOpen = computed(() => this.selectedEntityId() !== null);

  readonly currentPoint = computed(() => {
    const idx = this.currentIndex();
    const points = this.allPoints();
    return idx >= 0 && idx < points.length ? points[idx] : null;
  });

  readonly timeRange = computed(() => {
    const points = this.allPoints();
    if (points.length === 0) return null;
    return {
      start: points[0].timestamp,
      end: points[points.length - 1].timestamp,
    };
  });

  readonly progress = computed(() => {
    const points = this.allPoints();
    const idx = this.currentIndex();
    if (points.length === 0) return 0;
    return idx / (points.length - 1);
  });

  open(entityId: string, entityName: string, points: TrackPoint[]): void {
    this.selectedEntityId.set(entityId);
    this.selectedEntityName.set(entityName);
    this.allPoints.set(points);
    this.currentIndex.set(-1);
    this.isPlaying.set(false);
    this.speedMultiplier.set(1);
    this.isExpanded.set(true);
  }

  close(): void {
    this.selectedEntityId.set(null);
    this.selectedEntityName.set('');
    this.allPoints.set([]);
    this.currentIndex.set(-1);
    this.isPlaying.set(false);
  }

  toggleExpanded(): void {
    this.isExpanded.update((v) => !v);
  }
}
```

- [ ] **Step 2: Commit**

```bash
git add apps/web/src/app/features/track-panel/track-panel.store.ts
git commit -m "feat(web): add TrackPanelStore with signal-based state (SEN-80)"
```

---

## Task 9: Track Render Service

**Files:**
- Create: `apps/web/src/app/features/track-panel/track-render.service.ts`
- Create: `apps/web/src/app/features/track-panel/track-render.service.spec.ts`

- [ ] **Step 1: Write test**

```typescript
// apps/web/src/app/features/track-panel/track-render.service.spec.ts
import { TrackRenderService } from './track-render.service';

describe('TrackRenderService', () => {
  let service: TrackRenderService;
  let mockViewer: any;
  let mockCesium: any;

  beforeEach(() => {
    service = new TrackRenderService();
    mockViewer = {
      entities: {
        add: jest.fn().mockReturnValue({}),
        remove: jest.fn(),
      },
    };
    mockCesium = {
      Cartesian3: { fromDegreesArray: jest.fn().mockReturnValue([]), fromDegrees: jest.fn().mockReturnValue({}) },
      Color: {
        CYAN: { withAlpha: jest.fn().mockReturnValue('cyan-alpha') },
        GRAY: { withAlpha: jest.fn().mockReturnValue('gray-alpha') },
        YELLOW: {},
        RED: {},
      },
      PolylineGlowMaterialProperty: jest.fn().mockReturnValue({}),
      CallbackProperty: jest.fn().mockImplementation((cb) => cb),
      ConstantProperty: jest.fn().mockImplementation((v) => v),
      HeightReference: { NONE: 0 },
    };
    service.init(mockViewer, mockCesium);
  });

  it('should initialize with viewer and Cesium references', () => {
    expect(service).toBeDefined();
  });

  it('should draw static track polyline', () => {
    const points = [
      { latitude: 10, longitude: 20, altitude: 100 },
      { latitude: 11, longitude: 21, altitude: 200 },
    ];
    service.drawStaticTrack(points as any);
    expect(mockViewer.entities.add).toHaveBeenCalled();
  });

  it('should clear all track entities', () => {
    const points = [
      { latitude: 10, longitude: 20, altitude: 100 },
      { latitude: 11, longitude: 21, altitude: 200 },
    ];
    service.drawStaticTrack(points as any);
    service.clearAll();
    expect(mockViewer.entities.remove).toHaveBeenCalled();
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `npx nx test web --testPathPattern=track-render.service.spec`
Expected: FAIL

- [ ] **Step 3: Implement TrackRenderService**

```typescript
// apps/web/src/app/features/track-panel/track-render.service.ts
import { Injectable } from '@angular/core';
import { TrackPoint } from '../../core/services/track-api.service';

@Injectable()
export class TrackRenderService {
  private viewer: any = null;
  private Cesium: any = null;
  private trackEntities: any[] = [];
  private replayMarker: any = null;
  private currentReplayIndex = 0;

  init(viewer: any, Cesium: any): void {
    this.viewer = viewer;
    this.Cesium = Cesium;
  }

  drawStaticTrack(points: TrackPoint[]): void {
    if (!this.viewer || points.length < 2) return;

    const positions = this.pointsToCartesians(points);

    const polyline = this.viewer.entities.add({
      polyline: {
        positions,
        width: 2,
        material: new this.Cesium.PolylineGlowMaterialProperty({
          glowPower: 0.2,
          color: this.Cesium.Color.CYAN.withAlpha(0.8),
        }),
        clampToGround: false,
      },
    });
    this.trackEntities.push(polyline);

    // Start marker
    const startMarker = this.viewer.entities.add({
      position: this.Cesium.Cartesian3.fromDegrees(
        points[0].longitude, points[0].latitude, points[0].altitude ?? 0,
      ),
      point: { pixelSize: 8, color: this.Cesium.Color.YELLOW },
    });
    this.trackEntities.push(startMarker);

    // End marker
    const endPoint = points[points.length - 1];
    const endMarker = this.viewer.entities.add({
      position: this.Cesium.Cartesian3.fromDegrees(
        endPoint.longitude, endPoint.latitude, endPoint.altitude ?? 0,
      ),
      point: { pixelSize: 8, color: this.Cesium.Color.RED },
    });
    this.trackEntities.push(endMarker);
  }

  setupReplayPolylines(points: TrackPoint[]): void {
    if (!this.viewer || points.length < 2) return;

    this.clearAll();

    this.currentReplayIndex = 0;

    // Played polyline (highlighted)
    const playedPolyline = this.viewer.entities.add({
      polyline: {
        positions: new this.Cesium.CallbackProperty(() => {
          return this.pointsToCartesians(points.slice(0, this.currentReplayIndex + 1));
        }, false),
        width: 3,
        material: new this.Cesium.PolylineGlowMaterialProperty({
          glowPower: 0.3,
          color: this.Cesium.Color.CYAN.withAlpha(0.9),
        }),
        clampToGround: false,
      },
    });
    this.trackEntities.push(playedPolyline);

    // Future polyline (dimmed)
    const futurePolyline = this.viewer.entities.add({
      polyline: {
        positions: new this.Cesium.CallbackProperty(() => {
          return this.pointsToCartesians(points.slice(this.currentReplayIndex));
        }, false),
        width: 1,
        material: this.Cesium.Color.GRAY.withAlpha(0.3),
        clampToGround: false,
      },
    });
    this.trackEntities.push(futurePolyline);

    // Replay marker
    this.replayMarker = this.viewer.entities.add({
      position: new this.Cesium.CallbackProperty(() => {
        const idx = this.currentReplayIndex;
        if (idx < 0 || idx >= points.length) return null;
        const p = points[idx];
        return this.Cesium.Cartesian3.fromDegrees(p.longitude, p.latitude, p.altitude ?? 0);
      }, false),
      point: { pixelSize: 12, color: this.Cesium.Color.CYAN },
    });
    this.trackEntities.push(this.replayMarker);
  }

  updateReplayIndex(index: number): void {
    this.currentReplayIndex = index;
  }

  clearAll(): void {
    if (!this.viewer) return;
    for (const entity of this.trackEntities) {
      this.viewer.entities.remove(entity);
    }
    this.trackEntities = [];
    this.replayMarker = null;
    this.currentReplayIndex = 0;
  }

  private pointsToCartesians(points: TrackPoint[]): any[] {
    return points.map((p) =>
      this.Cesium.Cartesian3.fromDegrees(p.longitude, p.latitude, p.altitude ?? 0),
    );
  }
}
```

- [ ] **Step 4: Run tests**

Run: `npx nx test web --testPathPattern=track-render.service.spec`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add apps/web/src/app/features/track-panel/track-render.service.ts
git add apps/web/src/app/features/track-panel/track-render.service.spec.ts
git commit -m "feat(web): add TrackRenderService for Cesium polylines (SEN-80)"
```

---

## Task 10: Context Menu Component

**Files:**
- Create: `apps/web/src/app/shared/components/context-menu/context-menu.component.ts`

- [ ] **Step 1: Write the component**

```typescript
// apps/web/src/app/shared/components/context-menu/context-menu.component.ts
import {
  Component,
  ChangeDetectionStrategy,
  input,
  output,
  signal,
  HostListener,
} from '@angular/core';

export interface ContextMenuItem {
  label: string;
  action: string;
}

@Component({
  selector: 'app-context-menu',
  standalone: true,
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    @if (visible()) {
      <div
        class="context-menu"
        [style.left.px]="x()"
        [style.top.px]="y()"
      >
        @for (item of items(); track item.action) {
          <button class="context-menu-item" (click)="onItemClick($event, item)">
            {{ item.label }}
          </button>
        }
      </div>
    }
  `,
  styles: [`
    .context-menu {
      position: fixed;
      z-index: 1000;
      background: var(--surface-secondary, #1a1f2e);
      border: 1px solid var(--border-color, #2a3040);
      border-radius: 4px;
      padding: 4px 0;
      min-width: 160px;
      box-shadow: 0 4px 12px rgba(0, 0, 0, 0.4);
    }
    .context-menu-item {
      display: block;
      width: 100%;
      padding: 8px 16px;
      border: none;
      background: none;
      color: var(--text-primary, #e0e0e0);
      font-size: 13px;
      text-align: left;
      cursor: pointer;
    }
    .context-menu-item:hover {
      background: var(--surface-hover, #252a3a);
    }
  `],
})
export class ContextMenuComponent {
  items = input<ContextMenuItem[]>([]);
  itemSelected = output<string>();

  readonly visible = signal(false);
  readonly x = signal(0);
  readonly y = signal(0);

  show(x: number, y: number): void {
    this.x.set(x);
    this.y.set(y);
    this.visible.set(true);
  }

  hide(): void {
    this.visible.set(false);
  }

  onItemClick(event: Event, item: ContextMenuItem): void {
    event.stopPropagation(); // Prevent document:click from firing onDismiss
    this.itemSelected.emit(item.action);
    this.hide();
  }

  @HostListener('document:click')
  @HostListener('document:keydown.escape')
  onDismiss(): void {
    this.hide();
  }
}
```

- [ ] **Step 2: Commit**

```bash
git add apps/web/src/app/shared/components/context-menu/context-menu.component.ts
git commit -m "feat(web): add reusable context menu component (SEN-80)"
```

---

## Task 11: Track Panel Component

**Files:**
- Create: `apps/web/src/app/features/track-panel/track-panel.component.ts`
- Create: `apps/web/src/app/features/track-panel/track-panel.component.scss`
- Create: `apps/web/src/app/features/track-panel/track-panel.component.spec.ts`

- [ ] **Step 1: Write test**

```typescript
// apps/web/src/app/features/track-panel/track-panel.component.spec.ts
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { TrackPanelComponent } from './track-panel.component';
import { TrackPanelStore } from './track-panel.store';
import { TrackRenderService } from './track-render.service';
import { TrackApiService } from '../../core/services/track-api.service';

describe('TrackPanelComponent', () => {
  let component: TrackPanelComponent;
  let fixture: ComponentFixture<TrackPanelComponent>;
  let store: TrackPanelStore;

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [TrackPanelComponent],
      providers: [
        TrackPanelStore,
        { provide: TrackRenderService, useValue: { clearAll: jest.fn(), setupReplayPolylines: jest.fn(), updateReplayIndex: jest.fn() } },
        { provide: TrackApiService, useValue: { replayStream: jest.fn() } },
      ],
    }).compileComponents();

    fixture = TestBed.createComponent(TrackPanelComponent);
    component = fixture.componentInstance;
    store = TestBed.inject(TrackPanelStore);
  });

  it('should create', () => {
    expect(component).toBeTruthy();
  });

  it('should not be visible when store is closed', () => {
    expect(store.isOpen()).toBe(false);
  });

  it('should toggle expanded state', () => {
    expect(store.isExpanded()).toBe(true);
    store.toggleExpanded();
    expect(store.isExpanded()).toBe(false);
  });

  it('should close and clear render service', () => {
    const renderService = TestBed.inject(TrackRenderService);
    store.open('e1', 'Test Entity', []);
    expect(store.isOpen()).toBe(true);

    component.close();
    expect(store.isOpen()).toBe(false);
    expect(renderService.clearAll).toHaveBeenCalled();
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `npx nx test web --testPathPattern=track-panel.component.spec`
Expected: FAIL

- [ ] **Step 3: Write component SCSS**

```scss
// apps/web/src/app/features/track-panel/track-panel.component.scss
:host {
  display: block;
  position: fixed;
  bottom: 0;
  left: 0;
  right: 0;
  z-index: 500;
}

.track-panel {
  background: var(--surface-primary, #0d1117);
  border-top: 1px solid var(--border-color, #2a3040);
  transition: height 0.2s ease;
  display: flex;
  flex-direction: column;

  &.expanded {
    height: 200px;
  }

  &.collapsed {
    height: 36px;
  }
}

.panel-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 6px 16px;
  background: var(--surface-secondary, #1a1f2e);
  border-bottom: 1px solid var(--border-color, #2a3040);
  cursor: pointer;
  user-select: none;
  min-height: 36px;

  .entity-name {
    font-size: 13px;
    font-weight: 500;
    color: var(--text-primary, #e0e0e0);
  }

  .header-controls {
    display: flex;
    gap: 8px;
    align-items: center;
  }

  .btn-icon {
    background: none;
    border: none;
    color: var(--text-secondary, #8b949e);
    cursor: pointer;
    font-size: 16px;
    padding: 2px 6px;

    &:hover {
      color: var(--text-primary, #e0e0e0);
    }
  }
}

.panel-body {
  flex: 1;
  display: flex;
  flex-direction: column;
  padding: 8px 16px;
  overflow: hidden;
}

.playback-controls {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-bottom: 8px;

  .btn-play {
    background: var(--accent-color, #58a6ff);
    border: none;
    border-radius: 4px;
    color: #fff;
    width: 32px;
    height: 28px;
    cursor: pointer;
    font-size: 14px;

    &:hover {
      opacity: 0.9;
    }
  }

  .speed-select {
    background: var(--surface-secondary, #1a1f2e);
    border: 1px solid var(--border-color, #2a3040);
    color: var(--text-primary, #e0e0e0);
    border-radius: 4px;
    padding: 4px 8px;
    font-size: 12px;
  }

  .timeline-scrubber {
    flex: 1;
    accent-color: var(--accent-color, #58a6ff);
  }
}

.status-line {
  display: flex;
  gap: 16px;
  font-size: 12px;
  color: var(--text-secondary, #8b949e);
  font-family: monospace;

  .status-item {
    white-space: nowrap;
  }
}
```

- [ ] **Step 4: Write component**

```typescript
// apps/web/src/app/features/track-panel/track-panel.component.ts
import {
  Component,
  ChangeDetectionStrategy,
  OnDestroy,
} from '@angular/core';
import { CommonModule } from '@angular/common';
import { FormsModule } from '@angular/forms';
import { Subscription } from 'rxjs';
import { TrackPanelStore } from './track-panel.store';
import { TrackRenderService } from './track-render.service';
import { TrackApiService, TrackPoint } from '../../core/services/track-api.service';

@Component({
  selector: 'app-track-panel',
  standalone: true,
  changeDetection: ChangeDetectionStrategy.OnPush,
  imports: [CommonModule, FormsModule],
  templateUrl: './track-panel.component.html',
  styleUrls: ['./track-panel.component.scss'],
  // Note: TrackPanelStore and TrackRenderService are provided by MapComponent.
  // This component inherits them through the DI tree.
})
export class TrackPanelComponent implements OnDestroy {
  readonly speeds = [0.5, 1, 2, 5, 10];
  private replaySub: Subscription | null = null;

  constructor(
    readonly store: TrackPanelStore,
    private readonly renderService: TrackRenderService,
    private readonly trackApi: TrackApiService,
  ) {}

  ngOnDestroy(): void {
    this.stopReplay();
  }

  play(): void {
    const points = this.store.allPoints();
    const timeRange = this.store.timeRange();
    if (!timeRange || points.length === 0) return;

    const entityId = this.store.selectedEntityId()!;
    const currentPoint = this.store.currentPoint();

    // Use current position as start time if resuming, else start from beginning
    const startTime = currentPoint
      ? new Date(new Date(currentPoint.timestamp).getTime() + 1).toISOString()
      : timeRange.start;

    this.renderService.setupReplayPolylines(points);
    this.store.isPlaying.set(true);

    // Track replay points by stream order, not timestamp matching
    let replayIdx = this.store.currentIndex() >= 0 ? this.store.currentIndex() : 0;

    this.replaySub = this.trackApi.replayStream(entityId, {
      startTime,
      endTime: timeRange.end,
      speedMultiplier: this.store.speedMultiplier(),
    }).subscribe({
      next: () => {
        replayIdx++;
        if (replayIdx < points.length) {
          this.store.currentIndex.set(replayIdx);
          this.renderService.updateReplayIndex(replayIdx);
        }
      },
      complete: () => {
        this.store.isPlaying.set(false);
      },
      error: () => {
        this.store.isPlaying.set(false);
      },
    });
  }

  pause(): void {
    this.stopReplay();
    this.store.isPlaying.set(false);
  }

  togglePlay(): void {
    if (this.store.isPlaying()) {
      this.pause();
    } else {
      this.play();
    }
  }

  onSpeedChange(speed: number): void {
    this.store.speedMultiplier.set(speed);
    if (this.store.isPlaying()) {
      this.stopReplay();
      this.play();
    }
  }

  onScrub(event: Event): void {
    const input = event.target as HTMLInputElement;
    const idx = parseInt(input.value, 10);
    this.stopReplay();
    this.store.isPlaying.set(false);
    this.store.currentIndex.set(idx);
    this.renderService.updateReplayIndex(idx);
  }

  close(): void {
    this.stopReplay();
    this.renderService.clearAll();
    this.store.close();
  }

  private stopReplay(): void {
    if (this.replaySub) {
      this.replaySub.unsubscribe();
      this.replaySub = null;
    }
  }
}
```

- [ ] **Step 5: Write component template**

Create `apps/web/src/app/features/track-panel/track-panel.component.html`:

```html
@if (store.isOpen()) {
  <div class="track-panel" [class.expanded]="store.isExpanded()" [class.collapsed]="!store.isExpanded()">
    <div class="panel-header" (click)="store.toggleExpanded()">
      <span class="entity-name">Track: {{ store.selectedEntityName() }}</span>
      <div class="header-controls">
        <button class="btn-icon" (click)="$event.stopPropagation(); store.toggleExpanded()">
          {{ store.isExpanded() ? '&#9660;' : '&#9650;' }}
        </button>
        <button class="btn-icon" (click)="$event.stopPropagation(); close()">&times;</button>
      </div>
    </div>

    @if (store.isExpanded()) {
      <div class="panel-body">
        <div class="playback-controls">
          <button class="btn-play" (click)="togglePlay()">
            {{ store.isPlaying() ? '&#9646;&#9646;' : '&#9654;' }}
          </button>

          <select
            class="speed-select"
            [ngModel]="store.speedMultiplier()"
            (ngModelChange)="onSpeedChange($event)"
          >
            @for (s of speeds; track s) {
              <option [ngValue]="s">{{ s }}x</option>
            }
          </select>

          <input
            type="range"
            class="timeline-scrubber"
            [min]="0"
            [max]="store.allPoints().length - 1"
            [value]="store.currentIndex() >= 0 ? store.currentIndex() : 0"
            (input)="onScrub($event)"
          />
        </div>

        @if (store.currentPoint(); as point) {
          <div class="status-line">
            <span class="status-item">{{ point.timestamp }}</span>
            <span class="status-item">{{ point.latitude | number:'1.4-4' }}, {{ point.longitude | number:'1.4-4' }}</span>
            @if (point.altitude !== null) {
              <span class="status-item">Alt: {{ point.altitude | number:'1.0-0' }}m</span>
            }
            @if (point.speedKnots !== null) {
              <span class="status-item">Spd: {{ point.speedKnots | number:'1.1-1' }}kn</span>
            }
            @if (point.heading !== null) {
              <span class="status-item">Hdg: {{ point.heading | number:'1.0-0' }}&deg;</span>
            }
          </div>
        }
      </div>
    }
  </div>
}
```

- [ ] **Step 6: Run test**

Run: `npx nx test web --testPathPattern=track-panel.component.spec`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add apps/web/src/app/features/track-panel/
git commit -m "feat(web): add track panel component with playback controls (SEN-80)"
```

---

## Task 12: MapComponent Integration

**Files:**
- Modify: `apps/web/src/app/features/map/map.component.ts:1-40,107,148-156,172-196`
- Modify: `apps/web/src/app/features/map/map.component.html:78-85`

- [ ] **Step 1: Add imports and inject services**

In `map.component.ts`:

Add imports:
```typescript
import { TrackPanelComponent } from '../track-panel/track-panel.component';
import { TrackPanelStore } from '../track-panel/track-panel.store';
import { TrackRenderService } from '../track-panel/track-render.service';
import { TrackApiService } from '../../core/services/track-api.service';
import { ContextMenuComponent, ContextMenuItem } from '../../shared/components/context-menu/context-menu.component';
```

Add to `imports` array in `@Component`:
```typescript
imports: [CommonModule, FormsModule, EntityDetailPanelComponent, TrackPanelComponent, ContextMenuComponent],
```

Add to constructor:
```typescript
readonly trackRenderService: TrackRenderService,
private readonly trackApiService: TrackApiService,
```

Add component-level providers:
```typescript
providers: [TrackPanelStore, TrackRenderService],
```

Add context menu signals:
```typescript
readonly contextMenuItems: ContextMenuItem[] = [
  { label: 'Show Track History', action: 'show-track' },
];
```

Add a `@ViewChild` for the context menu:
```typescript
@ViewChild('contextMenu') contextMenu!: ContextMenuComponent;
private contextMenuEntityId: string | null = null;
```

- [ ] **Step 2: Init TrackRenderService in ngAfterViewInit**

After `this.buildingsService.init(this.viewer, this.Cesium);` (line 192), add:
```typescript
this.trackRenderService.init(this.viewer, this.Cesium);
```

- [ ] **Step 3: Add right-click handler**

In the `setupEventHandlers()` method (or after viewer init), add a right-click handler inside `ngZone.runOutsideAngular`:

```typescript
const rightClickHandler = new this.Cesium.ScreenSpaceEventHandler(this.viewer.scene.canvas);
rightClickHandler.setInputAction((movement: any) => {
  const picked = this.viewer.scene.pick(movement.position);
  if (picked?.id?.entityData) {
    const entity = picked.id.entityData as Entity;
    this.ngZone.run(() => {
      this.contextMenuEntityId = entity.id;
      this.contextMenu.show(movement.position.x, movement.position.y);
    });
  }
}, this.Cesium.ScreenSpaceEventType.RIGHT_CLICK);
```

- [ ] **Step 4: Handle context menu selection**

Inject `TrackPanelStore` in the constructor alongside the other services:
```typescript
private readonly trackPanelStore: TrackPanelStore,
```

Add method to `MapComponent`:
```typescript
onContextMenuAction(action: string): void {
  if (action === 'show-track' && this.contextMenuEntityId) {
    const entityId = this.contextMenuEntityId;
    const entityData = this.entityMap.get(entityId);
    const entityName = entityData?.name ?? entityId;

    this.trackApiService.getHistory(entityId).subscribe((points) => {
      this.trackPanelStore.open(entityId, entityName, points);
      this.trackRenderService.drawStaticTrack(points);
    });
  }
}
```

- [ ] **Step 5: Update template**

In `map.component.html`, before the closing `</div>` of `map-container`, add:

```html
<app-context-menu
  #contextMenu
  [items]="contextMenuItems"
  (itemSelected)="onContextMenuAction($event)"
/>

<app-track-panel />
```

- [ ] **Step 6: Run all web tests**

Run: `npx nx test web`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add apps/web/src/app/features/map/map.component.ts
git add apps/web/src/app/features/map/map.component.html
git commit -m "feat(web): integrate track panel and context menu into map (SEN-80)"
```

---

## Task 13: Final Verification

- [ ] **Step 1: Run all track-service tests**

Run: `npx nx test track-service`
Expected: All PASS

- [ ] **Step 2: Run all api-gateway tests**

Run: `npx nx test api-gateway`
Expected: All PASS

- [ ] **Step 3: Run all web tests**

Run: `npx nx test web`
Expected: All PASS

- [ ] **Step 4: Build check**

Run: `npx nx build track-service && npx nx build api-gateway && npx nx build web`
Expected: All succeed

- [ ] **Step 5: Final commit (if any fixes needed)**

```bash
git commit -m "fix: address test/build issues from track service integration (SEN-80)"
```
