import { LngLat } from '../../shared/models/geofence.model';
import { Emplacement, EmplacementDraft, SiteHeartbeat, emplacementIdOf, formatZulu } from '../../shared/models/emplacement.model';
import { EntityType } from '../../shared/models/entity.model';
import { ENTITY_TYPE_PIN_COLORS, LABEL_SCALE_BY_DISTANCE, LABEL_TRANSLUCENCY_BY_DISTANCE } from './cesium-config';
import { coverageRings, hasCoverage } from './emplacement-geometry';
import { locationDotDataUrl } from './location-dot';

/** The id the form's unsaved draft is drawn under; it is never an emplacement id. */
export const EMPLACEMENT_DRAFT_ID = '__emplacement_draft__';
/** The grey of a silent site. */
export const SILENT_SITE_COLOR = '#6b7280';

/** Where a mobile emplacement's SENSOR entity is now, if it is on the stream. */
export type LivePosition = (emplacementId: string) => { lon: number; lat: number; heading: number | null } | null;
/** A site's heartbeat, judged now; null for a site whose connection does not heartbeat it. */
export type HeartbeatOf = (emplacementId: string) => SiteHeartbeat | null;

type Placeable = Pick<Emplacement, 'id' | 'mobility' | 'position' | 'boresightDeg' | 'azimuthReference'>;
type Drawable = Placeable & Pick<Emplacement, 'name' | 'coverage'>;

/**
 * The emplacement whose layer draws this entity, or null to draw it as an
 * ordinary entity. Only an emplacement the client has loaded counts, so an
 * entity claiming any other EMPLACEMENT identity is never hidden.
 */
export function emplacementDrawingOf(
  entity: { identities?: { type: string; value: string }[] | null },
  loaded: ReadonlySet<string>,
): string | null {
  const id = emplacementIdOf(entity);
  return id != null && loaded.has(id) ? id : null;
}

/** The SENSOR entity that stands for an emplacement, among those on the stream. */
export function siteEntityOf<T extends { identities?: { type: string; value: string }[] | null }>(entities: Iterable<T>, emplacementId: string): T | null {
  for (const e of entities) {
    if (emplacementIdOf(e) === emplacementId) return e;
  }
  return null;
}

/**
 * The sites drawn on the map: those whose connection's layer is shown. One
 * with no connection has no layer; it is drawn only while selected or edited.
 */
export function sitesShown(list: Emplacement[], hidden: ReadonlySet<string>): Emplacement[] {
  return list.filter((e) => !e.feedId || !hidden.has(e.feedId));
}

/** Whether a site is drawn as silent: heard from and lapsed, or never heard from. */
export function isSilent(hb: SiteHeartbeat | null): boolean {
  return hb !== null && (hb.state === 'offline' || hb.state === 'never');
}

/** A site's label: its name, and, while it is silent, since when. */
export function siteLabel(name: string, hb: SiteHeartbeat | null, nowMs: number = Date.now()): string {
  if (!hb || !isSilent(hb)) return name;
  if (hb.state === 'never') return `${name} · no heartbeat yet`;
  return hb.silentSince ? `${name} · silent since ${formatZulu(hb.silentSince, nowMs)}` : `${name} · silent`;
}

/**
 * Where an emplacement is drawn and which way it points. A fixed one is at
 * its surveyed position. A mobile one is at its live fix, else its stored
 * position; a platform-relative boresight turns with the live heading.
 */
export function placementOf(e: Placeable, live: LivePosition): { lon: number; lat: number; boresightDeg: number } | null {
  const now = e.mobility === 'MOBILE' ? live(e.id) : null;
  const lon = now?.lon ?? e.position?.lon;
  const lat = now?.lat ?? e.position?.lat;
  if (lon == null || lat == null) return null;
  const boresightDeg =
    e.azimuthReference === 'PLATFORM' && now?.heading != null ? (((now.heading + e.boresightDeg) % 360) + 360) % 360 : e.boresightDeg;
  return { lon, lat, boresightDeg };
}

/**
 * Keeps the viewer's entities in step with the emplacements: a marker and
 * label per emplacement, and its coverage footprint when coverage is shown.
 * A silent site is a grey hollow dot, labelled since when, with a dim
 * footprint. A mobile emplacement's entities follow its live fix through
 * callbacks. Every entity carries `_sentinelEmplacement` so a click can
 * select the site.
 */
export class EmplacementLayer {
  private readonly drawn = new Map<string, { entities: any[]; key: string }>();
  private live: LivePosition = () => null;

  constructor(private readonly viewer: any, private readonly Cesium: any) {}

