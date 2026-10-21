import { Entity, EntityType, TrackEnvironment } from '../../shared/models/entity.model';

/**
 * How an entity sits against the ground on the globe.
 *
 *  - `absolute`: `heightM` metres above the WGS84 ellipsoid;
 *  - `clamp`: on the terrain surface, `heightM` is 0 and unused;
 *  - `relative`: `heightM` metres above the terrain surface.
 */
export type HeightMode = { kind: 'absolute' | 'clamp' | 'relative'; heightM: number };

/**
 * The height rule for drawing an entity, first match wins:
 *  1. a radar height above ground: relative to the terrain at that height;
 *  2. a GROUND track: clamped to the terrain;
 *  3. an altitude above zero: absolute (ellipsoidal, as every altitude is);
 *  4. anything else: clamped.
 */
export function heightModeOf(e: Entity): HeightMode {
  const aboveGround = e.platformData?.radar?.heightAboveGroundM;
  if (aboveGround != null && Number.isFinite(aboveGround)) {
    return { kind: 'relative', heightM: aboveGround };
  }
  if (e.trackEnvironment === TrackEnvironment.GROUND) {
    return { kind: 'clamp', heightM: 0 };
  }
  const altitude = e.position?.altitude;
  if (altitude != null && Number.isFinite(altitude) && altitude > 0) {
    return { kind: 'absolute', heightM: altitude };
  }
  return { kind: 'clamp', heightM: 0 };
}

/**
 * Whether the entity gets a horizontal uncertainty ellipse: a SIGNAL, a radar
 * track or an EO/IR detection that reports a position covariance.
 */
export function drawsUncertainty(e: Entity): boolean {
  if (!e.kinematics?.positionCovariance) return false;
  return e.entityType === EntityType.SIGNAL || e.platformData?.radar != null || e.platformData?.eoir != null;
}
