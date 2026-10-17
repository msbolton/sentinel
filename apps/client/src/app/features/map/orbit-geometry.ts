/**
 * Geometry for the selected satellite's overlays: the path it will trace
 * over its next revolution, the matching ground track, and the circle of
 * ground it can see. Pure functions over satellite.js element sets.
 */
import type { SatRec } from 'satellite.js';
import { positionAt, type SatPosition } from './satellite-tracker';

export const EARTH_RADIUS_M = 6_371_008.8;

/** Cesium's ellipse geometry degrades near a quarter circumference; GEO is ~9 040 km. */
export const MAX_FOOTPRINT_RADIUS_M = 9_000_000;

export interface OrbitSample extends SatPosition {
  atMs: number;
}

/**
 * Positions over the next revolution, n+1 samples from startMs. They are
 * Earth-fixed (the geodetic conversion applies sidereal time), so the
 * path is where the satellite will be relative to the ground, and the
 * satellite rides along it. Samples the orbit cannot produce are skipped.
 */
export function sampleOrbit(satrec: SatRec, startMs: number, periodMin: number, n = 120): OrbitSample[] {
  if (!(periodMin > 0) || n < 2) return [];
  const stepMs = (periodMin * 60_000) / n;
  const out: OrbitSample[] = [];
  for (let i = 0; i <= n; i++) {
    const atMs = startMs + i * stepMs;
    const pos = positionAt(satrec, atMs);
    if (pos) out.push({ ...pos, atMs });
  }
  return out;
}

/** Radius on the ground of the horizon circle seen from a height, capped for the renderer. */
export function footprintRadiusM(heightM: number): number {
  if (!(heightM > 0)) return 0;
  const r = EARTH_RADIUS_M * Math.acos(EARTH_RADIUS_M / (EARTH_RADIUS_M + heightM));
  return Math.min(r, MAX_FOOTPRINT_RADIUS_M);
}

/**
 * Split a sample run wherever consecutive longitudes jump across the
 * antimeridian, so a ground track drawn on the surface does not get a
 * segment wrapping the whole globe.
 */
export function splitAtAntimeridian<T extends { lon: number }>(samples: T[]): T[][] {
  const runs: T[][] = [];
  let current: T[] = [];
  for (const s of samples) {
    const prev = current[current.length - 1];
    if (prev && Math.abs(s.lon - prev.lon) > 180) {
      if (current.length > 1) runs.push(current);
      current = [];
    }
    current.push(s);
  }
  if (current.length > 1) runs.push(current);
  return runs;
}
