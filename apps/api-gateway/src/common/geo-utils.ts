export interface Coordinate {
  latitude: number;
  longitude: number;
  altitudeMeters?: number;
}

export interface BoundingBox {
  north: number;
  south: number;
  east: number;
  west: number;
}

const EARTH_RADIUS_METERS = 6_371_000;

/** Convert degrees to radians */
export function toRadians(degrees: number): number {
  return (degrees * Math.PI) / 180;
}

/** Convert radians to degrees */
export function toDegrees(radians: number): number {
  return (radians * 180) / Math.PI;
}

/** Haversine distance between two coordinates in meters */
export function haversineDistance(a: Coordinate, b: Coordinate): number {
  const dLat = toRadians(b.latitude - a.latitude);
  const dLon = toRadians(b.longitude - a.longitude);
  const lat1 = toRadians(a.latitude);
  const lat2 = toRadians(b.latitude);

  const h =
    Math.sin(dLat / 2) * Math.sin(dLat / 2) +
    Math.cos(lat1) * Math.cos(lat2) * Math.sin(dLon / 2) * Math.sin(dLon / 2);

  return 2 * EARTH_RADIUS_METERS * Math.asin(Math.sqrt(h));
}

/** Check if a coordinate is within a bounding box */
export function isWithinBoundingBox(
  point: Coordinate,
  bbox: BoundingBox,
): boolean {
  return (
    point.latitude >= bbox.south &&
    point.latitude <= bbox.north &&
    point.longitude >= bbox.west &&
    point.longitude <= bbox.east
  );
}

/** Calculate bearing between two coordinates in degrees */
export function calculateBearing(from: Coordinate, to: Coordinate): number {
  const lat1 = toRadians(from.latitude);
  const lat2 = toRadians(to.latitude);
  const dLon = toRadians(to.longitude - from.longitude);

  const y = Math.sin(dLon) * Math.cos(lat2);
  const x =
    Math.cos(lat1) * Math.sin(lat2) -
    Math.sin(lat1) * Math.cos(lat2) * Math.cos(dLon);

  return (toDegrees(Math.atan2(y, x)) + 360) % 360;
}

/** Calculate a destination point given start, bearing, and distance */
export function destinationPoint(
  start: Coordinate,
  bearingDeg: number,
  distanceMeters: number,
): Coordinate {
  const lat1 = toRadians(start.latitude);
  const lon1 = toRadians(start.longitude);
  const bearing = toRadians(bearingDeg);
  const angularDist = distanceMeters / EARTH_RADIUS_METERS;

  const lat2 = Math.asin(
    Math.sin(lat1) * Math.cos(angularDist) +
      Math.cos(lat1) * Math.sin(angularDist) * Math.cos(bearing),
  );
  const lon2 =
    lon1 +
    Math.atan2(
      Math.sin(bearing) * Math.sin(angularDist) * Math.cos(lat1),
      Math.cos(angularDist) - Math.sin(lat1) * Math.sin(lat2),
    );

  return {
    latitude: toDegrees(lat2),
    longitude: toDegrees(lon2),
  };
}

/** Convert knots to meters per second */
export function knotsToMps(knots: number): number {
  return knots * 0.514444;
}

/** Convert meters per second to knots */
export function mpsToKnots(mps: number): number {
  return mps / 0.514444;
}

/** Expand a bounding box by a percentage buffer */
export function expandBoundingBox(
  bbox: BoundingBox,
  bufferPercent: number,
): BoundingBox {
  const latRange = bbox.north - bbox.south;
  const lonRange = bbox.east - bbox.west;
  const latBuffer = latRange * (bufferPercent / 100);
  const lonBuffer = lonRange * (bufferPercent / 100);

  return {
    north: Math.min(90, bbox.north + latBuffer),
    south: Math.max(-90, bbox.south - latBuffer),
    east: Math.min(180, bbox.east + lonBuffer),
    west: Math.max(-180, bbox.west - lonBuffer),
  };
}
