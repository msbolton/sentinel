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
  }

  async enable(): Promise<void> {
    if (!this.viewer || !this.Cesium) return;

    if (this.tileset) {
      this.tileset.show = true;
      this.buildings3dEnabled.set(true);
      return;
    }

    const apiKey = environment.googleMapsApiKey;
    if (!apiKey) {
      console.warn('Google Maps API key not configured. Set googleMapsApiKey in environment config to enable 3D buildings.');
      return;
    }

    try {
      this.tileset = await this.Cesium.Cesium3DTileset.fromUrl(
        `https://tile.googleapis.com/v1/3dtiles/root.json?key=${apiKey}`,
      );
      this.viewer.scene.primitives.add(this.tileset);
      this.buildings3dEnabled.set(true);
    } catch (err) {
      console.error('Failed to load Google 3D Tiles:', err);
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
