import { EllipseAxes } from './uncertainty';

/**
 * The smallest semi-axis an uncertainty ellipse is drawn with, in metres. A
 * camera fix at a grazing angle is a sliver kilometres long and metres wide:
 * drawn true to size it disappears, and the point reads as certain.
 */
export const MIN_DRAWN_SEMI_AXIS_M = 15;

/** How many points the outline ring has. */
const RING_POINTS = 72;

const WGS84_A = 6378137;
const WGS84_E2 = 0.00669437999014;

/** The axes an ellipse is drawn with: each semi-axis at least MIN_DRAWN_SEMI_AXIS_M. */
export function drawnEllipseAxes(axes: EllipseAxes): EllipseAxes {
  return {
    ...axes,
    semiMajorM: Math.max(axes.semiMajorM, MIN_DRAWN_SEMI_AXIS_M),
    semiMinorM: Math.max(axes.semiMinorM, MIN_DRAWN_SEMI_AXIS_M),
  };
}

/**
 * The boundary of an ellipse centred on a point, as a closed ring of
 * longitude/latitude pairs (the first point repeated last). The major axis
 * lies along orientationDeg, clockwise from true north. Offsets are taken to
 * degrees with the WGS84 radii of curvature at the centre, which is exact
 * enough at the few kilometres an uncertainty ellipse spans.
 */
export function ellipseRing(centre: { lat: number; lon: number }, axes: EllipseAxes, points = RING_POINTS): { lat: number; lon: number }[] {
  const phi = (centre.lat * Math.PI) / 180;
  const w = 1 - WGS84_E2 * Math.sin(phi) ** 2;
  const meridional = (WGS84_A * (1 - WGS84_E2)) / w ** 1.5;
  const normal = WGS84_A / Math.sqrt(w);
  const theta = (axes.orientationDeg * Math.PI) / 180;
  const [ue, un] = [Math.sin(theta), Math.cos(theta)]; // along the major axis
  const [ve, vn] = [Math.cos(theta), -Math.sin(theta)]; // along the minor axis
  const ring: { lat: number; lon: number }[] = [];
  for (let i = 0; i <= points; i++) {
    const t = ((i % points) / points) * 2 * Math.PI;
    const a = axes.semiMajorM * Math.cos(t);
    const b = axes.semiMinorM * Math.sin(t);
    const east = a * ue + b * ve;
    const north = a * un + b * vn;
    ring.push({
      lat: centre.lat + ((north / meridional) * 180) / Math.PI,
      lon: centre.lon + ((east / (normal * Math.cos(phi))) * 180) / Math.PI,
    });
  }
  return ring;
}

/**
 * Ground-clamped outlines of the uncertainty ellipses. Cesium draws no
 * outline for an ellipse clamped to the ground, so each is a polyline of its
 * own, clamped to the ground (which works with terrain on or off), and a
 * child of the entity it belongs to, so it shows and hides with it.
 */
export class EllipseOutlineLayer {
  private readonly drawn = new Map<string, { entity: any; collection: any; key: string }>();

  constructor(private readonly Cesium: any) {}

  /**
   * Draw, move or remove the outline of entity id: around centre with the
   * given (already floored) axes, or none when axes is null.
   */
  sync(id: string, parent: any, collection: any, centre: { lat: number; lon: number } | null, axes: EllipseAxes | null, color: any): void {
    if (!centre || !axes) {
      this.remove(id);
      return;
    }
    const key = JSON.stringify([centre.lat, centre.lon, axes.semiMajorM, axes.semiMinorM, axes.orientationDeg, color?.toCssColorString?.() ?? String(color)]);
    const existing = this.drawn.get(id);
    if (existing?.key === key && existing.collection === collection) return;
    this.remove(id);
    const C = this.Cesium;
    const positions = ellipseRing(centre, axes).map((p) => C.Cartesian3.fromDegrees(p.lon, p.lat));
    const entity = collection.add({
      parent,
      polyline: { positions, width: 1.5, material: color, clampToGround: true },
    });
    this.drawn.set(id, { entity, collection, key });
  }

  remove(id: string): void {
    const d = this.drawn.get(id);
    if (!d) return;
    d.collection.remove(d.entity);
    this.drawn.delete(id);
  }

  has(id: string): boolean {
    return this.drawn.has(id);
  }
}
