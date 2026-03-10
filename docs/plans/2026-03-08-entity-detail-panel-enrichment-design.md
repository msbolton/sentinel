# Entity Detail Panel Enrichment — Design

## Overview

Enhance the existing entity detail panel to display enriched data from the data model overhaul: protocol-specific platform data, kinematic state, operational status, identity details, and measurement quality. All new sections degrade gracefully when data is absent.

## Dependency

Requires the data model overhaul (PR #19) to be merged so the backend serves enriched fields via Kafka events and API responses.

## Frontend Entity Model Changes

Extend `Entity` interface in `entity.model.ts` with all new optional fields:

- `trackEnvironment`, `trackProcessingState` — track context enums
- `affiliation`, `identityConfidence`, `characterization` — identity disposition
- `operationalStatus`, `damageAssessment` — operational state
- `countryOfOrigin` — ISO 3166-1 alpha-2
- `sourceEntityId` — protocol-assigned ID (MMSI, ICAO hex, NORAD, etc.)
- `circularError` — CEP in meters
- `dimensionLength`, `dimensionWidth`, `dimensionHeight` — physical dimensions in meters
- `platformData` — discriminated union: `{ ais?, adsb?, tle?, link16?, cot?, uav? }`
- `kinematics` — velocity, acceleration, covariance matrices
- `orientation` — yaw/pitch/roll

Platform data interfaces (AISData, ADSBData, TLEData, Link16Data, CoTData, UAVData) defined in a new `platform-data.model.ts` file mirroring the shared-models types.

## Panel Sections (revised order, top to bottom)

### 1. Header (enhanced)
- Existing: entity name, type chip, close/fly-to buttons
- **Add**: country flag emoji + ISO code badge (from `countryOfOrigin`), track environment chip (AIR/SEA_SURFACE/GROUND/SPACE)

### 2. Identity (new section, always visible when data present)
- **Primary ID**: Protocol-specific — MMSI for AIS, ICAO hex for ADS-B, NORAD ID for TLE, CoT UID, Link16 JTN
- **Secondary IDs**: callsign, registration/tail number, squawk code, IMO number — each as labeled value
- **Affiliation**: Colored chip (FRIENDLY=blue, HOSTILE=red, NEUTRAL=green, UNKNOWN=gray)
- **Source Entity ID**: Raw `sourceEntityId` value

### 3. Classification (existing, unchanged)

### 4. Position (enhanced)
- Existing: lat, lon, altitude
- **Add**: circular error display (e.g., "CEP: 10m"), orientation (yaw/pitch/roll) when available

### 5. Movement (enhanced)
- Existing: speed, heading, course
- **Add**: velocity vector (N/E/Up components in m/s) if present in kinematics

### 6. Operational Status (new section, visible only when set)
- Status chip: OPERATIONAL (green), DEGRADED (yellow), DAMAGED (orange), DESTROYED (red), INACTIVE (gray)
- Damage assessment chip if present
- Physical dimensions (L x W x H) if present

### 7. Platform Details (new section, collapsed by default)
Collapsible section with chevron toggle. Content varies by protocol:

- **Vessel Details** (AIS): vessel name, ship type, nav status, destination, ETA, draught, dimensions (A/B/C/D), rate of turn, SOG, COG, true heading, position accuracy, message type
- **Aircraft Details** (ADS-B): registration, aircraft type/name, operator, squawk, emergency status, Mode 1/2, aircraft ID, altitudes (baro/geom), vertical rate, ground status, IAS/TAS/ground speed, magnetic heading, emitter category
- **Satellite Orbit** (TLE): NORAD ID, intl designator, TLE epoch, inclination, eccentricity, RAAN, arg of perigee, mean anomaly, mean motion, period, apogee/perigee altitudes, object type, RCS size, launch/decay dates
- **CoT Data**: UID, CoT type, how, CE/LE, stale time, access control
- **Link16 Data**: track number, J-series label, originating unit, quality, exercise/simulation flags

### 8. Signal Quality (new section, collapsed by default, ADS-B only)
- NACp (Navigation Accuracy Category - Position)
- NACv (Velocity)
- SIL (Source Integrity Level) + type
- NIC (Navigation Integrity Category)
- RC (Containment Radius)
- GVA (Geometric Vertical Accuracy)
- SDA (System Design Assurance)

### 9. Source & Timing (existing, enhanced)
- Existing: source badge, timestamps
- **Add**: `lastObservationSource` if present

### 10. Affiliations (existing, unchanged)
### 11. Description (existing, unchanged)
### 12. Metadata (existing, unchanged — catches any remaining untyped data)

## Collapsible Section Pattern

Use Angular signals for expand/collapse state:
```typescript
expandedSections = signal<Record<string, boolean>>({
  platformDetails: false,
  signalQuality: false,
});

toggleSection(key: string) {
  this.expandedSections.update(s => ({ ...s, [key]: !s[key] }));
}
```

Section header: label + chevron icon that rotates on toggle. Content area with `@if (expandedSections()[key])` conditional rendering.

## Computed Signals

```typescript
platformType = computed(() => {
  const pd = this.entity()?.platformData;
  if (pd?.ais) return 'ais';
  if (pd?.adsb) return 'adsb';
  if (pd?.tle) return 'tle';
  if (pd?.link16) return 'link16';
  if (pd?.cot) return 'cot';
  if (pd?.uav) return 'uav';
  return null;
});

primaryId = computed(() => {
  const pd = this.entity()?.platformData;
  if (pd?.ais) return { label: 'MMSI', value: pd.ais.mmsi };
  if (pd?.adsb) return { label: 'ICAO', value: pd.adsb.icaoHex };
  if (pd?.tle) return { label: 'NORAD', value: String(pd.tle.noradId) };
  if (pd?.cot) return { label: 'UID', value: pd.cot.uid };
  if (pd?.link16) return { label: 'JTN', value: String(pd.link16.trackNumber) };
  return null;
});
```

## Visual Style

- Consistent with existing panel design (backdrop blur, CSS variables, subtle dividers)
- Collapsible headers: `cursor: pointer`, subtle hover effect, chevron rotation transition
- Data labels: `color: var(--text-muted)`, values: `color: var(--text-primary)`
- Status chips use semantic colors matching existing classification/type chips pattern

## Files

- **Create**: `apps/web/src/app/shared/models/platform-data.model.ts` — platform data interfaces
- **Modify**: `apps/web/src/app/shared/models/entity.model.ts` — add enriched fields
- **Modify**: `apps/web/src/app/shared/components/entity-detail-panel.component.ts` — add all new sections
- **Modify**: `apps/web/src/app/features/map/map.component.ts` — ensure enriched fields flow through entity updates
