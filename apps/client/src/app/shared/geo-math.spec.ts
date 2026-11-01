import { bearingDeg, rangeNm, viewWidthNm } from './geo-math';

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
