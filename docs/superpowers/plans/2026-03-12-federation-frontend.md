# Federation Frontend Rendering — Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add frontend support for displaying federated entities, remote user presence, and federation status on the CesiumJS map — so users can visually distinguish local vs. federated data, see where remote users are looking, and monitor peer connection health.

**Architecture:** Three new files decompose federation rendering from the 946-line map component: a `FederationService` for state management and API calls, a `FederationOverlayService` for CesiumJS primitive rendering, and a `FederationStatusComponent` for the status badge + source legend overlay. The existing Entity model, WebSocket service, and map component receive minimal modifications.

**Tech Stack:** Angular 19 (zoneless, standalone components, signals), CesiumJS (BillboardCollection, LabelCollection, PolylineCollection), RxJS, Socket.IO

**Spec:** `docs/superpowers/specs/2026-03-12-federation-design.md`

---

## Chunk 1: Model Changes, Federation Service, and WebSocket Integration

### Task 1: Extend Entity Model with Federation Fields

**Files:**
- Modify: `apps/web/src/app/shared/models/entity.model.ts`

- [ ] **Step 1: Add federation fields to Entity interface**

In `apps/web/src/app/shared/models/entity.model.ts`, add these optional fields to the `Entity` interface after the `circularError` field:

```typescript
  // Federation metadata (set for entities received from peer instances)
  sourceInstanceId?: string;
  sourceInstanceName?: string;
```

- [ ] **Step 2: Verify build**

Run: `npx nx build web --skip-nx-cache`
Expected: BUILD SUCCESSFUL

- [ ] **Step 3: Commit**

```bash
git add apps/web/src/app/shared/models/entity.model.ts
git commit -m "feat(federation): add sourceInstanceId/Name to Entity model"
```

---

### Task 2: Add Federation Events to WebSocket Service

**Files:**
- Modify: `apps/web/src/app/core/services/websocket.service.ts`

- [ ] **Step 1: Add federation event types and subjects**

In `apps/web/src/app/core/services/websocket.service.ts`, add new interfaces and subjects:

```typescript
// Add these interfaces after the existing imports
export interface FederationPeerStatus {
  instanceId: string;
  displayName: string;
  status: 'connected' | 'stale' | 'disconnected';
  color: string;
  entityCount: number;
  userCount: number;
}

export interface FederationStatusEvent {
  peers: FederationPeerStatus[];
}

export interface PresenceEntry {
  userId: string;
  displayName: string;
  instanceId: string;
  instanceName: string;
  cameraCenter: { lat: number; lon: number };
  zoom: number;
  timestamp: number;
  color: string;
}

export interface PresenceUpdateEvent {
  users: PresenceEntry[];
}
```

Add new subjects and observables to the class:

```typescript
  private readonly federationStatusSubject = new Subject<FederationStatusEvent>();
  private readonly presenceUpdateSubject = new Subject<PresenceUpdateEvent>();

  readonly federationStatus$ = this.federationStatusSubject.asObservable();
  readonly presenceUpdates$ = this.presenceUpdateSubject.asObservable();
```

- [ ] **Step 2: Subscribe to federation Socket.IO events in connect()**

Inside the `connect()` method, after the existing `alert:new` handler, add:

```typescript
    // Federation events (pushed by API gateway from federation module)
    this.socket.on('federation:status', (event: FederationStatusEvent) => {
      this.federationStatusSubject.next(event);
    });

    this.socket.on('federation:presence', (event: PresenceUpdateEvent) => {
      this.presenceUpdateSubject.next(event);
    });
```

- [ ] **Step 3: Verify build**

Run: `npx nx build web --skip-nx-cache`
Expected: BUILD SUCCESSFUL

- [ ] **Step 4: Commit**

```bash
git add apps/web/src/app/core/services/websocket.service.ts
git commit -m "feat(federation): add federation status and presence events to WebSocket service"
```

---

### Task 3: Create FederationService

The `FederationService` manages federation state (peer list, presence entries) and provides HTTP methods for admin config. It consumes WebSocket events and exposes observables for the overlay service and status component.

**Files:**
- Create: `apps/web/src/app/core/services/federation.service.ts`
- Create: `apps/web/src/app/core/services/federation.service.spec.ts`

