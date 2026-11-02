import { bearingDeg, inBounds, rangeNm, viewWidthNm } from './geo-math';

const djibouti = { lat: 11.59, lon: 43.15 };
const aden = { lat: 12.79, lon: 45.03 };

describe('geo-math', () => {
  it('computes an initial bearing', () => {
    expect(bearingDeg(djibouti, aden)).toBeCloseTo(56.8, 0);
  });

  it('computes range in nautical miles', () => {
    expect(rangeNm(djibouti, aden)).toBeCloseTo(131.8, 1);
  });

  it('measures the view width at mid-latitude', () => {
    expect(viewWidthNm({ north: 13, south: 11, east: 46, west: 44 })).toBeCloseTo(117.4, 0);
  });
});

describe('inBounds', () => {
  const view = { north: 10, south: -10, east: 10, west: -10 };

  it('is true inside the view, edges included, and false outside', () => {
    expect(inBounds({ lat: 0, lon: 0 }, view)).toBe(true);
    expect(inBounds({ lat: 10, lon: -10 }, view)).toBe(true);
    expect(inBounds({ lat: 11, lon: 0 }, view)).toBe(false);
    expect(inBounds({ lat: 0, lon: 11 }, view)).toBe(false);
  });

  it('handles a view across the antimeridian', () => {
    const wrap = { north: 10, south: -10, east: -170, west: 170 };
    expect(inBounds({ lat: 0, lon: 175 }, wrap)).toBe(true);
    expect(inBounds({ lat: 0, lon: -175 }, wrap)).toBe(true);
    expect(inBounds({ lat: 0, lon: 0 }, wrap)).toBe(false);
  });
});
