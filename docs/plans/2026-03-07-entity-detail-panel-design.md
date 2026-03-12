# Entity Detail Panel — Design

## Overview

Replace the existing bottom-center entity popup with a full-height floating right panel that slides in when an entity is clicked on the map.

## Component

New standalone `EntityDetailPanelComponent` at `apps/web/src/app/shared/components/entity-detail-panel.component.ts`.

## Layout & Positioning

- `position: absolute; top: 0; right: 0; height: calc(100% - var(--status-bar-height))`
- Width: `380px` (existing `--panel-width` CSS variable)
- `z-index: 100`
- Background: `var(--bg-panel)` with `backdrop-filter: blur(20px)`
- Border-left: `1px solid var(--border-color)`
- Slide animation: `transform: translateX(100%) -> translateX(0)` over 250ms

## Panel Sections (top to bottom)

1. **Header** — Entity name, type chip (colored by entity type), close button (X)
2. **Classification** — Classification badge
3. **Position** — Lat, lon, altitude (with units)
4. **Movement** — Speed (knots), heading (degrees), course (degrees)
5. **Source & Timing** — Source badge, created at, updated at, last seen at (relative timestamps)
6. **Affiliations** — Affiliation chips (hidden if empty)
7. **Metadata** — Key-value table from metadata record (hidden if empty)

Sections with no data are hidden. Panel scrolls vertically if content exceeds height. Sections separated by subtle `var(--border-color)` dividers.

## Interaction

- **Open**: Entity clicked on map -> panel slides in
- **Close**: X button or clicking empty map space
- **Swap**: Clicking different entity updates content in place (no close/reopen)
- **Fly To**: Button in header to fly camera to entity

## Data Flow

Map component's existing `selectedEntity` signal drives the panel. Panel receives it as an input. When non-null, panel is visible.

## Files

- **Create**: `apps/web/src/app/shared/components/entity-detail-panel.component.ts`
- **Modify**: `apps/web/src/app/features/map/map.component.html` — remove bottom popup, add panel
- **Modify**: `apps/web/src/app/features/map/map.component.ts` — remove popup code, expose flyToEntity for panel
