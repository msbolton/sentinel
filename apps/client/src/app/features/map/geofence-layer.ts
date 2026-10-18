import { GeofenceConfig, GeofenceRule, LngLat } from '../../shared/models/geofence.model';
import { LABEL_SCALE_BY_DISTANCE, LABEL_TRANSLUCENCY_BY_DISTANCE } from './cesium-config';
import { circlePolygon, formatDistance, geodesicDistanceM } from './geofence-geometry';

/** Where a ring's anchor track is drawn right now, or null when it is not on the map. */
export type AnchorPosition = (entityId: string) => LngLat | null;

const FILL_ALPHA = 0.14;
const OUTLINE_WIDTH = 2;
const SELECTED_OUTLINE_WIDTH = 3;

/**
 * The Cesium entity description for one zone: a filled polygon with a
 * separate outline polyline (polygon outlines are one pixel wide on most
 * GPUs) and a name chip at the first vertex. A ring is an ellipse that
 * follows its anchor and hides while the anchor is missing. Every entity
 * carries `_sentinelGeofence` so the pick handler can select the zone.
 */
export function zoneEntityOptions(zone: GeofenceRule, selected: boolean, Cesium: any, anchorPosition: AnchorPosition): Record<string, unknown> {
  const color = Cesium.Color.fromCssColorString(zone.config.color);
  const label = (text: string) => ({
    text,
    font: '11px JetBrains Mono, monospace',
    fillColor: color,
    outlineColor: Cesium.Color.BLACK,
    outlineWidth: 2,
    style: Cesium.LabelStyle.FILL_AND_OUTLINE,
    showBackground: true,
    backgroundColor: Cesium.Color.fromCssColorString('#0a0e17').withAlpha(0.75),
    backgroundPadding: new Cesium.Cartesian2(6, 3),
    pixelOffset: new Cesium.Cartesian2(8, -6),
    horizontalOrigin: Cesium.HorizontalOrigin.LEFT,
    verticalOrigin: Cesium.VerticalOrigin.BOTTOM,
    scaleByDistance: new Cesium.NearFarScalar(...LABEL_SCALE_BY_DISTANCE),
    translucencyByDistance: new Cesium.NearFarScalar(...LABEL_TRANSLUCENCY_BY_DISTANCE),
    disableDepthTestDistance: Number.POSITIVE_INFINITY,
    heightReference: Cesium.HeightReference.CLAMP_TO_GROUND,
  });

  if (zone.config.shape === 'ring' && zone.config.ring) {
    const ring = zone.config.ring;
    const radius = ring.radiusM;
    const position = new Cesium.CallbackProperty(() => {
      const p = anchorPosition(ring.entityId);
      return p ? Cesium.Cartesian3.fromDegrees(p[0], p[1], 0) : undefined;
    }, false);
    return {
      _sentinelGeofence: zone.id,
      position,
      show: new Cesium.CallbackProperty(() => anchorPosition(ring.entityId) !== null, false),
      ellipse: {
        semiMajorAxis: radius,
        semiMinorAxis: radius,
        material: color.withAlpha(FILL_ALPHA),
        outline: true,
        outlineColor: color,
        outlineWidth: selected ? SELECTED_OUTLINE_WIDTH : OUTLINE_WIDTH,
        heightReference: Cesium.HeightReference.CLAMP_TO_GROUND,
      },
      label: label(`${zone.name} · ${formatDistance(radius)}`),
    };
  }

  const ring = zone.config.polygon ?? [];
  const positions = ring.map(([lng, lat]) => Cesium.Cartesian3.fromDegrees(lng, lat, 0));
  const closed = positions.length > 0 ? [...positions, positions[0]] : positions;
  return {
    _sentinelGeofence: zone.id,
    position: positions[0],
    polygon: {
      hierarchy: positions,
      material: color.withAlpha(FILL_ALPHA),
      heightReference: Cesium.HeightReference.CLAMP_TO_GROUND,
    },
    polyline: {
      positions: closed,
      width: selected ? SELECTED_OUTLINE_WIDTH : OUTLINE_WIDTH,
      material: color,
      clampToGround: true,
    },
    label: label(zone.name),
  };
}

/** The grab points of a zone: every vertex, or a circle's centre (0) and east edge (1), or a ring's east edge (1). */
export function handlePositions(config: GeofenceConfig, anchor: LngLat | null): { index: number; position: LngLat }[] {
  if (config.shape === 'circle' && config.circle) {
    const { center, radiusM } = config.circle;
    return [
      { index: 0, position: center },
      { index: 1, position: circlePolygon(center, radiusM, 4)[1] },
    ];
  }
  if (config.shape === 'ring' && config.ring) {
    return anchor ? [{ index: 1, position: circlePolygon(anchor, config.ring.radiusM, 4)[1] }] : [];
  }
  return (config.polygon ?? []).map((position, index) => ({ index, position }));
}

/**
 * The config after handle `index` is dragged to `to`: a vertex moves; a
 * circle's centre (0) carries the circle, its edge (1) sets the radius; a
 * ring's edge (1) sets its radius from `anchor`.
 */