  sync(
    list: Emplacement[],
    selectedId: string | null,
    draft: EmplacementDraft | null,
    showCoverage: boolean,
    live: LivePosition,
    heartbeat: HeartbeatOf = () => null,
  ): void {
    this.live = live;
    const items = new Map<string, { e: Drawable; selected: boolean; hb: SiteHeartbeat | null }>();
    for (const e of list) {
      if (e.active || e.id === selectedId) items.set(e.id, { e, selected: e.id === selectedId, hb: heartbeat(e.id) });
    }
    if (draft) {
      const id = draft.id ?? EMPLACEMENT_DRAFT_ID;
      items.set(id, { e: { ...draft, id }, selected: true, hb: null });
    }
    for (const [id, { e, selected, hb }] of items) this.build(id, e, selected, showCoverage, hb);
    for (const [id, { entities }] of this.drawn) {
      if (!items.has(id)) {
        for (const x of entities) this.viewer.entities.remove(x);
        this.drawn.delete(id);
      }
    }
    this.viewer.scene.requestRender();
  }

  destroy(): void {
    if (!this.viewer.isDestroyed()) {
      for (const { entities } of this.drawn.values()) for (const x of entities) this.viewer.entities.remove(x);
    }
    this.drawn.clear();
  }

  private build(id: string, e: Drawable, selected: boolean, showCoverage: boolean, hb: SiteHeartbeat | null): void {
    const silent = isSilent(hb);
    const text = siteLabel(e.name || 'New emplacement', hb);
    const key = JSON.stringify([e.name, e.mobility, e.position, e.boresightDeg, e.azimuthReference, e.coverage, selected, showCoverage, silent, text]);
    const existing = this.drawn.get(id);
    if (existing?.key === key) return;
    if (existing) for (const x of existing.entities) this.viewer.entities.remove(x);

    const C = this.Cesium;
    const cssColor = silent ? SILENT_SITE_COLOR : (ENTITY_TYPE_PIN_COLORS[EntityType.SENSOR] ?? '#8b5cf6');
    const color = C.Color.fromCssColorString(cssColor);
    const mobile = e.mobility === 'MOBILE';
    const place = () => placementOf(e, this.live);
    const cartesian = () => {
      const p = place();
      return p ? C.Cartesian3.fromDegrees(p.lon, p.lat, 0) : undefined;
    };
    const dynamic = <T>(fn: () => T) => (mobile ? new C.CallbackProperty(fn, false) : fn());
    if (!mobile && !place()) {
      this.drawn.set(id, { entities: [], key });
      return;
    }

    const entities: any[] = [];
    const marker = this.viewer.entities.add({
      position: dynamic(cartesian),
      show: mobile ? new C.CallbackProperty(() => place() !== null, false) : true,
      billboard: {
        image: locationDotDataUrl(cssColor, { dashed: mobile, hollow: silent }),
        scale: selected ? 0.9 : 0.7,
        heightReference: C.HeightReference.CLAMP_TO_GROUND,
        disableDepthTestDistance: Number.POSITIVE_INFINITY,
        verticalOrigin: C.VerticalOrigin.CENTER,
      },
      label: {
        text,
        font: '11px JetBrains Mono, monospace',
        fillColor: color,
        outlineColor: C.Color.BLACK,
        outlineWidth: 2,
        style: C.LabelStyle.FILL_AND_OUTLINE,
        pixelOffset: new C.Cartesian2(14, -6),
        horizontalOrigin: C.HorizontalOrigin.LEFT,
        verticalOrigin: C.VerticalOrigin.BOTTOM,
        scaleByDistance: new C.NearFarScalar(...LABEL_SCALE_BY_DISTANCE),
        translucencyByDistance: new C.NearFarScalar(...LABEL_TRANSLUCENCY_BY_DISTANCE),
        disableDepthTestDistance: Number.POSITIVE_INFINITY,
        heightReference: C.HeightReference.CLAMP_TO_GROUND,
      },
    });
    marker._sentinelEmplacement = id;
    entities.push(marker);

    if (showCoverage && hasCoverage(e.coverage)) {
      const toCartesian = (ring: LngLat[]) => ring.map(([lon, lat]) => C.Cartesian3.fromDegrees(lon, lat, 0));
      const rings = () => {
        const p = place();
        return p ? coverageRings([p.lon, p.lat], p.boresightDeg, e.coverage) : null;
      };
      const hierarchy = () => {
        const r = rings();
        if (!r) return undefined;
        const holes = r[1] ? [new C.PolygonHierarchy(toCartesian(r[1]))] : [];
        return new C.PolygonHierarchy(toCartesian(r[0]), holes);
      };
      const outline = () => {
        const r = rings();
        if (!r) return [];
        const ring = toCartesian(r[0]);
        return [...ring, ring[0]];
      };
      const fill = silent ? 0.04 : selected ? 0.18 : 0.08;
      const footprint = this.viewer.entities.add({
        polygon: { hierarchy: dynamic(hierarchy), material: color.withAlpha(fill), heightReference: C.HeightReference.CLAMP_TO_GROUND },
        polyline: { positions: dynamic(outline), width: selected ? 2.5 : 1.5, material: color.withAlpha(silent ? 0.35 : 0.8), clampToGround: true },
      });
      footprint._sentinelEmplacement = id;
      entities.push(footprint);
    }
    this.drawn.set(id, { entities, key });
  }
}
