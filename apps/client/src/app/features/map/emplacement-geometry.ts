import { LngLat } from '../../shared/models/geofence.model';
import { EmplacementCoverage } from '../../shared/models/emplacement.model';
import { circlePolygon, destination } from './geofence-geometry';

/** A sector's arcs get a point every this many degrees. */
const ARC_STEP_DEG = 2;
/** Points on an omni coverage's circles. */
const CIRCLE_POINTS = 90;

export function hasCoverage(c: EmplacementCoverage | null | undefined): c is EmplacementCoverage {
  return !!c && c.maxRangeM > 0 && c.azimuthWidthDeg > 0;
}

/**
 * The ground footprint of a coverage, as unclosed rings of [lng, lat]. A
 * sector is one ring: the outer arc clockwise from boresight − width/2,
 * then back along the inner arc, or to the apex when minRangeM is 0. Omni
 * coverage is a circle, or [outer, inner] when there is a minimum range so
 * the caller can cut the hole. Points are geodesic, so a footprint across
 * the antimeridian keeps every point at range.
 */
export function coverageRings(center: LngLat, boresightDeg: number, c: EmplacementCoverage): LngLat[][] {
  if (c.azimuthWidthDeg >= 360) {
    const outer = circlePolygon(center, c.maxRangeM, CIRCLE_POINTS);
    return c.minRangeM > 0 ? [outer, circlePolygon(center, c.minRangeM, CIRCLE_POINTS)] : [outer];
  }
  const start = boresightDeg - c.azimuthWidthDeg / 2;
  const steps = Math.max(2, Math.ceil(c.azimuthWidthDeg / ARC_STEP_DEG));
  const arc = (rangeM: number): LngLat[] =>
    Array.from({ length: steps + 1 }, (_, i) => destination(center, start + (c.azimuthWidthDeg * i) / steps, rangeM));
  const inner = c.minRangeM > 0 ? arc(c.minRangeM).reverse() : [center];
  return [[...arc(c.maxRangeM), ...inner]];
}