- [ ] **Step 1: Write the test**

Create `apps/web/src/app/core/services/federation.service.spec.ts`:

```typescript
import { TestBed } from '@angular/core/testing';
import { provideHttpClient } from '@angular/common/http';
import { provideHttpClientTesting, HttpTestingController } from '@angular/common/http/testing';
import { FederationService } from './federation.service';
import { WebSocketService, FederationStatusEvent, PresenceUpdateEvent } from './websocket.service';
import { Subject } from 'rxjs';

describe('FederationService', () => {
  let service: FederationService;
  let httpMock: HttpTestingController;
  const federationStatusSubject = new Subject<FederationStatusEvent>();
  const presenceUpdateSubject = new Subject<PresenceUpdateEvent>();

  const mockWsService = {
    federationStatus$: federationStatusSubject.asObservable(),
    presenceUpdates$: presenceUpdateSubject.asObservable(),
  };

  beforeEach(() => {
    TestBed.configureTestingModule({
      providers: [
        FederationService,
        provideHttpClient(),
        provideHttpClientTesting(),
        { provide: WebSocketService, useValue: mockWsService },
      ],
    });

    service = TestBed.inject(FederationService);
    httpMock = TestBed.inject(HttpTestingController);
  });

  afterEach(() => {
    httpMock.verify();
  });

  it('should be created', () => {
    expect(service).toBeTruthy();
  });

  it('should update peers when federation status arrives', () => {
    const statusEvent: FederationStatusEvent = {
      peers: [{
        instanceId: 'peer-1',
        displayName: 'Bravo',
        status: 'connected',
        color: '#f97316',
        entityCount: 47,
        userCount: 2,
      }],
    };

    federationStatusSubject.next(statusEvent);
    expect(service.peers().length).toBe(1);
    expect(service.peers()[0].displayName).toBe('Bravo');
  });

  it('should update presence entries when presence event arrives', () => {
    const presenceEvent: PresenceUpdateEvent = {
      users: [{
        userId: 'u1',
        displayName: 'j.smith',
        instanceId: 'peer-1',
        instanceName: 'Bravo',
        cameraCenter: { lat: 34.05, lon: -118.25 },
        zoom: 8,
        timestamp: Date.now(),
        color: '#f97316',
      }],
    };

    presenceUpdateSubject.next(presenceEvent);
    expect(service.presenceEntries().length).toBe(1);
  });

  it('should expire presence entries after 5s', () => {
    jest.useFakeTimers();
    const presenceEvent: PresenceUpdateEvent = {
      users: [{
        userId: 'u1',
        displayName: 'j.smith',
        instanceId: 'peer-1',
        instanceName: 'Bravo',
        cameraCenter: { lat: 34.05, lon: -118.25 },
        zoom: 8,
        timestamp: Date.now() - 6000, // 6 seconds ago
        color: '#f97316',
      }],
    };

    presenceUpdateSubject.next(presenceEvent);
    jest.advanceTimersByTime(1000); // trigger cleanup
    expect(service.presenceEntries().length).toBe(0);
    jest.useRealTimers();
  });

  it('should track federation enabled state', () => {
    expect(service.federationActive()).toBe(false);

    federationStatusSubject.next({
      peers: [{
        instanceId: 'peer-1',
        displayName: 'Bravo',
        status: 'connected',
        color: '#f97316',
        entityCount: 10,
        userCount: 1,
      }],
    });

    expect(service.federationActive()).toBe(true);
  });
});
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `npx nx test web --testFile=src/app/core/services/federation.service.spec.ts`
Expected: FAIL — module not found

- [ ] **Step 3: Implement FederationService**

Create `apps/web/src/app/core/services/federation.service.ts`:

```typescript
import { Injectable, OnDestroy, signal, computed } from '@angular/core';
import { HttpClient } from '@angular/common/http';
import { Subscription } from 'rxjs';
import {
  WebSocketService,
  FederationPeerStatus,
  PresenceEntry,
} from './websocket.service';

@Injectable({ providedIn: 'root' })
export class FederationService implements OnDestroy {
  /** Currently connected peers with status. */
  readonly peers = signal<FederationPeerStatus[]>([]);

  /** Active remote user presence entries. */
  readonly presenceEntries = signal<PresenceEntry[]>([]);

