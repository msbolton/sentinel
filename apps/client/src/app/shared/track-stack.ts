import { ViewportBounds } from '../features/map/viewport-bounds';
import { Entity } from './models/entity.model';

/** Rows the track stack shows. */
export const TRACK_STACK_LIMIT = 10;

function inView(lat: number, lon: number, b: ViewportBounds): boolean {
  if (lat < b.south || lat > b.north) return false;
  // west > east means the view crosses the antimeridian.
  return b.west <= b.east ? lon >= b.west && lon <= b.east : lon >= b.west || lon <= b.east;
}

function seenMs(e: Entity): number {
  const ms = Date.parse(e.lastSeenAt ?? e.updatedAt);
  return Number.isFinite(ms) ? ms : -Infinity;
}

function cmpDesc(a: number, b: number): number {
  return a === b ? 0 : b > a ? 1 : -1;
}

/**
 * The tracks inside the viewport that reported most recently, freshest first,
 * cut to `limit`. Empty before the map has reported a viewport; tracks with no
 * position are skipped. `nowMs` is accepted for symmetry with `freshnessOf` and
 * for a future age filter; it is not used yet.
 */
export function freshestInView(
  entities: Iterable<Entity>,
  bounds: ViewportBounds | null,
  nowMs: number,
  limit = TRACK_STACK_LIMIT,
): Entity[] {
  void nowMs;
  if (!bounds) return [];
  const found: Entity[] = [];
  for (const e of entities) {
    if (e.position && inView(e.position.latitude, e.position.longitude, bounds)) found.push(e);
  }
  // Compare rather than subtract: two unparseable times would make -Infinity - -Infinity NaN.
  return found.sort((a, b) => cmpDesc(seenMs(a), seenMs(b))).slice(0, limit);
}
