import { FlightPath, FlightPathPoint } from '../../shared/models/flight-path.model';
import { splitAtAntimeridian } from './orbit-geometry';

const FT_TO_M = 0.3048;
const MONTHS = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];

export interface PathVertex {
  lon: number;
  lat: number;
  height: number; // metres
}

/** The path as runs of vertices at altitude, split where it crosses the antimeridian. */
export function pathRuns(points: FlightPathPoint[]): PathVertex[][] {
  const vertices = points.map((p) => ({ lon: p.lon, lat: p.lat, height: Math.max(0, p.altFt) * FT_TO_M }));
  return splitAtAntimeridian(vertices);
}

function utc(ts: number): string {
  const d = new Date(ts * 1000);
  const pad = (n: number) => String(n).padStart(2, '0');
  return `${d.getUTCDate()} ${MONTHS[d.getUTCMonth()]} ${pad(d.getUTCHours())}:${pad(d.getUTCMinutes())} UTC`;
}

/** "34 points since 15 Oct 23:40 UTC", or why there is nothing to draw. */
export function pathSummary(path: FlightPath | null): string {
  if (!path || path.points.length === 0) return 'No IntelSky history for this aircraft';
  const n = path.points.length;
  return `${n} ${n === 1 ? 'point' : 'points'} since ${utc(path.points[0].ts)}`;
}
