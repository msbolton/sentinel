import { Injectable, Signal, signal } from '@angular/core';
import { LatLon } from '../../shared/geo-math';

/**
 * What the map knows that the cockpit HUD displays: the globe point under the
 * mouse and the camera altitude. MapComponent writes; the overlays read.
 */
@Injectable({ providedIn: 'root' })
export class MapCursorService {
  private readonly cursorState = signal<LatLon | null>(null);
  private readonly altitudeState = signal<number | null>(null);

  /** The last globe pick under the mouse; null when the mouse is off the globe. */
  readonly cursor: Signal<LatLon | null> = this.cursorState.asReadonly();
  /** Camera height above the ellipsoid in metres; null until the camera is known. */
  readonly cameraAltitudeM: Signal<number | null> = this.altitudeState.asReadonly();

  /** Records the pick under the mouse; null, or a non-finite coordinate, means off the globe. */
  setCursor(p: LatLon | null): void {
    this.cursorState.set(p && Number.isFinite(p.lat) && Number.isFinite(p.lon) ? { lat: p.lat, lon: p.lon } : null);
  }

  setCameraAltitude(m: number | null): void {
    this.altitudeState.set(m !== null && Number.isFinite(m) ? m : null);
  }
}