  /** True if at least one peer is connected. */
  readonly federationActive = computed(() => this.peers().some(p => p.status === 'connected'));

  /** Total federated entity count across all peers. */
  readonly totalFederatedEntities = computed(() =>
    this.peers().reduce((sum, p) => sum + p.entityCount, 0),
  );

  private readonly subscriptions = new Subscription();
  private presenceCleanupTimer: ReturnType<typeof setInterval> | null = null;

  private static readonly PRESENCE_EXPIRY_MS = 5_000;

  constructor(
    private readonly http: HttpClient,
    private readonly wsService: WebSocketService,
  ) {
    // Subscribe to federation status updates
    this.subscriptions.add(
      this.wsService.federationStatus$.subscribe(event => {
        this.peers.set(event.peers);
      }),
    );

    // Subscribe to presence updates — merge into existing entries
    this.subscriptions.add(
      this.wsService.presenceUpdates$.subscribe(event => {
        const existing = new Map(
          this.presenceEntries().map(e => [e.userId, e]),
        );
        for (const user of event.users) {
          existing.set(user.userId, user);
        }
        this.presenceEntries.set(Array.from(existing.values()));
      }),
    );

    // Periodically expire stale presence entries
    this.presenceCleanupTimer = setInterval(() => {
      const now = Date.now();
      const active = this.presenceEntries().filter(
        e => now - e.timestamp < FederationService.PRESENCE_EXPIRY_MS,
      );
      if (active.length !== this.presenceEntries().length) {
        this.presenceEntries.set(active);
      }
    }, 1_000);
  }

  ngOnDestroy(): void {
    this.subscriptions.unsubscribe();
    if (this.presenceCleanupTimer) {
      clearInterval(this.presenceCleanupTimer);
    }
  }

  /** Get peer color by instance ID. */
  getPeerColor(instanceId: string): string | null {
    return this.peers().find(p => p.instanceId === instanceId)?.color ?? null;
  }
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `npx nx test web --testFile=src/app/core/services/federation.service.spec.ts`
Expected: PASS (4 tests)

- [ ] **Step 5: Commit**

```bash
git add apps/web/src/app/core/services/federation.service.ts \
        apps/web/src/app/core/services/federation.service.spec.ts
git commit -m "feat(federation): add FederationService for peer state and presence management"
```

---

## Chunk 2: Federation Overlay Service and Map Integration

### Task 4: Create FederationOverlayService

The overlay service manages all CesiumJS primitives for federation visuals: colored rings around federated entities, source badges on labels, presence markers with viewport cones, and the source legend data. It operates outside Angular zone for performance.

**Files:**
- Create: `apps/web/src/app/features/map/federation-overlay.service.ts`
- Create: `apps/web/src/app/features/map/federation-overlay.service.spec.ts`

- [ ] **Step 1: Write the test**

Create `apps/web/src/app/features/map/federation-overlay.service.spec.ts`:

```typescript
import { FederationOverlayService } from './federation-overlay.service';

describe('FederationOverlayService', () => {
  let service: FederationOverlayService;

  beforeEach(() => {
    service = new FederationOverlayService();
  });

  it('should be created', () => {
    expect(service).toBeTruthy();
  });

  describe('isFederatedEntity', () => {
    it('should return true for entities with sourceInstanceId', () => {
      expect(service.isFederatedEntity({ sourceInstanceId: 'peer-1' } as any)).toBe(true);
    });

    it('should return false for local entities', () => {
      expect(service.isFederatedEntity({} as any)).toBe(false);
      expect(service.isFederatedEntity({ sourceInstanceId: undefined } as any)).toBe(false);
    });
  });

  describe('formatFederatedLabel', () => {
    it('should append source badge to entity name', () => {
      const result = service.formatFederatedLabel('HAWK-9', 'BRAVO');
      expect(result).toBe('HAWK-9 [BRAVO]');
    });

    it('should return plain name for local entities', () => {
      const result = service.formatFederatedLabel('UAV-307', undefined);
      expect(result).toBe('UAV-307');
    });
  });

  describe('hexToRgb', () => {
    it('should parse hex color to RGB object', () => {
      const rgb = service.hexToRgb('#f97316');
      expect(rgb).toEqual({ r: 249, g: 115, b: 22 });
    });

    it('should return null for invalid hex', () => {
      expect(service.hexToRgb('invalid')).toBeNull();
    });
  });
});
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `npx nx test web --testFile=src/app/features/map/federation-overlay.service.spec.ts`
Expected: FAIL — module not found

- [ ] **Step 3: Implement FederationOverlayService**

Create `apps/web/src/app/features/map/federation-overlay.service.ts`:

```typescript
import { Injectable } from '@angular/core';
import { Entity } from '../../shared/models/entity.model';
import { PresenceEntry } from '../../core/services/websocket.service';
import {
  BILLBOARD_SCALE_BY_DISTANCE,
  LABEL_SCALE_BY_DISTANCE,
  LABEL_TRANSLUCENCY_BY_DISTANCE,
  svgToDataUrl,
} from './cesium-config';

/** SVG for the federation ring overlay — a simple colored circle outline. */
function ringBillboardSvg(color: string): string {
  return `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 64 64" width="64" height="64">
    <circle cx="32" cy="32" r="28" fill="none" stroke="${color}" stroke-width="3" opacity="0.8"/>
  </svg>`;
}

/** SVG for presence marker dot. */
function presenceDotSvg(color: string): string {
  return `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" width="24" height="24">
    <circle cx="12" cy="12" r="8" fill="${color}" stroke="white" stroke-width="2" opacity="0.9"/>
  </svg>`;
}

interface FederationRingEntry {
  billboard: any; // Cesium Billboard
  entityId: string;
}

interface PresenceMarkerEntry {
  billboard: any;
  label: any;
  userId: string;
}

@Injectable()
export class FederationOverlayService {
  private Cesium: any = null;
  private viewer: any = null;
  private ringBillboardCollection: any = null;
  private presenceBillboardCollection: any = null;
  private presenceLabelCollection: any = null;

