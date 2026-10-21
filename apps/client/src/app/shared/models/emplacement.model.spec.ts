import {
  draftOf, emplacementIdOf, inputOf, newDraft, Emplacement, coverageSummary,
  bearingsOf, bearingEnd, formatZulu, heartbeatBadgeOf, livenessOf, rowHeartbeatOf, siteHeartbeatOf, sitesSummaryOf,
} from './emplacement.model';

const stored: Emplacement = {
  id: 'e1', name: 'Coastal', description: '', mobility: 'FIXED', sensorType: 'RADAR',
  position: { lat: 50, lon: -4 }, altitude: null, boresightDeg: 270, azimuthReference: 'TRUE_NORTH',
  coverage: { minRangeM: 0, maxRangeM: 60000, azimuthWidthDeg: 120 }, defaultSigma: {},
  feedId: null, classification: 'UNCLASSIFIED', exercise: false, active: true,
  lastReportAt: null, createdBy: 'u', createdAt: '', updatedAt: '', version: 3,
};

describe('emplacement model', () => {
  it('finds the emplacement id of an emplacement entity, and nothing on other entities', () => {
    expect(emplacementIdOf({ identities: [{ type: 'CALLSIGN', value: 'R1' }, { type: 'EMPLACEMENT', value: 'e1' }] })).toBe('e1');
    expect(emplacementIdOf({ identities: [{ type: 'ICAO24', value: 'ABC123' }] })).toBeNull();
    expect(emplacementIdOf({})).toBeNull();
  });

  it('turns a stored emplacement into a draft and back into an input', () => {
    const d = draftOf(stored);
    expect(d.id).toBe('e1');
    expect(d.version).toBe(3);
    expect(newDraft().version).toBeNull();
    const input = inputOf({ ...d, feedId: null });
    expect(input.feedId).toBe(''); // "" unbinds on the server
    expect(input.position).toEqual({ lat: 50, lon: -4 });
    expect('id' in input).toBe(false);
    expect('version' in input).toBe(false);
  });

  it('sends no coverage as all zeros', () => {
    const input = inputOf({ ...newDraft(), coverage: { minRangeM: 0, maxRangeM: 0, azimuthWidthDeg: 120 } });
    expect(input.coverage).toEqual({ minRangeM: 0, maxRangeM: 0, azimuthWidthDeg: 0 });
  });

  it('summarises coverage', () => {
    expect(coverageSummary(stored.coverage)).toBe('120° · 60 km');
    expect(coverageSummary({ minRangeM: 0, maxRangeM: 5000, azimuthWidthDeg: 360 })).toBe('omni · 5 km');
    expect(coverageSummary({ minRangeM: 0, maxRangeM: 0, azimuthWidthDeg: 0 })).toBe('no coverage');
  });
});

