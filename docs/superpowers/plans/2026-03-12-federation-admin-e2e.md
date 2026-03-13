# Federation Admin UI & End-to-End Wiring — Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Complete the federation feature by wiring inbound federated data through the Entity Gateway to browser clients, adding admin UI for federation configuration in the Settings page, implementing peer visibility filtering on the map, and adding HTTP methods to the frontend FederationService.

**Architecture:** A new `FederationBridgeService` listens for EventEmitter events from `PeerManagerService` (entity batches, presence updates, peer connect/disconnect) and forwards them to browser clients via the Entity Gateway's Socket.IO namespace. The Settings component gains a Federation tab for admin-only management of federation config, seed peers, and sharing policies. The map component's peer toggle stub gets a real implementation.

**Tech Stack:** NestJS (EventEmitter, Socket.IO), Angular 19 (signals, standalone components, HttpClient), TypeScript

**Spec:** `docs/superpowers/specs/2026-03-12-federation-design.md`

---

## Chunk 1: Backend Bridge and Entity Gateway Wiring

### Task 1: Extend PeerManagerService.getConnectedPeers to include color

The bridge service needs peer colors to enrich status and presence events. The current `getConnectedPeers()` return type only includes `instanceId`, `displayName`, and `ceiling`. We need to add `color`.

**Files:**
- Modify: `apps/api-gateway/src/modules/federation/peer-manager.service.ts`

- [ ] **Step 1: Add color to PeerConnection interface**

The `PeerConnection` interface (line ~26) does not store `color`. Add it:

```typescript
interface PeerConnection {
  ws: WebSocket;
  instanceId: string;
  displayName: string;
  classificationLevel: string;
  connectionCeiling: string;
  state: PeerConnectionState;
  lastHeartbeat: number;
  reconnectAttempts: number;
  reconnectTimer?: ReturnType<typeof setTimeout>;
  color: string;
}
```

- [ ] **Step 2: Set color when creating PeerConnection**

In `handleHandshakeResponse` (outbound connections, line ~216), add `color` to the connection object:

```typescript
    const conn: PeerConnection = {
      ws,
      instanceId: payload.instanceId,
      displayName: payload.displayName,
      classificationLevel: payload.classificationLevel,
      connectionCeiling: ceiling,
      state: 'connected',
      lastHeartbeat: Date.now(),
      reconnectAttempts: 0,
      color: this.assignPeerColor(payload.instanceId),
    };
```

Similarly in `registerIncomingPeer` (line ~323), add `color` to the connection object:

```typescript
    const conn: PeerConnection = {
      ws,
      instanceId: payload.instanceId,
      displayName: payload.displayName,
      classificationLevel: payload.classificationLevel,
      connectionCeiling: ceiling,
      state: 'connected',
      lastHeartbeat: Date.now(),
      reconnectAttempts: 0,
      color: this.assignPeerColor(payload.instanceId),
    };
```

- [ ] **Step 3: Extend getConnectedPeers return type**

Change `getConnectedPeers` (line ~380) to include `color`:

```typescript
  getConnectedPeers(): Array<{ instanceId: string; displayName: string; ceiling: string; color: string }> {
    const result: Array<{ instanceId: string; displayName: string; ceiling: string; color: string }> = [];
    for (const [, conn] of this.connections) {
      if (conn.state === 'connected') {
        result.push({
          instanceId: conn.instanceId,
          displayName: conn.displayName,
          ceiling: conn.connectionCeiling,
          color: conn.color,
        });
      }
    }
    return result;
  }
```

- [ ] **Step 4: Add test for color in getConnectedPeers**

In `apps/api-gateway/src/modules/federation/peer-manager.service.spec.ts`, add a test that verifies the `color` field is included when a peer is connected. Find the `getConnectedPeers` describe block and add:

```typescript
    it('should include color in connected peer data', async () => {
      // Register an incoming peer so the connection map is populated
      const mockWs = createMockWebSocket();
      await service.registerIncomingPeer(mockWs as any, {
        type: FederationMessageType.HANDSHAKE,
        sourceInstanceId: 'peer-color-test',
        classificationLevel: 'classification-u',
        payload: { instanceId: 'peer-color-test', displayName: 'ColorTest', classificationLevel: 'classification-u', capabilities: ['entity-sharing'] },
      } as any, 'ws://color-test:3100');

      const peers = service.getConnectedPeers();
      expect(peers.length).toBe(1);
      expect(peers[0].color).toBeDefined();
      expect(peers[0].color).toMatch(/^#[0-9a-f]{6}$/i);
    });
```

