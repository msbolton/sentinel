import { Injectable } from '@angular/core';
import { Entity } from '../../shared/models/entity.model';
import { PresenceEntry } from '../../core/services/websocket.service';
import {
  BILLBOARD_SCALE_BY_DISTANCE,
  LABEL_SCALE_BY_DISTANCE,
  LABEL_TRANSLUCENCY_BY_DISTANCE,
  svgToDataUrl,
} from './cesium-config';

/** SVG for the federation ring overlay — a simple colored circle outline. */
function ringBillboardSvg(color: string): string {
  return `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 64 64" width="64" height="64">
    <circle cx="32" cy="32" r="28" fill="none" stroke="${color}" stroke-width="3" opacity="0.8"/>
  </svg>`;
}

/** SVG for presence marker dot. */
function presenceDotSvg(color: string): string {
  return `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" width="24" height="24">
    <circle cx="12" cy="12" r="8" fill="${color}" stroke="white" stroke-width="2" opacity="0.9"/>
  </svg>`;
}

interface FederationRingEntry {
  billboard: any; // Cesium Billboard
  entityId: string;
}

interface PresenceMarkerEntry {
  billboard: any;
  label: any;
  userId: string;
}

@Injectable()
export class FederationOverlayService {
  private Cesium: any = null;
  private viewer: any = null;
  private ringBillboardCollection: any = null;
  private presenceBillboardCollection: any = null;
  private presenceLabelCollection: any = null;

  private federationRings = new Map<string, FederationRingEntry>();
  private presenceMarkers = new Map<string, PresenceMarkerEntry>();
  private ringImageCache = new Map<string, string>();
  private presenceDotCache = new Map<string, string>();

  /**
   * Initialize with Cesium module and viewer instance.
   * Called once from MapComponent after Cesium is loaded.
   */
  init(Cesium: any, viewer: any): void {
    this.Cesium = Cesium;
    this.viewer = viewer;

    this.ringBillboardCollection = viewer.scene.primitives.add(
      new Cesium.BillboardCollection({ scene: viewer.scene }),
    );
    this.presenceBillboardCollection = viewer.scene.primitives.add(
      new Cesium.BillboardCollection({ scene: viewer.scene }),
    );
    this.presenceLabelCollection = viewer.scene.primitives.add(
      new Cesium.LabelCollection({ scene: viewer.scene }),
    );
  }

  /** Returns true if the entity is from a federated peer. */
  isFederatedEntity(entity: Entity): boolean {
    return !!entity.sourceInstanceId;
  }

  /**
   * Formats label text with source badge for federated entities.
   * Local entities get plain name.
   */
  formatFederatedLabel(name: string, sourceInstanceName?: string): string {
    if (!sourceInstanceName) return name;
    return `${name} [${sourceInstanceName}]`;
  }

  /**
   * Adds or updates a colored ring around a federated entity billboard.
   * The ring is rendered as a separate billboard slightly larger than the entity icon.
   */
  addOrUpdateRing(entityId: string, position: any, color: string): void {
    if (!this.Cesium || !this.ringBillboardCollection) return;

    const existing = this.federationRings.get(entityId);

    if (existing) {
      existing.billboard.position = position;
      return;
    }

    // Get or create ring image for this color
    let ringImage = this.ringImageCache.get(color);
    if (!ringImage) {
      ringImage = svgToDataUrl(ringBillboardSvg(color));
      this.ringImageCache.set(color, ringImage);
    }

    const billboard = this.ringBillboardCollection.add({
      position,
      image: ringImage,
      scale: 1.2,
      id: `fed-ring-${entityId}`,
      scaleByDistance: new this.Cesium.NearFarScalar(...BILLBOARD_SCALE_BY_DISTANCE),
    });

    this.federationRings.set(entityId, { billboard, entityId });
  }

  /** Remove a federation ring when the entity is removed. */
  removeRing(entityId: string): void {
    const entry = this.federationRings.get(entityId);
    if (entry && this.ringBillboardCollection) {
      this.ringBillboardCollection.remove(entry.billboard);
      this.federationRings.delete(entityId);
    }
  }

  /**
   * Updates presence markers on the map.
   * Called periodically with the current set of remote user positions.
   */
  updatePresenceMarkers(entries: PresenceEntry[]): void {
    if (!this.Cesium || !this.viewer) return;

    const Cesium = this.Cesium;
    const activeIds = new Set(entries.map(e => e.userId));

    // Remove stale markers
    for (const [userId, marker] of this.presenceMarkers) {
      if (!activeIds.has(userId)) {
        this.presenceBillboardCollection.remove(marker.billboard);
        this.presenceLabelCollection.remove(marker.label);
        this.presenceMarkers.delete(userId);
      }
    }

    // Add or update markers
    for (const entry of entries) {
      const position = Cesium.Cartesian3.fromDegrees(
        entry.cameraCenter.lon,
        entry.cameraCenter.lat,
        0,
      );

      const existing = this.presenceMarkers.get(entry.userId);
      if (existing) {
        existing.billboard.position = position;
        existing.label.position = position;
        continue;
      }

      // Get or create presence dot for this color
      let dotImage = this.presenceDotCache.get(entry.color);
      if (!dotImage) {
        dotImage = svgToDataUrl(presenceDotSvg(entry.color));
        this.presenceDotCache.set(entry.color, dotImage);
      }

      const billboard = this.presenceBillboardCollection.add({
        position,
        image: dotImage,
        scale: 1.0,
        id: `presence-${entry.userId}`,
        scaleByDistance: new Cesium.NearFarScalar(...BILLBOARD_SCALE_BY_DISTANCE),
      });

      const label = this.presenceLabelCollection.add({
        position,
        text: `${entry.displayName} (${entry.instanceName})`,
        font: '11px sans-serif',
        fillColor: Cesium.Color.WHITE,
        outlineColor: Cesium.Color.BLACK,
        outlineWidth: 2,
        style: Cesium.LabelStyle.FILL_AND_OUTLINE,
        pixelOffset: new Cesium.Cartesian2(0, -18),
        scaleByDistance: new Cesium.NearFarScalar(...LABEL_SCALE_BY_DISTANCE),
        translucencyByDistance: new Cesium.NearFarScalar(...LABEL_TRANSLUCENCY_BY_DISTANCE),
      });

      this.presenceMarkers.set(entry.userId, { billboard, label, userId: entry.userId });
    }
  }

  /** Parse hex color to RGB components. */
  hexToRgb(hex: string): { r: number; g: number; b: number } | null {
    const result = /^#?([a-f\d]{2})([a-f\d]{2})([a-f\d]{2})$/i.exec(hex);
    if (!result) return null;
    return {
      r: parseInt(result[1], 16),
      g: parseInt(result[2], 16),
      b: parseInt(result[3], 16),
    };
  }

  /** Clean up all primitives. */
  destroy(): void {
    if (this.viewer && !this.viewer.isDestroyed()) {
      if (this.ringBillboardCollection) {
        this.viewer.scene.primitives.remove(this.ringBillboardCollection);
      }
      if (this.presenceBillboardCollection) {
        this.viewer.scene.primitives.remove(this.presenceBillboardCollection);
      }
      if (this.presenceLabelCollection) {
        this.viewer.scene.primitives.remove(this.presenceLabelCollection);
      }
    }
    this.federationRings.clear();
    this.presenceMarkers.clear();
  }
}
