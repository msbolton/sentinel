import { SiteBearing, bearingClockOf, bearingEnd, bearingExpired } from '../../shared/models/emplacement.model';
import { BEARING_LINE_CONFIG } from './cesium-config';

/** One camera site's bearing lines, ready to draw. */
export interface BearingSite {
  siteEntityId: string;
  /** The connection whose layer toggle shows or hides these lines; null has none to hide it. */
  feedId: string | null;
  /** altitude is metres above the ellipsoid; null starts the lines on the ground. */
  origin: { lat: number; lon: number; alt: number | null };
  color: string;
  bearings: SiteBearing[];
  ttlSec: number;
}

/** "vehicle 87%", or "Unknown" for a bearing nothing classified. */
export function bearingLabel(b: Pick<SiteBearing, 'classification' | 'confidence'>): string {
  const name = b.classification || 'Unknown';
  return b.classification && b.confidence != null && Number.isFinite(b.confidence) ? `${name} ${Math.round(b.confidence * 100)}%` : name;
}

/**
 * How opaque a bearing line whose ttl runs from since (bearingClockOf) is:
 * fully, until the last fadeFraction of its ttl,
 * then fading to minAlpha. Rounded to alphaStep, so a layer redrawing on a
 * timer redraws a line only when the fade has moved a visible step.
 */
export function bearingAlpha(since: string, ttlSec: number, nowMs: number): number {
  const { fadeFraction, minAlpha, alphaStep } = BEARING_LINE_CONFIG;
  const remainingMs = Date.parse(since) + ttlSec * 1000 - nowMs;
  const window = ttlSec * 1000 * fadeFraction;
  const a = window > 0 ? remainingMs / window : 1;
  const stepped = Math.round(a / alphaStep) * alphaStep;
  return Math.min(1, Math.max(minAlpha, Math.round(stepped * 100) / 100));
}

/**
 * Keeps the viewer's entities in step with the bearing lines of camera sites:
 * one entity per bearing, a dashed polyline from the site to the bearing's far
 * end with a label there. The entity is id'd `bearing:<site>:<track|index>` and
 * carries `_sentinelBearing` (the site entity's id) so a click can select the
 * site. A line is dropped when its bearing is gone, expired by the client's
 * own reckoning, or its connection's layer is hidden.
 */
export class BearingLayer {
  private readonly drawn = new Map<string, { entity: any; key: string }>();

  constructor(private readonly viewer: any, private readonly Cesium: any) {}

  sync(sites: BearingSite[], hidden: ReadonlySet<string>, nowMs: number): void {
    const wanted = new Set<string>();
    for (const site of sites) {
      if (site.feedId && hidden.has(site.feedId)) continue;
      site.bearings.forEach((b, index) => {
        if (bearingExpired(bearingClockOf(b), site.ttlSec, nowMs)) return;
        const id = `bearing:${site.siteEntityId}:${b.trackId ?? index}`;
        if (wanted.has(id)) return; // a repeated track id: the first wins
        wanted.add(id);
        this.build(id, site, b, nowMs);
      });
    }
    for (const [id, { entity }] of this.drawn) {
      if (!wanted.has(id)) {
        this.viewer.entities.remove(entity);
        this.drawn.delete(id);
      }
    }
    this.viewer.scene.requestRender();
  }

  destroy(): void {
    if (!this.viewer.isDestroyed()) {
      for (const { entity } of this.drawn.values()) this.viewer.entities.remove(entity);
    }
    this.drawn.clear();
  }

  /** Where a line with no origin altitude starts: the ground the globe reports there, else sea level. */
  private groundAt(lat: number, lon: number): number {
    const C = this.Cesium;
    const height = C.Cartographic ? this.viewer.scene.globe?.getHeight?.(C.Cartographic.fromDegrees(lon, lat)) : undefined;
    return typeof height === 'number' && Number.isFinite(height) ? height : 0;
  }

  private build(id: string, site: BearingSite, b: SiteBearing, nowMs: number): void {
    const { lat, lon } = site.origin;
    const alt = site.origin.alt ?? this.groundAt(lat, lon);
    const alpha = bearingAlpha(bearingClockOf(b), site.ttlSec, nowMs);
    const key = JSON.stringify([lat, lon, alt, site.color, b, alpha]);
    const existing = this.drawn.get(id);
    if (existing?.key === key) return;
    if (existing) this.viewer.entities.remove(existing.entity);

    const C = this.Cesium;
    const end = bearingEnd({ lat, lon, alt }, b);
    const color = C.Color.fromCssColorString(site.color).withAlpha(alpha);
    const entity = this.viewer.entities.add({
      id,
      position: C.Cartesian3.fromDegrees(end.lon, end.lat, end.alt),
      polyline: {
        positions: [C.Cartesian3.fromDegrees(lon, lat, alt), C.Cartesian3.fromDegrees(end.lon, end.lat, end.alt)],
        width: BEARING_LINE_CONFIG.width,
        material: new C.PolylineDashMaterialProperty({ color, dashLength: BEARING_LINE_CONFIG.dashLength }),
        // Straight, as the line of sight is: the default geodesic arc follows
        // the ellipsoid and sags (about 18 m at 30 km) below it.
        arcType: C.ArcType?.NONE,
      },
      label: {
        text: bearingLabel(b),
        font: BEARING_LINE_CONFIG.labelFont,
        fillColor: color,
        outlineColor: C.Color.BLACK?.withAlpha?.(alpha) ?? C.Color.BLACK,
        outlineWidth: 2,
        style: C.LabelStyle.FILL_AND_OUTLINE,
        horizontalOrigin: C.HorizontalOrigin.LEFT,
        verticalOrigin: C.VerticalOrigin.BOTTOM,
        pixelOffset: new C.Cartesian2(6, -4),
      },
    });
    entity._sentinelBearing = site.siteEntityId;
    this.drawn.set(id, { entity, key });
  }
}