- [ ] **Step 5: Verify tests pass**

Run: `npx nx test api-gateway --skip-nx-cache`
Expected: ALL PASS

- [ ] **Step 6: Commit**

```bash
git add apps/api-gateway/src/modules/federation/peer-manager.service.ts \
        apps/api-gateway/src/modules/federation/peer-manager.service.spec.ts
git commit -m "feat(federation): include color in PeerConnection and getConnectedPeers"
```

---

### Task 2: Create FederationBridgeService

The bridge service listens for EventEmitter events from PeerManagerService and forwards federated entity batches, presence updates, and peer status changes to browser clients via the Entity Gateway's Socket.IO server.

**Files:**
- Create: `apps/api-gateway/src/modules/gateway/federation-bridge.service.ts`
- Create: `apps/api-gateway/src/modules/gateway/federation-bridge.service.spec.ts`

- [ ] **Step 1: Write the test**

Create `apps/api-gateway/src/modules/gateway/federation-bridge.service.spec.ts`:

```typescript
import { Test, TestingModule } from '@nestjs/testing';
import { EventEmitterModule, EventEmitter2 } from '@nestjs/event-emitter';
import { FederationBridgeService } from './federation-bridge.service';
import { EntityGateway } from './entity.gateway';
import { PeerManagerService } from '../federation/peer-manager.service';

describe('FederationBridgeService', () => {
  let service: FederationBridgeService;
  let eventEmitter: EventEmitter2;
  let mockEntityGateway: Partial<EntityGateway>;

  beforeEach(async () => {
    mockEntityGateway = {
      broadcastEntityBatch: jest.fn().mockResolvedValue(undefined),
      server: {
        emit: jest.fn(),
      } as any,
    };

    const mockPeerManager = {
      getConnectedPeers: jest.fn().mockReturnValue([
        { instanceId: 'peer-1', displayName: 'Bravo', ceiling: 'classification-u', color: '#f97316' },
      ]),
    };

    // Must use EventEmitterModule.forRoot() so @OnEvent decorators are registered.
    // A bare EventEmitter2 provider would skip decorator wiring and tests would silently pass.
    const module: TestingModule = await Test.createTestingModule({
      imports: [EventEmitterModule.forRoot()],
      providers: [
        FederationBridgeService,
        { provide: EntityGateway, useValue: mockEntityGateway },
        { provide: PeerManagerService, useValue: mockPeerManager },
      ],
    }).compile();

    service = module.get(FederationBridgeService);
    eventEmitter = module.get(EventEmitter2);
  });

  it('should be defined', () => {
    expect(service).toBeDefined();
  });

  it('should forward entity batch to gateway on federation event', async () => {
    const message = {
      type: 'fed:entity:batch',
      sourceInstanceId: 'peer-1',
      classificationLevel: 'classification-u',
      payload: {
        entities: [{
          entityId: 'ent-1',
          entityType: 'AIRCRAFT',
          latitude: 34.05,
          longitude: -118.25,
          classification: 'UNCLASSIFIED',
          source: 'peer-radar',
          timestamp: new Date().toISOString(),
          sourceInstanceId: 'peer-1',
          sourceInstanceName: 'Bravo',
        }],
      },
    };

    await eventEmitter.emitAsync('federation.fed:entity:batch', message);

    expect(mockEntityGateway.broadcastEntityBatch).toHaveBeenCalledWith(
      expect.arrayContaining([
        expect.objectContaining({
          entity: expect.objectContaining({
            entityId: 'ent-1',
            sourceInstanceId: 'peer-1',
            sourceInstanceName: 'Bravo',
          }),
          eventType: 'updated',
        }),
      ]),
    );
  });

  it('should forward presence to browser clients with peer metadata', async () => {
    const message = {
      type: 'fed:presence:update',
      sourceInstanceId: 'peer-1',
      classificationLevel: 'classification-u',
      payload: {
        users: [{
          userId: 'u1',
          displayName: 'j.smith',
          cameraCenter: { lat: 34.05, lon: -118.25 },
          zoom: 8,
          timestamp: Date.now(),
        }],
      },
    };

    await eventEmitter.emitAsync('federation.fed:presence:update', message);

    expect(mockEntityGateway.server!.emit).toHaveBeenCalledWith(
      'federation:presence',
      expect.objectContaining({
        users: expect.arrayContaining([
          expect.objectContaining({
            userId: 'u1',
            instanceId: 'peer-1',
            instanceName: 'Bravo',
            color: '#f97316',
          }),
        ]),
      }),
    );
  });

  it('should broadcast federation status on peer connect', async () => {
    await eventEmitter.emitAsync('federation.peer.connected', {
      instanceId: 'peer-1',
      displayName: 'Bravo',
      ceiling: 'classification-u',
    });

    expect(mockEntityGateway.server!.emit).toHaveBeenCalledWith(
      'federation:status',
      expect.objectContaining({
        peers: expect.arrayContaining([
          expect.objectContaining({
            instanceId: 'peer-1',
            displayName: 'Bravo',
            color: '#f97316',
          }),
        ]),
      }),
    );
  });

  it('should broadcast federation status on peer disconnect', async () => {
    await eventEmitter.emitAsync('federation.peer.disconnected', {
      instanceId: 'peer-1',
    });

    expect(mockEntityGateway.server!.emit).toHaveBeenCalledWith(
      'federation:status',
      expect.objectContaining({ peers: expect.any(Array) }),
    );
  });
});
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `npx nx test api-gateway --testFile=src/modules/gateway/federation-bridge.service.spec.ts`
Expected: FAIL — module not found

- [ ] **Step 3: Implement FederationBridgeService**

Create `apps/api-gateway/src/modules/gateway/federation-bridge.service.ts`:

```typescript
import { Injectable, Logger } from '@nestjs/common';
import { OnEvent } from '@nestjs/event-emitter';
import { EntityGateway, EntityPositionUpdate } from './entity.gateway';
import { PeerManagerService } from '../federation/peer-manager.service';
import {
  FederationMessage,
  EntityBatchPayload,
  PresenceUpdatePayload,
  FederatedEntity,
} from '../federation/federation.types';

