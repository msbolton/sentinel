import { drawsUncertainty, heightModeOf } from './height-mode';
import { Classification, Entity, EntityType, TrackEnvironment } from '../../shared/models/entity.model';

const entity = (over: Partial<Entity>): Entity =>
  ({
    id: 'e',
    name: 'E',
    entityType: EntityType.VEHICLE,
    source: 'MANUAL' as Entity['source'],
    classification: Classification.UNCLASSIFIED,
    metadata: {},
    affiliations: [],
    createdAt: '',
    updatedAt: '',
    ...over,
  }) as Entity;

describe('heightModeOf', () => {
  it('is relative to the ground at the radar height above ground', () => {
    const e = entity({
      entityType: EntityType.DRONE,
      trackEnvironment: TrackEnvironment.AIR,
      position: { latitude: 1, longitude: 2, altitude: 840 },
      platformData: { radar: { heightAboveGroundM: 120 } },
    });
    expect(heightModeOf(e, true)).toEqual({ kind: 'relative', heightM: 120 });
  });

  it('without terrain, a radar target is absolute at its altitude', () => {
    const e = entity({
      entityType: EntityType.DRONE,
      trackEnvironment: TrackEnvironment.AIR,
      position: { latitude: 1, longitude: 2, altitude: 840 },
      platformData: { radar: { heightAboveGroundM: 120 } },
    });
    expect(heightModeOf(e, false)).toEqual({ kind: 'absolute', heightM: 840 });
  });

  it('without terrain, a GROUND radar target is still clamped and one with no altitude too', () => {
    const ground = entity({
      trackEnvironment: TrackEnvironment.GROUND,
      position: { latitude: 1, longitude: 2, altitude: 300 },
      platformData: { radar: { heightAboveGroundM: 15 } },
    });
    expect(heightModeOf(ground, false)).toEqual({ kind: 'clamp', heightM: 0 });
    expect(heightModeOf(entity({ platformData: { radar: { heightAboveGroundM: 15 } } }), false)).toEqual({ kind: 'clamp', heightM: 0 });
  });

  it('lets radar height above ground beat GROUND', () => {
    const e = entity({
      trackEnvironment: TrackEnvironment.GROUND,
      position: { latitude: 1, longitude: 2, altitude: 300 },
      platformData: { radar: { heightAboveGroundM: 15 } },
    });
    expect(heightModeOf(e, true)).toEqual({ kind: 'relative', heightM: 15 });
  });

  it('keeps a radar height of zero relative', () => {
    const e = entity({ platformData: { radar: { heightAboveGroundM: 0 } } });
    expect(heightModeOf(e, true)).toEqual({ kind: 'relative', heightM: 0 });
  });

  it('clamps a GROUND entity, whatever its altitude', () => {
    const e = entity({
      trackEnvironment: TrackEnvironment.GROUND,
      position: { latitude: 1, longitude: 2, altitude: 640 },
    });
    expect(heightModeOf(e, true)).toEqual({ kind: 'clamp', heightM: 0 });
  });

  it('is absolute when the altitude is above zero', () => {
    const e = entity({
      entityType: EntityType.AIRCRAFT,
      trackEnvironment: TrackEnvironment.AIR,
      position: { latitude: 1, longitude: 2, altitude: 9000 },
      platformData: { radar: {} },
    });
    expect(heightModeOf(e, true)).toEqual({ kind: 'absolute', heightM: 9000 });
  });

  it('clamps when there is no altitude or it is zero or negative', () => {
    expect(heightModeOf(entity({ position: { latitude: 1, longitude: 2 } }), true)).toEqual({ kind: 'clamp', heightM: 0 });
    expect(heightModeOf(entity({ position: { latitude: 1, longitude: 2, altitude: 0 } }), true)).toEqual({ kind: 'clamp', heightM: 0 });
    expect(heightModeOf(entity({ position: { latitude: 1, longitude: 2, altitude: -3 } }), true)).toEqual({ kind: 'clamp', heightM: 0 });
    expect(heightModeOf(entity({}), true)).toEqual({ kind: 'clamp', heightM: 0 });
  });

  it('ignores a non-finite radar height', () => {
    const e = entity({
      position: { latitude: 1, longitude: 2, altitude: 50 },
      platformData: { radar: { heightAboveGroundM: Number.NaN } },
    });
    expect(heightModeOf(e, true)).toEqual({ kind: 'absolute', heightM: 50 });
  });
});

describe('drawsUncertainty', () => {
  const cov = { pnPn: 100, pnPe: 0, pePe: 100 };

  it('is true for a SIGNAL with a covariance', () => {
    expect(drawsUncertainty(entity({ entityType: EntityType.SIGNAL, kinematics: { positionCovariance: cov } }))).toBe(true);
  });

  it('is true for a radar track with a covariance', () => {
    expect(
      drawsUncertainty(entity({ entityType: EntityType.DRONE, platformData: { radar: {} }, kinematics: { positionCovariance: cov } })),
    ).toBe(true);
  });

  it('is true for an EO/IR detection with a covariance', () => {
    expect(
      drawsUncertainty(entity({ platformData: { eoir: { band: 'LWIR' } }, kinematics: { positionCovariance: cov } })),
    ).toBe(true);
  });

  it('is false for a plain vehicle, even with a covariance', () => {
    expect(drawsUncertainty(entity({ kinematics: { positionCovariance: cov } }))).toBe(false);
  });

  it('is false without a covariance', () => {
    expect(drawsUncertainty(entity({ entityType: EntityType.SIGNAL }))).toBe(false);
    expect(drawsUncertainty(entity({ platformData: { radar: {} }, kinematics: {} }))).toBe(false);
  });
});
