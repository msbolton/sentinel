/**
 * Satellites move seven kilometres a second and the server reports a fix
 * once a minute, so between fixes the client propagates them itself from
 * the two-line elements every satellite entity carries (platformData.tle).
 * This module is pure: no Cesium, no Angular, so it is unit-testable like
 * dead-reckoning.ts.
 */
import { degreesLat, degreesLong, eciToGeodetic, gstime, propagate, twoline2satrec, type SatRec } from 'satellite.js';
import { Entity, EntityType } from '../../shared/models/entity.model';

/** A propagated position: degrees and metres above the WGS84 ellipsoid. */
export interface SatPosition {
  lat: number;
  lon: number;
  height: number;
}

/** Parse two-line elements; null when the lines are malformed. */
export function parseTle(line1: string | undefined, line2: string | undefined): SatRec | null {
  if (!line1 || !line2 || line1.length < 69 || line2.length < 69) return null;
  try {
    const satrec = twoline2satrec(line1, line2);
    if (!satrec || satrec.error !== 0 || !Number.isFinite(satrec.no)) return null;
    return satrec;
  } catch {
    return null;
  }
}

/** The satellite's geodetic position at a time, or null when SGP4 fails (decayed orbit). */
export function positionAt(satrec: SatRec, atMs: number): SatPosition | null {
  const date = new Date(atMs);
  let pv: ReturnType<typeof propagate> | null;
  try {
    pv = propagate(satrec, date);
  } catch {
    return null;
  }
  if (!pv || !pv.position || typeof pv.position === 'boolean') return null;
  const eci = pv.position;
  if (!Number.isFinite(eci.x) || !Number.isFinite(eci.y) || !Number.isFinite(eci.z)) return null;
  const geo = eciToGeodetic(eci, gstime(date));
  const lat = degreesLat(geo.latitude);
  const lon = degreesLong(geo.longitude);
  const height = geo.height * 1000;
  if (!Number.isFinite(lat) || !Number.isFinite(lon) || !Number.isFinite(height)) return null;
  return { lat, lon, height };
}

/** Orbital period in minutes from the element set. */
export function periodMinutes(satrec: SatRec): number {
  // satrec.no is the mean motion in radians per minute.
  return satrec.no > 0 ? (2 * Math.PI) / satrec.no : 0;
}

interface Tracked {
  satrec: SatRec;
  line1: string;
  line2: string;
}

/**
 * Keeps one parsed element set per satellite entity and propagates them on
 * demand. Entities that are not satellites, or carry no elements, are not
 * tracked.
 */
export class SatelliteTracker {
  private readonly tracked = new Map<string, Tracked>();

  constructor(readonly maxTracked = 3000) {}

  /** Track or refresh an entity; returns whether it is tracked afterwards. */
  upsert(entity: Entity): boolean {
    if (entity.entityType !== EntityType.SATELLITE) {
      this.tracked.delete(entity.id);
      return false;
    }
    const tle = entity.platformData?.['tle'] as { line1?: string; line2?: string } | undefined;
    const line1 = tle?.line1;
    const line2 = tle?.line2;
    if (!line1 || !line2) {
      this.tracked.delete(entity.id);
      return false;
    }
    const existing = this.tracked.get(entity.id);
    if (existing && existing.line1 === line1 && existing.line2 === line2) {
      return true;
    }
    if (!existing && this.tracked.size >= this.maxTracked) {
      return false;
    }
    const satrec = parseTle(line1, line2);
    if (!satrec) {
      this.tracked.delete(entity.id);
      return false;
    }
    this.tracked.set(entity.id, { satrec, line1, line2 });
    return true;
  }

  remove(id: string): void {
    this.tracked.delete(id);
  }

  clear(): void {
    this.tracked.clear();
  }

  has(id: string): boolean {
    return this.tracked.has(id);
  }

  get size(): number {
    return this.tracked.size;
  }

  satrecOf(id: string): SatRec | undefined {
    return this.tracked.get(id)?.satrec;
  }

  positionOf(id: string, nowMs: number): SatPosition | null {
    const t = this.tracked.get(id);
    return t ? positionAt(t.satrec, nowMs) : null;
  }

  /**
   * Every tracked satellite's position at a time. A satellite whose orbit
   * no longer propagates is dropped so it is not retried every step.
   */
  step(nowMs: number): Array<[string, SatPosition]> {
    const out: Array<[string, SatPosition]> = [];
    for (const [id, t] of this.tracked) {
      const pos = positionAt(t.satrec, nowMs);
      if (pos) out.push([id, pos]);
      else this.tracked.delete(id);
    }
    return out;
  }
}
