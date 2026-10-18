import { Injectable } from '@angular/core';
import { Subject } from 'rxjs';

/** Something to show on the map: an entity, a place, or an entity with a place to fall back on. */
export interface FocusTarget {
  entityId?: string;
  position?: { latitude: number; longitude: number };
  label: string;
}

/** True when the target names an entity or a place. */
export function canFocus(target: Pick<FocusTarget, 'entityId' | 'position'>): boolean {
  return !!target.entityId || !!target.position;
}

/**
 * Asks the map to show something: panels over the map (alerts) call
 * `focus`, and the map selects the entity and flies to it, or flies to the
 * position when the entity is gone or there is none.
 */
@Injectable({ providedIn: 'root' })
export class EntityFocusService {
  private readonly subject = new Subject<FocusTarget>();
  readonly focus$ = this.subject.asObservable();

  /** Returns false (and does nothing) when there is nothing to show. */
  focus(target: FocusTarget): boolean {
    if (!canFocus(target)) return false;
    this.subject.next(target);
    return true;
  }
}
