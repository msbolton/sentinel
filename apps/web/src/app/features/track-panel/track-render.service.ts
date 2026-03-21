import { Injectable } from '@angular/core';
import { TrackPoint } from '../../core/services/track-api.service';

@Injectable()
export class TrackRenderService {
  private viewer: any = null;
  private Cesium: any = null;
  private trackEntities: any[] = [];
  private replayMarker: any = null;
  private currentReplayIndex = 0;

  init(viewer: any, Cesium: any): void {
    this.viewer = viewer;
    this.Cesium = Cesium;
  }

  drawStaticTrack(points: TrackPoint[]): void {
    if (!this.viewer || points.length < 2) return;

    const positions = this.pointsToCartesians(points);

    const polyline = this.viewer.entities.add({
      polyline: {
        positions,
        width: 2,
        material: new this.Cesium.PolylineGlowMaterialProperty({
          glowPower: 0.2,
          color: this.Cesium.Color.CYAN.withAlpha(0.8),
        }),
        clampToGround: false,
      },
    });
    this.trackEntities.push(polyline);

    // Start marker
    const startMarker = this.viewer.entities.add({
      position: this.Cesium.Cartesian3.fromDegrees(
        points[0].longitude, points[0].latitude, points[0].altitude ?? 0,
      ),
      point: { pixelSize: 8, color: this.Cesium.Color.YELLOW },
    });
    this.trackEntities.push(startMarker);

    // End marker
    const endPoint = points[points.length - 1];
    const endMarker = this.viewer.entities.add({
      position: this.Cesium.Cartesian3.fromDegrees(
        endPoint.longitude, endPoint.latitude, endPoint.altitude ?? 0,
      ),
      point: { pixelSize: 8, color: this.Cesium.Color.RED },
    });
    this.trackEntities.push(endMarker);
  }

  setupReplayPolylines(points: TrackPoint[]): void {
    if (!this.viewer || points.length < 2) return;

    this.clearAll();
    this.currentReplayIndex = 0;

    // Played polyline (highlighted)
    const playedPolyline = this.viewer.entities.add({
      polyline: {
        positions: new this.Cesium.CallbackProperty(() => {
          return this.pointsToCartesians(points.slice(0, this.currentReplayIndex + 1));
        }, false),
        width: 3,
        material: new this.Cesium.PolylineGlowMaterialProperty({
          glowPower: 0.3,
          color: this.Cesium.Color.CYAN.withAlpha(0.9),
        }),
        clampToGround: false,
      },
    });
    this.trackEntities.push(playedPolyline);

    // Future polyline (dimmed)
    const futurePolyline = this.viewer.entities.add({
      polyline: {
        positions: new this.Cesium.CallbackProperty(() => {
          return this.pointsToCartesians(points.slice(this.currentReplayIndex));
        }, false),
        width: 1,
        material: this.Cesium.Color.GRAY.withAlpha(0.3),
        clampToGround: false,
      },
    });
    this.trackEntities.push(futurePolyline);

    // Replay marker
    this.replayMarker = this.viewer.entities.add({
      position: new this.Cesium.CallbackProperty(() => {
        const idx = this.currentReplayIndex;
        if (idx < 0 || idx >= points.length) return null;
        const p = points[idx];
        return this.Cesium.Cartesian3.fromDegrees(p.longitude, p.latitude, p.altitude ?? 0);
      }, false),
      point: { pixelSize: 12, color: this.Cesium.Color.CYAN },
    });
    this.trackEntities.push(this.replayMarker);
  }

  updateReplayIndex(index: number): void {
    this.currentReplayIndex = index;
  }

  clearAll(): void {
    if (!this.viewer) return;
    for (const entity of this.trackEntities) {
      this.viewer.entities.remove(entity);
    }
    this.trackEntities = [];
    this.replayMarker = null;
    this.currentReplayIndex = 0;
  }

  private pointsToCartesians(points: TrackPoint[]): any[] {
    return points.map((p) =>
      this.Cesium.Cartesian3.fromDegrees(p.longitude, p.latitude, p.altitude ?? 0),
    );
  }
}
