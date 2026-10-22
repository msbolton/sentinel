import { Injectable, signal } from '@angular/core';
import { environment } from '../../../environments/environment';
import { startsLite } from '../../features/map/lite-mode';

/** localStorage key for the remembered terrain choice ('true' or 'false'). */
export const TERRAIN_STORAGE_KEY = 'sentinel.map.terrain';

/**
 * Cesium World Terrain on the map, on or off.
 *
 * World Terrain needs a Cesium ion token, so without one the service is
 * unavailable and the globe stays on the ellipsoid. With one it is on unless
 * the user turned it off (remembered in localStorage). On a phone (narrow or
 * touch screen) it starts off until the user has chosen; a saved choice wins.
 * If World Terrain fails to load, the globe falls back to the ellipsoid and the
 * toggle reads off.
 */
@Injectable({ providedIn: 'root' })
export class TerrainService {
  private readonly _enabled = signal(false);
  private readonly _available = signal(!!environment.cesiumIonAccessToken);
  readonly terrainEnabled = this._enabled.asReadonly();
  /** True when a Cesium ion token is present, so World Terrain can load. */
  readonly available = this._available.asReadonly();

  private viewer: any = null;
  private Cesium: any = null;
  /** Cancels the load in flight, settling its promise as "not loaded". */
  private cancelLoad: (() => void) | null = null;

  async init(viewer: unknown, Cesium: unknown): Promise<void> {
    this.viewer = viewer;
    this.Cesium = Cesium;
    this._available.set(!!environment.cesiumIonAccessToken);
    if (!this._available()) {
      this._enabled.set(false);
      return;
    }
    if ((this.readStored() ?? !startsLite()) === false) {
      this._enabled.set(false);
      return;
    }
    await this.enable();
  }

  async toggle(): Promise<void> {
    if (!this.viewer || !this.Cesium || !this._available()) return;
    if (this._enabled()) {
      this.disable();
      this.store(false);
      return;
    }
    if (await this.enable()) this.store(true);
  }

  /** Loads World Terrain; resolves true once it is on the globe, false when it fell back. */
  private async enable(): Promise<boolean> {
    this.cancelLoad?.();
    this._enabled.set(true);
    let loaded = false;
    let cancelled = false;
    try {
      loaded = await new Promise<boolean>((resolve) => {
        const terrain = this.Cesium.Terrain.fromWorldTerrain();
        const off: Array<() => void> = [];
        const settle = (ok: boolean, err?: unknown) => {
          off.splice(0).forEach((fn) => fn());
          if (this.cancelLoad === cancel) this.cancelLoad = null;
          if (!ok && err !== undefined) console.warn('[Terrain] World Terrain failed to load; using the ellipsoid.', err);
          resolve(ok);
        };
        const cancel = () => {
          cancelled = true;
          settle(false);
        };
        this.cancelLoad = cancel;
        off.push(terrain.readyEvent.addEventListener(() => settle(true)));
        off.push(terrain.errorEvent.addEventListener((err: unknown) => settle(false, err ?? new Error('unknown error'))));
        this.viewer.scene.setTerrain(terrain);
        if (terrain.ready) settle(true);
      });
    } catch (err) {
      console.warn('[Terrain] World Terrain failed to load; using the ellipsoid.', err);
    }
    if (cancelled) return false; // the user switched it off meanwhile; disable() already ran
    if (!loaded) {
      this.useEllipsoid();
      this._enabled.set(false);
      return false;
    }
    this.viewer.scene.requestRender?.();
    return true;
  }

  private disable(): void {
    this.cancelLoad?.();
    this.useEllipsoid();
    this._enabled.set(false);
  }

  private useEllipsoid(): void {
    try {
      this.viewer.scene.terrainProvider = new this.Cesium.EllipsoidTerrainProvider();
      this.viewer.scene.requestRender?.();
    } catch (err) {
      console.warn('[Terrain] Could not restore the ellipsoid.', err);
    }
  }

  private readStored(): boolean | null {
    try {
      const v = localStorage.getItem(TERRAIN_STORAGE_KEY);
      return v === 'true' ? true : v === 'false' ? false : null;
    } catch {
      return null;
    }
  }

  private store(on: boolean): void {
    try {
      localStorage.setItem(TERRAIN_STORAGE_KEY, String(on));
    } catch {
      /* storage unavailable: the choice lasts for this session only */
    }
  }
}
