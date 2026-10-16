import { Injectable, signal } from '@angular/core';
import { ViewportBounds } from '../../features/map/viewport-bounds';

/**
 * The last viewport the map reported. The map is not in the DOM on page
 * routes (connections, settings), so anything there that wants "the area
 * the operator was looking at" reads this cached value instead.
 */
@Injectable({ providedIn: 'root' })
export class MapViewportService {
  private readonly boundsState = signal<ViewportBounds | null>(null);

  /** Degree bounds of the last map view, or null before the map has reported one. */
  readonly bounds = this.boundsState.asReadonly();

  set(bounds: ViewportBounds): void {
    this.boundsState.set({ ...bounds });
  }
}
