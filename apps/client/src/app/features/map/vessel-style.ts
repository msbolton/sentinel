import { Entity, EntityType } from '../../shared/models/entity.model';
import { VESSEL_CATEGORY_COLORS, vesselCategory } from '../../shared/vessel-format';

/** Below this a vessel is treated as stopped: its course says nothing about its bow. */
export const VESSEL_MOVING_KNOTS = 0.5;

/** AIS "heading not available". */
const NO_HEADING = 511;

/** The pin colour for a vessel, from its AIS ship type. */
export function vesselColor(entity: Entity): string {
  const ais = entity.platformData?.ais;
  return VESSEL_CATEGORY_COLORS[vesselCategory(ais?.shipType)];
}

/**
 * The direction a vessel's icon points, in degrees, or null to leave it
 * unrotated. A moving vessel points along its heading (the true heading, or
 * the course when the transponder sends none); a stopped one only when it
 * reports a true heading, since its course is noise.
 */
export function vesselHeading(entity: Entity): number | null {
  if (entity.entityType !== EntityType.VESSEL || entity.heading == null || !Number.isFinite(entity.heading)) return null;
  const moving = (entity.speedKnots ?? 0) >= VESSEL_MOVING_KNOTS;
  const trueHeading = entity.platformData?.ais?.trueHeading;
  const hasTrueHeading = trueHeading != null && trueHeading !== NO_HEADING;
  return moving || hasTrueHeading ? entity.heading : null;
}
