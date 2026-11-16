import { Injectable, WritableSignal, inject, signal } from '@angular/core';
import { Observable, Subject } from 'rxjs';
import { Entity } from '../../shared/models/entity.model';
import { LatLon, inBounds } from '../../shared/geo-math';
import { EntityService } from './entity.service';
import { MapViewportService } from './map-viewport.service';
import { ReplayService } from './replay.service';

/**
 * The selected track, shared by the map and the cockpit overlays around it.
 * The map flies the camera on fly$ and reports where the track sits on screen.
 */
@Injectable({ providedIn: 'root' })
export class SelectionService {
  private readonly entities = inject(EntityService);
  private readonly viewport = inject(MapViewportService);
  private readonly replay = inject(ReplayService);
  private readonly flyRequests = new Subject<LatLon>();

  /**
   * The selected track. MapComponent writes it directly, which is how a track
   * picked from the replay is selected; {@link select} is the path for live
   * tracks from the panels, the track stack and the alert ticker.
   */
  readonly entity: WritableSignal<Entity | null> = signal<Entity | null>(null);
  /** Window pixels of the selected track, set by MapComponent each render; null when unselected, behind the globe or behind the camera. */
  readonly screenPoint: WritableSignal<{ x: number; y: number } | null> = signal<{ x: number; y: number } | null>(null);
  /** Where MapComponent should fly the camera. */
  readonly fly$: Observable<LatLon> = this.flyRequests.asObservable();

  /**
   * Selects an entity (or clears with null). One with a position outside the
   * current view (or before the map has reported one) also asks the map to fly
   * there; one already on screen leaves the camera where it is.
   *
   * While the map replays history a live track has no place on it: selecting
   * one does nothing and returns false. Clearing (null) always works.
   */
  select(entity: Entity | null): boolean {
    if (entity && this.replay.mode() === 'replay') return false;
    this.entity.set(entity);
    const position = entity?.position;
    if (!position) return true;
    const point = { lat: position.latitude, lon: position.longitude };
    const bounds = this.viewport.bounds();
    if (bounds && inBounds(point, bounds)) return true;
    this.flyRequests.next(point);
    return true;
  }

  /** Asks the map to fly to a point without changing the selection. */
  flyTo(point: LatLon): void {
    this.flyRequests.next(point);
  }

  /**
   * Selects the entity the EntityService has cached under this id. Returns
   * false, leaving the selection alone, when the id is not cached or the map
   * is replaying (see {@link select}).
   */
  selectById(id: string): boolean {
    const found = this.entities.getEntityById(id);
    return !!found && this.select(found);
  }
}
