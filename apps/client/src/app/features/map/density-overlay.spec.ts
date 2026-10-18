import { densityCellDegrees, densityDiscSize, densityLabel, densityOffsetPx, isWideView, viewAreaSqDeg, withAlpha } from './density-overlay';

const europe = { north: 70, south: 20, east: 40, west: -30 };
const channel = { north: 51.5, south: 49.5, east: 2, west: -2 };
const globe = { north: 90, south: -90, east: 180, west: -180 };

describe('density overlay', () => {
  it('measures views, including the globe and views across the antimeridian', () => {
    expect(viewAreaSqDeg(europe)).toBe(3500);
    expect(viewAreaSqDeg(globe)).toBe(64800);
    expect(viewAreaSqDeg({ north: 10, south: 0, east: -170, west: 170 })).toBe(200);
  });

  it('switches to counts above the cap', () => {
    expect(isWideView(europe, 2500)).toBe(true);
    expect(isWideView(channel, 2500)).toBe(false);
    expect(isWideView(globe, 0)).toBe(false); // no cap
    expect(isWideView(null, 2500)).toBe(false);
  });

  it('asks for a grid of about a dozen cells across the view', () => {
    expect(densityCellDegrees(europe)).toBe(10); // 70° wide
    expect(densityCellDegrees(globe)).toBe(30);
    expect(densityCellDegrees({ north: 52, south: 48, east: 3, west: -3 })).toBe(0.5);
  });

  it('writes counts compactly, sizes discs by count and keeps types apart', () => {
    expect(densityLabel(950)).toBe('950');
    expect(densityLabel(1234)).toBe('1.2k');
    expect(densityLabel(12_345)).toBe('12k');
    expect(densityDiscSize(1)).toBe(34);
    expect(densityDiscSize(10_000_000)).toBe(60);
    expect(new Set(['AIRCRAFT', 'VESSEL', 'SATELLITE'].map(densityOffsetPx)).size).toBe(3);
    expect(withAlpha('#06b6d4', 0.6)).toBe('rgba(6, 182, 212, 0.6)');
  });
});