@Injectable()
export class FederationBridgeService {
  private readonly logger = new Logger(FederationBridgeService.name);

  constructor(
    private readonly entityGateway: EntityGateway,
    private readonly peerManager: PeerManagerService,
  ) {}

  @OnEvent('federation.fed:entity:batch')
  async handleEntityBatch(message: FederationMessage): Promise<void> {
    const payload = message.payload as EntityBatchPayload;
    if (!payload.entities || payload.entities.length === 0) return;

    const updates = payload.entities.map((e: FederatedEntity) => ({
      entity: {
        entityId: e.entityId,
        entityType: e.entityType,
        latitude: e.latitude,
        longitude: e.longitude,
        altitude: e.altitude,
        heading: e.heading,
        speed: e.speed,
        classification: e.classification,
        source: e.source,
        timestamp: e.timestamp,
        metadata: e.metadata,
        sourceInstanceId: e.sourceInstanceId,
        sourceInstanceName: e.sourceInstanceName,
      } as EntityPositionUpdate,
      eventType: 'updated' as const,
    }));

    await this.entityGateway.broadcastEntityBatch(updates);
    this.logger.verbose(`Bridged ${payload.entities.length} federated entities from ${message.sourceInstanceId}`);
  }

  @OnEvent('federation.fed:presence:update')
  handlePresenceUpdate(message: FederationMessage): void {
    const payload = message.payload as PresenceUpdatePayload;
    if (!payload.users || payload.users.length === 0) return;

    const peer = this.peerManager.getConnectedPeers().find(p => p.instanceId === message.sourceInstanceId);

    const enrichedUsers = payload.users.map(u => ({
      ...u,
      instanceId: message.sourceInstanceId,
      instanceName: peer?.displayName ?? message.sourceInstanceId,
      color: peer?.color ?? '#888888',
    }));

    this.entityGateway.server.emit('federation:presence', { users: enrichedUsers });
  }

  @OnEvent('federation.peer.connected')
  handlePeerConnected(): void {
    this.broadcastFederationStatus();
  }

  @OnEvent('federation.peer.disconnected')
  handlePeerDisconnected(): void {
    this.broadcastFederationStatus();
  }