  private federationRings = new Map<string, FederationRingEntry>();
  private presenceMarkers = new Map<string, PresenceMarkerEntry>();
  private ringImageCache = new Map<string, string>();
  private presenceDotCache = new Map<string, string>();

  /**
   * Initialize with Cesium module and viewer instance.
   * Called once from MapComponent after Cesium is loaded.
   */
  init(Cesium: any, viewer: any): void {
    this.Cesium = Cesium;
    this.viewer = viewer;

    this.ringBillboardCollection = viewer.scene.primitives.add(
      new Cesium.BillboardCollection({ scene: viewer.scene }),
    );
    this.presenceBillboardCollection = viewer.scene.primitives.add(
      new Cesium.BillboardCollection({ scene: viewer.scene }),
    );
    this.presenceLabelCollection = viewer.scene.primitives.add(
      new Cesium.LabelCollection({ scene: viewer.scene }),
    );
  }

  /** Returns true if the entity is from a federated peer. */
  isFederatedEntity(entity: Entity): boolean {
    return !!entity.sourceInstanceId;
  }

  /**
   * Formats label text with source badge for federated entities.
   * Local entities get plain name.
   */
  formatFederatedLabel(name: string, sourceInstanceName?: string): string {
    if (!sourceInstanceName) return name;
    return `${name} [${sourceInstanceName}]`;
  }

  /**
   * Adds or updates a colored ring around a federated entity billboard.
   * The ring is rendered as a separate billboard slightly larger than the entity icon.
   */
  addOrUpdateRing(entityId: string, position: any, color: string): void {
    if (!this.Cesium || !this.ringBillboardCollection) return;

    const existing = this.federationRings.get(entityId);

    if (existing) {
      existing.billboard.position = position;
      return;
    }

    // Get or create ring image for this color
    let ringImage = this.ringImageCache.get(color);
    if (!ringImage) {
      ringImage = svgToDataUrl(ringBillboardSvg(color));
      this.ringImageCache.set(color, ringImage);
    }

    const billboard = this.ringBillboardCollection.add({
      position,
      image: ringImage,
      scale: 1.2,
      id: `fed-ring-${entityId}`,
      scaleByDistance: new this.Cesium.NearFarScalar(...BILLBOARD_SCALE_BY_DISTANCE),
    });

    this.federationRings.set(entityId, { billboard, entityId });
  }

