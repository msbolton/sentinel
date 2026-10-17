import { EARTH_RADIUS_M, MAX_FOOTPRINT_RADIUS_M, footprintRadiusM, sampleOrbit, splitAtAntimeridian } from './orbit-geometry';
import { parseTle, periodMinutes, positionAt } from './satellite-tracker';

const ISS_LINE1 = '1 25544U 98067A   26063.86671769  .00009014  00000+0  17477-3 0  9999';
const ISS_LINE2 = '2 25544  51.6315  96.2009 0008177 157.5272 202.6076 15.48447334555585';
const START_MS = Date.UTC(2026, 2, 4, 20, 0, 0);

describe('sampleOrbit', () => {
  it('samples one revolution, earth-fixed, starting at the current position', () => {
    const satrec = parseTle(ISS_LINE1, ISS_LINE2)!;
    const period = periodMinutes(satrec);
    const samples = sampleOrbit(satrec, START_MS, period, 120);
    expect(samples.length).toBe(121);
    const first = positionAt(satrec, START_MS)!;
    expect(samples[0].lat).toBeCloseTo(first.lat, 6);
    expect(samples[0].lon).toBeCloseTo(first.lon, 6);
    expect(samples[0].atMs).toBe(START_MS);
    // One inertial revolution later the latitude repeats, but the Earth has
    // turned under the orbit by about a quarter degree per minute.
    const last = samples[samples.length - 1];
    expect(Math.abs(last.lat - first.lat)).toBeLessThan(2.5);
    let dLon = first.lon - last.lon;
    if (dLon < -180) dLon += 360;
    if (dLon > 180) dLon -= 360;
    expect(dLon).toBeGreaterThan(18);
    expect(dLon).toBeLessThan(28);
    for (const s of samples) {
      expect(s.height).toBeGreaterThan(380_000);
      expect(s.height).toBeLessThan(450_000);
    }
  });

  it('returns nothing for a bad period', () => {
    const satrec = parseTle(ISS_LINE1, ISS_LINE2)!;
    expect(sampleOrbit(satrec, START_MS, 0)).toEqual([]);
    expect(sampleOrbit(satrec, START_MS, 90, 1)).toEqual([]);
  });
});

describe('footprintRadiusM', () => {
  it('matches the horizon geometry and caps at the renderer limit', () => {
    expect(footprintRadiusM(0)).toBe(0);
    expect(footprintRadiusM(400_000)).toBeGreaterThan(2_150_000);
    expect(footprintRadiusM(400_000)).toBeLessThan(2_230_000);
    const geo = EARTH_RADIUS_M * Math.acos(EARTH_RADIUS_M / (EARTH_RADIUS_M + 35_786_000));
    expect(geo).toBeGreaterThan(MAX_FOOTPRINT_RADIUS_M);
    expect(footprintRadiusM(35_786_000)).toBe(MAX_FOOTPRINT_RADIUS_M);
  });
});

describe('splitAtAntimeridian', () => {
  it('breaks a run where the longitude wraps and drops single-point runs', () => {
    const runs = splitAtAntimeridian([{ lon: 170 }, { lon: 178 }, { lon: -179 }, { lon: -170 }, { lon: -160 }]);
    expect(runs).toEqual([[{ lon: 170 }, { lon: 178 }], [{ lon: -179 }, { lon: -170 }, { lon: -160 }]]);
    expect(splitAtAntimeridian([{ lon: 179 }, { lon: -179 }])).toEqual([]);
    expect(splitAtAntimeridian([{ lon: 1 }, { lon: 2 }, { lon: 3 }])).toEqual([[{ lon: 1 }, { lon: 2 }, { lon: 3 }]]);
  });
});