  private broadcastFederationStatus(): void {
    const connectedPeers = this.peerManager.getConnectedPeers();
    const peers = connectedPeers.map(p => ({
      instanceId: p.instanceId,
      displayName: p.displayName,
      status: 'connected' as const,
      color: p.color,
      entityCount: 0, // TODO: track per-peer entity counts in a future iteration
      userCount: 0,   // TODO: track per-peer user counts in a future iteration
    }));

    this.entityGateway.server.emit('federation:status', { peers });
  }
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `npx nx test api-gateway --testFile=src/modules/gateway/federation-bridge.service.spec.ts`
Expected: PASS (5 tests)

- [ ] **Step 5: Commit**

```bash
git add apps/api-gateway/src/modules/gateway/federation-bridge.service.ts \
        apps/api-gateway/src/modules/gateway/federation-bridge.service.spec.ts
git commit -m "feat(federation): add FederationBridgeService to forward federated data to browser clients"
```

---

### Task 3: Wire FederationBridgeService into EntityGatewayModule and extend EntityPositionUpdate

Register the bridge service, add the FederationModule import, and extend the EntityPositionUpdate interface to carry federation metadata through to browsers.

**Files:**
- Modify: `apps/api-gateway/src/modules/gateway/entity-gateway.module.ts`
- Modify: `apps/api-gateway/src/modules/gateway/entity.gateway.ts`

- [ ] **Step 1: Update entity-gateway.module.ts**

Add imports at top:
```typescript
import { FederationBridgeService } from './federation-bridge.service';
import { FederationModule } from '../federation/federation.module';
```

Add `FederationModule` to the `imports` array (after `ConfigModule`). Note: `EventEmitterModule` is already registered globally in `AppModule`, so it does NOT need to be imported here. TypeORM entities from `FederationModule` are available via `autoLoadEntities: true` in `AppModule`.

Add `FederationBridgeService` to the `providers` array.

- [ ] **Step 2: Add federation fields to EntityPositionUpdate**

In `entity.gateway.ts`, add optional federation fields to the `EntityPositionUpdate` interface (after `circularError`):

```typescript
  sourceInstanceId?: string;
  sourceInstanceName?: string;
```

- [ ] **Step 3: Pass federation fields through broadcastEntityBatch**

In `broadcastEntityBatch`, update the entity event mapping (inside the `events` array map, line ~276) to include:

```typescript
            sourceInstanceId: entity.sourceInstanceId,
            sourceInstanceName: entity.sourceInstanceName,
```

Add these to the `entity` object within the event, after `circularError`.

Also add `sourceInstanceId` and `sourceInstanceName` to the `EntityEvent` interface's inline `entity` property type (the anonymous object type starting at line ~43, after `circularError?: number;` at line ~63):

```typescript
    sourceInstanceId?: string;
    sourceInstanceName?: string;
```

Note: `EntityEvent` has an inline anonymous object type for its `entity` property — this is separate from the `EntityPositionUpdate` interface. Both need the new fields.

- [ ] **Step 4: Verify all backend tests pass**

Run: `npx nx test api-gateway --skip-nx-cache`
Expected: ALL PASS

- [ ] **Step 5: Commit**

```bash
git add apps/api-gateway/src/modules/gateway/entity-gateway.module.ts \
        apps/api-gateway/src/modules/gateway/entity.gateway.ts
git commit -m "feat(federation): wire bridge into gateway module and extend EntityPositionUpdate"
```

---

## Chunk 2: Frontend Admin UI and Peer Visibility

### Task 4: Add HTTP Methods to FederationService

Add `HttpClient` and REST API methods to `FederationService` for admin config, peer management, and policy CRUD. The Settings Federation tab will consume these.

**Files:**
- Modify: `apps/web/src/app/core/services/federation.service.ts`
- Modify: `apps/web/src/app/core/services/federation.service.spec.ts`

- [ ] **Step 1: Add HttpClient import and injection**

In `federation.service.ts`, add to the imports (alongside `Injectable`, etc.):

```typescript
import { inject } from '@angular/core';
import { HttpClient } from '@angular/common/http';
```

Add `HttpClient` as a class field using `inject()` (matching the pattern used in `SettingsComponent`):

```typescript
  private readonly http = inject(HttpClient);
```

Do NOT add it as a constructor parameter — the existing codebase uses the functional `inject()` pattern.

- [ ] **Step 2: Add HTTP methods**

Add these methods to the class (returning Observables, matching the pattern used in Settings):

```typescript
  getConfig() {
    return this.http.get<any>('/api/v1/federation/config');
  }

  updateConfig(body: { displayName?: string; federationEnabled?: boolean }) {
    return this.http.put<any>('/api/v1/federation/config', body);
  }

  getPeers() {
    return this.http.get<any[]>('/api/v1/federation/peers');
  }

  addPeer(url: string, displayName: string) {
    return this.http.post<any>('/api/v1/federation/peers', { url, displayName });
  }

  removePeer(instanceId: string) {
    return this.http.delete(`/api/v1/federation/peers/${instanceId}`);
  }

  getPolicy(peerInstanceId: string) {
    return this.http.get<any>(`/api/v1/federation/policies/${peerInstanceId}`);
  }

  updatePolicy(peerInstanceId: string, body: { entityTypesAllowed?: string[]; geoBounds?: any; enabled?: boolean }) {
    return this.http.put<any>(`/api/v1/federation/policies/${peerInstanceId}`, body);
  }
```

- [ ] **Step 3: Update test file**

In `federation.service.spec.ts`:

Add imports:
```typescript
import { provideHttpClient } from '@angular/common/http';
import { provideHttpClientTesting, HttpTestingController } from '@angular/common/http/testing';
```

Add `httpMock` variable:
```typescript
  let httpMock: HttpTestingController;
```

Add to `beforeEach` providers:
```typescript
        provideHttpClient(),
        provideHttpClientTesting(),
```

Add after `service = TestBed.inject(FederationService)`:
```typescript
    httpMock = TestBed.inject(HttpTestingController);
```

Add `afterEach`:
```typescript
  afterEach(() => {
    httpMock.verify();
  });
```

Also update the fake-timers test block (the `'should expire presence entries after 5s'` test) — its inner `TestBed.configureTestingModule` call also needs `provideHttpClient()` and `provideHttpClientTesting()` in its providers array, otherwise the reconstructed `FederationService` will fail to inject `HttpClient`:

```typescript
    TestBed.configureTestingModule({
      providers: [
        FederationService,
        { provide: WebSocketService, useValue: mockWsService },
        provideHttpClient(),
        provideHttpClientTesting(),
      ],
    });
```

Add a test:
```typescript
  it('should call federation config endpoint', () => {
    service.getConfig().subscribe();
    const req = httpMock.expectOne('/api/v1/federation/config');
    expect(req.request.method).toBe('GET');
    req.flush({ instanceId: 'test', federationEnabled: true });
  });
```

- [ ] **Step 4: Run tests**

Run: `npx nx test web --testFile=src/app/core/services/federation.service.spec.ts`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add apps/web/src/app/core/services/federation.service.ts \
        apps/web/src/app/core/services/federation.service.spec.ts
git commit -m "feat(federation): add HTTP methods to FederationService for admin config"
```

---

### Task 5: Add Federation Tab to Settings Component

Add a `'federation'` tab to the Settings page, visible only to admins. The tab shows federation config (enable/disable, display name), and seed peer management. Uses `FederationService` HTTP methods (from Task 4).

**Files:**
- Modify: `apps/web/src/app/features/settings/settings.component.ts`

- [ ] **Step 1: Import FederationService and expand activeTab type**

Add import:
```typescript
import { FederationService } from '../../core/services/federation.service';
```

Change `activeTab` signal type (line ~484) from:
```typescript
  activeTab = signal<'profile' | 'management'>('profile');
```
to:
```typescript
  activeTab = signal<'profile' | 'management' | 'federation'>('profile');
```

Add `FederationService` as a class field using `inject()` (matching existing pattern in `SettingsComponent`):
```typescript
  private readonly federationService = inject(FederationService);
```

- [ ] **Step 2: Add federation state signals**

Add these signals to the component class:

```typescript
  // Federation state
  federationConfig = signal<any>(null);
  federationPeers = signal<any[]>([]);
  loadingFederation = signal(false);
  federationError = signal('');
  federationSuccess = signal('');
  addPeerUrl = signal('');
  addPeerName = signal('');
```

- [ ] **Step 3: Update switchTab method**

Change the `switchTab` parameter type (line ~516) and add federation case:

```typescript
  switchTab(tab: 'profile' | 'management' | 'federation'): void {
    this.activeTab.set(tab);
    if (tab === 'management' && !this.managementLoaded) {
      this.managementLoaded = true;
      this.loadPendingUsers();
      this.loadActiveUsers();
    }
    if (tab === 'federation') {
      this.loadFederationData();
    }
  }
```

- [ ] **Step 4: Add federation tab button to template**

In the tab-bar section, after the User Management button block (line ~49), add:

```html
        @if (isAdmin()) {
          <button
            class="tab-btn"
            [class.active]="activeTab() === 'federation'"
            (click)="switchTab('federation')"
          >Federation</button>
        }
```

- [ ] **Step 5: Add federation tab content to template**

After the management tab content block (`@if (activeTab() === 'management') { ... }`), add:

```html
      @if (activeTab() === 'federation') {
        <div class="tab-content federation-tab">
          @if (federationError()) {
            <div class="toast error">{{ federationError() }}</div>
          }
          @if (federationSuccess()) {
            <div class="toast success">{{ federationSuccess() }}</div>
          }

          <div class="settings-section">
            <h2>Instance Configuration</h2>
            @if (federationConfig(); as config) {
              <div class="config-form">
                <div class="form-row">
                  <label>Instance ID</label>
                  <span class="monospace">{{ config.instanceId }}</span>
                </div>
                <div class="form-row">
                  <label>Display Name</label>
                  <input
                    type="text"
                    [value]="config.displayName"
                    (change)="updateFederationDisplayName($event)"
                    class="input-field"
                  />
                </div>
                <div class="form-row">
                  <label>Classification</label>
                  <span class="classification-badge">{{ config.classificationLevel | uppercase }}</span>
                </div>
                <div class="form-row">
                  <label>Federation</label>
                  <label class="toggle-label">
                    <input
                      type="checkbox"
                      [checked]="config.federationEnabled"
                      (change)="toggleFederation($event)"
                    />
                    {{ config.federationEnabled ? 'Enabled' : 'Disabled' }}
                  </label>
                </div>
              </div>
            } @else if (loadingFederation()) {
              <div class="loading">Loading federation config...</div>
            }
          </div>

          <div class="settings-section">
            <h2>Seed Peers</h2>
            <div class="add-peer-form">
              <input
                type="text"
                placeholder="ws://hostname:3100"
                [value]="addPeerUrl()"
                (input)="addPeerUrl.set($any($event.target).value)"
                class="input-field"
              />
              <input
                type="text"
                placeholder="Display Name"
                [value]="addPeerName()"
                (input)="addPeerName.set($any($event.target).value)"
                class="input-field input-sm"
              />
              <button class="btn-primary btn-sm" (click)="addSeedPeer()">Add Peer</button>
            </div>

            <div class="peer-table">
              @for (peer of federationPeers(); track peer.instanceId) {
                <div class="peer-row-admin">
                  <span class="peer-status-dot" [class]="'status-' + peer.status"></span>
                  <span class="peer-name">{{ peer.displayName }}</span>
                  <span class="peer-url monospace">{{ peer.url }}</span>
                  <span class="peer-classification">{{ peer.classificationLevel | uppercase }}</span>
                  <button class="btn-danger btn-xs" (click)="removePeer(peer.instanceId)">Remove</button>
                </div>
              } @empty {
                <div class="empty-state">No peers configured</div>
              }
            </div>
          </div>
        </div>
      }
```

- [ ] **Step 6: Add federation methods**

Add these methods to the component class. They use `FederationService` HTTP methods and follow the existing `.subscribe()` pattern from the management tab:

```typescript
  loadFederationData(): void {
    this.loadingFederation.set(true);
    this.federationError.set('');
    this.federationService.getConfig().subscribe({
      next: (config) => this.federationConfig.set(config),
      error: (err) => this.federationError.set(err.error?.message ?? 'Failed to load config'),
    });
    this.federationService.getPeers().subscribe({
      next: (peers) => {
        this.federationPeers.set(peers);
        this.loadingFederation.set(false);
      },
      error: (err) => {
        this.federationError.set(err.error?.message ?? 'Failed to load peers');
        this.loadingFederation.set(false);
      },
    });
  }

