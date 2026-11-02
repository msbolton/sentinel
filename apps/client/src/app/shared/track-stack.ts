import { ViewportBounds } from '../features/map/viewport-bounds';
import { inBounds } from './geo-math';
import { Affiliation, Entity } from './models/entity.model';

/** Rows the track stack shows. */
export const TRACK_STACK_LIMIT = 10;

const AFFILIATION_ABBREV: Partial<Record<Affiliation, string>> = {
  [Affiliation.FRIENDLY]: 'FRD',
  [Affiliation.HOSTILE]: 'HOS',
  [Affiliation.NEUTRAL]: 'NEU',
  [Affiliation.UNKNOWN]: 'UNK',
  [Affiliation.ASSUMED_FRIENDLY]: 'AFR',
  [Affiliation.SUSPECT]: 'SUS',
  [Affiliation.PENDING]: 'PND',
};

/** Three-letter code for a stack row; every affiliation gets its own, missing or unrecognised reads UNK. */
export function affiliationAbbrev(a: Affiliation | undefined): string {
  return (a && AFFILIATION_ABBREV[a]) || 'UNK';
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
    if (e.position && inBounds({ lat: e.position.latitude, lon: e.position.longitude }, bounds)) found.push(e);
  }
  // Compare rather than subtract: two unparseable times would make -Infinity - -Infinity NaN.
  return found.sort((a, b) => cmpDesc(seenMs(a), seenMs(b))).slice(0, limit);
}
