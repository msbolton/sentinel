import { Emplacement } from '../../shared/models/emplacement.model';
import { sensorBounds, visitorBounds } from './demo-bounds';

function site(id: string, feedId: string | null, lat: number, lon: number, active = true, position = true): Emplacement {
  return { id, feedId, active, position: position ? { lat, lon } : null } as Emplacement;
}

describe('demo-bounds', () => {
  const list = [
    site('radar', 'sim', 10, 20),
    site('camera', 'sim', 12, 24),
    site('manual', null, 14, 26),
    site('asleep', 'sim', 60, 100, false),
    site('guest-a', 'visitors', -30, -40),
    site('guest-b', 'visitors', -32, -44),
    site('guest-gone', 'visitors', 70, 70, false),
    site('unplaced', 'sim', 0, 0, true, false),
  ];

  it('sensorBounds excludes the visitors connection and inactive sites', () => {
    // Near the sensors (10,20)…(14,26), each within 200 km of none of the
    // others, so each is its own cluster: the one nearest the visitor.
    expect(sensorBounds(list, 'visitors', { lat: 12, lon: 24.5 })).toEqual({ west: 23.5, south: 11.5, east: 24.5, north: 12.5 });
    // A visitor beside the inactive site still gets an active one.
    expect(sensorBounds(list, 'visitors', { lat: 60, lon: 100 })).not.toEqual(expect.objectContaining({ north: 60.5 }));
    // A visitor beside the visitors' own sites gets a sensor, never them.
    expect(sensorBounds(list, 'visitors', { lat: -31, lon: -42 })!.north).toBeGreaterThan(0);
  });

  it('visitorBounds covers only the active sites on the visitors connection', () => {
    expect(visitorBounds(list, 'visitors')).toEqual({ west: -44.5, south: -32.5, east: -39.5, north: -29.5 });
  });

  describe('sensorBounds frames the sensor cluster nearest the visitor', () => {
    // The example sites: radar and EO/IR at Keswick, ESM at Monterey, some 8000 km apart.
    const sensors = [
      site('radar', 'sim', 54.6, -3.13),
      site('eoir', 'sim', 54.62, -3.1),
      site('esm', 'sim', 36.6, -121.9),
      site('guest-a', 'visitors', 51.55, -0.15),
    ];
    const keswick = { west: -3.63, south: 54.1, east: -2.6, north: 55.12 };
    const monterey = { west: -122.4, south: 36.1, east: -121.4, north: 37.1 };
    const expectBox = (got: ReturnType<typeof sensorBounds>, want: typeof keswick) => {
      expect(got).not.toBeNull();
      for (const k of ['west', 'south', 'east', 'north'] as const) expect(got![k]).toBeCloseTo(want[k], 6);
    };

    it('a visitor in London gets Keswick, not a box across the Atlantic', () => {
      expectBox(sensorBounds(sensors, 'visitors', { lat: 51.5, lon: -0.12 }), keswick);
    });

    it('a visitor in San Francisco gets Monterey', () => {
      expectBox(sensorBounds(sensors, 'visitors', { lat: 37.77, lon: -122.42 }), monterey);
    });

    it('with no position known, the largest cluster', () => {
      expectBox(sensorBounds(sensors, 'visitors', null), keswick);
      expectBox(sensorBounds(sensors, 'visitors'), keswick);
    });

    it('sites within 200 km of one another chain into one cluster', () => {
      // About 150 km apart in a line: one cluster although the ends are 300 km apart.
      const chain = [site('a', 'sim', 50, 0), site('b', 'sim', 51.35, 0), site('c', 'sim', 52.7, 0), site('far', 'sim', 10, 10)];
      expectBox(sensorBounds(chain, 'visitors', { lat: 50, lon: 0 }), { west: -0.5, south: 49.5, east: 0.5, north: 53.2 });
    });
  });

  it('are null when there is nothing to show', () => {
    expect(sensorBounds([], 'visitors')).toBeNull();
    expect(visitorBounds([list[0]], 'visitors')).toBeNull();
    // Without a visitors connection no site counts as a visitor.
    expect(visitorBounds(list, undefined)).toBeNull();
    expect(sensorBounds([site('a', 'x', 1, 2)], undefined)).toEqual({ west: 1.5, south: 0.5, east: 2.5, north: 1.5 });
  });
});