  updateFederationDisplayName(event: Event): void {
    const name = (event.target as HTMLInputElement).value;
    this.federationService.updateConfig({ displayName: name }).subscribe({
      next: (config) => {
        this.federationConfig.set(config);
        this.federationSuccess.set('Display name updated');
        setTimeout(() => this.federationSuccess.set(''), 3000);
      },
      error: (err) => this.federationError.set(err.error?.message ?? 'Failed to update'),
    });
  }

  toggleFederation(event: Event): void {
    const enabled = (event.target as HTMLInputElement).checked;
    this.federationService.updateConfig({ federationEnabled: enabled }).subscribe({
      next: (config) => {
        this.federationConfig.set(config);
        this.federationSuccess.set(`Federation ${enabled ? 'enabled' : 'disabled'}`);
        setTimeout(() => this.federationSuccess.set(''), 3000);
      },
      error: (err) => this.federationError.set(err.error?.message ?? 'Failed to toggle'),
    });
  }

  addSeedPeer(): void {
    const url = this.addPeerUrl().trim();
    const name = this.addPeerName().trim();
    if (!url || !name) return;
    this.federationService.addPeer(url, name).subscribe({
      next: () => {
        this.addPeerUrl.set('');
        this.addPeerName.set('');
        this.federationSuccess.set('Peer added');
        setTimeout(() => this.federationSuccess.set(''), 3000);
        this.loadFederationData();
      },
      error: (err) => this.federationError.set(err.error?.message ?? 'Failed to add peer'),
    });
  }

