import {
  circlePolygon,
  crossesAntimeridian,
  distinctPoints,
  formatArea,
  formatDistance,
  geodesicDistanceM,
  normalizeHex,
  polygonAreaM2,
  rectanglePolygon,
} from './geofence-geometry';

describe('geofence geometry', () => {
  it('measures a known geodesic', () => {
    // one degree of longitude at 25.2° N
    expect(geodesicDistanceM([55.2708, 25.2048], [56.2708, 25.2048])).toBeCloseTo(100_600, -3);
  });

  it('builds a 64-point circle whose points sit on the radius', () => {
    const ring = circlePolygon([55.3, 25.2], 15000);
    expect(ring).toHaveLength(64);
    for (const p of ring) expect(Math.abs(geodesicDistanceM([55.3, 25.2], p) - 15000)).toBeLessThan(15);
  });

  it('builds a rectangle from two corners', () => {
    expect(rectanglePolygon([55.0, 25.0], [56.0, 24.0])).toEqual([[55, 25], [55, 24], [56, 24], [56, 25]]);
  });

  it('measures the seeded Persian Gulf zone to within 2%', () => {
    const km2 = polygonAreaM2([[51, 27], [56, 27], [56, 24], [51, 24]]) / 1e6;
    expect(km2).toBeGreaterThan(163_000);
    expect(km2).toBeLessThan(170_000);
  });

  it('flags antimeridian crossings and counts distinct points', () => {
    expect(crossesAntimeridian([[179, 10], [-179, 10], [-179, 11]])).toBe(true);
    expect(crossesAntimeridian([[51, 27], [56, 27], [56, 24]])).toBe(false);
    expect(distinctPoints([[1, 1], [1, 1], [2, 2]])).toBe(2);
  });

  it('formats distances, areas and hex colours', () => {
    expect(formatDistance(850)).toBe('850 m');
    expect(formatDistance(3812)).toBe('3.8 km');
    expect(formatDistance(142_400)).toBe('142 km');
    expect(formatArea(142_400_000)).toBe('142 km²');
    expect(formatArea(400_000)).toBe('0.4 km²');
    expect(normalizeHex('f97316')).toBe('#f97316');
    expect(normalizeHex('#F97316')).toBe('#f97316');
    expect(normalizeHex('xyz')).toBeNull();
  });
});
