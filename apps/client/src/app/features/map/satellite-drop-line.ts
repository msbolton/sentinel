import { formatAltitude } from '../../shared/geo-format';

export interface GeoPoint {
  lat: number;
  lon: number;
  /** Metres above the ellipsoid. */
  height: number;
}

/**
 * The geometry of the line that shows how high a satellite sits: from the
 * satellite straight down to the point beneath it, with the altitude
 * written at the midpoint.
 */
export function dropLinePoints(pos: GeoPoint): { top: GeoPoint; ground: GeoPoint; mid: GeoPoint; label: string } {
  return {
    top: { lat: pos.lat, lon: pos.lon, height: pos.height },
    ground: { lat: pos.lat, lon: pos.lon, height: 0 },
    mid: { lat: pos.lat, lon: pos.lon, height: pos.height / 2 },
    label: formatAltitude(pos.height),
  };
}

/** Appearance of the drop line and its label. */
export const DROP_LINE_CONFIG = {
  width: 1.5,
  dashLength: 12,
  labelFont: '11px JetBrains Mono, monospace',
};
