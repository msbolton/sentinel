import {
  DR_DEFAULTS,
  DeadReckoningTracker,
  Fix,
  applyFix,
  displayPosition,
  fixFromEntity,
  predict,
  wrapLongitude,
} from './dead-reckoning';
import { Entity } from '../../shared/models/entity.model';

const T0 = Date.parse('2026-10-14T00:00:00Z');
const METERS_PER_DEG_LAT = 111_320;

function aircraft(overrides: Partial<Entity> = {}): Entity {
  return {
    id: 'a1',
    entityType: 'AIRCRAFT',
    name: 'UAL123',
    source: 'OPENSKY',
    classification: 'UNCLASSIFIED',
    position: { latitude: 41.9, longitude: -87.9, altitude: 10000 },
    speedKnots: 194.384, // 100 m/s
    heading: 90,
    metadata: {},
    affiliations: [],
    createdAt: '2026-10-14T00:00:00Z',
    updatedAt: '2026-10-14T00:00:00Z',
    lastSeenAt: '2026-10-14T00:00:00Z',
    ...overrides,
  } as Entity;
}

function fix(overrides: Partial<Fix> = {}): Fix {
  return { lat: 41.9, lon: -87.9, alt: 10000, vNorth: 0, vEast: 100, vUp: 0, timeMs: T0, ...overrides };
}

describe('fixFromEntity', () => {
  it('derives north/east velocity from speed and heading', () => {
    const f = fixFromEntity(aircraft(), T0)!;
    expect(f).not.toBeNull();
    expect(f.vEast).toBeCloseTo(100, 3);
    expect(f.vNorth).toBeCloseTo(0, 3);
    expect(f.timeMs).toBe(T0);
    expect(f.alt).toBe(10000);
  });

  it('prefers course over heading and reported velocity over both', () => {
    const byCourse = fixFromEntity(aircraft({ heading: 90, course: 0 }), T0)!;
    expect(byCourse.vNorth).toBeCloseTo(100, 3);
    expect(byCourse.vEast).toBeCloseTo(0, 3);

    const reported = fixFromEntity(aircraft({ kinematics: { velocity: { north: 5, east: -7, up: 2 } } }), T0)!;
    expect(reported).toEqual(expect.objectContaining({ vNorth: 5, vEast: -7, vUp: 2 }));
  });

  it('rejects untracked types, positionless, stationary, and directionless entities', () => {
    expect(fixFromEntity(aircraft({ entityType: 'PERSON' } as Partial<Entity>), T0)).toBeNull();
    // Moving vessels are extrapolated too: their fixes are thinned to one every 15 s.
    expect(fixFromEntity(aircraft({ entityType: 'VESSEL' } as Partial<Entity>), T0)).not.toBeNull();
    expect(fixFromEntity(aircraft({ position: undefined }), T0)).toBeNull();
    expect(fixFromEntity(aircraft({ speedKnots: 0.5 }), T0)).toBeNull();
    expect(fixFromEntity(aircraft({ heading: undefined, course: undefined }), T0)).toBeNull();
  });

  it('never dates a fix in the future and falls back to updatedAt then now', () => {
    expect(fixFromEntity(aircraft({ lastSeenAt: '2026-10-14T00:05:00Z' }), T0)!.timeMs).toBe(T0);
    expect(fixFromEntity(aircraft({ lastSeenAt: undefined, updatedAt: '2026-10-13T23:59:00Z' }), T0)!.timeMs).toBe(T0 - 60_000);
    expect(fixFromEntity(aircraft({ lastSeenAt: undefined, updatedAt: 'garbage' }), T0)!.timeMs).toBe(T0);
  });
});

describe('predict', () => {
  it('moves east by the right longitude for the latitude', () => {
    const p = predict(fix(), T0 + 10_000)!;
    const expectedDLon = 1000 / (METERS_PER_DEG_LAT * Math.cos((41.9 * Math.PI) / 180));
    expect(p.lon - -87.9).toBeCloseTo(expectedDLon, 6);
    expect(p.lat).toBeCloseTo(41.9, 9);
  });

  it('moves north by 0.00898 degrees for 1 km', () => {
    const p = predict(fix({ vNorth: 100, vEast: 0 }), T0 + 10_000)!;
    expect(p.lat - 41.9).toBeCloseTo(0.00898, 4);
  });

  it('climbs and never goes below ground', () => {
    expect(predict(fix({ vUp: 10 }), T0 + 10_000)!.alt).toBeCloseTo(10100, 6);
    expect(predict(fix({ alt: 50, vUp: -10 }), T0 + 10_000)!.alt).toBe(0);
  });

  it('expires after the maximum extrapolation and clamps future fixes', () => {
    expect(predict(fix(), T0 + DR_DEFAULTS.maxExtrapolationMs + 1)).toBeNull();
    const p = predict(fix({ timeMs: T0 + 5000 }), T0)!;
    expect(p).toEqual({ lat: 41.9, lon: -87.9, alt: 10000 });
  });

  it('wraps across the antimeridian and guards the poles', () => {
    const p = predict(fix({ lat: 0, lon: 179.999, vEast: 1000 }), T0 + 1000)!;
    expect(p.lon).toBeLessThan(-179.9);
    expect(p.lon).toBeGreaterThan(-180);
    const polar = predict(fix({ lat: 89.99, vEast: 100 }), T0 + 1000)!;
    expect(Number.isFinite(polar.lon)).toBe(true);
  });
});