export function movedConfig(config: GeofenceConfig, index: number, to: LngLat, anchor?: LngLat | null): GeofenceConfig {
  if (config.shape === 'circle' && config.circle) {
    const center = index === 0 ? to : config.circle.center;
    const radiusM = index === 0 ? config.circle.radiusM : Math.max(1, geodesicDistanceM(config.circle.center, to));
    return { ...config, circle: { center, radiusM }, polygon: circlePolygon(center, radiusM) };
  }
  if (config.shape === 'ring' && config.ring) {
    if (!anchor) return config;
    return { ...config, ring: { ...config.ring, radiusM: Math.max(1, geodesicDistanceM(anchor, to)) } };
  }
  const polygon = (config.polygon ?? []).map((p, i) => (i === index ? to : p));
  return { ...config, polygon };
}

/**
 * Keeps the viewer's entities in step with the zones: one entity per zone,
 * rebuilt when the zone changes or its selection flips.
 */
export class GeofenceLayer {
  private readonly entities = new Map<string, { entity: any; key: string }>();
  private handles: any[] = [];
  private zones: GeofenceRule[] = [];
  private selectedId: string | null = null;
  private anchorPosition: AnchorPosition = () => null;
  /** The config of the selected zone while one of its handles is being dragged. */
  private dragConfig: GeofenceConfig | null = null;

  constructor(private readonly viewer: any, private readonly Cesium: any) {}

  sync(zones: GeofenceRule[], selectedId: string | null, anchorPosition: AnchorPosition): void {
    this.zones = zones;
    this.selectedId = selectedId;
    this.anchorPosition = anchorPosition;
    this.dragConfig = null;
    const seen = new Set<string>();
    for (const zone of zones) {
      seen.add(zone.id);
      this.buildZone(zone, zone.id === selectedId);
    }
    for (const [id, { entity }] of this.entities) {
      if (!seen.has(id)) {
        this.viewer.entities.remove(entity);
        this.entities.delete(id);
      }
    }
    this.rebuildHandles();
    this.viewer.scene.requestRender();
  }

  /** The handle under a pick result, if it is one of ours. */
  handleAt(picked: any): { zoneId: string; index: number } | null {
    const h = picked?.id?._sentinelGeofenceHandle;
    return h ? { zoneId: h.zoneId, index: h.index } : null;
  }

  /** Drags a handle to `to`, redrawing the zone live; returns the config to save. */
  moveHandle(zoneId: string, index: number, to: LngLat): GeofenceConfig | null {
    const zone = this.zones.find((z) => z.id === zoneId);
    if (!zone) return null;
    const base = this.dragConfig ?? zone.config;
    const anchor = base.ring ? this.anchorPosition(base.ring.entityId) : null;
    this.dragConfig = movedConfig(base, index, to, anchor);
    this.buildZone({ ...zone, config: this.dragConfig }, true);
    this.rebuildHandles();
    this.viewer.scene.requestRender();
    return this.dragConfig;
  }

  destroy(): void {
    if (!this.viewer.isDestroyed()) {
      for (const { entity } of this.entities.values()) this.viewer.entities.remove(entity);
      for (const h of this.handles) this.viewer.entities.remove(h);
    }
    this.entities.clear();
    this.handles = [];
  }

  private buildZone(zone: GeofenceRule, selected: boolean): void {
    const key = `${zone.updatedAt}|${selected}|${zone.enabled}|${JSON.stringify(zone.config)}|${zone.name}`;
    const existing = this.entities.get(zone.id);
    if (existing?.key === key) return;
    if (existing) this.viewer.entities.remove(existing.entity);
    const options = zoneEntityOptions(zone, selected, this.Cesium, this.anchorPosition);
    const { _sentinelGeofence, ...entityOptions } = options as { _sentinelGeofence: string } & Record<string, unknown>;
    const entity = this.viewer.entities.add(entityOptions);
    entity._sentinelGeofence = _sentinelGeofence;
    if (!zone.enabled && entity.polygon) entity.polygon.material = this.Cesium.Color.fromCssColorString(zone.config.color).withAlpha(FILL_ALPHA / 2);
    this.entities.set(zone.id, { entity, key });
  }

  /** Grab points for the selected zone only. */
  private rebuildHandles(): void {
    for (const h of this.handles) this.viewer.entities.remove(h);
    this.handles = [];
    const zone = this.zones.find((z) => z.id === this.selectedId);
    if (!zone) return;
    const config = this.dragConfig ?? zone.config;
    const anchor = config.ring ? this.anchorPosition(config.ring.entityId) : null;
    const color = this.Cesium.Color.fromCssColorString(config.color);
    for (const { index, position } of handlePositions(config, anchor)) {
      const h = this.viewer.entities.add({
        position: this.Cesium.Cartesian3.fromDegrees(position[0], position[1], 0),
        point: {
          pixelSize: 10,
          color: this.Cesium.Color.WHITE,
          outlineColor: color,
          outlineWidth: 3,
          heightReference: this.Cesium.HeightReference.CLAMP_TO_GROUND,
          disableDepthTestDistance: Number.POSITIVE_INFINITY,
        },
      });
      h._sentinelGeofenceHandle = { zoneId: zone.id, index };
      this.handles.push(h);
    }
  }
}
