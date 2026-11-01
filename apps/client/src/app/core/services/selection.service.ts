import { Injectable, WritableSignal, inject, signal } from '@angular/core';
import { Observable, Subject } from 'rxjs';
import { Entity } from '../../shared/models/entity.model';
import { LatLon } from '../../shared/geo-math';
import { EntityService } from './entity.service';

/**
 * The selected track, shared by the map and the cockpit overlays around it.
 * The map flies the camera on fly$ and reports where the track sits on screen.
 */
@Injectable({ providedIn: 'root' })
export class SelectionService {
  private readonly entities = inject(EntityService);
  private readonly flyRequests = new Subject<LatLon>();

  /** The selected track. */
  readonly entity: WritableSignal<Entity | null> = signal<Entity | null>(null);
  /** Window pixels of the selected track, set by MapComponent each render; null when off the globe or unselected. */
  readonly screenPoint: WritableSignal<{ x: number; y: number } | null> = signal<{ x: number; y: number } | null>(null);
  /** Where MapComponent should fly the camera. */
  readonly fly$: Observable<LatLon> = this.flyRequests.asObservable();

  /** Selects an entity (or clears with null); one with a position also asks the map to fly there. */
  select(entity: Entity | null): void {
    this.entity.set(entity);
    const position = entity?.position;
    if (position) this.flyRequests.next({ lat: position.latitude, lon: position.longitude });
  }

  /** Selects the entity the EntityService knows by this id; unknown ids leave the selection alone. */
  selectById(id: string): void {
    const found = this.entities.getEntityById(id);
    if (found) this.select(found);
  }
}
