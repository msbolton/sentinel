import { freshestInView } from './track-stack';
import { Classification, Entity, EntitySource, EntityType } from './models/entity.model';

const now = Date.parse('2026-11-01T14:32:00Z');
const seenAgo = (s: number) => new Date(now - s * 1000).toISOString();

function track(id: string, over: Partial<Entity> = {}): Entity {
  return {
    id,
    entityType: EntityType.AIRCRAFT,
    name: id,
    source: EntitySource.OPENSKY,
    classification: Classification.UNCLASSIFIED,
    metadata: {},
    affiliations: [],
    createdAt: seenAgo(3600),
    updatedAt: seenAgo(3600),
    position: { latitude: 0, longitude: 0 },
    ...over,
  };
}

const at = (lat: number, lon: number, id = `${lat},${lon}`) => track(id, { position: { latitude: lat, longitude: lon } });
const view = { north: 10, south: -10, east: 10, west: -10 };

describe('freshestInView', () => {
  it('returns nothing before the map has reported a viewport', () => {
    expect(freshestInView([track('a')], null, now)).toEqual([]);
  });

  it('keeps only tracks inside the bounds, freshest first, at most ten', () => {
    const inside = Array.from({ length: 12 }, (_, i) => track(`in-${i}`, { lastSeenAt: seenAgo(100 - i) }));
    const outside = [at(50, 0, 'north'), at(0, 50, 'east')];
    const ids = freshestInView([...outside, ...inside], view, now).map((e) => e.id);
    expect(ids).toEqual(['in-11', 'in-10', 'in-9', 'in-8', 'in-7', 'in-6', 'in-5', 'in-4', 'in-3', 'in-2']);
  });

  it('honours an explicit limit', () => {
    const list = [track('a', { lastSeenAt: seenAgo(1) }), track('b', { lastSeenAt: seenAgo(2) }), track('c', { lastSeenAt: seenAgo(3) })];
    expect(freshestInView(list, view, now, 2).map((e) => e.id)).toEqual(['a', 'b']);
  });

  it('treats the bounds edges as inside', () => {
    expect(freshestInView([at(10, 10), at(-10, -10), at(10.1, 0)], view, now)).toHaveLength(2);
  });

  it('skips tracks that have no position', () => {
    expect(freshestInView([track('nopos', { position: undefined }), track('pos')], view, now).map((e) => e.id)).toEqual(['pos']);
  });

  it('falls back to updatedAt when there is no lastSeenAt', () => {
    const a = track('a', { updatedAt: seenAgo(5) });
    const b = track('b', { lastSeenAt: seenAgo(50), updatedAt: seenAgo(1) });
    expect(freshestInView([b, a], view, now).map((e) => e.id)).toEqual(['a', 'b']);
  });

  it('sorts tracks with an unparseable time last', () => {
    const bad = track('bad', { lastSeenAt: 'garbage' });
    const ok = track('ok', { lastSeenAt: seenAgo(500) });
    expect(freshestInView([bad, ok], view, now).map((e) => e.id)).toEqual(['ok', 'bad']);
  });

  it('does not misorder others when several times are unparseable', () => {
    const list = [track('bad1', { lastSeenAt: 'x' }), track('ok', { lastSeenAt: seenAgo(5) }), track('bad2', { lastSeenAt: 'y' })];
    expect(freshestInView(list, view, now)[0].id).toBe('ok');
  });

  it('accepts any iterable, such as a Map\'s values', () => {
    const m = new Map([['a', track('a')]]);
    expect(freshestInView(m.values(), view, now)).toHaveLength(1);
  });

  it('handles a viewport that crosses the antimeridian', () => {
    const found = freshestInView([at(0, 179), at(0, -179), at(0, 0)], { north: 1, south: -1, east: -178, west: 178 }, now);
    expect(found.length).toBe(2);
    expect(found.map((e) => e.position?.longitude).sort()).toEqual([-179, 179]);
  });
});