  removePeer(instanceId: string): void {
    this.federationService.removePeer(instanceId).subscribe({
      next: () => {
        this.federationSuccess.set('Peer removed');
        setTimeout(() => this.federationSuccess.set(''), 3000);
        this.loadFederationData();
      },
      error: (err) => this.federationError.set(err.error?.message ?? 'Failed to remove peer'),
    });
  }
```

- [ ] **Step 7: Add federation tab styles**

Add these styles to the component's styles block:

```css
    .federation-tab .settings-section { margin-bottom: 24px; }
    .config-form { display: flex; flex-direction: column; gap: 12px; }
    .form-row { display: flex; align-items: center; gap: 12px; }
    .form-row label { width: 140px; color: #888; font-size: 13px; }
    .input-field {
      background: rgba(255, 255, 255, 0.05);
      border: 1px solid #333;
      border-radius: 4px;
      padding: 6px 10px;
      color: #ccc;
      font-size: 13px;
    }
    .input-sm { width: 160px; }
    .toggle-label { display: flex; align-items: center; gap: 8px; cursor: pointer; color: #ccc; }
    .add-peer-form { display: flex; gap: 8px; margin-bottom: 12px; align-items: center; }
    .peer-table { display: flex; flex-direction: column; gap: 6px; }
    .peer-row-admin {
      display: flex; align-items: center; gap: 10px;
      padding: 8px 12px; background: rgba(255, 255, 255, 0.03); border-radius: 6px;
    }
    .peer-status-dot { width: 8px; height: 8px; border-radius: 50%; flex-shrink: 0; }
    .status-connected { background: #22c55e; }
    .status-stale { background: #eab308; }
    .status-disconnected { background: #ef4444; }
    .peer-name { color: #ccc; font-weight: 500; }
    .peer-url { color: #666; font-size: 11px; flex: 1; }
    .peer-classification { color: #888; font-size: 11px; text-transform: uppercase; }
    .btn-primary {
      background: #3b82f6; color: white; border: none;
      border-radius: 4px; padding: 6px 14px; cursor: pointer; font-size: 12px;
    }
    .btn-primary:hover { background: #2563eb; }
    .btn-danger {
      background: transparent; color: #ef4444; border: 1px solid #ef4444;
      border-radius: 4px; padding: 3px 10px; cursor: pointer; font-size: 11px;
    }
    .btn-danger:hover { background: rgba(239, 68, 68, 0.1); }
    .btn-xs { padding: 2px 8px; font-size: 10px; }
    .empty-state { color: #666; font-style: italic; padding: 12px 0; }
```

- [ ] **Step 8: Update existing settings spec**

In `apps/web/src/app/features/settings/settings.component.spec.ts`, update the admin tab count assertion (line 76):

From:
```typescript
      expect(tabs.length).toBe(2);
```
To:
```typescript
      expect(tabs.length).toBe(3);
```

Also add a check for the Federation tab:
```typescript
      expect(tabs[2].textContent).toContain('Federation');
```

The non-admin test at line 200 (`expect(tabs.length).toBe(1)`) remains correct since Federation is also admin-only.

- [ ] **Step 9: Verify build and tests**

Run: `npx nx build web --skip-nx-cache`
Expected: BUILD SUCCESSFUL

Run: `npx nx test web --skip-nx-cache`
Expected: ALL PASS

- [ ] **Step 10: Commit**

```bash
git add apps/web/src/app/features/settings/settings.component.ts \
        apps/web/src/app/features/settings/settings.component.spec.ts
git commit -m "feat(federation): add Federation tab to Settings page with config and peer management"
```

---

### Task 6: Implement Peer Visibility Filtering

Replace the `toggleFederationPeer` stub in MapComponent with a working implementation. Also add `instanceId` tracking to `FederationRingEntry` so rings can be toggled per peer.

**Files:**
- Modify: `apps/web/src/app/features/map/map.component.ts`
- Modify: `apps/web/src/app/features/map/federation-overlay.service.ts`

- [ ] **Step 1: Add instanceId to FederationRingEntry**

In `federation-overlay.service.ts`, change the `FederationRingEntry` interface:

```typescript
interface FederationRingEntry {
  billboard: any; // Cesium Billboard
  entityId: string;
  instanceId: string;
}
```

Update `addOrUpdateRing` signature to accept `instanceId`:

```typescript
  addOrUpdateRing(entityId: string, position: any, color: string, instanceId: string): void {
```

Also fix a pre-existing bug in the create branch (lines 111-116) — the cache lookup uses unsanitized `color` while the update branch uses `sanitizeColor(color)`. Change the create branch to:

```typescript
    // Get or create ring image for this color
    const safeColor = sanitizeColor(color);
    let ringImage = this.ringImageCache.get(safeColor);
    if (!ringImage) {
      ringImage = svgToDataUrl(ringBillboardSvg(safeColor));
      this.ringImageCache.set(safeColor, ringImage);
    }
```

Update the `this.federationRings.set` call at the end of the create branch:

```typescript
    this.federationRings.set(entityId, { billboard, entityId, instanceId });
```

- [ ] **Step 2: Add setRingVisibility method**

Add to `FederationOverlayService`:

```typescript
  setRingVisibility(instanceId: string, visible: boolean): void {
    for (const [, entry] of this.federationRings) {
      if (entry.instanceId === instanceId) {
        entry.billboard.show = visible;
      }
    }
  }
```

- [ ] **Step 3: Update map component ring calls**

In `map.component.ts`, find the two `addOrUpdateRing` calls (in the update and create branches of `addOrUpdateCesiumEntity`) and add the `instanceId` argument:

From:
```typescript
this.federationOverlay.addOrUpdateRing(entity.id, position, color);
```
To:
```typescript
this.federationOverlay.addOrUpdateRing(entity.id, position, color, entity.sourceInstanceId);
```

(Do this for both the update branch and the create branch.)

- [ ] **Step 4: Add hidden peers set and implement toggleFederationPeer**

Add a class field:
```typescript
  private hiddenFederationPeers = new Set<string>();
```

Replace the `toggleFederationPeer` stub with:

```typescript
  toggleFederationPeer(instanceId: string, event: Event): void {
    const checked = (event.target as HTMLInputElement).checked;
    if (checked) {
      this.hiddenFederationPeers.delete(instanceId);
    } else {
      this.hiddenFederationPeers.add(instanceId);
    }

    this.ngZone.runOutsideAngular(() => {
      // Toggle visibility of all entities from this peer
      this.entityMap.forEach((entry) => {
        if (entry.sentinelEntity.sourceInstanceId === instanceId) {
          entry.billboard.show = checked;
          entry.label.show = checked;
          if (entry.polyline) {
            entry.polyline.show = checked;
          }
        }
      });
      // Toggle federation rings for this peer
      this.federationOverlay.setRingVisibility(instanceId, checked);
      this.scheduleRender();
    });
  }
```

- [ ] **Step 5: Add hidden peer guard in addOrUpdateCesiumEntity**

In `addOrUpdateCesiumEntity`, immediately after the layer visibility check at line 497 (`if (layer && !layer.visible) return;`) and BEFORE the `const cesiumColor = ...` line — this ensures it guards both the update and create branches:

```typescript
    const layer = this.layers.find((l) => l.entityType === entity.entityType);
    if (layer && !layer.visible) return;

    // Skip hidden federation peers
    if (entity.sourceInstanceId && this.hiddenFederationPeers.has(entity.sourceInstanceId)) return;

    const cesiumColor = this.getCesiumColor(entity.entityType);
```

- [ ] **Step 6: Verify tests pass**

Run: `npx nx test web --skip-nx-cache`
Expected: ALL PASS

- [ ] **Step 7: Verify backend tests still pass**

Run: `npx nx test api-gateway --skip-nx-cache`
Expected: ALL PASS

- [ ] **Step 8: Commit**

```bash
git add apps/web/src/app/features/map/map.component.ts \
        apps/web/src/app/features/map/federation-overlay.service.ts
git commit -m "feat(federation): implement peer visibility filtering with ring toggling"
```
