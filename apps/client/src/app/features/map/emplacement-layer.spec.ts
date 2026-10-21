import { EmplacementLayer, emplacementDrawingOf, placementOf, siteEntityOf, siteLabel, sitesShown } from './emplacement-layer';
import { Emplacement } from '../../shared/models/emplacement.model';
import { locationDotDataUrl } from './location-dot';

const fixed = { id: 'f', mobility: 'FIXED' as const, position: { lat: 50, lon: -4 }, boresightDeg: 270, azimuthReference: 'TRUE_NORTH' as const };
const ship = { id: 's', mobility: 'MOBILE' as const, position: { lat: 1, lon: 1 }, boresightDeg: 90, azimuthReference: 'PLATFORM' as const };

describe('placementOf', () => {
  it('draws a fixed emplacement where it was surveyed, whatever the stream says', () => {
    expect(placementOf(fixed, () => ({ lon: 9, lat: 9, heading: 0 }))).toEqual({ lon: -4, lat: 50, boresightDeg: 270 });
  });

  it('draws a mobile emplacement at its live fix, turning a platform-relative boresight with the heading', () => {
    expect(placementOf(ship, (id) => (id === 's' ? { lon: 3, lat: 4, heading: 300 } : null))).toEqual({ lon: 3, lat: 4, boresightDeg: 30 });
  });

  it('keeps the turned boresight within 0-360 when the live heading is negative', () => {
    expect(placementOf(ship, () => ({ lon: 3, lat: 4, heading: -100 }))).toEqual({ lon: 3, lat: 4, boresightDeg: 350 });
  });

  it('falls back to the stored position while the mobile one has no live fix', () => {
    expect(placementOf(ship, () => null)).toEqual({ lon: 1, lat: 1, boresightDeg: 90 });
  });

  it('has nowhere to draw an emplacement with no position at all', () => {
    expect(placementOf({ ...ship, position: null }, () => null)).toBeNull();
  });
});

describe('emplacementDrawingOf', () => {
  const loaded = new Set(['e-1']);
  const sensor = (value?: string) => ({ identities: value ? [{ type: 'EMPLACEMENT', value }] : [{ type: 'MMSI', value: '1' }] });

  it('hands an entity to the emplacement layer when it stands for a loaded emplacement', () => {
    expect(emplacementDrawingOf(sensor('e-1'), loaded)).toBe('e-1');
  });

  it('draws an entity claiming an emplacement nobody loaded as an ordinary entity', () => {
    expect(emplacementDrawingOf(sensor('forged'), loaded)).toBeNull();
  });

  it('draws an entity with no emplacement identity as an ordinary entity', () => {
    expect(emplacementDrawingOf(sensor(), loaded)).toBeNull();
  });
});

describe('EmplacementLayer', () => {
  it('renders an emplacement marker as a violet location-dot billboard instead of a point', () => {
    const added: any[] = [];
    const viewer = {
      entities: {
        add: (cfg: any) => {
          added.push(cfg);
          return cfg;
        },
        remove: jest.fn(),
      },
      scene: { requestRender: jest.fn() },
      isDestroyed: () => false,
    };
    const Cesium = {
      Color: {
        fromCssColorString: (css: string) => ({ css, withAlpha: (a: number) => ({ css, a }) }),
        BLACK: 'black',
      },
      Cartesian3: { fromDegrees: (lon: number, lat: number, h: number) => ({ lon, lat, h }) },
      Cartesian2: function (x: number, y: number) {
        return { x, y };
      },
      NearFarScalar: function (...args: number[]) {
        return args;
      },
      CallbackProperty: function (fn: () => unknown) {
        return fn;
      },
      HeightReference: { CLAMP_TO_GROUND: 'CLAMP_TO_GROUND' },
      LabelStyle: { FILL_AND_OUTLINE: 'FILL_AND_OUTLINE' },
      HorizontalOrigin: { LEFT: 'LEFT' },
      VerticalOrigin: { CENTER: 'CENTER', BOTTOM: 'BOTTOM' },
    };

    const emp = {
      id: 'emp-1',
      name: 'EW Hilltop',
      description: '',
      sensorType: 'RF' as const,
      mobility: 'FIXED' as const,
      position: { lat: 50.8, lon: -1.1 },
      altitude: null,
      boresightDeg: 180,
      azimuthReference: 'TRUE_NORTH' as const,
      coverage: { minRangeM: 0, maxRangeM: 50000, azimuthWidthDeg: 120 },
      defaultSigma: {},
      feedId: null,
      classification: 'UNCLASSIFIED',
      exercise: false,
      active: true,
      lastReportAt: null,
      createdBy: 'admin',
      createdAt: '2026-10-20T00:00:00Z',
      updatedAt: '2026-10-20T00:00:00Z',
      version: 1,
    };

    const layer = new EmplacementLayer(viewer, Cesium);
    layer.sync([emp], null, null, false, () => null);

    expect(added).toHaveLength(1);
    expect(added[0].point).toBeUndefined();
    expect(added[0].billboard).toEqual(
      expect.objectContaining({
        image: locationDotDataUrl('#8b5cf6'),
        scale: 0.7,
        heightReference: 'CLAMP_TO_GROUND',
        verticalOrigin: 'CENTER',
      }),
    );
    expect(added[0].label.pixelOffset).toEqual({ x: 14, y: -6 });

    // Selecting scales the billboard up to 0.9, and mobile uses a dashed outer ring
    added.length = 0;
    layer.sync([emp], 'emp-1', null, false, () => null);
    expect(added[0].billboard.scale).toBe(0.9);

    added.length = 0;
    layer.sync([{ ...emp, id: 'emp-2', mobility: 'MOBILE' }], null, null, false, () => null);
    expect(added[0].billboard.image).toBe(locationDotDataUrl('#8b5cf6', { dashed: true }));
  });
});

