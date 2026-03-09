# Entity Detail Panel Enrichment Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Display enriched entity data (platform-specific details, operational status, identity, measurement quality) in the entity detail panel with collapsible sections.

**Architecture:** Extend the frontend Entity model to receive enriched fields already being sent by the API Gateway via WebSocket. Add new template sections to the existing `EntityDetailPanelComponent` with computed signals for platform-type detection and collapsible section state.

**Tech Stack:** Angular 19 (standalone components, signals, zoneless change detection), TypeScript

---

## Task 1: Create frontend platform data interfaces

**Files:**
- Create: `apps/web/src/app/shared/models/platform-data.model.ts`

These interfaces mirror the shared-models types at `libs/shared-models/src/platform-data.ts` and `libs/shared-models/src/kinematics.ts`. The frontend has its own model layer (doesn't import from `@sentinel/shared-models` for entity types).

**Step 1: Create the file**

```typescript
// --- Enums ---

export enum TrackEnvironment {
  AIR = 'AIR',
  SEA_SURFACE = 'SEA_SURFACE',
  SUBSURFACE = 'SUBSURFACE',
  GROUND = 'GROUND',
  SPACE = 'SPACE',
  UNKNOWN = 'UNKNOWN',
}

export enum OperationalStatus {
  OPERATIONAL = 'OPERATIONAL',
  DEGRADED = 'DEGRADED',
  DAMAGED = 'DAMAGED',
  DESTROYED = 'DESTROYED',
  INACTIVE = 'INACTIVE',
  UNKNOWN = 'UNKNOWN',
}

export enum DamageAssessment {
  NONE = 'NONE',
  LIGHT = 'LIGHT',
  MODERATE = 'MODERATE',
  HEAVY = 'HEAVY',
  DESTROYED = 'DESTROYED',
  UNKNOWN = 'UNKNOWN',
}

export enum Affiliation {
  FRIENDLY = 'FRIENDLY',
  HOSTILE = 'HOSTILE',
  NEUTRAL = 'NEUTRAL',
  UNKNOWN = 'UNKNOWN',
  ASSUMED_FRIENDLY = 'ASSUMED_FRIENDLY',
  SUSPECT = 'SUSPECT',
  PENDING = 'PENDING',
}

export enum NavigationalStatus {
  UNDER_WAY_USING_ENGINE = 'UNDER_WAY_USING_ENGINE',
  AT_ANCHOR = 'AT_ANCHOR',
  NOT_UNDER_COMMAND = 'NOT_UNDER_COMMAND',
  RESTRICTED_MANOEUVRABILITY = 'RESTRICTED_MANOEUVRABILITY',
  CONSTRAINED_BY_DRAUGHT = 'CONSTRAINED_BY_DRAUGHT',
  MOORED = 'MOORED',
  AGROUND = 'AGROUND',
  ENGAGED_IN_FISHING = 'ENGAGED_IN_FISHING',
  UNDER_WAY_SAILING = 'UNDER_WAY_SAILING',
  AIS_SART = 'AIS_SART',
  UNKNOWN = 'UNKNOWN',
}

// --- Platform Data Interfaces ---

export interface AISData {
  mmsi: string;
  imo?: string;
  callsign?: string;
  vesselName?: string;
  shipType?: number;
  shipTypeName?: string;
  flag?: string;
  destination?: string;
  eta?: string;
  draught?: number;
  dimensionA?: number;
  dimensionB?: number;
  dimensionC?: number;
  dimensionD?: number;
  lengthOverall?: number;
  beam?: number;
  navStatus?: NavigationalStatus;
  rateOfTurn?: number;
  speedOverGround?: number;
  courseOverGround?: number;
  trueHeading?: number;
  positionAccuracyHigh?: boolean;
  specialManoeuvre?: boolean;
  messageType?: number;
  repeatIndicator?: number;
}

export interface ADSBData {
  icaoHex: string;
  registration?: string;
  aircraftType?: string;
  aircraftTypeName?: string;
  operatorIcao?: string;
  operatorName?: string;
  squawk?: string;
  emergency?: string;
  mode1?: string;
  mode2?: string;
  aircraftId?: string;
  flightAirborne?: boolean;
  indicatedAirSpeed?: number;
  trueAirSpeed?: number;
  groundSpeed?: number;
  magneticHeading?: number;
  mode5FigureOfMerit?: number;
  nationalOriginCode?: number;
  missionCode?: number;
  altitudeBaro?: number;
  altitudeGeom?: number;
  verticalRate?: number;
  onGround?: boolean;
  category?: string;
  nacP?: number;
  nacV?: number;
  sil?: number;
  silType?: string;
  nic?: number;
  rc?: number;
  gva?: number;
  sda?: number;
}

export interface TLEData {
  noradId: number;
  intlDesignator?: string;
  satName?: string;
  line1: string;
  line2: string;
  epoch?: string;
  inclination?: number;
  eccentricity?: number;
  raan?: number;
  argOfPerigee?: number;
  meanAnomaly?: number;
  meanMotion?: number;
  period?: number;
  apogee?: number;
  perigee?: number;
  objectType?: string;
  rcsSize?: string;
  launchDate?: string;
  decayDate?: string;
  country?: string;
}

export interface Link16Data {
  trackNumber: number;
  jSeriesLabel: string;
  originatingUnit?: string;
  quality?: number;
  exerciseIndicator?: boolean;
  simulationIndicator?: boolean;
  forceIdentity?: string;
}

export interface CoTData {
  uid: string;
  cotType: string;
  how?: string;
  ce?: number;
  le?: number;
  staleTime?: string;
  accessControl?: string;
  opex?: string;
  qos?: string;
}

export interface UAVData {
  make?: string;
  model?: string;
  serialNumber?: string;
  macAddress?: string;
  operatingFrequency?: number;
  frequencyRange?: number;
}

export interface PlatformData {
  ais?: AISData;
  adsb?: ADSBData;
  tle?: TLEData;
  link16?: Link16Data;
  cot?: CoTData;
  uav?: UAVData;
}

// --- Kinematics ---

export interface GeodeticVelocity {
  north: number;
  east: number;
  up: number;
}

export interface Orientation {
  yaw: number;
  pitch: number;
  roll: number;
}

export interface KinematicState {
  velocity?: GeodeticVelocity;
  acceleration?: { north: number; east: number; up: number };
}
```

**Step 2: Verify no build errors**

Run: `npx nx build web --skip-nx-cache 2>&1 | tail -5`
Expected: Build succeeds (new file is standalone, not yet imported)

**Commit:** `feat(web): add platform data and kinematics model interfaces`

---

## Task 2: Extend frontend Entity interface with enriched fields

**Files:**
- Modify: `apps/web/src/app/shared/models/entity.model.ts`

The API Gateway already sends these fields via WebSocket — the frontend Entity interface just needs to accept them.

**Step 1: Add imports and new fields**

Add import at top:
```typescript
import {
  Affiliation,
  DamageAssessment,
  KinematicState,
  OperationalStatus,
  Orientation,
  PlatformData,
  TrackEnvironment,
} from './platform-data.model';
```

Re-export for convenience:
```typescript
export { Affiliation, DamageAssessment, OperationalStatus, TrackEnvironment };
```

Add these optional fields to the `Entity` interface (after `lastSeenAt`):
```typescript
  // Identity
  affiliation?: Affiliation;
  sourceEntityId?: string;
  countryOfOrigin?: string;

  // Track context
  trackEnvironment?: TrackEnvironment;

  // Operational status
  operationalStatus?: OperationalStatus;
  damageAssessment?: DamageAssessment;

  // Physical dimensions
  dimensions?: {
    length?: number;
    width?: number;
    height?: number;
  };

  // Kinematics
  orientation?: Orientation;
  kinematics?: KinematicState;

  // Protocol-specific data
  platformData?: PlatformData;

  // Measurement quality
  circularError?: number;
```

**Step 2: Verify build**

Run: `npx nx build web --skip-nx-cache 2>&1 | tail -5`
Expected: Build succeeds

**Step 3: Run tests**

Run: `npx nx test web`
Expected: All tests pass (no tests reference Entity fields directly)

**Commit:** `feat(web): extend Entity interface with enriched data model fields`

---

## Task 3: Add computed signals and collapsible section logic to panel

**Files:**
- Modify: `apps/web/src/app/shared/components/entity-detail-panel.component.ts`

**Step 1: Add imports**

Add `signal` to the `@angular/core` import (already has `computed`, `input`, `output`).

Add platform data imports:
```typescript
import {
  Affiliation,
  PlatformData,
  AISData,
  ADSBData,
  TLEData,
  Link16Data,
  CoTData,
  TrackEnvironment,
  OperationalStatus,
} from '../models/platform-data.model';
```

**Step 2: Add computed signals and section state to the class**

After the existing `modelViewerHtml` computed signal, add:

```typescript
  protected expandedSections = signal<Record<string, boolean>>({
    platformDetails: false,
    signalQuality: false,
  });

  protected platformType = computed(() => {
    const pd = this.entity()?.platformData;
    if (!pd) return null;
    if (pd.ais) return 'ais' as const;
    if (pd.adsb) return 'adsb' as const;
    if (pd.tle) return 'tle' as const;
    if (pd.link16) return 'link16' as const;
    if (pd.cot) return 'cot' as const;
    if (pd.uav) return 'uav' as const;
    return null;
  });

  protected primaryId = computed(() => {
    const pd = this.entity()?.platformData;
    if (!pd) return null;
    if (pd.ais) return { label: 'MMSI', value: pd.ais.mmsi };
    if (pd.adsb) return { label: 'ICAO', value: pd.adsb.icaoHex };
    if (pd.tle) return { label: 'NORAD', value: String(pd.tle.noradId) };
    if (pd.cot) return { label: 'UID', value: pd.cot.uid };
    if (pd.link16) return { label: 'JTN', value: String(pd.link16.trackNumber) };
    return null;
  });

  protected secondaryIds = computed(() => {
    const pd = this.entity()?.platformData;
    const ids: { label: string; value: string }[] = [];
    if (!pd) return ids;

    if (pd.ais) {
      if (pd.ais.callsign) ids.push({ label: 'Callsign', value: pd.ais.callsign });
      if (pd.ais.imo) ids.push({ label: 'IMO', value: pd.ais.imo });
      if (pd.ais.vesselName) ids.push({ label: 'Vessel', value: pd.ais.vesselName });
    }
    if (pd.adsb) {
      if (pd.adsb.aircraftId) ids.push({ label: 'Flight', value: pd.adsb.aircraftId });
      if (pd.adsb.registration) ids.push({ label: 'Reg', value: pd.adsb.registration });
      if (pd.adsb.squawk) ids.push({ label: 'Squawk', value: pd.adsb.squawk });
      if (pd.adsb.operatorName) ids.push({ label: 'Operator', value: pd.adsb.operatorName });
    }
    if (pd.tle?.satName) ids.push({ label: 'Name', value: pd.tle.satName });
    if (pd.link16?.originatingUnit) ids.push({ label: 'Unit', value: pd.link16.originatingUnit });

    return ids;
  });

  protected affiliationColor = computed(() => {
    switch (this.entity()?.affiliation) {
      case 'FRIENDLY': case 'ASSUMED_FRIENDLY': return 'friendly';
      case 'HOSTILE': case 'SUSPECT': return 'hostile';
      case 'NEUTRAL': return 'neutral';
      default: return 'unknown';
    }
  });

  protected envLabel = computed(() => {
    switch (this.entity()?.trackEnvironment) {
      case 'AIR': return 'Air';
      case 'SEA_SURFACE': return 'Sea';
      case 'SUBSURFACE': return 'Sub';
      case 'GROUND': return 'Ground';
      case 'SPACE': return 'Space';
      default: return null;
    }
  });

  protected toggleSection(key: string): void {
    this.expandedSections.update(s => ({ ...s, [key]: !s[key] }));
  }
```

**Step 3: Verify build**

Run: `npx nx build web --skip-nx-cache 2>&1 | tail -5`
Expected: Build succeeds

**Commit:** `feat(web): add computed signals for platform data extraction and collapsible sections`

---

## Task 4: Add Identity and header enrichment to panel template

**Files:**
- Modify: `apps/web/src/app/shared/components/entity-detail-panel.component.ts`

**Step 1: Enhance the header**

In the template, after the entity type chip inside `.header-info`, add track environment and country badges:

```html
            @if (envLabel()) {
              <span class="chip chip-env">{{ envLabel() }}</span>
            }
            @if (e.countryOfOrigin) {
              <span class="chip chip-country">{{ e.countryOfOrigin }}</span>
            }
```

**Step 2: Add Identity section**

After the Classification section and before the 3D Model section, add:

```html
          <!-- Identity -->
          @if (primaryId() || e.affiliation) {
            <section class="section">
              <h4 class="section-label">Identity</h4>
              @if (primaryId(); as pid) {
                <div class="field-row">
                  <span class="field-label">{{ pid.label }}</span>
                  <span class="field-value mono">{{ pid.value }}</span>
                </div>
              }
              @if (e.sourceEntityId && e.sourceEntityId !== primaryId()?.value) {
                <div class="field-row">
                  <span class="field-label">Source ID</span>
                  <span class="field-value mono">{{ e.sourceEntityId }}</span>
                </div>
              }
              @for (sid of secondaryIds(); track sid.label) {
                <div class="field-row">
                  <span class="field-label">{{ sid.label }}</span>
                  <span class="field-value mono">{{ sid.value }}</span>
                </div>
              }
              @if (e.affiliation) {
                <div class="field-row">
                  <span class="field-label">Affiliation</span>
                  <span class="chip chip-affiliation-id" [ngClass]="affiliationColor()">
                    {{ e.affiliation }}
                  </span>
                </div>
              }
            </section>
          }
```

**Step 3: Add styles for new chips**

Add to the styles string:

```css
    .chip-env {
      background: color-mix(in srgb, var(--accent-blue) 15%, transparent);
      color: var(--accent-blue);
      border-color: color-mix(in srgb, var(--accent-blue) 30%, transparent);
      font-size: 0.65rem;
    }

    .chip-country {
      background: color-mix(in srgb, var(--text-muted) 15%, transparent);
      color: var(--text-secondary);
      border-color: color-mix(in srgb, var(--text-muted) 30%, transparent);
      font-size: 0.65rem;
      font-family: var(--font-mono);
    }

    .chip-affiliation-id {
      font-size: 0.7rem;
      &.friendly {
        background: color-mix(in srgb, #3b82f6 15%, transparent);
        color: #3b82f6;
        border-color: color-mix(in srgb, #3b82f6 30%, transparent);
      }
      &.hostile {
        background: color-mix(in srgb, #ef4444 15%, transparent);
        color: #ef4444;
        border-color: color-mix(in srgb, #ef4444 30%, transparent);
      }
      &.neutral {
        background: color-mix(in srgb, #22c55e 15%, transparent);
        color: #22c55e;
        border-color: color-mix(in srgb, #22c55e 30%, transparent);
      }
      &.unknown {
        background: color-mix(in srgb, var(--text-muted) 15%, transparent);
        color: var(--text-muted);
        border-color: color-mix(in srgb, var(--text-muted) 30%, transparent);
      }
    }
```

**Step 4: Verify build**

Run: `npx nx build web --skip-nx-cache 2>&1 | tail -5`
Expected: Build succeeds

**Commit:** `feat(web): add identity section and header badges to entity detail panel`

---

## Task 5: Add Position enrichment and Operational Status section

**Files:**
- Modify: `apps/web/src/app/shared/components/entity-detail-panel.component.ts`

**Step 1: Enhance Position section**

After the existing altitude field-row inside the Position section, add:

```html
              @if (e.circularError != null) {
                <div class="field-row">
                  <span class="field-label">CEP</span>
                  <span class="field-value mono">{{ e.circularError | number:'1.0-1' }} m</span>
                </div>
              }
```

**Step 2: Enhance Movement section**

After the existing course field-row, add velocity vector display:

```html
              @if (e.kinematics?.velocity; as vel) {
                <div class="field-row">
                  <span class="field-label">Velocity</span>
                  <span class="field-value mono">
                    N{{ vel.north | number:'1.1-1' }} E{{ vel.east | number:'1.1-1' }} U{{ vel.up | number:'1.1-1' }} m/s
                  </span>
                </div>
              }
```

**Step 3: Add Operational Status section**

After the Movement section, add:

```html
          <!-- Operational Status -->
          @if (e.operationalStatus && e.operationalStatus !== 'UNKNOWN') {
            <section class="section">
              <h4 class="section-label">Operational Status</h4>
              <div class="field-row">
                <span class="field-label">Status</span>
                <span class="chip chip-op-status" [ngClass]="e.operationalStatus">
                  {{ e.operationalStatus }}
                </span>
              </div>
              @if (e.damageAssessment && e.damageAssessment !== 'UNKNOWN' && e.damageAssessment !== 'NONE') {
                <div class="field-row">
                  <span class="field-label">Damage</span>
                  <span class="chip chip-damage" [ngClass]="e.damageAssessment">
                    {{ e.damageAssessment }}
                  </span>
                </div>
              }
              @if (e.dimensions) {
                <div class="field-row">
                  <span class="field-label">Dimensions</span>
                  <span class="field-value mono">
                    @if (e.dimensions.length != null) { {{ e.dimensions.length | number:'1.0-1' }}m }
                    @if (e.dimensions.width != null) { &times; {{ e.dimensions.width | number:'1.0-1' }}m }
                    @if (e.dimensions.height != null) { &times; {{ e.dimensions.height | number:'1.0-1' }}m }
                  </span>
                </div>
              }
            </section>
          }
```

**Step 4: Add styles for operational status chips**

```css
    .chip-op-status {
      font-size: 0.7rem;
      &.OPERATIONAL {
        background: color-mix(in srgb, #22c55e 15%, transparent);
        color: #22c55e;
        border-color: color-mix(in srgb, #22c55e 30%, transparent);
      }
      &.DEGRADED {
        background: color-mix(in srgb, #eab308 15%, transparent);
        color: #eab308;
        border-color: color-mix(in srgb, #eab308 30%, transparent);
      }
      &.DAMAGED {
        background: color-mix(in srgb, #f97316 15%, transparent);
        color: #f97316;
        border-color: color-mix(in srgb, #f97316 30%, transparent);
      }
      &.DESTROYED, &.INACTIVE {
        background: color-mix(in srgb, #ef4444 15%, transparent);
        color: #ef4444;
        border-color: color-mix(in srgb, #ef4444 30%, transparent);
      }
    }

    .chip-damage {
      font-size: 0.7rem;
      &.LIGHT {
        background: color-mix(in srgb, #eab308 15%, transparent);
        color: #eab308;
        border-color: color-mix(in srgb, #eab308 30%, transparent);
      }
      &.MODERATE {
        background: color-mix(in srgb, #f97316 15%, transparent);
        color: #f97316;
        border-color: color-mix(in srgb, #f97316 30%, transparent);
      }
      &.HEAVY, &.DESTROYED {
        background: color-mix(in srgb, #ef4444 15%, transparent);
        color: #ef4444;
        border-color: color-mix(in srgb, #ef4444 30%, transparent);
      }
    }
```

**Step 5: Verify build**

Run: `npx nx build web --skip-nx-cache 2>&1 | tail -5`
Expected: Build succeeds

**Commit:** `feat(web): add operational status, CEP, and velocity to entity detail panel`

---

## Task 6: Add collapsible Platform Details section

**Files:**
- Modify: `apps/web/src/app/shared/components/entity-detail-panel.component.ts`

This is the largest template change. Add a collapsible section that renders protocol-specific details based on `platformType()`.

**Step 1: Add collapsible section styles**

```css
    .collapsible-header {
      display: flex;
      align-items: center;
      justify-content: space-between;
      cursor: pointer;
      padding: 2px 0;
      user-select: none;

      &:hover {
        opacity: 0.8;
      }
    }

    .chevron {
      font-size: 0.7rem;
      color: var(--text-muted);
      transition: transform 150ms ease;

      &.expanded {
        transform: rotate(90deg);
      }
    }

    .collapsible-content {
      padding-top: 8px;
    }

    .sub-label {
      font-size: 0.65rem;
      font-weight: 600;
      color: var(--text-muted);
      text-transform: uppercase;
      letter-spacing: 0.05em;
      margin: 10px 0 4px;
    }
```

**Step 2: Add Platform Details template**

Insert after the Operational Status section, before the Source & Timing section:

```html
          <!-- Platform Details (collapsible) -->
          @if (platformType()) {
            <section class="section">
              <div class="collapsible-header" (click)="toggleSection('platformDetails')">
                <h4 class="section-label" style="margin:0">
                  @switch (platformType()) {
                    @case ('ais') { Vessel Details }
                    @case ('adsb') { Aircraft Details }
                    @case ('tle') { Satellite Orbit }
                    @case ('cot') { CoT Data }
                    @case ('link16') { Link 16 Data }
                    @case ('uav') { UAV Data }
                  }
                </h4>
                <span class="chevron" [class.expanded]="expandedSections()['platformDetails']">&#9654;</span>
              </div>
              @if (expandedSections()['platformDetails']) {
                <div class="collapsible-content">
                  @switch (platformType()) {
                    @case ('ais') {
                      @if (e.platformData?.ais; as ais) {
                        @if (ais.shipTypeName) {
                          <div class="field-row"><span class="field-label">Ship Type</span><span class="field-value">{{ ais.shipTypeName }}</span></div>
                        }
                        @if (ais.navStatus && ais.navStatus !== 'UNKNOWN') {
                          <div class="field-row"><span class="field-label">Nav Status</span><span class="field-value">{{ ais.navStatus.replace('_', ' ') }}</span></div>
                        }
                        @if (ais.destination) {
                          <div class="field-row"><span class="field-label">Destination</span><span class="field-value">{{ ais.destination }}</span></div>
                        }
                        @if (ais.eta) {
                          <div class="field-row"><span class="field-label">ETA</span><span class="field-value mono">{{ ais.eta | date:'short' }}</span></div>
                        }
                        @if (ais.draught != null) {
                          <div class="field-row"><span class="field-label">Draught</span><span class="field-value mono">{{ ais.draught | number:'1.1-1' }} m</span></div>
                        }
                        @if (ais.lengthOverall != null) {
                          <div class="field-row"><span class="field-label">Length</span><span class="field-value mono">{{ ais.lengthOverall | number:'1.0-0' }} m</span></div>
                        }
                        @if (ais.beam != null) {
                          <div class="field-row"><span class="field-label">Beam</span><span class="field-value mono">{{ ais.beam | number:'1.0-0' }} m</span></div>
                        }
                        @if (ais.flag) {
                          <div class="field-row"><span class="field-label">Flag</span><span class="field-value">{{ ais.flag }}</span></div>
                        }
                        @if (ais.rateOfTurn != null) {
                          <div class="field-row"><span class="field-label">Rate of Turn</span><span class="field-value mono">{{ ais.rateOfTurn | number:'1.1-1' }}&deg;/min</span></div>
                        }
                        @if (ais.trueHeading != null && ais.trueHeading !== 511) {
                          <div class="field-row"><span class="field-label">True Heading</span><span class="field-value mono">{{ ais.trueHeading | number:'1.0-0' }}&deg;</span></div>
                        }
                        @if (ais.speedOverGround != null) {
                          <div class="field-row"><span class="field-label">SOG</span><span class="field-value mono">{{ ais.speedOverGround | number:'1.1-1' }} kts</span></div>
                        }
                        @if (ais.courseOverGround != null) {
                          <div class="field-row"><span class="field-label">COG</span><span class="field-value mono">{{ ais.courseOverGround | number:'1.1-1' }}&deg;</span></div>
                        }
                        @if (ais.positionAccuracyHigh != null) {
                          <div class="field-row"><span class="field-label">DGPS</span><span class="field-value">{{ ais.positionAccuracyHigh ? 'Yes' : 'No' }}</span></div>
                        }
                        @if (ais.messageType != null) {
                          <div class="field-row"><span class="field-label">Msg Type</span><span class="field-value mono">{{ ais.messageType }}</span></div>
                        }
                      }
                    }
                    @case ('adsb') {
                      @if (e.platformData?.adsb; as adsb) {
                        @if (adsb.aircraftTypeName || adsb.aircraftType) {
                          <div class="field-row"><span class="field-label">Aircraft</span><span class="field-value">{{ adsb.aircraftTypeName || adsb.aircraftType }}</span></div>
                        }
                        @if (adsb.operatorName) {
                          <div class="field-row"><span class="field-label">Operator</span><span class="field-value">{{ adsb.operatorName }}</span></div>
                        }
                        @if (adsb.emergency) {
                          <div class="field-row"><span class="field-label">Emergency</span><span class="field-value chip-damage HEAVY">{{ adsb.emergency }}</span></div>
                        }
                        @if (adsb.altitudeBaro != null) {
                          <div class="field-row"><span class="field-label">Alt (Baro)</span><span class="field-value mono">{{ adsb.altitudeBaro | number:'1.0-0' }} m</span></div>
                        }
                        @if (adsb.altitudeGeom != null) {
                          <div class="field-row"><span class="field-label">Alt (GPS)</span><span class="field-value mono">{{ adsb.altitudeGeom | number:'1.0-0' }} m</span></div>
                        }
                        @if (adsb.verticalRate != null) {
                          <div class="field-row"><span class="field-label">Vert Rate</span><span class="field-value mono">{{ adsb.verticalRate | number:'1.1-1' }} m/s</span></div>
                        }
                        @if (adsb.groundSpeed != null) {
                          <div class="field-row"><span class="field-label">Ground Speed</span><span class="field-value mono">{{ adsb.groundSpeed | number:'1.0-0' }} kts</span></div>
                        }
                        @if (adsb.indicatedAirSpeed != null) {
                          <div class="field-row"><span class="field-label">IAS</span><span class="field-value mono">{{ adsb.indicatedAirSpeed | number:'1.0-0' }} kts</span></div>
                        }
                        @if (adsb.trueAirSpeed != null) {
                          <div class="field-row"><span class="field-label">TAS</span><span class="field-value mono">{{ adsb.trueAirSpeed | number:'1.0-0' }} kts</span></div>
                        }
                        @if (adsb.magneticHeading != null) {
                          <div class="field-row"><span class="field-label">Mag Heading</span><span class="field-value mono">{{ adsb.magneticHeading | number:'1.0-0' }}&deg;</span></div>
                        }
                        @if (adsb.onGround != null) {
                          <div class="field-row"><span class="field-label">On Ground</span><span class="field-value">{{ adsb.onGround ? 'Yes' : 'No' }}</span></div>
                        }
                        @if (adsb.category) {
                          <div class="field-row"><span class="field-label">Category</span><span class="field-value mono">{{ adsb.category }}</span></div>
                        }
                      }
                    }
                    @case ('tle') {
                      @if (e.platformData?.tle; as tle) {
                        @if (tle.intlDesignator) {
                          <div class="field-row"><span class="field-label">Intl Desig</span><span class="field-value mono">{{ tle.intlDesignator }}</span></div>
                        }
                        @if (tle.objectType) {
                          <div class="field-row"><span class="field-label">Object Type</span><span class="field-value">{{ tle.objectType }}</span></div>
                        }
                        @if (tle.epoch) {
                          <div class="field-row"><span class="field-label">TLE Epoch</span><span class="field-value mono">{{ tle.epoch | date:'short' }}</span></div>
                        }
                        @if (tle.inclination != null) {
                          <div class="field-row"><span class="field-label">Inclination</span><span class="field-value mono">{{ tle.inclination | number:'1.2-2' }}&deg;</span></div>
                        }
                        @if (tle.eccentricity != null) {
                          <div class="field-row"><span class="field-label">Eccentricity</span><span class="field-value mono">{{ tle.eccentricity | number:'1.6-6' }}</span></div>
                        }
                        @if (tle.period != null) {
                          <div class="field-row"><span class="field-label">Period</span><span class="field-value mono">{{ tle.period | number:'1.1-1' }} min</span></div>
                        }
                        @if (tle.apogee != null) {
                          <div class="field-row"><span class="field-label">Apogee</span><span class="field-value mono">{{ tle.apogee | number:'1.0-0' }} km</span></div>
                        }
                        @if (tle.perigee != null) {
                          <div class="field-row"><span class="field-label">Perigee</span><span class="field-value mono">{{ tle.perigee | number:'1.0-0' }} km</span></div>
                        }
                        @if (tle.meanMotion != null) {
                          <div class="field-row"><span class="field-label">Mean Motion</span><span class="field-value mono">{{ tle.meanMotion | number:'1.4-4' }} rev/day</span></div>
                        }
                        @if (tle.raan != null) {
                          <div class="field-row"><span class="field-label">RAAN</span><span class="field-value mono">{{ tle.raan | number:'1.2-2' }}&deg;</span></div>
                        }
                        @if (tle.rcsSize) {
                          <div class="field-row"><span class="field-label">RCS</span><span class="field-value">{{ tle.rcsSize }}</span></div>
                        }
                        @if (tle.country) {
                          <div class="field-row"><span class="field-label">Country</span><span class="field-value">{{ tle.country }}</span></div>
                        }
                        @if (tle.launchDate) {
                          <div class="field-row"><span class="field-label">Launched</span><span class="field-value mono">{{ tle.launchDate | date:'mediumDate' }}</span></div>
                        }
                      }
                    }
                    @case ('cot') {
                      @if (e.platformData?.cot; as cot) {
                        <div class="field-row"><span class="field-label">CoT Type</span><span class="field-value mono">{{ cot.cotType }}</span></div>
                        @if (cot.how) {
                          <div class="field-row"><span class="field-label">How</span><span class="field-value mono">{{ cot.how }}</span></div>
                        }
                        @if (cot.ce != null) {
                          <div class="field-row"><span class="field-label">CE</span><span class="field-value mono">{{ cot.ce | number:'1.1-1' }} m</span></div>
                        }
                        @if (cot.le != null) {
                          <div class="field-row"><span class="field-label">LE</span><span class="field-value mono">{{ cot.le | number:'1.1-1' }} m</span></div>
                        }
                        @if (cot.staleTime) {
                          <div class="field-row"><span class="field-label">Stale</span><span class="field-value mono">{{ cot.staleTime | date:'short' }}</span></div>
                        }
                        @if (cot.accessControl) {
                          <div class="field-row"><span class="field-label">Access</span><span class="field-value">{{ cot.accessControl }}</span></div>
                        }
                      }
                    }
                    @case ('link16') {
                      @if (e.platformData?.link16; as l16) {
                        <div class="field-row"><span class="field-label">J-Series</span><span class="field-value mono">{{ l16.jSeriesLabel }}</span></div>
                        @if (l16.originatingUnit) {
                          <div class="field-row"><span class="field-label">Orig Unit</span><span class="field-value">{{ l16.originatingUnit }}</span></div>
                        }
                        @if (l16.quality != null) {
                          <div class="field-row"><span class="field-label">Quality</span><span class="field-value mono">{{ l16.quality }}/15</span></div>
                        }
                        @if (l16.forceIdentity) {
                          <div class="field-row"><span class="field-label">Force ID</span><span class="field-value">{{ l16.forceIdentity }}</span></div>
                        }
                        @if (l16.exerciseIndicator) {
                          <div class="field-row"><span class="field-label">Exercise</span><span class="field-value">Yes</span></div>
                        }
                        @if (l16.simulationIndicator) {
                          <div class="field-row"><span class="field-label">Simulation</span><span class="field-value">Yes</span></div>
                        }
                      }
                    }
                    @case ('uav') {
                      @if (e.platformData?.uav; as uav) {
                        @if (uav.make) {
                          <div class="field-row"><span class="field-label">Make</span><span class="field-value">{{ uav.make }}</span></div>
                        }
                        @if (uav.model) {
                          <div class="field-row"><span class="field-label">Model</span><span class="field-value">{{ uav.model }}</span></div>
                        }
                        @if (uav.serialNumber) {
                          <div class="field-row"><span class="field-label">Serial</span><span class="field-value mono">{{ uav.serialNumber }}</span></div>
                        }
                      }
                    }
                  }
                </div>
              }
            </section>
          }
```

**Step 3: Verify build**

Run: `npx nx build web --skip-nx-cache 2>&1 | tail -5`
Expected: Build succeeds

**Commit:** `feat(web): add collapsible platform details section to entity detail panel`

---

## Task 7: Add collapsible Signal Quality section (ADS-B only)

**Files:**
- Modify: `apps/web/src/app/shared/components/entity-detail-panel.component.ts`

**Step 1: Add Signal Quality template**

Insert after the Platform Details section, before Source & Timing:

```html
          <!-- Signal Quality (ADS-B only, collapsible) -->
          @if (platformType() === 'adsb' && e.platformData?.adsb; as adsb) {
            @if (adsb.nacP != null || adsb.sil != null || adsb.nic != null) {
              <section class="section">
                <div class="collapsible-header" (click)="toggleSection('signalQuality')">
                  <h4 class="section-label" style="margin:0">Signal Quality</h4>
                  <span class="chevron" [class.expanded]="expandedSections()['signalQuality']">&#9654;</span>
                </div>
                @if (expandedSections()['signalQuality']) {
                  <div class="collapsible-content">
                    @if (adsb.nacP != null) {
                      <div class="field-row"><span class="field-label">NACp</span><span class="field-value mono">{{ adsb.nacP }}</span></div>
                    }
                    @if (adsb.nacV != null) {
                      <div class="field-row"><span class="field-label">NACv</span><span class="field-value mono">{{ adsb.nacV }}</span></div>
                    }
                    @if (adsb.sil != null) {
                      <div class="field-row"><span class="field-label">SIL</span><span class="field-value mono">{{ adsb.sil }}@if (adsb.silType) { ({{ adsb.silType }}) }</span></div>
                    }
                    @if (adsb.nic != null) {
                      <div class="field-row"><span class="field-label">NIC</span><span class="field-value mono">{{ adsb.nic }}</span></div>
                    }
                    @if (adsb.rc != null) {
                      <div class="field-row"><span class="field-label">RC</span><span class="field-value mono">{{ adsb.rc | number:'1.0-0' }} m</span></div>
                    }
                    @if (adsb.gva != null) {
                      <div class="field-row"><span class="field-label">GVA</span><span class="field-value mono">{{ adsb.gva }}</span></div>
                    }
                    @if (adsb.sda != null) {
                      <div class="field-row"><span class="field-label">SDA</span><span class="field-value mono">{{ adsb.sda }}</span></div>
                    }
                  </div>
                }
              </section>
            }
          }
```

**Step 2: Verify build**

Run: `npx nx build web --skip-nx-cache 2>&1 | tail -5`
Expected: Build succeeds

**Step 3: Run all tests**

Run: `npx nx test web`
Expected: All tests pass

**Commit:** `feat(web): add collapsible signal quality section for ADS-B entities`

---

## Verification

1. `npx nx build web --skip-nx-cache` — build succeeds with no errors
2. `npx nx test web` — all existing tests pass
3. Visual verification: Start the stack with `make dev`, select entities on the map:
   - AIS vessel: Identity shows MMSI, callsign, IMO; Vessel Details section expands to show ship type, nav status, destination, dimensions
   - ADS-B aircraft: Identity shows ICAO hex, registration, squawk; Aircraft Details and Signal Quality sections available
   - Satellite: Identity shows NORAD ID; Satellite Orbit section shows orbital elements
   - Entities without enriched data: Panel shows existing sections unchanged, new sections hidden
4. Collapsible sections: clicking chevron expands/collapses platform details and signal quality
5. Header badges: track environment chip and country code appear when data is present
