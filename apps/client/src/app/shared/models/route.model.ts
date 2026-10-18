import { Entity, EntitySource } from './entity.model';
import { icaoHexOf } from './flight-path.model';

/** A departure or arrival: an airport, or the first point IntelSky saw a military aircraft at today. */
export interface RoutePlace {
  icao?: string;
  iata?: string;
  name: string;
  municipality?: string;
  country?: string;
  lat: number;
  lon: number;
  time?: string; // RFC 3339, for a first-seen point
}

export interface RouteAirline {
  name: string;
  icao?: string;
  iata?: string;
  country?: string;
}

/** GET /api/v1/aircraft/{hex}/route. provider: "adsbdb" (scheduled route by callsign), "intelsky" (first seen today) or absent. */
export interface AircraftRoute {
  hex: string;
  callsign?: string;
  provider?: string;
  airline?: RouteAirline;
  departure?: RoutePlace;
  arrival?: RoutePlace;
}

/** What to ask the route endpoint for an entity, or null when it is not an aircraft with an ICAO address. */
export function routeQueryOf(entity: Entity | null | undefined): { hex: string; callsign: string | null; intelsky: boolean } | null {
  const hex = icaoHexOf(entity);
  if (!hex || !entity) return null;
  const raw = (entity.platformData?.adsb?.aircraftId ?? '').trim().toUpperCase();
  const callsign = /^[A-Z0-9]{2,8}$/.test(raw) ? raw : null;
  const intelsky = entity.source === EntitySource.INTELSKY;
  if (!callsign && !intelsky) return null;
  return { hex, callsign, intelsky };
}

/** "EGLL London Heathrow Airport · London", or "First seen today 07:45 UTC". */
export function placeLabel(p: RoutePlace | undefined): string {
  if (!p) return '';
  if (p.time) {
    const d = new Date(p.time);
    const pad = (n: number) => String(n).padStart(2, '0');
    return Number.isNaN(d.getTime()) ? p.name : `${p.name} ${pad(d.getUTCHours())}:${pad(d.getUTCMinutes())} UTC`;
  }
  const code = p.icao || p.iata;
  const town = p.municipality && !p.name.includes(p.municipality) ? ` · ${p.municipality}` : '';
  return `${code ? code + ' ' : ''}${p.name}${town}`;
}
