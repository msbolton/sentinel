import { DensityGrid } from '../../shared/models/density.model';
import { ViewportBounds, isGlobalBounds } from './viewport-bounds';

/**
 * Pure helpers for the vessel density overlay: whether a grid applies to
 * the current view, how its cells merge into discs that stay readable at
 * the view's scale, and how counts are written. No Cesium.
 */

/** A grid older than this is ignored: the feed stopped sending or the view shrank. */
export const DENSITY_FRESH_MS = 60_000;

/** Roughly how many discs fit across the view. */
const BUCKETS_ACROSS = 12;
const BUCKET_STEPS_DEG = [1, 2, 5, 10, 15, 30, 45, 90];

/** One disc on the map: the count-weighted centre of the cells merged into it. */
export interface DensityBucket {
  lat: number;
  lon: number;
  count: number;
}

/** Longitude span of a view, handling views that wrap across the antimeridian. */
export function lonSpan(b: ViewportBounds): number {
  return b.east >= b.west ? b.east - b.west : 360 - (b.west - b.east);
}

/** Area of a view in square degrees, the unit the feed's cap is in. */
export function viewAreaSqDeg(b: ViewportBounds): number {
  if (isGlobalBounds(b)) return 180 * 360;
  return Math.max(0, b.north - b.south) * lonSpan(b);
}

/** True when the grid should be drawn for this view: fresh, and the view is over the cap. */
export function densityApplies(grid: DensityGrid | null, receivedAtMs: number, view: ViewportBounds | null, nowMs: number): boolean {
  if (!grid || !view) return false;
  if (nowMs - receivedAtMs > DENSITY_FRESH_MS) return false;
  return grid.capSquareDegrees > 0 && viewAreaSqDeg(view) > grid.capSquareDegrees;
}

/** The bucket size for a view: about BUCKETS_ACROSS across it, never finer than the grid. */
export function bucketDegrees(view: ViewportBounds, cellDegrees: number): number {
  const want = Math.max(cellDegrees, lonSpan(view) / BUCKETS_ACROSS);
  return BUCKET_STEPS_DEG.find((s) => s >= want) ?? 90;
}

function inView(lat: number, lon: number, v: ViewportBounds): boolean {
  if (lat < v.south || lat > v.north) return false;
  return v.east >= v.west ? lon >= v.west && lon <= v.east : lon >= v.west || lon <= v.east;
}

/** Merges the grid's cells inside the view into buckets of `sizeDeg`. */
export function bucketCells(cells: [number, number, number][], view: ViewportBounds, sizeDeg: number): DensityBucket[] {
  const sums = new Map<string, { lat: number; lon: number; count: number }>();
  for (const [lat, lon, count] of cells) {
    if (count <= 0 || !inView(lat, lon, view)) continue;
    const key = `${Math.floor(lat / sizeDeg)}:${Math.floor(lon / sizeDeg)}`;
    const s = sums.get(key) ?? { lat: 0, lon: 0, count: 0 };
    s.lat += lat * count;
    s.lon += lon * count;
    s.count += count;
    sums.set(key, s);
  }
  return [...sums.values()].map((s) => ({ lat: s.lat / s.count, lon: s.lon / s.count, count: s.count }));
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