describe('sitesShown', () => {
  it("draws a site under its connection's layer toggle", () => {
    const sites = [{ id: 'a', feedId: 'grpc-1' }, { id: 'b', feedId: 'grpc-2' }, { id: 'c', feedId: null }] as Emplacement[];
    expect(sitesShown(sites, new Set(['grpc-2'])).map((e) => e.id)).toEqual(['a', 'c']);
    expect(sitesShown(sites, new Set()).map((e) => e.id)).toEqual(['a', 'b', 'c']);
  });
});

describe('siteLabel', () => {
  const NOW = Date.parse('2026-10-20T15:00:00Z');
  it('says since when a silent site has been silent', () => {
    expect(siteLabel('Coastal', null, NOW)).toBe('Coastal');
    expect(siteLabel('Coastal', { state: 'online', intervalSec: 5, lastHeartbeatAt: null, silentSince: null }, NOW)).toBe('Coastal');
    expect(siteLabel('Coastal', { state: 'offline', intervalSec: 5, lastHeartbeatAt: '2026-10-20T14:02:00Z', silentSince: '2026-10-20T14:02:00Z' }, NOW)).toBe('Coastal · silent since 14:02Z');
    expect(siteLabel('Coastal', { state: 'never', intervalSec: 10, lastHeartbeatAt: null, silentSince: null }, NOW)).toBe('Coastal · no heartbeat yet');
  });
});

describe('siteEntityOf', () => {
  it('finds the SENSOR entity that stands for an emplacement', () => {
    const entities = [{ id: 'x', identities: [{ type: 'MMSI', value: '1' }] }, { id: 'y', identities: [{ type: 'EMPLACEMENT', value: 'e-1' }] }];
    expect(siteEntityOf(entities, 'e-1')?.id).toBe('y');
    expect(siteEntityOf(entities, 'e-2')).toBeNull();
  });
});

describe('EmplacementLayer, silent sites', () => {
  it('draws a silent site grey and hollow, says since when, dims its footprint, and redraws it violet once it heartbeats', () => {
    const added: any[] = [];
    const viewer = {
      entities: { add: (cfg: any) => { added.push(cfg); return cfg; }, remove: jest.fn() },
      scene: { requestRender: jest.fn() },
      isDestroyed: () => false,
    };
    const Cesium = {
      Color: { fromCssColorString: (css: string) => ({ css, withAlpha: (a: number) => ({ css, a }) }), BLACK: 'black' },
      Cartesian3: { fromDegrees: (lon: number, lat: number, h: number) => ({ lon, lat, h }) },
      Cartesian2: function (x: number, y: number) { return { x, y }; },
      NearFarScalar: function (...args: number[]) { return args; },
      CallbackProperty: function (fn: () => unknown) { return fn; },
      PolygonHierarchy: function (positions: unknown, holes: unknown) { return { positions, holes }; },
      HeightReference: { CLAMP_TO_GROUND: 'CLAMP_TO_GROUND' },
      LabelStyle: { FILL_AND_OUTLINE: 'FILL_AND_OUTLINE' },
      HorizontalOrigin: { LEFT: 'LEFT' },
      VerticalOrigin: { CENTER: 'CENTER', BOTTOM: 'BOTTOM' },
    };
    const emp = {
      id: 'emp-1', name: 'EW Hilltop', description: '', sensorType: 'RF' as const, mobility: 'FIXED' as const,
      position: { lat: 50.8, lon: -1.1 }, altitude: null, boresightDeg: 180, azimuthReference: 'TRUE_NORTH' as const,
      coverage: { minRangeM: 0, maxRangeM: 50000, azimuthWidthDeg: 120 }, defaultSigma: {}, feedId: 'grpc-1',
      classification: 'UNCLASSIFIED', exercise: false, active: true, lastReportAt: null, createdBy: 'op',
      createdAt: '2026-10-20T00:00:00Z', updatedAt: '2026-10-20T00:00:00Z', version: 1,
    };
    const silent = { state: 'offline' as const, intervalSec: 5, lastHeartbeatAt: '2026-10-20T14:02:00Z', silentSince: '2026-10-20T14:02:00Z' };
    const layer = new EmplacementLayer(viewer, Cesium);
    layer.sync([emp], null, null, true, () => null, () => silent);
    expect(added[0].billboard.image).toBe(locationDotDataUrl('#6b7280', { hollow: true }));
    expect(added[0].label.text).toMatch(/^EW Hilltop · silent since (\d\d-\d\d )?14:02Z$/);
    expect(added[1].polygon.material).toEqual({ css: '#6b7280', a: 0.04 });
    added.length = 0;
    layer.sync([emp], null, null, true, () => null, () => ({ ...silent, state: 'online', silentSince: null }));
    expect(added[0].billboard.image).toBe(locationDotDataUrl('#8b5cf6'));
    expect(added[0].label.text).toBe('EW Hilltop');
  });
});
