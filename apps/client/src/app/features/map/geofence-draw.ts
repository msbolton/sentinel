import { LngLat } from '../../shared/models/geofence.model';
import { DrawSession, liveMeasure, previewRing } from './geofence-draw-state';

/**
 * Draws a geofence session on the globe while it is being drawn: the shape
 * so far (fill and outline in the session colour), its vertices, and the
 * live measurement beside the cursor. One set of entities, fed by
 * CallbackProperties, so a mouse move costs no entity churn.
 */
export class GeofenceDrawTool {
  private ring: any[] = [];
  private vertexEntities: any[] = [];
  private cursor: any = null;
  private measure = '';
  private color: any = null;
  private colorHex = '';
  private shapeEntity: any = null;
  private labelEntity: any = null;

  constructor(private readonly viewer: any, private readonly Cesium: any) {}

  render(session: DrawSession): void {
    const Cesium = this.Cesium;
    if (session.color !== this.colorHex) {
      this.colorHex = session.color;
      this.color = Cesium.Color.fromCssColorString(session.color);
      this.clear(false);
    }
    const toCartesian = ([lng, lat]: LngLat) => Cesium.Cartesian3.fromDegrees(lng, lat, 0);
    this.ring = previewRing(session).map(toCartesian);
    this.cursor = session.cursor ? toCartesian(session.cursor) : null;
    this.measure = liveMeasure(session);

    if (!this.shapeEntity) {
      this.shapeEntity = this.viewer.entities.add({
        polygon: {
          hierarchy: new Cesium.CallbackProperty(() => new Cesium.PolygonHierarchy(this.ring.length >= 3 ? this.ring : []), false),
          material: this.color.withAlpha(0.14),
          heightReference: Cesium.HeightReference.CLAMP_TO_GROUND,
        },
        polyline: {
          positions: new Cesium.CallbackProperty(() => (this.ring.length >= 2 ? [...this.ring, this.ring[0]] : this.ring), false),
          width: 2,
          material: this.color,
          clampToGround: true,
        },
      });
      this.shapeEntity._sentinelDrawing = true;
      this.labelEntity = this.viewer.entities.add({
        position: new Cesium.CallbackProperty(() => this.cursor ?? this.ring[this.ring.length - 1], false),
        show: new Cesium.CallbackProperty(() => this.measure !== '' && (this.cursor !== null || this.ring.length > 0), false),
        label: {
          text: new Cesium.CallbackProperty(() => this.measure, false),
          font: '11px JetBrains Mono, monospace',
          fillColor: this.color,
          outlineColor: Cesium.Color.BLACK,
          outlineWidth: 2,
          style: Cesium.LabelStyle.FILL_AND_OUTLINE,
          showBackground: true,
          backgroundColor: Cesium.Color.fromCssColorString('#0a0e17').withAlpha(0.85),
          backgroundPadding: new Cesium.Cartesian2(6, 3),
          pixelOffset: new Cesium.Cartesian2(14, -14),
          horizontalOrigin: Cesium.HorizontalOrigin.LEFT,
          disableDepthTestDistance: Number.POSITIVE_INFINITY,
        },
      });
      this.labelEntity._sentinelDrawing = true;
    }

    // Vertices: the placed points (plus the anchor for a ring).
    const vertices: LngLat[] = session.shape === 'ring' ? (session.anchor ? [session.anchor.position] : []) : session.points;
    while (this.vertexEntities.length > vertices.length) this.viewer.entities.remove(this.vertexEntities.pop());
    vertices.forEach((v, i) => {
      const position = toCartesian(v);
      if (this.vertexEntities[i]) {
        this.vertexEntities[i].position = position;
      } else {
        const e = this.viewer.entities.add({
          position,
          point: {
            pixelSize: 8,
            color: Cesium.Color.WHITE,
            outlineColor: this.color,
            outlineWidth: 3,
            heightReference: Cesium.HeightReference.CLAMP_TO_GROUND,
            disableDepthTestDistance: Number.POSITIVE_INFINITY,
          },
        });
        e._sentinelDrawing = true;
        this.vertexEntities.push(e);
      }
    });
    this.viewer.scene.requestRender();
  }

  /** Removes everything drawn; `reset` also forgets the colour so the next render rebuilds. */
  clear(reset = true): void {
    if (!this.viewer.isDestroyed()) {
      if (this.shapeEntity) this.viewer.entities.remove(this.shapeEntity);
      if (this.labelEntity) this.viewer.entities.remove(this.labelEntity);
      for (const v of this.vertexEntities) this.viewer.entities.remove(v);
      this.viewer.scene.requestRender();
    }
    this.shapeEntity = null;
    this.labelEntity = null;
    this.vertexEntities = [];
    this.ring = [];
    this.cursor = null;
    if (reset) this.colorHex = '';
  }
}
