import { SatelliteTracker, parseTle, periodMinutes, positionAt } from './satellite-tracker';
import { Classification, Entity, EntitySource, EntityType } from '../../shared/models/entity.model';

// The ISS element set used by the ingest service's decoder tests.
const ISS_LINE1 = '1 25544U 98067A   26063.86671769  .00009014  00000+0  17477-3 0  9999';
const ISS_LINE2 = '2 25544  51.6315  96.2009 0008177 157.5272 202.6076 15.48447334555585';
const EPOCH_MS = Date.UTC(2026, 2, 4, 20, 0, 0);

function haversineKm(aLat: number, aLon: number, bLat: number, bLon: number): number {
  const r = Math.PI / 180;
  const dLat = (bLat - aLat) * r;
  const dLon = (bLon - aLon) * r;
  const h = Math.sin(dLat / 2) ** 2 + Math.cos(aLat * r) * Math.cos(bLat * r) * Math.sin(dLon / 2) ** 2;
  return 2 * 6371 * Math.asin(Math.sqrt(h));
}

function satellite(id: string, over: Partial<Entity> = {}): Entity {
  return {
    id,
    entityType: EntityType.SATELLITE,
    name: 'ISS (ZARYA)',
    source: 'CELESTRAK' as EntitySource,
    classification: Classification.UNCLASSIFIED,
    metadata: {},
    affiliations: [],
    createdAt: '2026-03-04T00:00:00Z',
    updatedAt: '2026-03-04T00:00:00Z',
    position: { latitude: 0, longitude: 0, altitude: 400000 },
    platformData: { tle: { noradId: 25544, line1: ISS_LINE1, line2: ISS_LINE2 } },
    ...over,
  } as Entity;
}

describe('satellite propagation', () => {
  it('parses real elements and rejects garbage', () => {
    expect(parseTle(ISS_LINE1, ISS_LINE2)).not.toBeNull();
    expect(parseTle('1 25544U', ISS_LINE2)).toBeNull();
    expect(parseTle(undefined, ISS_LINE2)).toBeNull();
    expect(parseTle('x'.repeat(69), 'y'.repeat(69))).toBeNull();
  });

  it('propagates the ISS to a low-earth position and moves it at orbital speed', () => {
    const satrec = parseTle(ISS_LINE1, ISS_LINE2)!;
    const a = positionAt(satrec, EPOCH_MS)!;
    expect(a).not.toBeNull();
    expect(Math.abs(a.lat)).toBeLessThanOrEqual(52);
    expect(a.height).toBeGreaterThan(380_000);
    expect(a.height).toBeLessThan(430_000);
    const b = positionAt(satrec, EPOCH_MS + 10_000)!;
    const km = haversineKm(a.lat, a.lon, b.lat, b.lon);
    expect(km).toBeGreaterThan(60); // ~7.66 km/s along the ground track
    expect(km).toBeLessThan(90);
    expect(periodMinutes(satrec)).toBeGreaterThan(92);
    expect(periodMinutes(satrec)).toBeLessThan(94);
  });
});

describe('SatelliteTracker', () => {
  it('tracks satellites with elements and ignores everything else', () => {
    const tracker = new SatelliteTracker();
    expect(tracker.upsert(satellite('iss'))).toBe(true);
    expect(tracker.upsert(satellite('plane', { entityType: EntityType.AIRCRAFT }))).toBe(false);
    expect(tracker.upsert(satellite('bare', { platformData: {} }))).toBe(false);
    expect(tracker.size).toBe(1);
    expect(tracker.has('iss')).toBe(true);
    const pos = tracker.positionOf('iss', EPOCH_MS)!;
    expect(pos.height).toBeGreaterThan(380_000);
  });

  it('re-parses only when the elements change and drops a satellite that stops being one', () => {
    const tracker = new SatelliteTracker();
    tracker.upsert(satellite('iss'));
    const first = tracker.satrecOf('iss');
    tracker.upsert(satellite('iss'));
    expect(tracker.satrecOf('iss')).toBe(first);
    const newer = ISS_LINE2.replace('202.6076', '212.6076');
    tracker.upsert(satellite('iss', { platformData: { tle: { noradId: 25544, line1: ISS_LINE1, line2: newer } } }));
    expect(tracker.satrecOf('iss')).not.toBe(first);
    tracker.upsert(satellite('iss', { platformData: {} }));
    expect(tracker.has('iss')).toBe(false);
  });

  it('steps every tracked satellite, removes on demand and respects the cap', () => {
    const tracker = new SatelliteTracker(2);
    tracker.upsert(satellite('a'));
    tracker.upsert(satellite('b'));
    expect(tracker.upsert(satellite('c'))).toBe(false);
    const stepped = tracker.step(EPOCH_MS);
    expect(stepped.map(([id]) => id).sort()).toEqual(['a', 'b']);
    tracker.remove('a');
    expect(tracker.size).toBe(1);
    tracker.clear();
    expect(tracker.size).toBe(0);
  });
});
