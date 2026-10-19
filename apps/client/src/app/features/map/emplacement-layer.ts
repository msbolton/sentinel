import { LngLat } from '../../shared/models/geofence.model';
import { Emplacement, EmplacementDraft } from '../../shared/models/emplacement.model';
import { EntityType } from '../../shared/models/entity.model';
import { ENTITY_TYPE_PIN_COLORS, LABEL_SCALE_BY_DISTANCE, LABEL_TRANSLUCENCY_BY_DISTANCE } from './cesium-config';
import { coverageRings, hasCoverage } from './emplacement-geometry';

/** The layer key emplacements obey: the built-in emplacements feed's row in the layers pill. */
export const EMPLACEMENTS_LAYER = 'emplacements';
/** The id the form's unsaved draft is drawn under; it is never an emplacement id. */
export const EMPLACEMENT_DRAFT_ID = '__emplacement_draft__';

/** Where a mobile emplacement's SENSOR entity is now, if it is on the stream. */
export type LivePosition = (emplacementId: string) => { lon: number; lat: number; heading: number | null } | null;

type Placeable = Pick<Emplacement, 'id' | 'mobility' | 'position' | 'boresightDeg' | 'azimuthReference'>;
type Drawable = Placeable & Pick<Emplacement, 'name' | 'coverage'>;

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
 * A mobile emplacement's entities follow its live fix through callbacks.
 * Every entity carries `_sentinelEmplacement` so a click can select it.
 */
export class EmplacementLayer {
  private readonly drawn = new Map<string, { entities: any[]; key: string }>();
  private live: LivePosition = () => null;

  constructor(private readonly viewer: any, private readonly Cesium: any) {}

  sync(list: Emplacement[], selectedId: string | null, draft: EmplacementDraft | null, showCoverage: boolean, live: LivePosition): void {
    this.live = live;
    const items = new Map<string, { e: Drawable; selected: boolean }>();
    for (const e of list) {
      if (e.active || e.id === selectedId) items.set(e.id, { e, selected: e.id === selectedId });
    }
    if (draft) {
      const id = draft.id ?? EMPLACEMENT_DRAFT_ID;
      items.set(id, { e: { ...draft, id }, selected: true });
    }
    for (const [id, { e, selected }] of items) this.build(id, e, selected, showCoverage);
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

  private build(id: string, e: Drawable, selected: boolean, showCoverage: boolean): void {
    const key = JSON.stringify([e.name, e.mobility, e.position, e.boresightDeg, e.azimuthReference, e.coverage, selected, showCoverage]);
    const existing = this.drawn.get(id);
    if (existing?.key === key) return;
    if (existing) for (const x of existing.entities) this.viewer.entities.remove(x);

    const C = this.Cesium;
    const color = C.Color.fromCssColorString(ENTITY_TYPE_PIN_COLORS[EntityType.SENSOR] ?? '#8b5cf6');
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
      point: {
        pixelSize: selected ? 14 : 11,
        color: mobile ? color.withAlpha(0.25) : color,
        outlineColor: mobile ? color : C.Color.WHITE,
        outlineWidth: mobile ? 3 : 2,
        heightReference: C.HeightReference.CLAMP_TO_GROUND,
        disableDepthTestDistance: Number.POSITIVE_INFINITY,
      },
      label: {
        text: e.name || 'New emplacement',
        font: '11px JetBrains Mono, monospace',
        fillColor: color,
        outlineColor: C.Color.BLACK,
        outlineWidth: 2,
        style: C.LabelStyle.FILL_AND_OUTLINE,
        pixelOffset: new C.Cartesian2(10, -6),
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
      const footprint = this.viewer.entities.add({
        polygon: { hierarchy: dynamic(hierarchy), material: color.withAlpha(selected ? 0.18 : 0.08), heightReference: C.HeightReference.CLAMP_TO_GROUND },
        polyline: { positions: dynamic(outline), width: selected ? 2.5 : 1.5, material: color.withAlpha(0.8), clampToGround: true },
      });
      footprint._sentinelEmplacement = id;
      entities.push(footprint);
    }
    this.drawn.set(id, { entities, key });
  }
}
