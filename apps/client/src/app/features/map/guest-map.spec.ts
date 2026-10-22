import { Emplacement } from '../../shared/models/emplacement.model';
import { boundsOf, isOwnSiteEntity, withoutOwnSite } from './guest-map';

describe('guest-map', () => {
  it('withoutOwnSite drops only the own id', () => {
    const list = [{ id: 'a' }, { id: 'own' }, { id: 'b' }] as Emplacement[];

    expect(withoutOwnSite(list, 'own').map((e) => e.id)).toEqual(['a', 'b']);
    // No own site (a non-guest, or a guest not placed yet): the list is untouched.
    expect(withoutOwnSite(list, null)).toBe(list);
    expect(withoutOwnSite(list, 'other').map((e) => e.id)).toEqual(['a', 'own', 'b']);
  });

  it('isOwnSiteEntity matches the EMPLACEMENT identity value', () => {
    const own = { identities: [{ type: 'MMSI', value: 'own' }, { type: 'EMPLACEMENT', value: 'own' }] };
    const other = { identities: [{ type: 'EMPLACEMENT', value: 'someone-else' }] };
    const notASite = { identities: [{ type: 'MMSI', value: 'own' }] };

    expect(isOwnSiteEntity(own, 'own')).toBe(true);
    expect(isOwnSiteEntity(other, 'own')).toBe(false);
    expect(isOwnSiteEntity(notASite, 'own')).toBe(false);
    expect(isOwnSiteEntity({ identities: null }, 'own')).toBe(false);
    expect(isOwnSiteEntity({}, 'own')).toBe(false);
    expect(isOwnSiteEntity(own, null)).toBe(false);
  });

  it('boundsOf pads and handles a single point', () => {
    expect(boundsOf([{ lat: 10, lon: 20 }])).toEqual({ west: 19.5, south: 9.5, east: 20.5, north: 10.5 });
    expect(boundsOf([{ lat: 10, lon: 20 }, { lat: -5, lon: 30 }, { lat: 2, lon: 25 }], 1)).toEqual({ west: 19, south: -6, east: 31, north: 11 });
    expect(boundsOf([{ lat: 10, lon: 20 }], 0)).toEqual({ west: 20, south: 10, east: 20, north: 10 });
    // Padding never leaves the globe.
    expect(boundsOf([{ lat: 89.8, lon: 179.9 }, { lat: -89.9, lon: -179.8 }])).toEqual({ west: -180, south: -90, east: 180, north: 90 });
    expect(boundsOf([])).toBeNull();
  });
});
