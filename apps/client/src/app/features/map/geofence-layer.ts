import { GeofenceRule, LngLat } from '../../shared/models/geofence.model';
import { LABEL_SCALE_BY_DISTANCE, LABEL_TRANSLUCENCY_BY_DISTANCE } from './cesium-config';
import { formatDistance } from './geofence-geometry';

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

/**
 * Keeps the viewer's entities in step with the zones: one entity per zone,
 * rebuilt when the zone changes or its selection flips.
 */
export class GeofenceLayer {
  private readonly entities = new Map<string, { entity: any; key: string }>();

  constructor(private readonly viewer: any, private readonly Cesium: any) {}

  sync(zones: GeofenceRule[], selectedId: string | null, anchorPosition: AnchorPosition): void {
    const seen = new Set<string>();
    for (const zone of zones) {
      seen.add(zone.id);
      const key = `${zone.updatedAt}|${zone.id === selectedId}|${zone.enabled}|${JSON.stringify(zone.config)}|${zone.name}`;
      const existing = this.entities.get(zone.id);
      if (existing?.key === key) continue;
      if (existing) this.viewer.entities.remove(existing.entity);
      const options = zoneEntityOptions(zone, zone.id === selectedId, this.Cesium, anchorPosition);
      const { _sentinelGeofence, ...entityOptions } = options as { _sentinelGeofence: string } & Record<string, unknown>;
      const entity = this.viewer.entities.add(entityOptions);
      entity._sentinelGeofence = _sentinelGeofence;
      if (!zone.enabled && entity.polygon) entity.polygon.material = this.Cesium.Color.fromCssColorString(zone.config.color).withAlpha(FILL_ALPHA / 2);
      this.entities.set(zone.id, { entity, key });
    }
    for (const [id, { entity }] of this.entities) {
      if (!seen.has(id)) {
        this.viewer.entities.remove(entity);
        this.entities.delete(id);
      }
    }
    this.viewer.scene.requestRender();
  }

  destroy(): void {
    if (!this.viewer.isDestroyed()) {
      for (const { entity } of this.entities.values()) this.viewer.entities.remove(entity);
    }
    this.entities.clear();
  }
}