describe('heartbeats', () => {
  const NOW = Date.parse('2026-10-20T14:10:00Z');
  const site = (fields: Record<string, string>) => ({ platformData: { generic: { fields } } });

  it('judges a site online from its last heartbeat against the server clock', () => {
    const last = '2026-10-20T14:09:50Z';
    expect(siteHeartbeatOf(site({ heartbeat: 'online', heartbeatIntervalSec: '5', lastHeartbeatAt: last }), NOW)).toEqual({
      state: 'online', intervalSec: 5, lastHeartbeatAt: last, silentSince: null,
    });
    // Published online, but three intervals have passed: offline now, before any publish says so.
    expect(siteHeartbeatOf(site({ heartbeat: 'online', heartbeatIntervalSec: '5', lastHeartbeatAt: '2026-10-20T14:09:45Z' }), NOW)).toEqual({
      state: 'offline', intervalSec: 5, lastHeartbeatAt: '2026-10-20T14:09:45Z', silentSince: '2026-10-20T14:09:45Z',
    });
  });

  it('reads a silent site, one never heard from and one waiting after a restart', () => {
    expect(siteHeartbeatOf(site({ heartbeat: 'offline', heartbeatIntervalSec: '5', lastHeartbeatAt: '2026-10-20T14:02:00Z', silentSince: '2026-10-20T14:02:00Z' }), NOW))
      .toEqual({ state: 'offline', intervalSec: 5, lastHeartbeatAt: '2026-10-20T14:02:00Z', silentSince: '2026-10-20T14:02:00Z' });
    expect(siteHeartbeatOf(site({ heartbeat: 'never', heartbeatIntervalSec: '10' }), NOW)?.state).toBe('never');
    expect(siteHeartbeatOf(site({ heartbeat: 'pending', heartbeatIntervalSec: '10' }), NOW)?.state).toBe('pending');
  });

  it('has nothing to say of a site whose connection does not heartbeat it', () => {
    expect(siteHeartbeatOf({ platformData: {} }, NOW)).toBeNull();
    expect(siteHeartbeatOf(null, NOW)).toBeNull();
  });

  it('bootstraps from the REST row', () => {
    expect(rowHeartbeatOf({ heartbeat: 'offline', silentSince: '2026-10-20T14:02:00Z', heartbeatIntervalSec: 5, lastHeartbeatAt: '2026-10-20T14:02:00Z' }))
      .toEqual({ state: 'offline', intervalSec: 5, lastHeartbeatAt: '2026-10-20T14:02:00Z', silentSince: '2026-10-20T14:02:00Z' });
    expect(rowHeartbeatOf({})).toBeNull();
  });

  it("summarises a connection's sites and badges each", () => {
    const detail = {
      emplacements: [
        { id: 'a', name: 'A', classification: 'UNCLASSIFIED', state: 'online', intervalSec: 5 },
        { id: 'b', name: 'B', classification: 'UNCLASSIFIED', state: 'offline', lastHeartbeatAt: '2026-10-20T14:02:00Z', intervalSec: 5 },
        { id: 'c', name: 'C', classification: 'UNCLASSIFIED', state: 'pending', intervalSec: 10 },
      ],
    };
    expect(sitesSummaryOf(detail)).toBe('1 of 3 sites online');
    expect(sitesSummaryOf({ reportsOk: 1 })).toBeNull();
    const live = livenessOf(detail);
    expect(heartbeatBadgeOf(stored, live.get('a'), NOW)).toEqual({ label: 'online', tone: 'online' });
    expect(heartbeatBadgeOf(stored, live.get('b'), NOW)).toEqual({ label: 'silent since 14:02Z', tone: 'offline' });
    expect(heartbeatBadgeOf(stored, live.get('c'), NOW)).toEqual({ label: 'awaiting first heartbeat', tone: 'pending' });
    expect(heartbeatBadgeOf({ ...stored, heartbeat: 'never' }, undefined, NOW)).toEqual({ label: 'awaiting first heartbeat', tone: 'offline' });
    expect(heartbeatBadgeOf(stored, undefined, NOW)).toBeNull();
  });

  it('writes times as Zulu, with the day when it is not today', () => {
    expect(formatZulu('2026-10-20T14:02:00Z', NOW)).toBe('14:02Z');
    expect(formatZulu('2026-10-19T23:59:00Z', NOW)).toBe('10-19 23:59Z');
    expect(formatZulu('nope', NOW)).toBe('');
  });
});

