import type { DemoBounds } from '../../core/services/demo.service';
import { Emplacement, emplacementIdOf } from '../../shared/models/emplacement.model';

/**
 * A demo guest's view of the map. The server only ever holds the visitor's
 * blurred position, so their own browser draws their site at the precise spot
 * and hides the blurred copy the server sends back, both in the emplacement
 * list and as its SENSOR entity on the stream.
 */

/** The emplacements less the visitor's own; the list itself when there is none. */
export function withoutOwnSite(list: Emplacement[], ownId: string | null): Emplacement[] {
  return ownId == null ? list : list.filter((e) => e.id !== ownId);
}

/** Whether a stream entity is the SENSOR entity of the visitor's own site. */
export function isOwnSiteEntity(entity: { identities?: { type: string; value: string }[] | null }, ownId: string | null): boolean {
  return ownId != null && emplacementIdOf(entity) === ownId;
}

/** The box around the points, padded by `padDeg` and kept on the globe; null for none. */
export function boundsOf(points: { lat: number; lon: number }[], padDeg = 0.5): DemoBounds | null {
  if (points.length === 0) return null;
  let west = Infinity;
  let south = Infinity;
  let east = -Infinity;
  let north = -Infinity;
  for (const p of points) {
    west = Math.min(west, p.lon);
    east = Math.max(east, p.lon);
    south = Math.min(south, p.lat);
    north = Math.max(north, p.lat);
  }
  return {
    west: Math.max(-180, west - padDeg),
    south: Math.max(-90, south - padDeg),
    east: Math.min(180, east + padDeg),
    north: Math.min(90, north + padDeg),
  };
}
