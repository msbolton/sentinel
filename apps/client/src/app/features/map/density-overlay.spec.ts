import { DensityGrid } from '../../shared/models/density.model';
import {
  DENSITY_FRESH_MS,
  bucketCells,
  bucketDegrees,
  densityApplies,
  densityDiscSize,
  densityLabel,
  viewAreaSqDeg,
} from './density-overlay';

const grid = (cap = 2500): DensityGrid => ({
  feedId: 'aisstream', source: 'AIS', entityType: 'VESSEL', cellDegrees: 1, windowSeconds: 600,
  capSquareDegrees: cap, generatedAt: '2026-10-18T12:00:00Z', classification: 'UNCLASSIFIED', total: 4,
  cells: [[50.5, -1.5, 2], [50.5, -0.5, 1], [51.5, 1.5, 1]],
});
const europe = { north: 70, south: 20, east: 40, west: -30 };
const channel = { north: 51.5, south: 49.5, east: 2, west: -2 };

describe('density overlay', () => {
  it('measures views, including the globe and views across the antimeridian', () => {
    expect(viewAreaSqDeg(europe)).toBe(3500);
    expect(viewAreaSqDeg({ north: 90, south: -90, east: 180, west: -180 })).toBe(64800);
    expect(viewAreaSqDeg({ north: 10, south: 0, east: -170, west: 170 })).toBe(200);
  });

  it('applies only to fresh grids and views over the cap', () => {
    const now = 1_000_000;
    expect(densityApplies(grid(), now, europe, now)).toBe(true);
    expect(densityApplies(grid(), now, channel, now)).toBe(false);
    expect(densityApplies(grid(), now - DENSITY_FRESH_MS - 1, europe, now)).toBe(false);
    expect(densityApplies(null, now, europe, now)).toBe(false);
    expect(densityApplies(grid(), now, null, now)).toBe(false);
    expect(densityApplies(grid(0), now, europe, now)).toBe(false);
  });

  it('picks a bucket size that keeps about a dozen discs across the view', () => {
    expect(bucketDegrees(europe, 1)).toBe(10); // 70° wide → 5.8° → 10
    expect(bucketDegrees({ north: 90, south: -90, east: 180, west: -180 }, 1)).toBe(30);
    expect(bucketDegrees({ north: 52, south: 48, east: 6, west: -6 }, 1)).toBe(1);
    expect(bucketDegrees({ north: 52, south: 48, east: 6, west: -6 }, 2)).toBe(2);
  });

  it('merges cells into count-weighted buckets inside the view', () => {
    // 10° buckets: the two cells west of Greenwich merge, the one east stays apart.
    const buckets = bucketCells(grid().cells, europe, 10).sort((a, b) => a.lon - b.lon);
    expect(buckets.map((b) => b.count)).toEqual([3, 1]);
    expect(buckets[0].lat).toBeCloseTo(50.5);
    expect(buckets[0].lon).toBeCloseTo((-1.5 * 2 - 0.5) / 3);
    expect(bucketCells(grid().cells, europe, 30).reduce((n, b) => n + b.count, 0)).toBe(4);

    expect(bucketCells(grid().cells, europe, 1).length).toBe(3);
    expect(bucketCells(grid().cells, { north: 10, south: 0, east: 10, west: 0 }, 1)).toEqual([]);
    // Across the antimeridian.
    expect(bucketCells([[0.5, 179.5, 3], [0.5, -179.5, 2]], { north: 10, south: -10, east: -170, west: 170 }, 30).length).toBe(2);
  });

  it('writes counts compactly and sizes discs by count', () => {
    expect(densityLabel(950)).toBe('950');
    expect(densityLabel(1000)).toBe('1k');
    expect(densityLabel(1234)).toBe('1.2k');
    expect(densityLabel(12_345)).toBe('12k');
    expect(densityDiscSize(1)).toBe(34);
    expect(densityDiscSize(1000)).toBe(52);
    expect(densityDiscSize(10_000_000)).toBe(60);
  });
});