describe('bearings', () => {
  const NOW = Date.parse('2026-10-20T14:10:00Z');
  const site = (fields: Record<string, string>) => ({ platformData: { generic: { fields } } });
  const b = (observedAt: string, extra: Record<string, unknown> = {}) => ({
    trackId: 't1', azimuthDeg: 45, elevationDeg: 2, lengthM: 3000, reason: 'no_ground_intersection', observedAt, ...extra,
  });

  it('parses the bearings a site carries', () => {
    const list = [b('2026-10-20T14:09:58Z'), b('2026-10-20T14:09:59Z', { trackId: undefined, classification: 'vehicle', confidence: 0.8 })];
    const got = bearingsOf(site({ bearings: JSON.stringify(list), bearingTtlSec: '10' }), NOW);
    expect(got).toHaveLength(2);
    expect(got[0]).toMatchObject({ trackId: 't1', azimuthDeg: 45, elevationDeg: 2, lengthM: 3000, reason: 'no_ground_intersection' });
    expect(got[1]).toMatchObject({ classification: 'vehicle', confidence: 0.8 });
  });

  it('drops a bearing whose ttl has run out, though the site has not republished', () => {
    const list = [b('2026-10-20T14:09:49Z'), b('2026-10-20T14:09:50Z', { trackId: 'edge' }), b('2026-10-20T14:09:55Z', { trackId: 'fresh' })];
    const ids = bearingsOf(site({ bearings: JSON.stringify(list), bearingTtlSec: '10' }), NOW).map((x) => x.trackId);
    expect(ids).toEqual(['fresh']); // observedAt + ttl <= now is expired
  });

  it('uses the default ttl when none is given or it is not a number', () => {
    const list = JSON.stringify([b('2026-10-20T14:09:55Z'), b('2026-10-20T14:09:45Z', { trackId: 'old' })]);
    expect(bearingsOf(site({ bearings: list }), NOW)).toHaveLength(1);
    expect(bearingsOf(site({ bearings: list, bearingTtlSec: 'x' }), NOW)).toHaveLength(1);
  });

  it('has none for a missing field, malformed JSON, a non-array or no entity', () => {
    expect(bearingsOf(site({}), NOW)).toEqual([]);
    expect(bearingsOf(site({ bearings: '{nope' }), NOW)).toEqual([]);
    expect(bearingsOf(site({ bearings: '{"a":1}' }), NOW)).toEqual([]);
    expect(bearingsOf({ platformData: {} }, NOW)).toEqual([]);
    expect(bearingsOf(null, NOW)).toEqual([]);
  });

  it('skips entries that are not bearings', () => {
    const list = [null, 5, { azimuthDeg: 'x' }, b('nope'), { ...b('2026-10-20T14:09:59Z'), reason: 'bogus' }, b('2026-10-20T14:09:59Z', { trackId: 'ok' })];
    expect(bearingsOf(site({ bearings: JSON.stringify(list), bearingTtlSec: '10' }), NOW).map((x) => x.trackId)).toEqual(['ok']);
  });
});

describe('bearingEnd', () => {
  const bearing = (azimuthDeg: number, elevationDeg: number, lengthM = 1000) => ({
    azimuthDeg, elevationDeg, lengthM, reason: 'no_ground_intersection' as const, observedAt: '2026-10-20T14:10:00Z',
  });

  it('goes east along the ellipsoid from the equator, curving away from it', () => {
    const end = bearingEnd({ lat: 0, lon: 0, alt: 0 }, bearing(90, 0));
    expect(end.lat).toBeCloseTo(0, 5);
    expect(Math.abs(end.lon - 0.008983)).toBeLessThan(1e-5);
    expect(end.alt).toBeGreaterThan(0.05);
    expect(end.alt).toBeLessThan(0.11);
  });

  it('goes north for azimuth 0', () => {
    const end = bearingEnd({ lat: 50, lon: 10, alt: 100 }, bearing(0, 0));
    expect(end.lat).toBeGreaterThan(50.008);
    expect(end.lat).toBeLessThan(50.0095);
    expect(Math.abs(end.lon - 10)).toBeLessThan(1e-6);
  });

  it('climbs with elevation and carries the origin altitude', () => {
    const end = bearingEnd({ lat: 10, lon: 20, alt: 250 }, bearing(123, 90, 500));
    expect(end.alt).toBeCloseTo(750, 1);
    expect(end.lat).toBeCloseTo(10, 6);
    expect(end.lon).toBeCloseTo(20, 6);
  });
});