  /** Remove a federation ring when the entity is removed. */
  removeRing(entityId: string): void {
    const entry = this.federationRings.get(entityId);
    if (entry && this.ringBillboardCollection) {
      this.ringBillboardCollection.remove(entry.billboard);
      this.federationRings.delete(entityId);
    }
  }

  /**
   * Updates presence markers on the map.
   * Called periodically with the current set of remote user positions.
   */
  updatePresenceMarkers(entries: PresenceEntry[]): void {
    if (!this.Cesium || !this.viewer) return;

    const Cesium = this.Cesium;
    const activeIds = new Set(entries.map(e => e.userId));

    // Remove stale markers
    for (const [userId, marker] of this.presenceMarkers) {
      if (!activeIds.has(userId)) {
        this.presenceBillboardCollection.remove(marker.billboard);
        this.presenceLabelCollection.remove(marker.label);
        this.presenceMarkers.delete(userId);
      }
    }

    // Add or update markers
    for (const entry of entries) {
      const position = Cesium.Cartesian3.fromDegrees(
        entry.cameraCenter.lon,
        entry.cameraCenter.lat,
        0,
      );

      const existing = this.presenceMarkers.get(entry.userId);
      if (existing) {
        existing.billboard.position = position;
        existing.label.position = position;
        continue;
      }

      // Get or create presence dot for this color
      let dotImage = this.presenceDotCache.get(entry.color);
      if (!dotImage) {
        dotImage = svgToDataUrl(presenceDotSvg(entry.color));
        this.presenceDotCache.set(entry.color, dotImage);
      }

      const billboard = this.presenceBillboardCollection.add({
        position,
        image: dotImage,
        scale: 1.0,
        id: `presence-${entry.userId}`,
        scaleByDistance: new Cesium.NearFarScalar(...BILLBOARD_SCALE_BY_DISTANCE),
      });

      const label = this.presenceLabelCollection.add({
        position,
        text: `${entry.displayName} (${entry.instanceName})`,
        font: '11px sans-serif',
        fillColor: Cesium.Color.WHITE,
        outlineColor: Cesium.Color.BLACK,
        outlineWidth: 2,
        style: Cesium.LabelStyle.FILL_AND_OUTLINE,
        pixelOffset: new Cesium.Cartesian2(0, -18),
        scaleByDistance: new Cesium.NearFarScalar(...LABEL_SCALE_BY_DISTANCE),
        translucencyByDistance: new Cesium.NearFarScalar(...LABEL_TRANSLUCENCY_BY_DISTANCE),
      });

      this.presenceMarkers.set(entry.userId, { billboard, label, userId: entry.userId });
    }
  }

  /** Parse hex color to RGB components. */
  hexToRgb(hex: string): { r: number; g: number; b: number } | null {
    const result = /^#?([a-f\d]{2})([a-f\d]{2})([a-f\d]{2})$/i.exec(hex);
    if (!result) return null;
    return {
      r: parseInt(result[1], 16),
      g: parseInt(result[2], 16),
      b: parseInt(result[3], 16),
    };
  }

