import { GLOBAL_BOUNDS, isGlobalBounds, rectangleToBounds } from './viewport-bounds';

const toDegrees = (rad: number) => (rad * 180) / Math.PI;

describe('rectangleToBounds', () => {
  it('maps an undefined rectangle to the whole globe', () => {
    expect(rectangleToBounds(undefined, toDegrees)).toEqual(GLOBAL_BOUNDS);
  });

  it('converts radians to degrees', () => {
    const rect = {
      north: (45 * Math.PI) / 180,
      south: (40 * Math.PI) / 180,
      east: (-80 * Math.PI) / 180,
      west: (-90 * Math.PI) / 180,
    };
    const b = rectangleToBounds(rect, toDegrees);
    expect(b.north).toBeCloseTo(45, 6);
    expect(b.south).toBeCloseTo(40, 6);
    expect(b.east).toBeCloseTo(-80, 6);
    expect(b.west).toBeCloseTo(-90, 6);
  });

  it('clamps out-of-range values', () => {
    const b = rectangleToBounds({ north: 2, south: -2, east: 4, west: -4 }, toDegrees);
    expect(b).toEqual({ north: 90, south: -90, east: 180, west: -180 });
  });

  it('falls back to the globe on non-finite values', () => {
    expect(rectangleToBounds({ north: NaN, south: 0, east: 0, west: 0 }, toDegrees)).toEqual(GLOBAL_BOUNDS);
  });

  it('returns a fresh object, not the frozen constant', () => {
    const b = rectangleToBounds(undefined, toDegrees);
    expect(Object.isFrozen(b)).toBe(false);
  });
});

describe('isGlobalBounds', () => {
  it('detects the whole globe', () => {
    expect(isGlobalBounds({ ...GLOBAL_BOUNDS })).toBe(true);
    expect(isGlobalBounds({ north: 45, south: 40, east: -80, west: -90 })).toBe(false);
  });
});
