import type { DemoBounds } from '../../core/services/demo.service';
import { Emplacement } from '../../shared/models/emplacement.model';
import { boundsOf } from '../map/guest-map';

type Point = { lat: number; lon: number };

/** Sites closer than this to one another count as one cluster. */
const CLUSTER_KM = 200;

/** The positions of the active, placed sites that pass `keep`. */
function positions(list: Emplacement[], keep: (e: Emplacement) => boolean): Point[] {
  return list.filter((e) => e.active && e.position != null && keep(e)).map((e) => e.position!);
}

/** Great-circle distance in km. */
function km(a: Point, b: Point): number {
  const rad = Math.PI / 180;
  const dLat = (b.lat - a.lat) * rad;
  const dLon = (b.lon - a.lon) * rad;
  const h = Math.sin(dLat / 2) ** 2 + Math.cos(a.lat * rad) * Math.cos(b.lat * rad) * Math.sin(dLon / 2) ** 2;
  return 2 * 6371 * Math.asin(Math.min(1, Math.sqrt(h)));
}

/** The points grouped so that each is within CLUSTER_KM of another in its group (single linkage), in first-seen order. */
function clusters(points: Point[]): Point[][] {
  const group = new Array<number>(points.length).fill(-1);
  const out: Point[][] = [];
  for (let i = 0; i < points.length; i++) {
    if (group[i] !== -1) continue;
    const members: Point[] = [];
    group[i] = out.length;
    const queue = [i];
    while (queue.length > 0) {
      const j = queue.pop()!;
      members.push(points[j]);
      for (let k = 0; k < points.length; k++) {
        if (group[k] === -1 && km(points[j], points[k]) <= CLUSTER_KM) {
          group[k] = out.length;
          queue.push(k);
        }
      }
    }
    out.push(members);
  }
  return out;
}

/**
 * A box around the cluster of active, non-visitor sites nearest `near` (the
 * visitor), or around the largest cluster when where they are is not known;
 * null when there are none. The example sensors sit continents apart, and one
 * box around them all would show nothing.
 */
export function sensorBounds(list: Emplacement[], visitorsFeedId: string | null | undefined, near?: Point | null): DemoBounds | null {
  const groups = clusters(positions(list, (e) => !visitorsFeedId || e.feedId !== visitorsFeedId));
  if (groups.length === 0) return null;
  let best = groups[0];
  if (near) {
    const distance = (g: Point[]) => Math.min(...g.map((p) => km(p, near)));
    for (const g of groups) if (distance(g) < distance(best)) best = g;
  } else {
    for (const g of groups) if (g.length > best.length) best = g;
  }
  return boundsOf(best);
}

/** A box around the active visitors' sites (the visitors connection); null when there are none. */
export function visitorBounds(list: Emplacement[], visitorsFeedId: string | null | undefined): DemoBounds | null {
  if (!visitorsFeedId) return null;
  return boundsOf(positions(list, (e) => e.feedId === visitorsFeedId));
}
