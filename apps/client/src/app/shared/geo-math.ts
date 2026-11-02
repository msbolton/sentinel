import type { ViewportBounds } from '../features/map/viewport-bounds';

export interface LatLon {
  lat: number;
  lon: number;
}

const EARTH_RADIUS_M = 6371008.8;
const METRES_PER_NM = 1852;
const NM_PER_DEGREE_AT_EQUATOR = 60;

const toRad = (deg: number): number => (deg * Math.PI) / 180;
const toDeg = (rad: number): number => (rad * 180) / Math.PI;

/** Initial great-circle bearing from one point to another, in degrees [0, 360). */
export function bearingDeg(from: LatLon, to: LatLon): number {
  const phi1 = toRad(from.lat);
  const phi2 = toRad(to.lat);
  const dLambda = toRad(to.lon - from.lon);
  const y = Math.sin(dLambda) * Math.cos(phi2);
  const x = Math.cos(phi1) * Math.sin(phi2) - Math.sin(phi1) * Math.cos(phi2) * Math.cos(dLambda);
  return (toDeg(Math.atan2(y, x)) + 360) % 360;
}

/** Great-circle (haversine) distance between two points, in nautical miles. */
export function rangeNm(from: LatLon, to: LatLon): number {
  const phi1 = toRad(from.lat);
  const phi2 = toRad(to.lat);
  const dPhi = phi2 - phi1;
  const dLambda = toRad(to.lon - from.lon);
  const a = Math.sin(dPhi / 2) ** 2 + Math.cos(phi1) * Math.cos(phi2) * Math.sin(dLambda / 2) ** 2;
  const metres = 2 * EARTH_RADIUS_M * Math.asin(Math.min(1, Math.sqrt(a)));
  return metres / METRES_PER_NM;
}

/** East-west extent of a viewport in nautical miles, measured at its mid-latitude. */
export function viewWidthNm(bounds: ViewportBounds): number {
  let degrees = bounds.east - bounds.west;
  if (bounds.east < bounds.west) degrees += 360;
  const midLat = toRad((bounds.north + bounds.south) / 2);
  return degrees * NM_PER_DEGREE_AT_EQUATOR * Math.cos(midLat);
}

/** True when the point lies inside the viewport bounds; a view whose west edge is east of its east edge crosses the antimeridian. */
export function inBounds(p: LatLon, b: ViewportBounds): boolean {
  if (p.lat < b.south || p.lat > b.north) return false;
  return b.west <= b.east ? p.lon >= b.west && p.lon <= b.east : p.lon >= b.west || p.lon <= b.east;
}
