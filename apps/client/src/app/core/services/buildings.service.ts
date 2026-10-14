import { Injectable, signal } from '@angular/core';
import { environment } from '../../../environments/environment';

@Injectable({ providedIn: 'root' })
export class BuildingsService {
  buildings3dEnabled = signal<boolean>(false);
  private tileset: any = null;
  private viewer: any = null;
  private Cesium: any = null;

  init(viewer: any, Cesium: any): void {
    this.viewer = viewer;
    this.Cesium = Cesium;

    // Configure Cesium Ion token if available
    if (environment.cesiumIonAccessToken) {
      Cesium.Ion.defaultAccessToken = environment.cesiumIonAccessToken;
    }
  }

  async enable(): Promise<void> {
    if (!this.viewer || !this.Cesium) return;

    if (this.tileset) {
      this.tileset.show = true;
      this.buildings3dEnabled.set(true);
      return;
    }

    try {
      // Try Cesium Ion OSM Buildings first (works in Vite dev server, no Draco needed)
      if (environment.cesiumIonAccessToken) {
        this.tileset = await this.Cesium.createOsmBuildingsAsync();
      } else if (environment.googleMapsApiKey) {
        // Google Photorealistic 3D Tiles (requires Draco, may not work in Vite dev server)
        this.tileset = await this.Cesium.createGooglePhotorealistic3DTileset({
          key: environment.googleMapsApiKey,
        });
      } else {
        console.warn('[Buildings] No 3D buildings provider configured. Set cesiumIonAccessToken (recommended) or googleMapsApiKey in environment config.');
        return;
      }
      this.viewer.scene.primitives.add(this.tileset);
      this.buildings3dEnabled.set(true);
    } catch (err) {
      console.error('[Buildings] Failed to load 3D buildings:', err);
    }
  }

  disable(): void {
    if (this.tileset) {
      this.tileset.show = false;
    }
    this.buildings3dEnabled.set(false);
  }

  toggle(): void {
    if (this.buildings3dEnabled()) {
      this.disable();
    } else {
      this.enable();
    }
  }

  ensureEnabled(): void {
    if (!this.buildings3dEnabled()) {
      this.enable();
    }
  }
}
