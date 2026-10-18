import { Entity, EntitySource, EntityType } from './entity.model';

/** One position on an aircraft's past path, oldest first (GET /api/v1/aircraft/{hex}/path). */
export interface FlightPathPoint {
  lat: number;
  lon: number;
  altFt: number;
  ts: number; // unix seconds
}

/** What IntelSky knows about the aircraft over a range of days. */
export interface FlightPathMeta {
  callsign?: string;
  registration?: string;
  type?: string;
  operator?: string;
  description?: string;
  firstSeen?: string;
  lastSeen?: string;
}

export interface FlightPath {
  hex: string;
  source: string;
  days: number;
  points: FlightPathPoint[];
  meta?: FlightPathMeta;
}

/** Off, today, or the last 3 or 7 days. */
export type FlightPathDays = 0 | 1 | 3 | 7;

export const FLIGHT_PATH_DAYS: { days: FlightPathDays; label: string }[] = [
  { days: 0, label: 'Off' },
  { days: 1, label: 'Today' },
  { days: 3, label: '3 d' },
  { days: 7, label: '7 d' },
];

/** The ICAO 24-bit address of an aircraft, or null when it has none. */
export function icaoHexOf(entity: Entity | null | undefined): string | null {
  if (!entity || entity.entityType !== EntityType.AIRCRAFT) return null;
  const hex = entity.platformData?.adsb?.icaoHex?.trim().toUpperCase();
  return hex && /^[0-9A-F]{6}$/.test(hex) ? hex : null;
}

/**
 * Today's path for aircraft IntelSky reports (military and government);
 * off for other aircraft, which IntelSky mostly does not know.
 */
export function defaultFlightPathDays(entity: Entity | null | undefined): FlightPathDays {
  return icaoHexOf(entity) && entity?.source === EntitySource.INTELSKY ? 1 : 0;
}