  /** Clean up all primitives. */
  destroy(): void {
    if (this.viewer && !this.viewer.isDestroyed()) {
      if (this.ringBillboardCollection) {
        this.viewer.scene.primitives.remove(this.ringBillboardCollection);
      }
      if (this.presenceBillboardCollection) {
        this.viewer.scene.primitives.remove(this.presenceBillboardCollection);
      }
      if (this.presenceLabelCollection) {
        this.viewer.scene.primitives.remove(this.presenceLabelCollection);
      }
    }
    this.federationRings.clear();
    this.presenceMarkers.clear();
  }
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `npx nx test web --testFile=src/app/features/map/federation-overlay.service.spec.ts`
Expected: PASS (5 tests)

- [ ] **Step 5: Commit**

```bash
git add apps/web/src/app/features/map/federation-overlay.service.ts \
        apps/web/src/app/features/map/federation-overlay.service.spec.ts
git commit -m "feat(federation): add FederationOverlayService for CesiumJS federation rendering"
```

---

### Task 5: Create FederationStatusComponent

Standalone overlay component showing federation peer status and source legend. Positioned in the map via absolute positioning.

**Files:**
- Create: `apps/web/src/app/features/map/federation-status.component.ts`
- Create: `apps/web/src/app/features/map/federation-status.component.spec.ts`

- [ ] **Step 1: Write the test**

Create `apps/web/src/app/features/map/federation-status.component.spec.ts`:

```typescript
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { FederationStatusComponent } from './federation-status.component';
import { FederationService } from '../../core/services/federation.service';
import { signal } from '@angular/core';

describe('FederationStatusComponent', () => {
  let component: FederationStatusComponent;
  let fixture: ComponentFixture<FederationStatusComponent>;

  const mockFederationService = {
    peers: signal([
      {
        instanceId: 'peer-1',
        displayName: 'BRAVO',
        status: 'connected' as const,
        color: '#f97316',
        entityCount: 47,
        userCount: 2,
      },
      {
        instanceId: 'peer-2',
        displayName: 'CHARLIE',
        status: 'stale' as const,
        color: '#a855f7',
        entityCount: 23,
        userCount: 1,
      },
    ]),
    federationActive: signal(true),
    totalFederatedEntities: signal(70),
  };

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [FederationStatusComponent],
      providers: [
        { provide: FederationService, useValue: mockFederationService },
      ],
    }).compileComponents();

    fixture = TestBed.createComponent(FederationStatusComponent);
    component = fixture.componentInstance;
    fixture.detectChanges();
  });

  it('should create', () => {
    expect(component).toBeTruthy();
  });

  it('should display peer count', () => {
    const el: HTMLElement = fixture.nativeElement;
    expect(el.textContent).toContain('BRAVO');
    expect(el.textContent).toContain('CHARLIE');
  });

  it('should show status dots with correct colors', () => {
    const dots = fixture.nativeElement.querySelectorAll('.status-dot');
    expect(dots.length).toBe(2);
  });
});
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `npx nx test web --testFile=src/app/features/map/federation-status.component.spec.ts`
Expected: FAIL — module not found

- [ ] **Step 3: Implement FederationStatusComponent**

Create `apps/web/src/app/features/map/federation-status.component.ts`:

```typescript
import { Component, ChangeDetectionStrategy } from '@angular/core';
import { FederationService } from '../../core/services/federation.service';

@Component({
  selector: 'app-federation-status',
  standalone: true,
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    @if (federationService.federationActive()) {
      <div class="federation-badge">
        <div class="badge-header">FEDERATION</div>
        <div class="peer-list">
          @for (peer of federationService.peers(); track peer.instanceId) {
            <div class="peer-row">
              <span class="status-dot" [class]="'status-' + peer.status"></span>
              <span class="peer-name">{{ peer.displayName }}</span>
              <span class="peer-stats">{{ peer.userCount }}u / {{ peer.entityCount }}e</span>
            </div>
          }
        </div>
      </div>

      <div class="source-legend">
        <div class="legend-header">SOURCES</div>
        <div class="legend-items">
          <div class="legend-item">
            <span class="legend-dot" style="background: #3b82f6"></span>
            <span class="legend-name">Local</span>
          </div>
          @for (peer of federationService.peers(); track peer.instanceId) {
            <div class="legend-item">
              <span class="legend-dot" [style.background]="peer.color"></span>
              <span class="legend-name">{{ peer.displayName }}</span>
            </div>
          }
        </div>
      </div>
    }
  `,
  styles: [`
    :host {
      pointer-events: none;
    }

    .federation-badge {
      position: absolute;
      top: 12px;
      right: 12px;
      background: rgba(0, 0, 0, 0.85);
      border: 1px solid #333;
      border-radius: 8px;
      padding: 8px 12px;
      font-size: 11px;
      pointer-events: auto;
      z-index: 10;
    }

    .badge-header {
      color: #888;
      font-weight: bold;
      margin-bottom: 4px;
      text-transform: uppercase;
      letter-spacing: 1px;
      font-size: 10px;
    }

    .peer-list {
      display: flex;
      flex-direction: column;
      gap: 3px;
    }

    .peer-row {
      display: flex;
      align-items: center;
      gap: 6px;
    }

    .status-dot {
      width: 6px;
      height: 6px;
      border-radius: 50%;
      flex-shrink: 0;
    }

    .status-connected {
      background: #22c55e;
      box-shadow: 0 0 4px #22c55e;
    }

    .status-stale {
      background: #eab308;
      box-shadow: 0 0 4px #eab308;
    }

    .status-disconnected {
      background: #ef4444;
      box-shadow: 0 0 4px #ef4444;
    }

    .peer-name {
      color: #ccc;
    }

    .peer-stats {
      color: #888;
      font-size: 10px;
      margin-left: auto;
    }

    .source-legend {
      position: absolute;
      bottom: 12px;
      left: 12px;
      background: rgba(0, 0, 0, 0.85);
      border: 1px solid #333;
      border-radius: 8px;
      padding: 10px 14px;
      font-size: 11px;
      pointer-events: auto;
      z-index: 10;
    }

    .legend-header {
      color: #888;
      font-weight: bold;
      margin-bottom: 6px;
      text-transform: uppercase;
      letter-spacing: 1px;
      font-size: 10px;
    }

    .legend-items {
      display: flex;
      gap: 12px;
      align-items: center;
    }

    .legend-item {
      display: flex;
      align-items: center;
      gap: 5px;
    }

    .legend-dot {
      width: 8px;
      height: 8px;
      border-radius: 2px;
      flex-shrink: 0;
    }

    .legend-name {
      color: #ccc;
    }
  `],
})
export class FederationStatusComponent {
  constructor(readonly federationService: FederationService) {}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `npx nx test web --testFile=src/app/features/map/federation-status.component.spec.ts`
Expected: PASS (3 tests)

- [ ] **Step 5: Commit**

```bash
git add apps/web/src/app/features/map/federation-status.component.ts \
        apps/web/src/app/features/map/federation-status.component.spec.ts
git commit -m "feat(federation): add FederationStatusComponent with peer status badge and source legend"
```

---

### Task 6: Integrate Federation into MapComponent

Wire the `FederationOverlayService` and `FederationStatusComponent` into the existing map component. The map component delegates all federation rendering to the overlay service.

**Files:**
- Modify: `apps/web/src/app/features/map/map.component.ts`
- Modify: `apps/web/src/app/features/map/map.component.html`

- [ ] **Step 1: Add imports and providers to map.component.ts**

Add to the imports at the top of the file:

```typescript
import { FederationOverlayService } from './federation-overlay.service';
import { FederationStatusComponent } from './federation-status.component';
import { FederationService } from '../../core/services/federation.service';
```

Add `FederationStatusComponent` to the component's `imports` array:

```typescript
  imports: [CommonModule, FormsModule, EntityDetailPanelComponent, FederationStatusComponent],
```

The component decorator does not have a `providers` array yet — add one:

```typescript
@Component({
  selector: 'app-map',
  standalone: true,
  changeDetection: ChangeDetectionStrategy.OnPush,
  imports: [CommonModule, FormsModule, EntityDetailPanelComponent, FederationStatusComponent],
  templateUrl: './map.component.html',
  styleUrls: ['./map.component.scss'],
  providers: [FederationOverlayService],
})
```

Add constructor parameters — `federationService` must be `public readonly` (not `private`) because the template accesses it directly:

```typescript
    private readonly federationOverlay: FederationOverlayService,
    public readonly federationService: FederationService,
```

- [ ] **Step 2: Initialize federation overlay after Cesium init**

In `ngAfterViewInit`, after `this.initUserLocation()`, add:

```typescript
    this.federationOverlay.init(this.Cesium, this.viewer);
    this.subscribeToFederation();
```

- [ ] **Step 3: Add federation subscription method**

Add a new private method to the class. This subscribes to the raw WebSocket presence stream (buffered at 500ms) and then reads the processed presence entries from `FederationService` to update the overlay:

```typescript
  private subscribeToFederation(): void {
    const presenceSub = this.wsService.presenceUpdates$.pipe(
      bufferTime(500),
      filter((batches) => batches.length > 0),
    ).subscribe(() => {
      this.ngZone.runOutsideAngular(() => {
        this.federationOverlay.updatePresenceMarkers(
          this.federationService.presenceEntries(),
        );
        this.scheduleRender();
      });
    });
    this.subscriptions.add(presenceSub);
  }
```

- [ ] **Step 4: Modify addOrUpdateCesiumEntity to handle federation rings and labels**

Find the existing `addOrUpdateCesiumEntity` method. It has two branches: an `if (existing)` update path (line ~516) and an `else` create path (line ~544). Both branches need federation changes.

**In the update branch** (inside `if (existing)`), after `existing.label.text = entity.name;` (line ~522), change the label text and add ring update:

```typescript
      existing.label.text = this.federationOverlay.formatFederatedLabel(entity.name, entity.sourceInstanceName);

      // Update federation ring position
      if (entity.sourceInstanceId) {
        const color = this.federationService.getPeerColor(entity.sourceInstanceId);
        if (color) {
          this.federationOverlay.addOrUpdateRing(entity.id, position, color);
        }
      }
```

**In the create branch** (inside `else`), change the label `text` property (line ~562) from `entity.name` to:

```typescript
        text: this.federationOverlay.formatFederatedLabel(entity.name, entity.sourceInstanceName),
```

And after the `this.entityMap.set(...)` call at the end of the create branch, add:

```typescript
      // Federation ring — add colored ring for federated entities
      if (entity.sourceInstanceId) {
        const color = this.federationService.getPeerColor(entity.sourceInstanceId);
        if (color) {
          this.federationOverlay.addOrUpdateRing(entity.id, billboard.position, color);
        }
      }
```

- [ ] **Step 5: Modify removeCesiumEntity to clean up rings**

In the existing `removeCesiumEntity` method, add ring cleanup:

```typescript
    this.federationOverlay.removeRing(entityId);
```

- [ ] **Step 6: Clean up overlay on destroy**

In `ngOnDestroy`, before `this.viewer.destroy()`, add:

```typescript
    this.federationOverlay.destroy();
```

- [ ] **Step 7: Add federation status component to template**

In `apps/web/src/app/features/map/map.component.html`, add after the entity detail panel:

```html
  <!-- Federation Status Overlay -->
  <app-federation-status />
```

- [ ] **Step 8: Add peer layer toggles to layer panel**

In the layer panel section of `map.component.html`, after the existing entity type layers, add a federation section. This requires also adding `federationLayers` signal to the component.

The `federationService` is already injected as `public readonly` via the constructor (Step 1). In the template's layer panel, after the `@for (layer of layers...)` block:

```html
          @if (federationService.federationActive()) {
            <div class="layer-divider"></div>
            <div class="layer-section-label">Federation</div>
            @for (peer of federationService.peers(); track peer.instanceId) {
              <label class="layer-item">
                <input type="checkbox" checked (change)="toggleFederationPeer(peer.instanceId, $event)" />
                <span class="layer-color" [style.background]="peer.color"></span>
                <span class="layer-name">{{ peer.displayName }}</span>
              </label>
            }
          }
```

Add a placeholder toggle method to the component:

```typescript
  toggleFederationPeer(instanceId: string, event: Event): void {
    // TODO: Filter entities by peer visibility in Plan 3
    const checked = (event.target as HTMLInputElement).checked;
    console.log(`Federation peer ${instanceId} visibility: ${checked}`);
  }
```

- [ ] **Step 9: Verify build and tests**

Run: `npx nx build web --skip-nx-cache`
Expected: BUILD SUCCESSFUL

Run: `npx nx test web`
Expected: ALL PASS

- [ ] **Step 10: Commit**

```bash
git add apps/web/src/app/features/map/map.component.ts \
        apps/web/src/app/features/map/map.component.html
git commit -m "feat(federation): integrate federation overlay and status into map component"
```

---

### Task 7: Add layer-divider and section-label styles

**Files:**
- Modify: `apps/web/src/app/features/map/map.component.scss`

- [ ] **Step 1: Add federation layer styles**

Add to the existing SCSS file, in the layer panel styles section:

```scss
.layer-divider {
  border-top: 1px solid rgba(255, 255, 255, 0.1);
  margin: 8px 0;
}

.layer-section-label {
  font-size: 10px;
  text-transform: uppercase;
  letter-spacing: 1px;
  color: #888;
  margin-bottom: 4px;
}
```

- [ ] **Step 2: Commit**

```bash
git add apps/web/src/app/features/map/map.component.scss
git commit -m "feat(federation): add federation layer panel styles"
```
