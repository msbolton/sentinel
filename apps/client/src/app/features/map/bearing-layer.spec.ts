import { BearingLayer, bearingLabel, bearingAlpha } from './bearing-layer';
import { BEARING_LINE_CONFIG } from './cesium-config';
import { SiteBearing } from '../../shared/models/emplacement.model';

const T0 = Date.parse('2026-10-20T14:10:00Z');

function rig(groundHeight: number | undefined = undefined) {
  const live = new Map<string, any>();
  const viewer = {
    entities: {
      add: jest.fn((cfg: any) => {
        const e = { ...cfg };
        live.set(cfg.id, e);
        return e;
      }),
      remove: jest.fn((e: any) => live.delete(e.id)),
    },
    scene: { requestRender: jest.fn(), globe: { getHeight: jest.fn(() => groundHeight) } },
    isDestroyed: () => false,
  };
  const Cesium = {
    Color: { fromCssColorString: (css: string) => ({ css, withAlpha: (a: number) => ({ css, a }) }) },
    Cartesian3: { fromDegrees: (lon: number, lat: number, h: number) => ({ lon, lat, h }) },
    Cartesian2: function (this: any, x: number, y: number) { this.x = x; this.y = y; },
    Cartographic: { fromDegrees: (lon: number, lat: number) => ({ lon, lat }) },
    PolylineDashMaterialProperty: function (this: any, o: any) { Object.assign(this, o); },
    LabelStyle: { FILL_AND_OUTLINE: 2 },
    VerticalOrigin: { BOTTOM: 1 },
    HorizontalOrigin: { LEFT: 1 },
    NearFarScalar: function (this: any, ...a: number[]) { this.v = a; },
    ArcType: { NONE: 0, GEODESIC: 1, RHUMB: 2 },
  };
  return { viewer, Cesium, live, layer: new BearingLayer(viewer, Cesium) };
}

const bearing = (extra: Partial<SiteBearing> = {}): SiteBearing => ({
  trackId: 't1', azimuthDeg: 90, elevationDeg: 0, lengthM: 1000, reason: 'no_ground_intersection',
  observedAt: new Date(T0 - 2_000).toISOString(), ...extra,
});
const site = (extra: Record<string, unknown> = {}) => ({
  siteEntityId: 'site-1', feedId: 'feed-1', origin: { lat: 0, lon: 0, alt: 10 as number | null }, color: '#8b5cf6',
  bearings: [bearing()], ttlSec: 10, ...extra,
});

describe('bearingLabel', () => {
  it('writes the class and confidence, or Unknown', () => {
    expect(bearingLabel(bearing({ classification: 'vehicle', confidence: 0.874 }))).toBe('vehicle 87%');
    expect(bearingLabel(bearing({ classification: 'person' }))).toBe('person');
    expect(bearingLabel(bearing())).toBe('Unknown');
  });
});

describe('bearingAlpha', () => {
  it('is opaque until the last 30% of the ttl, then fades to nothing', () => {
    const at = (ageMs: number) => bearingAlpha(new Date(T0 - ageMs).toISOString(), 10, T0);
    expect(at(0)).toBe(1);
    expect(at(7_000)).toBe(1);
    expect(at(8_500)).toBeCloseTo(0.5, 1);
    expect(at(10_000)).toBeLessThanOrEqual(BEARING_LINE_CONFIG.minAlpha);
    expect(at(12_000)).toBeLessThanOrEqual(BEARING_LINE_CONFIG.minAlpha);
  });
});

describe('bearing fade from receipt', () => {
  it('fades and expires a line from receivedAt when the bearing has one', () => {
    const { layer, live } = rig();
    const late = bearing({ observedAt: new Date(T0 - 30_000).toISOString(), receivedAt: new Date(T0 - 1_000).toISOString() });
    layer.sync([site({ bearings: [late] })], new Set(), T0);
    expect(live.has('bearing:site-1:t1')).toBe(true);
    expect(live.get('bearing:site-1:t1').polyline.material.color.a).toBe(1);
  });
});

