import { ViewportBounds, isGlobalBounds } from './viewport-bounds';

/**
 * Pure helpers for wide views: whether a view is wider than the gateway's
 * detail cap, how fine a grid to ask the entity service for, and how counts
 * are drawn. No Cesium.
 */

/** Roughly how many cells fit across the view. */
const CELLS_ACROSS = 12;
const CELL_STEPS_DEG = [0.5, 1, 2, 5, 10, 15, 30];

/** Longitude span of a view, handling views that wrap across the antimeridian. */
export function lonSpan(b: ViewportBounds): number {
  return b.east >= b.west ? b.east - b.west : 360 - (b.west - b.east);
}

/** Area of a view in square degrees, the unit the cap is in. */
export function viewAreaSqDeg(b: ViewportBounds): number {
  if (isGlobalBounds(b)) return 180 * 360;
  return Math.max(0, b.north - b.south) * lonSpan(b);
}

/** True when the view is wider than the cap: the map draws counts instead of objects. */
export function isWideView(view: ViewportBounds | null, capSqDeg: number): boolean {
  return !!view && capSqDeg > 0 && viewAreaSqDeg(view) > capSqDeg;
}

/** The grid to ask for: about CELLS_ACROSS across the view. */
export function densityCellDegrees(view: ViewportBounds): number {
  const want = lonSpan(view) / CELLS_ACROSS;
  return CELL_STEPS_DEG.find((s) => s >= want) ?? 30;
}

/** A count as a disc shows it: 950, 1.2k, 12k. */
export function densityLabel(count: number): string {
  if (count < 1000) return String(count);
  if (count < 10_000) return `${(count / 1000).toFixed(1).replace(/\.0$/, '')}k`;
  return `${Math.round(count / 1000)}k`;
}

/** Disc edge in px: grows with the count on a log scale. */
export function densityDiscSize(count: number): number {
  return Math.round(Math.min(60, 34 + 6 * Math.log10(Math.max(1, count))));
}

/**
 * Discs of different types in the same cell sit side by side: each type
 * gets its own horizontal pixel offset.
 */
const TYPE_OFFSETS: Record<string, number> = { AIRCRAFT: -22, VESSEL: 0, SATELLITE: 22 };
export function densityOffsetPx(entityType: string): number {
  return TYPE_OFFSETS[entityType] ?? 0;
}

/** `#rrggbb` as `rgba(r, g, b, alpha)`. */
export function withAlpha(hex: string, alpha: number): string {
  const m = /^#?([0-9a-f]{2})([0-9a-f]{2})([0-9a-f]{2})$/i.exec(hex);
  if (!m) return hex;
  return `rgba(${parseInt(m[1], 16)}, ${parseInt(m[2], 16)}, ${parseInt(m[3], 16)}, ${alpha})`;
}
