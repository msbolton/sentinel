# Federation Sidebar Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the scattered federation UI (badge, source legend, layer panel toggles) with a single toggleable floating panel on the right side of the map.

**Architecture:** A new `FederationPanelComponent` replaces `FederationStatusComponent`. It renders a compact badge when closed and a floating panel with peers, visibility toggles, and source legend when open. MapComponent's `toggleFederationPeer` is refactored to accept a boolean instead of a DOM Event.

**Tech Stack:** Angular 19 (standalone components, signals, OnPush), TypeScript

**Spec:** `docs/superpowers/specs/2026-03-14-federation-sidebar-design.md`

---

## Chunk 1: Federation Panel Component

### Task 1: Create FederationPanelComponent with tests

**Files:**
- Create: `apps/web/src/app/features/map/federation-panel.component.ts`
- Create: `apps/web/src/app/features/map/federation-panel.component.spec.ts`

- [ ] **Step 1: Write the test**

Create `apps/web/src/app/features/map/federation-panel.component.spec.ts`:

```typescript
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { FederationPanelComponent } from './federation-panel.component';
import { FederationService } from '../../core/services/federation.service';
import { signal } from '@angular/core';

describe('FederationPanelComponent', () => {
  let component: FederationPanelComponent;
  let fixture: ComponentFixture<FederationPanelComponent>;

  const mockFederationService = {
    peers: signal([
      {
        instanceId: 'peer-1',
        displayName: 'Bravo Station',
        status: 'connected' as const,
        color: '#f97316',
        entityCount: 12,
        userCount: 2,
      },
      {
        instanceId: 'peer-2',
        displayName: 'Charlie HQ',
        status: 'stale' as const,
        color: '#8b5cf6',
        entityCount: 8,
        userCount: 1,
      },
      {
        instanceId: 'peer-3',
        displayName: 'Delta Outpost',
        status: 'disconnected' as const,
        color: '#06b6d4',
        entityCount: 0,
        userCount: 0,
      },
    ]),
    federationActive: signal(true),
  };

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [FederationPanelComponent],
      providers: [
        { provide: FederationService, useValue: mockFederationService },
      ],
    }).compileComponents();

    fixture = TestBed.createComponent(FederationPanelComponent);
    component = fixture.componentInstance;
    fixture.detectChanges();
  });

  it('should create', () => {
    expect(component).toBeTruthy();
  });

  it('should show collapsed badge when panel is closed', () => {
    const badge = fixture.nativeElement.querySelector('.federation-badge');
    expect(badge).toBeTruthy();
    const panel = fixture.nativeElement.querySelector('.federation-panel');
    expect(panel).toBeNull();
  });

  it('should show status dots for each peer in badge', () => {
    const dots = fixture.nativeElement.querySelectorAll('.federation-badge .badge-dot');
    expect(dots.length).toBe(3);
  });

  it('should expand panel on badge click', () => {
    const badge = fixture.nativeElement.querySelector('.federation-badge');
    badge.click();
    fixture.detectChanges();
    const panel = fixture.nativeElement.querySelector('.federation-panel');
    expect(panel).toBeTruthy();
  });

  it('should display peer names in expanded panel', () => {
    component.panelOpen.set(true);
    fixture.detectChanges();
    const text = fixture.nativeElement.textContent;
    expect(text).toContain('Bravo Station');
    expect(text).toContain('Charlie HQ');
  });

  it('should show entity counts in expanded panel', () => {
    component.panelOpen.set(true);
    fixture.detectChanges();
    const text = fixture.nativeElement.textContent;
    expect(text).toContain('12 entities');
  });

  it('should show status text for non-connected peers', () => {
    component.panelOpen.set(true);
    fixture.detectChanges();
    const text = fixture.nativeElement.textContent;
    expect(text).toContain('Stale');
    expect(text).toContain('Disconnected');
  });

  it('should show visibility checkboxes in expanded panel', () => {
    component.panelOpen.set(true);
    fixture.detectChanges();
    const checkboxes = fixture.nativeElement.querySelectorAll('.visibility-section input[type="checkbox"]');
    expect(checkboxes.length).toBe(3);
  });

  it('should emit togglePeer event when checkbox changes', () => {
    component.panelOpen.set(true);
    fixture.detectChanges();
    const spy = jest.spyOn(component.togglePeer, 'emit');
    const checkbox = fixture.nativeElement.querySelector('.visibility-section input[type="checkbox"]');
    checkbox.checked = false;
    checkbox.dispatchEvent(new Event('change'));
    fixture.detectChanges();
    expect(spy).toHaveBeenCalledWith({ instanceId: 'peer-1', visible: false });
  });

  it('should show source legend with Local and only connected peers', () => {
    component.panelOpen.set(true);
    fixture.detectChanges();
    const legendItems = fixture.nativeElement.querySelectorAll('.legend-item');
    // Local + 1 connected peer (Bravo Station) = 2 items
    expect(legendItems.length).toBe(2);
    const text = fixture.nativeElement.textContent;
    expect(text).toContain('Local');
    expect(text).toContain('Bravo Station');
  });

  it('should close panel when close button is clicked', () => {
    component.panelOpen.set(true);
    fixture.detectChanges();
    const closeBtn = fixture.nativeElement.querySelector('.panel-close');
    closeBtn.click();
    fixture.detectChanges();
    const panel = fixture.nativeElement.querySelector('.federation-panel');
    expect(panel).toBeNull();
  });

  it('should not render when federation is inactive', () => {
    mockFederationService.federationActive.set(false);
    fixture.detectChanges();
    const badge = fixture.nativeElement.querySelector('.federation-badge');
    const panel = fixture.nativeElement.querySelector('.federation-panel');
    expect(badge).toBeNull();
    expect(panel).toBeNull();
    // Reset
    mockFederationService.federationActive.set(true);
  });
});
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `npx nx test web --testFile=src/app/features/map/federation-panel.component.spec.ts`
Expected: FAIL — module not found

- [ ] **Step 3: Implement FederationPanelComponent**

Create `apps/web/src/app/features/map/federation-panel.component.ts`:

```typescript
import { Component, ChangeDetectionStrategy, signal, output } from '@angular/core';
import { FederationService } from '../../core/services/federation.service';

