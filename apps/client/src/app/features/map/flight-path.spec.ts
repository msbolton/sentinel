import { EntitySource, EntityType } from '../../shared/models/entity.model';
import { defaultFlightPathDays, icaoHexOf } from '../../shared/models/flight-path.model';
import { pathRuns, pathSummary } from './flight-path';

const aircraft = (source: EntitySource, icaoHex?: string) =>
  ({ id: 'a', entityType: EntityType.AIRCRAFT, source, platformData: icaoHex ? { adsb: { icaoHex } } : undefined }) as any;

describe('flight path', () => {
  it('finds the ICAO address of aircraft only', () => {
    expect(icaoHexOf(aircraft(EntitySource.INTELSKY, 'ae7457'))).toBe('AE7457');
    expect(icaoHexOf(aircraft(EntitySource.OPENSKY, 'nope'))).toBeNull();
    expect(icaoHexOf(aircraft(EntitySource.INTELSKY))).toBeNull();
    expect(icaoHexOf({ ...aircraft(EntitySource.INTELSKY, 'AE7457'), entityType: EntityType.VESSEL })).toBeNull();
    expect(icaoHexOf(null)).toBeNull();
  });

  it('shows today for IntelSky aircraft and nothing by default for others', () => {
    expect(defaultFlightPathDays(aircraft(EntitySource.INTELSKY, 'AE7457'))).toBe(1);
    expect(defaultFlightPathDays(aircraft(EntitySource.OPENSKY, 'A1B2C3'))).toBe(0);
    expect(defaultFlightPathDays(null)).toBe(0);
  });

  it('turns points into runs at altitude, split at the antimeridian', () => {
    const runs = pathRuns([
      { lat: 26, lon: 179, altFt: 30000, ts: 1 },
      { lat: 26.1, lon: 179.5, altFt: 31000, ts: 2 },
      { lat: 26.2, lon: -179.8, altFt: 32000, ts: 3 },
      { lat: 26.3, lon: -179.2, altFt: -50, ts: 4 },
    ]);
    expect(runs.length).toBe(2);
    expect(runs[0][0]).toEqual({ lon: 179, lat: 26, height: 30000 * 0.3048 });
    expect(runs[1][1].height).toBe(0);
  });

  it('summarises the path', () => {
    expect(pathSummary(null)).toBe('No IntelSky history for this aircraft');
    expect(pathSummary({ hex: 'AE7457', source: 'INTELSKY', days: 1, points: [] })).toBe('No IntelSky history for this aircraft');
    expect(pathSummary({ hex: 'AE7457', source: 'INTELSKY', days: 3, points: [{ lat: 0, lon: 0, altFt: 0, ts: 1792100404 }, { lat: 0, lon: 1, altFt: 0, ts: 1792100704 }] }))
      .toBe('2 points since 15 Oct 21:40 UTC'); // from ts; IntelSky's own date and time strings are UTC+2
  });
});
