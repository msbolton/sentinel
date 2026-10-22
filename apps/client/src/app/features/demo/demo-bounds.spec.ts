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
    expect(sensorBounds(list, 'visitors')).toEqual({ west: 19.5, south: 9.5, east: 26.5, north: 14.5 });
  });

  it('visitorBounds covers only the active sites on the visitors connection', () => {
    expect(visitorBounds(list, 'visitors')).toEqual({ west: -44.5, south: -32.5, east: -39.5, north: -29.5 });
  });

  it('are null when there is nothing to show', () => {
    expect(sensorBounds([], 'visitors')).toBeNull();
    expect(visitorBounds([list[0]], 'visitors')).toBeNull();
    // Without a visitors connection no site counts as a visitor.
    expect(visitorBounds(list, undefined)).toBeNull();
    expect(sensorBounds([site('a', 'x', 1, 2)], undefined)).toEqual({ west: 1.5, south: 0.5, east: 2.5, north: 1.5 });
  });
});
