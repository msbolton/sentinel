# Federation Sidebar — Design Spec

## Overview

Consolidate all federation UI on the map into a single toggleable floating panel on the right side. The current federation badge (top-right), source legend (bottom-left), and federation layer toggles (inside the layer panel) are replaced by one unified component.

## Current State

Federation UI is scattered across three locations:
- **FederationStatusComponent** renders a badge (top-right) showing peer names/status, and a source legend (bottom-left) showing color keys
- **Layer panel** (left side) has a "Federation" section with per-peer visibility checkboxes
- These are independent — no single place to see the full federation picture

## Design

### Collapsed State (Badge)

When the panel is closed and federation is active, a compact badge appears in the top-right corner of the map:

- Shows "FED" label + one status dot per connected peer (green/yellow/red)
- Clickable — opens the expanded panel
- Hidden entirely when `federationActive()` is false (no connected peers)
- Styled to match existing map overlays (dark translucent background, border-radius, backdrop-filter)

### Expanded State (Floating Panel)

Clicking the badge opens a floating panel anchored to the top-right, containing three sections:

**1. Connected Peers**
- Section header: "CONNECTED PEERS" (uppercase, muted)
- One row per peer with:
  - Status dot (green=connected, yellow=stale, red=disconnected) with glow
  - Display name (primary text)
  - Entity count subtitle (e.g., "12 entities" or "Stale")
  - Color swatch (small rounded square showing the peer's assigned color)
- Stale/disconnected peers shown at reduced opacity

**2. Visibility**
- Section header: "VISIBILITY" (uppercase, muted)
- One checkbox row per peer with:
  - Checkbox (accent-color matching peer color)
  - Color swatch
  - Peer display name
- Toggling a checkbox calls `toggleFederationPeer(instanceId, event)` — same logic as the current layer panel toggle

**3. Source Legend**
- Section header: "SOURCE LEGEND" (uppercase, muted)
- Horizontal flex-wrap layout with color swatch + label for each source:
  - "Local" with blue (#3b82f6) swatch (always first)
  - One entry per connected peer with their assigned color

**Panel chrome:**
- Header bar with "Federation" title and close button (×)
- Close button and clicking the badge again both close the panel
- Width: ~260px
- Styled consistently: `rgba(10, 14, 23, 0.95)` background, `#333` border, `border-radius: 8px`, same font sizes as existing map controls

### What Gets Removed

- **FederationStatusComponent**: The entire component is deleted. Its badge and source legend are replaced by the new panel.
- **Layer panel federation section**: The `@if (federationService.federationActive())` block in `map.component.html` (the divider, "Federation" label, and per-peer checkboxes) is removed. Visibility controls move into the new panel.

## Component Architecture

### FederationPanelComponent (new)

- Standalone Angular component, `OnPush` change detection
- Selector: `app-federation-panel`
- Injected: `FederationService` (for peer data signals), no direct Cesium dependency
- Signals:
  - `panelOpen = signal(false)` — controls collapsed/expanded state
- Template outputs:
  - `(togglePeer)` — emits `{ instanceId: string, event: Event }` when a visibility checkbox changes
- Placed in `map.component.html` where `<app-federation-status />` currently sits
- MapComponent handles the `(togglePeer)` output by calling `toggleFederationPeer()`

### Changes to Existing Components

**MapComponent (`map.component.ts` / `.html`)**
- Replace `<app-federation-status />` with `<app-federation-panel (togglePeer)="toggleFederationPeer($event.instanceId, $event.event)" />`
- Remove the federation section from the layer panel template (divider, label, checkboxes)
- Remove `FederationStatusComponent` from imports array
- Add `FederationPanelComponent` to imports array

**FederationStatusComponent (`federation-status.component.ts`)**
- Delete the file entirely
- Delete `federation-status.component.spec.ts`

## Styling

All styles are inline in the component (matching existing pattern). Key values:
- Panel background: `rgba(10, 14, 23, 0.95)`
- Border: `1px solid #333`
- Border radius: `8px`
- Section headers: `9px`, uppercase, `letter-spacing: 1px`, color `#888`
- Peer rows: `rgba(255, 255, 255, 0.03)` background, `6px` border-radius
- Status dot sizes: `7px` in panel, `6px` in badge
- Matches the layer panel's visual weight and blur treatment

## Behavior

- **Federation inactive**: Nothing renders (same as current)
- **Federation active, panel closed**: Compact badge visible, clickable
- **Federation active, panel open**: Full panel with all three sections
- **Closing**: Click × or click badge again
- **Peer visibility**: Checkboxes control the same `toggleFederationPeer` logic already implemented in MapComponent