describe('BearingLayer', () => {
  it('adds one dashed polyline per bearing from the origin to the far end, with the site colour and an id to pick by', () => {
    const { layer, live } = rig();
    layer.sync([site({ bearings: [bearing(), bearing({ trackId: undefined, azimuthDeg: 0 })] })], new Set(), T0);
    expect([...live.keys()].sort()).toEqual(['bearing:site-1:1', 'bearing:site-1:t1']);
    const line = live.get('bearing:site-1:t1');
    expect(line.polyline.width).toBe(BEARING_LINE_CONFIG.width);
    expect(line.polyline.material.dashLength).toBe(BEARING_LINE_CONFIG.dashLength);
    expect(line.polyline.material.color.css).toBe('#8b5cf6');
    expect(line.polyline.positions[0]).toEqual({ lon: 0, lat: 0, h: 10 });
    expect(line.polyline.positions[1].lon).toBeCloseTo(0.008983, 5);
    expect(line._sentinelBearing).toBe('site-1');
    expect(line._sentinelBearingKey).toBe('t:t1');
    expect(live.get('bearing:site-1:1')._sentinelBearingKey).toMatch(/^o:/);
    expect(line.polyline.arcType).toBe(0); // ArcType.NONE: a straight line, as the bearing is
    expect(line.polyline.clampToGround).not.toBe(true);
    expect(line.label.text).toBe('Unknown');
  });

  it('starts a line with no origin altitude on the ground the globe reports there', () => {
    const { layer, live } = rig(312);
    layer.sync([site({ origin: { lat: 0, lon: 0, alt: null } })], new Set(), T0);
    expect(live.get('bearing:site-1:t1').polyline.positions[0].h).toBe(312);
  });

  it('falls back to sea level when the globe has no height there yet', () => {
    const { layer, live } = rig(undefined);
    layer.sync([site({ origin: { lat: 0, lon: 0, alt: null } })], new Set(), T0);
    expect(live.get('bearing:site-1:t1').polyline.positions[0].h).toBe(0);
  });

  it('removes a line once its bearing is gone, and a site once it is gone', () => {
    const { layer, live } = rig();
    layer.sync([site(), site({ siteEntityId: 'site-2' })], new Set(), T0);
    expect(live.size).toBe(2);
    layer.sync([site({ bearings: [] }), site({ siteEntityId: 'site-2' })], new Set(), T0 + 1000);
    expect([...live.keys()]).toEqual(['bearing:site-2:t1']);
    layer.sync([], new Set(), T0 + 2000);
    expect(live.size).toBe(0);
  });

  it('removes a line the client judges expired on the next sync, whatever the site still says', () => {
    const { layer, live } = rig();
    const s = site();
    layer.sync([s], new Set(), T0);
    expect(live.size).toBe(1);
    layer.sync([s], new Set(), T0 + 9_000); // observed 2 s before T0, ttl 10 s: gone at T0 + 8 s
    expect(live.size).toBe(0);
  });

  it('hides the lines of a hidden feed and brings them back', () => {
    const { layer, live } = rig();
    layer.sync([site()], new Set(['feed-1']), T0);
    expect(live.size).toBe(0);
    layer.sync([site()], new Set(), T0 + 1000);
    expect(live.size).toBe(1);
    layer.sync([site({ feedId: null })], new Set(['feed-1']), T0 + 2000);
    expect(live.size).toBe(1); // no feed, no layer to hide
  });

  it('fades a line as it nears the end of its ttl, redrawing only when the look changes', () => {
    const { layer, live, viewer } = rig();
    layer.sync([site()], new Set(), T0);
    expect(live.get('bearing:site-1:t1').polyline.material.color.a).toBe(1);
    const adds = viewer.entities.add.mock.calls.length;
    layer.sync([site()], new Set(), T0 + 1_000); // still inside the opaque part
    expect(viewer.entities.add.mock.calls.length).toBe(adds);
    layer.sync([site()], new Set(), T0 + 6_000); // 8 s old of 10: fading
    expect(live.get('bearing:site-1:t1').polyline.material.color.a).toBeLessThan(1);
    expect(live.size).toBe(1);
  });

  it('leaves no orphans after destroy', () => {
    const { layer, live } = rig();
    layer.sync([site(), site({ siteEntityId: 'site-2' })], new Set(), T0);
    layer.destroy();
    expect(live.size).toBe(0);
  });
});
