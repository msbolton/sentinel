import { formatAltitude } from '../../shared/geo-format';
import { Entity, EntityType } from '../../shared/models/entity.model';

export interface GeoPoint {
  lat: number;
  lon: number;
  /** Metres above the ellipsoid. */
  height: number;
}

/** Types that fly: the drop line applies to these. */
export const AIRBORNE_TYPES = new Set<string>([EntityType.AIRCRAFT, EntityType.DRONE, EntityType.SATELLITE]);

/**
 * Whether the selected entity gets a drop line. Aircraft and drones need a
 * fix above the ground; a satellite always qualifies, since its position
 * comes from the propagator rather than a stored fix.
 */
export function showsDropLine(entity: Entity): boolean {
  if (!AIRBORNE_TYPES.has(entity.entityType)) return false;
  if (entity.entityType === EntityType.SATELLITE) return true;
  return (entity.position?.altitude ?? 0) > 0;
}

/**
 * The geometry of the line that shows how high something flies: from the
 * entity straight down to the point beneath it, with the altitude written
 * at the midpoint. `groundHeight` is the terrain height under the entity in
 * metres above the ellipsoid; it is 0 over the plain ellipsoid.
 */
export function dropLinePoints(
  pos: GeoPoint,
  groundHeight = 0,
): { top: GeoPoint; ground: GeoPoint; mid: GeoPoint; label: string } {
  const ground = Number.isFinite(groundHeight) ? groundHeight : 0;
  return {
    top: { lat: pos.lat, lon: pos.lon, height: pos.height },
    ground: { lat: pos.lat, lon: pos.lon, height: ground },
    mid: { lat: pos.lat, lon: pos.lon, height: (pos.height + ground) / 2 },
    label: formatAltitude(pos.height),
  };
}

/** Appearance of the drop line and its label: the yellow accent, dashed. */
export const DROP_LINE_CONFIG = {
  color: '#f59e0b',
  width: 1.5,
  dashLength: 12,
  labelFont: '11px JetBrains Mono, monospace',
};
