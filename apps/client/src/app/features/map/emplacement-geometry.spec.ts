import { coverageRings, hasCoverage } from './emplacement-geometry';
import { geodesicDistanceM } from './geofence-geometry';

describe('coverageRings', () => {
  const center: [number, number] = [-4, 50];

  it('draws a sector as the outer arc and back to the apex', () => {
    const [ring, ...rest] = coverageRings(center, 90, { minRangeM: 0, maxRangeM: 10_000, azimuthWidthDeg: 120 });
    expect(rest).toHaveLength(0);
    expect(ring).toHaveLength(61 + 1); // 60 steps of 2° plus the apex
    expect(ring[ring.length - 1]).toEqual(center);
    expect(geodesicDistanceM(center, ring[0])).toBeCloseTo(10_000, -1);
  });

  it('closes a sector with a minimum range along the inner arc', () => {
    const [ring] = coverageRings(center, 0, { minRangeM: 2_000, maxRangeM: 10_000, azimuthWidthDeg: 90 });
    expect(ring).toHaveLength(46 * 2);
    expect(geodesicDistanceM(center, ring[ring.length - 1])).toBeCloseTo(2_000, -1);
  });

  it('draws omni coverage as a circle, or an annulus with a minimum range', () => {
    expect(coverageRings(center, 0, { minRangeM: 0, maxRangeM: 5_000, azimuthWidthDeg: 360 })).toHaveLength(1);
    const [outer, inner] = coverageRings(center, 0, { minRangeM: 1_000, maxRangeM: 5_000, azimuthWidthDeg: 360 });
    expect(geodesicDistanceM(center, outer[0])).toBeCloseTo(5_000, -1);
    expect(geodesicDistanceM(center, inner[0])).toBeCloseTo(1_000, -1);
  });

  // Review focus: an emplacement on the antimeridian looking east.
  it('keeps every point at range across the antimeridian', () => {
    const edge: [number, number] = [179.9, 10];
    // 180° so the sector spans north to south and straddles the line; a 60° one lies wholly past it at this range.
    const [ring] = coverageRings(edge, 90, { minRangeM: 0, maxRangeM: 100_000, azimuthWidthDeg: 180 });
    const arc = ring.slice(0, -1);
    expect(arc.some(([lng]) => lng < 0)).toBe(true);
    expect(arc.some(([lng]) => lng > 0)).toBe(true);
    for (const p of arc) expect(geodesicDistanceM(edge, p)).toBeCloseTo(100_000, -2);
  });

  it('knows when there is no coverage', () => {
    expect(hasCoverage({ minRangeM: 0, maxRangeM: 0, azimuthWidthDeg: 0 })).toBe(false);
    expect(hasCoverage(null)).toBe(false);
    expect(hasCoverage({ minRangeM: 0, maxRangeM: 1, azimuthWidthDeg: 10 })).toBe(true);
  });
});
