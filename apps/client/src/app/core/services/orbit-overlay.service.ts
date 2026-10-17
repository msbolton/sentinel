import { Injectable, signal } from '@angular/core';

export type OrbitOverlayKey = 'path' | 'groundTrack' | 'footprint';

/**
 * Which overlays the map draws for the selected satellite. The detail
 * panel toggles them; the map reads them.
 */
@Injectable({ providedIn: 'root' })
export class OrbitOverlayService {
  /** The path the satellite will trace over its next revolution, at altitude. */
  readonly showPath = signal(true);
  /** The sub-satellite line on the surface for the next revolution. */
  readonly showGroundTrack = signal(true);
  /** The circle of ground the satellite can see from where it is now. */
  readonly showFootprint = signal(true);

  toggle(key: OrbitOverlayKey): void {
    const s = this.signalFor(key);
    s.set(!s());
  }

  isOn(key: OrbitOverlayKey): boolean {
    return this.signalFor(key)();
  }

  private signalFor(key: OrbitOverlayKey) {
    switch (key) {
      case 'path':
        return this.showPath;
      case 'groundTrack':
        return this.showGroundTrack;
      case 'footprint':
        return this.showFootprint;
    }
  }
}
