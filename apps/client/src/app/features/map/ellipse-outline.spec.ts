import { EllipseOutlineLayer, MIN_DRAWN_SEMI_AXIS_M, drawnEllipseAxes, ellipseRing } from './ellipse-outline';

const axes = (semiMajorM: number, semiMinorM: number, orientationDeg: number) => ({
  semiMajorM, semiMinorM, orientationDeg, rotationRad: Math.PI / 2 - (orientationDeg * Math.PI) / 180,
});

describe('drawnEllipseAxes', () => {
  it('floors each semi-axis at 15 m and keeps larger ones', () => {
    expect(drawnEllipseAxes(axes(1100, 2.4, 30))).toMatchObject({ semiMajorM: 1100, semiMinorM: MIN_DRAWN_SEMI_AXIS_M, orientationDeg: 30 });
    expect(drawnEllipseAxes(axes(4, 3, 0))).toMatchObject({ semiMajorM: 15, semiMinorM: 15 });
    expect(drawnEllipseAxes(axes(300, 120, 0))).toMatchObject({ semiMajorM: 300, semiMinorM: 120 });
  });
});

describe('ellipseRing', () => {
  // Metres per degree at 50° N on WGS84, from the meridional and normal radii.
  const lat0 = 50;
  const mPerDegLat = 111_229.3;
  const mPerDegLon = 71_696.5;
  const offset = (p: { lat: number; lon: number }) => ({ n: (p.lat - lat0) * mPerDegLat, e: p.lon * mPerDegLon });

  it('closes on itself and puts the major axis along its bearing from north', () => {
    const ring = ellipseRing({ lat: lat0, lon: 0 }, axes(1000, 100, 30), 72);
    expect(ring).toHaveLength(73);
    expect(ring[72]).toEqual(ring[0]);
    const tip = offset(ring[0]); // t = 0: the end of the major axis
    expect(tip.e).toBeCloseTo(1000 * Math.sin(Math.PI / 6), -1);
    expect(tip.n).toBeCloseTo(1000 * Math.cos(Math.PI / 6), -1);
    const side = offset(ring[18]); // t = 90°: the end of the minor axis
    expect(Math.hypot(side.e, side.n)).toBeCloseTo(100, 0);
  });
});

describe('EllipseOutlineLayer', () => {
  const Cesium = { Cartesian3: { fromDegrees: (lon: number, lat: number) => ({ lon, lat }) } };
  const collection = () => {
    const live: any[] = [];
    return { live, add: jest.fn((o: any) => (live.push(o), o)), remove: jest.fn((o: any) => live.splice(live.indexOf(o), 1)) };
  };

  it('adds one clamped ring per entity as its child, redraws only on change, and removes it', () => {
    const layer = new EllipseOutlineLayer(Cesium);
    const c = collection();
    const parent = { id: 'p' };
    layer.sync('e1', parent, c, { lat: 50, lon: 0 }, axes(300, 100, 0), 'red');
    expect(c.live).toHaveLength(1);
    expect(c.live[0].parent).toBe(parent);
    expect(c.live[0].polyline.clampToGround).toBe(true);
    layer.sync('e1', parent, c, { lat: 50, lon: 0 }, axes(300, 100, 0), 'red');
    expect(c.add).toHaveBeenCalledTimes(1);
    layer.sync('e1', parent, c, { lat: 50.001, lon: 0 }, axes(300, 100, 0), 'red');
    expect(c.add).toHaveBeenCalledTimes(2);
    expect(c.live).toHaveLength(1);
    layer.sync('e1', parent, c, { lat: 50.001, lon: 0 }, null, 'red');
    expect(c.live).toHaveLength(0);
    expect(layer.has('e1')).toBe(false);
  });
});