describe('wrapLongitude', () => {
  it('maps into [-180, 180]', () => {
    expect(wrapLongitude(181)).toBe(-179);
    expect(wrapLongitude(-181)).toBe(179);
    expect(wrapLongitude(540)).toBe(180);
    expect(wrapLongitude(-180)).toBe(-180);
    expect(wrapLongitude(45)).toBe(45);
  });
});

describe('applyFix and displayPosition', () => {
  it('blends a correction away over blendMs', () => {
    const first = applyFix(undefined, fix(), T0);
    const shownAt10 = displayPosition(first, T0 + 10_000)!;
    // The new fix says the aircraft is 0.001° further north than predicted.
    const newFix = fix({ lat: shownAt10.lat + 0.001, lon: shownAt10.lon, timeMs: T0 + 10_000 });
    const second = applyFix(first, newFix, T0 + 10_000);
    expect(second.offsetLat).toBeCloseTo(-0.001, 9);

    const atStart = displayPosition(second, T0 + 10_000)!;
    expect(atStart.lat).toBeCloseTo(shownAt10.lat, 9); // no jump
    const halfway = displayPosition(second, T0 + 10_500)!;
    expect(halfway.lat).toBeCloseTo(newFix.lat - 0.0005, 9);
    const done = displayPosition(second, T0 + 11_000)!;
    expect(done.lat).toBeCloseTo(newFix.lat, 9);
    expect(done.extrapolating).toBe(true);
  });

  it('snaps when the correction is larger than snapDistanceM', () => {
    const first = applyFix(undefined, fix(), T0);
    const far = fix({ lat: 42.9, timeMs: T0 + 10_000 }); // ~111 km north
    const second = applyFix(first, far, T0 + 10_000);
    expect(second.offsetLat).toBe(0);
    expect(displayPosition(second, T0 + 10_000)!.lat).toBeCloseTo(42.9, 9);
  });

  it('returns null once the fix expired', () => {
    const state = applyFix(undefined, fix(), T0);
    expect(displayPosition(state, T0 + 61_000)).toBeNull();
  });
});

describe('DeadReckoningTracker', () => {
  it('tracks eligible entities, steps them, and evicts expired ones', () => {
    const tracker = new DeadReckoningTracker();
    expect(tracker.upsert(aircraft(), T0)).toBe(true);
    expect(tracker.upsert(aircraft({ id: 'walker', entityType: 'PERSON' } as Partial<Entity>), T0)).toBe(false);
    expect(tracker.size).toBe(1);

    const stepped = tracker.step(T0 + 10_000);
    expect(stepped).toHaveLength(1);
    expect(stepped[0][0]).toBe('a1');
    expect(stepped[0][1].lon).toBeGreaterThan(-87.9);

    expect(tracker.step(T0 + 61_000)).toHaveLength(0);
    expect(tracker.size).toBe(0);
  });

  it('drops an entity that becomes stationary and honours remove and maxTracked', () => {
    const tracker = new DeadReckoningTracker({ ...DR_DEFAULTS, maxTracked: 1 });
    expect(tracker.upsert(aircraft(), T0)).toBe(true);
    expect(tracker.upsert(aircraft({ id: 'a2' }), T0)).toBe(false); // over the cap
    expect(tracker.upsert(aircraft({ speedKnots: 0 }), T0)).toBe(false); // now stationary
    expect(tracker.has('a1')).toBe(false);

    expect(tracker.upsert(aircraft(), T0)).toBe(true);
    tracker.remove('a1');
    expect(tracker.size).toBe(0);
  });

  it('exposes the current position of one entity', () => {
    const tracker = new DeadReckoningTracker();
    tracker.upsert(aircraft(), T0);
    expect(tracker.positionOf('a1', T0)!.lat).toBeCloseTo(41.9, 9);
    expect(tracker.positionOf('nope', T0)).toBeNull();
  });
});
