import { zoneEntityOptions } from './geofence-layer';
import { GeofenceRule } from '../../shared/models/geofence.model';

/** Just enough of Cesium for the option builders: values pass through so the spec can read them. */
const FakeCesium = {
  Color: { fromCssColorString: (hex: string) => ({ hex, alpha: 1, withAlpha: (alpha: number) => ({ hex, alpha }) }), BLACK: { hex: '#000' } },
  Cartesian3: { fromDegrees: (lng: number, lat: number, h = 0) => [lng, lat, h] },
  Cartesian2: class { constructor(public x: number, public y: number) {} },
  NearFarScalar: class { constructor(public near: number, public nearValue: number, public far: number, public farValue: number) {} },
  CallbackProperty: class { constructor(public fn: () => unknown, public isConstant: boolean) {} getValue() { return this.fn(); } },
  PolylineDashMaterialProperty: class { constructor(public opts: { color: unknown; dashLength: number }) {} },
  LabelStyle: { FILL_AND_OUTLINE: 2 },
  VerticalOrigin: { BOTTOM: 1 },
  HorizontalOrigin: { LEFT: -1 },
  HeightReference: { CLAMP_TO_GROUND: 1 },
};

const polygonZone: GeofenceRule = {
  id: 'r1', name: 'Gulf', ruleType: 'GEOFENCE',
  config: { shape: 'polygon', polygon: [[51, 27], [56, 27], [56, 24], [51, 24]], triggerOn: 'ENTRY', color: '#ef4444' },
  monitoredEntityTypes: [], severity: 'HIGH', enabled: true, createdAt: '', updatedAt: '',
};
const ringZone: GeofenceRule = {
  ...polygonZone, id: 'r3', name: 'Escort',
  config: { shape: 'ring', triggerOn: 'ENTRY', color: '#f59e0b', ring: { entityId: 'e9', entityName: 'MV HORIZON STAR', radiusM: 8000 } },
};

describe('zoneEntityOptions', () => {
  it('fills a zone in its colour at 0.14 and outlines it in the colour', () => {
    const o = zoneEntityOptions(polygonZone, false, FakeCesium, () => null) as any;
    expect(o._sentinelGeofence).toBe('r1');
    expect(o.polygon.material).toEqual({ hex: '#ef4444', alpha: 0.14 });
    expect(o.polyline.width).toBe(2);
    expect(o.polyline.material).toEqual(expect.objectContaining({ hex: '#ef4444' }));
    // closed ring: first point repeated at the end
    expect(o.polyline.positions).toHaveLength(5);
    expect(o.polyline.positions[4]).toEqual(o.polyline.positions[0]);
    expect(o.label.text).toBe('Gulf');
    expect(o.position).toEqual([51, 27, 0]);
  });

  it('thickens the outline of the selected zone', () => {
    const o = zoneEntityOptions(polygonZone, true, FakeCesium, () => null) as any;
    expect(o.polyline.width).toBe(3);
  });

  it('draws a ring around its anchor and hides it while the anchor is missing', () => {
    let anchor: [number, number] | null = null;
    const o = zoneEntityOptions(ringZone, false, FakeCesium, () => anchor) as any;
    expect(o.ellipse.semiMajorAxis).toBe(8000);
    expect(o.ellipse.semiMinorAxis).toBe(8000);
    expect(o.show.getValue()).toBe(false);
    anchor = [55.3, 25.2];
    expect(o.show.getValue()).toBe(true);
    expect(o.position.getValue()).toEqual([55.3, 25.2, 0]);
    expect(o.label.text).toBe('Escort · 8.0 km');
  });
});
