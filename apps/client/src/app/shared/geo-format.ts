import { forward } from 'mgrs';

/** A coordinate as an operator reads it: four decimals and a hemisphere letter. */
export function formatCoordinate(value: number, axis: 'lat' | 'lon'): string {
  const hemisphere = axis === 'lat' ? (value < 0 ? 'S' : 'N') : value < 0 ? 'W' : 'E';
  return `${Math.abs(value).toFixed(4)}° ${hemisphere}`;
}

/**
 * A height above the ellipsoid as an operator reads it: whole metres up to
 * ten kilometres, then kilometres (one decimal under a hundred). A dash
 * stands in for a value that is missing.
 */
export function formatAltitude(metres: number | null | undefined): string {
  if (metres == null || !Number.isFinite(metres)) return '—';
  if (metres < 10_000) return `${Math.round(metres)} m`;
  const km = metres / 1000;
  return km < 100 ? `${km.toFixed(1)} km` : `${Math.round(km)} km`;
}

/**
 * A position as a Military Grid Reference at 10 m precision, grouped for
 * reading (`38P MC 7118 3306`). A dash stands in for a position outside the
 * UTM bands or one the converter rejects.
 */
export function formatMgrs(lat: number, lon: number): string {
  if (!Number.isFinite(lat) || !Number.isFinite(lon) || Math.abs(lat) > 84) return '—';
  try {
    const raw = forward([lon, lat], 4);
    const match = /^(\d{1,2}[A-Z])([A-Z]{2})(\d{4})(\d{4})$/.exec(raw);
    return match ? `${match[1]} ${match[2]} ${match[3]} ${match[4]}` : '—';
  } catch {
    return '—';
  }
}
