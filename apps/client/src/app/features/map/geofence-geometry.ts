import { LngLat } from '../../shared/models/geofence.model';

/** Mean Earth radius in metres (IUGG). */
const EARTH_RADIUS_M = 6_371_008.8;

const toRad = (deg: number) => (deg * Math.PI) / 180;
const toDeg = (rad: number) => (rad * 180) / Math.PI;

/** Great-circle distance between two positions, in metres (haversine). */
export function geodesicDistanceM(a: LngLat, b: LngLat): number {
  const [lng1, lat1] = a.map(toRad);
  const [lng2, lat2] = b.map(toRad);
  const dLat = lat2 - lat1;
  const dLng = lng2 - lng1;
  const h = Math.sin(dLat / 2) ** 2 + Math.cos(lat1) * Math.cos(lat2) * Math.sin(dLng / 2) ** 2;
  return 2 * EARTH_RADIUS_M * Math.asin(Math.min(1, Math.sqrt(h)));
}

/** The point `distanceM` from `from` along `bearingDeg`. */
function destination(from: LngLat, bearingDeg: number, distanceM: number): LngLat {
  const [lng1, lat1] = from.map(toRad);
  const brg = toRad(bearingDeg);
  const d = distanceM / EARTH_RADIUS_M;
  const lat2 = Math.asin(Math.sin(lat1) * Math.cos(d) + Math.cos(lat1) * Math.sin(d) * Math.cos(brg));
  const lng2 = lng1 + Math.atan2(Math.sin(brg) * Math.sin(d) * Math.cos(lat1), Math.cos(d) - Math.sin(lat1) * Math.sin(lat2));
  return [((toDeg(lng2) + 540) % 360) - 180, toDeg(lat2)];
}

/** A circle around `center` as an unclosed ring of `points` geodesic points. */
export function circlePolygon(center: LngLat, radiusM: number, points = 64): LngLat[] {
  const ring: LngLat[] = [];
  for (let i = 0; i < points; i++) ring.push(destination(center, (360 * i) / points, radiusM));
  return ring;
}

/** The four corners of the rectangle spanned by two opposite corners, starting at `a`. */
export function rectanglePolygon(a: LngLat, b: LngLat): LngLat[] {
  return [
    [a[0], a[1]],
    [a[0], b[1]],
    [b[0], b[1]],
    [b[0], a[1]],
  ];
}

/** Area of a ring on the sphere, in square metres (spherical excess). */
export function polygonAreaM2(ring: LngLat[]): number {
  if (ring.length < 3) return 0;
  let sum = 0;
  for (let i = 0; i < ring.length; i++) {
    const [lng1, lat1] = ring[i].map(toRad);
    const [lng2, lat2] = ring[(i + 1) % ring.length].map(toRad);
    sum += (lng2 - lng1) * (2 + Math.sin(lat1) + Math.sin(lat2));
  }
  return Math.abs((sum * EARTH_RADIUS_M * EARTH_RADIUS_M) / 2);
}

/** Whether any edge jumps more than 180° of longitude, i.e. wraps the antimeridian. */
export function crossesAntimeridian(ring: LngLat[]): boolean {
  for (let i = 1; i < ring.length; i++) {
    if (Math.abs(ring[i][0] - ring[i - 1][0]) > 180) return true;
  }
  return false;
}

/** How many of the ring's points differ from one another. */
export function distinctPoints(ring: LngLat[]): number {
  return new Set(ring.map(([lng, lat]) => `${lng},${lat}`)).size;
}

/** `850 m`, `3.8 km`, `142 km`. */
export function formatDistance(m: number): string {
  if (m < 1000) return `${Math.round(m)} m`;
  const km = m / 1000;
  return km < 100 ? `${km.toFixed(1)} km` : `${Math.round(km)} km`;
}

/** `0.4 km²`, `142 km²`. */
export function formatArea(m2: number): string {
  const km2 = m2 / 1e6;
  return km2 < 10 ? `${km2.toFixed(1)} km²` : `${Math.round(km2)} km²`;
}

/** `f97316` or `#F97316` → `#f97316`; anything else null. */
export function normalizeHex(input: string): string | null {
  const m = /^#?([0-9a-fA-F]{6})$/.exec(input.trim());
  return m ? `#${m[1].toLowerCase()}` : null;
}