@Component({
  selector: 'app-federation-panel',
  standalone: true,
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    @if (federationService.federationActive()) {
      @if (!panelOpen()) {
        <div class="federation-badge" (click)="panelOpen.set(true)">
          <span class="badge-label">FED</span>
          <div class="badge-dots">
            @for (peer of federationService.peers(); track peer.instanceId) {
              <div [class]="'badge-dot status-' + peer.status"></div>
            }
          </div>
          <span class="badge-arrow">&#9662;</span>
        </div>
      }

      @if (panelOpen()) {
        <div class="federation-panel">
          <div class="panel-header">
            <span class="panel-title">Federation</span>
            <button class="panel-close" (click)="panelOpen.set(false)">&times;</button>
          </div>

          <div class="panel-body">
            <div class="panel-section">
              <div class="section-header">CONNECTED PEERS</div>
              @for (peer of federationService.peers(); track peer.instanceId) {
                <div class="peer-row" [class.peer-inactive]="peer.status !== 'connected'">
                  <div [class]="'status-dot status-' + peer.status"></div>
                  <div class="peer-info">
                    <div class="peer-name">{{ peer.displayName }}</div>
                    <div class="peer-meta">{{ peer.status === 'connected' ? peer.entityCount + ' entities' : peer.status === 'stale' ? 'Stale' : 'Disconnected' }}</div>
                  </div>
                  <div class="peer-color" [style.background]="peer.color"></div>
                </div>
              }
            </div>

            <div class="panel-divider"></div>

            <div class="panel-section visibility-section">
              <div class="section-header">VISIBILITY</div>
              @for (peer of federationService.peers(); track peer.instanceId) {
                <label class="visibility-row">
                  <input
                    type="checkbox"
                    checked
                    [style.accent-color]="peer.color"
                    (change)="onToggle(peer.instanceId, $event)"
                  />
                  <span class="peer-color" [style.background]="peer.color"></span>
                  <span class="visibility-name">{{ peer.displayName }}</span>
                </label>
              }
            </div>

            <div class="panel-divider"></div>

            <div class="panel-section">
              <div class="section-header">SOURCE LEGEND</div>
              <div class="legend-items">
                <div class="legend-item">
                  <span class="legend-dot" style="background: #3b82f6"></span>
                  <span class="legend-name">Local</span>
                </div>
                @for (peer of federationService.peers(); track peer.instanceId) {
                  @if (peer.status === 'connected') {
                    <div class="legend-item">
                      <span class="legend-dot" [style.background]="peer.color"></span>
                      <span class="legend-name">{{ peer.displayName }}</span>
                    </div>
                  }
                }
              </div>
            </div>
          </div>
        </div>
      }
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
      padding: 6px 10px;
      display: flex;
      align-items: center;
      gap: 6px;
      cursor: pointer;
      pointer-events: auto;
      z-index: 10;
      transition: background 0.15s;
    }
    .federation-badge:hover {
      background: rgba(30, 40, 60, 0.9);
      border-color: rgba(255, 255, 255, 0.2);
    }

    .badge-label {
      color: #888;
      font-weight: bold;
      letter-spacing: 1px;
      font-size: 9px;
    }

    .badge-dots {
      display: flex;
      gap: 3px;
    }

    .badge-dot {
      width: 6px;
      height: 6px;
      border-radius: 50%;
    }

    .badge-arrow {
      color: #666;
      font-size: 10px;
    }

    .federation-panel {
      position: absolute;
      top: 12px;
      right: 12px;
      width: 260px;
      background: rgba(10, 14, 23, 0.95);
      border: 1px solid #333;
      border-radius: 8px;
      overflow: hidden;
      pointer-events: auto;
      z-index: 10;
      backdrop-filter: blur(16px);
      box-shadow: 0 8px 32px rgba(0, 0, 0, 0.4);
    }

    .panel-header {
      display: flex;
      align-items: center;
      justify-content: space-between;
      padding: 10px 14px;
      border-bottom: 1px solid rgba(255, 255, 255, 0.08);
    }
    .panel-title {
      color: #ccc;
      font-weight: 600;
      font-size: 12px;
    }
    .panel-close {
      background: none;
      border: none;
      color: #666;
      font-size: 16px;
      cursor: pointer;
      padding: 0 2px;
      line-height: 1;
    }
    .panel-close:hover { color: #ccc; }

    .panel-body {
      padding: 0;
    }

    .panel-section {
      padding: 10px 14px;
    }

    .panel-divider {
      border-top: 1px solid rgba(255, 255, 255, 0.06);
      margin: 0 14px;
    }

    .section-header {
      color: #888;
      font-size: 9px;
      text-transform: uppercase;
      letter-spacing: 1px;
      margin-bottom: 8px;
    }

    .peer-row {
      display: flex;
      align-items: center;
      gap: 8px;
      padding: 7px 10px;
      background: rgba(255, 255, 255, 0.03);
      border-radius: 6px;
      margin-bottom: 4px;
    }
    .peer-inactive { opacity: 0.6; }

    .status-dot {
      width: 7px;
      height: 7px;
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

    .peer-info { flex: 1; }
    .peer-name {
      color: #ccc;
      font-size: 11px;
      font-weight: 500;
    }
    .peer-meta {
      color: #666;
      font-size: 9px;
    }

    .peer-color {
      width: 10px;
      height: 10px;
      border-radius: 2px;
      flex-shrink: 0;
    }

    .visibility-row {
      display: flex;
      align-items: center;
      gap: 8px;
      padding: 4px 0;
      cursor: pointer;
    }
    .visibility-row input[type="checkbox"] {
      width: 13px;
      height: 13px;
      cursor: pointer;
    }
    .visibility-name {
      color: #ccc;
      font-size: 11px;
    }

    .legend-items {
      display: flex;
      flex-wrap: wrap;
      gap: 10px;
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
      font-size: 10px;
    }
  `],
})
export class FederationPanelComponent {
  readonly panelOpen = signal(false);
  readonly togglePeer = output<{ instanceId: string; visible: boolean }>();

  constructor(readonly federationService: FederationService) {}

  onToggle(instanceId: string, event: Event): void {
    const visible = (event.target as HTMLInputElement).checked;
    this.togglePeer.emit({ instanceId, visible });
  }
}
```

- [ ] **Step 4: Run tests**

Run: `npx nx test web --testFile=src/app/features/map/federation-panel.component.spec.ts`
Expected: ALL PASS (12 tests)

- [ ] **Step 5: Commit**

```bash
git add apps/web/src/app/features/map/federation-panel.component.ts \
        apps/web/src/app/features/map/federation-panel.component.spec.ts
git commit -m "feat(federation): add FederationPanelComponent with toggleable floating drawer"
```

---

### Task 2: Wire panel into MapComponent and remove old federation UI

**Files:**
- Modify: `apps/web/src/app/features/map/map.component.ts` (line 117 imports, line 713 toggleFederationPeer)
- Modify: `apps/web/src/app/features/map/map.component.html` (lines 24-34 layer panel federation section, line 98 federation-status tag)
- Modify: `apps/web/src/app/features/map/map.component.scss` (lines 133-145 federation layer styles)
- Delete: `apps/web/src/app/features/map/federation-status.component.ts`
- Delete: `apps/web/src/app/features/map/federation-status.component.spec.ts`

- [ ] **Step 1: Update MapComponent imports**

In `map.component.ts`, line 117, change the `imports` array:

From:
```typescript
  imports: [CommonModule, FormsModule, EntityDetailPanelComponent, FederationStatusComponent],
```
To:
```typescript
  imports: [CommonModule, FormsModule, EntityDetailPanelComponent, FederationPanelComponent],
```

Update the import statements at the top of the file. Remove:
```typescript
import { FederationStatusComponent } from './federation-status.component';
```
Add:
```typescript
import { FederationPanelComponent } from './federation-panel.component';
```

- [ ] **Step 2: Add onToggleFederationPeer method**

Add a new method after `toggleFederationPeer` (line ~734):

```typescript
  onToggleFederationPeer(event: { instanceId: string; visible: boolean }): void {
    if (event.visible) {
      this.hiddenFederationPeers.delete(event.instanceId);
    } else {
      this.hiddenFederationPeers.add(event.instanceId);
    }

    this.ngZone.runOutsideAngular(() => {
      this.entityMap.forEach((entry) => {
        if (entry.sentinelEntity.sourceInstanceId === event.instanceId) {
          entry.billboard.show = event.visible;
          entry.label.show = event.visible;
          if (entry.polyline) {
            entry.polyline.show = event.visible;
          }
        }
      });
      this.federationOverlay.setRingVisibility(event.instanceId, event.visible);
      this.scheduleRender();
    });
  }
```

The old `toggleFederationPeer(instanceId, event)` method can be removed since no template references it anymore.

- [ ] **Step 3: Update map.component.html**

Replace the `<app-federation-status />` tag (line 98) with:
```html
  <app-federation-panel (togglePeer)="onToggleFederationPeer($event)" />
```

Remove the federation section from the layer panel (lines 24-34 — the `@if (federationService.federationActive())` block including the divider, label, and per-peer checkboxes):
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

- [ ] **Step 4: Clean up map.component.scss**

Remove the federation layer styles (lines 133-145):
```scss
// --- Federation Layer Styles ---
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

- [ ] **Step 5: Delete old files**

```bash
rm apps/web/src/app/features/map/federation-status.component.ts
rm apps/web/src/app/features/map/federation-status.component.spec.ts
```

- [ ] **Step 6: Run all frontend tests**

Run: `npx nx test web --skip-nx-cache`
Expected: ALL PASS (tests reduced by 3 from deleted spec, increased by 12 from new spec — net ~141 tests across ~17 suites)

- [ ] **Step 7: Commit**

```bash
git add apps/web/src/app/features/map/map.component.ts \
        apps/web/src/app/features/map/map.component.html \
        apps/web/src/app/features/map/map.component.scss \
        apps/web/src/app/features/map/federation-panel.component.ts \
        apps/web/src/app/features/map/federation-panel.component.spec.ts
git rm apps/web/src/app/features/map/federation-status.component.ts \
       apps/web/src/app/features/map/federation-status.component.spec.ts
git commit -m "feat(federation): replace scattered federation UI with unified floating panel"
```
