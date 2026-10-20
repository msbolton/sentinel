import { Injectable, OnDestroy } from '@angular/core';
import { HttpClient, HttpParams } from '@angular/common/http';
import { BehaviorSubject, Observable, Subject, Subscription, interval, map, shareReplay, switchMap, tap } from 'rxjs';
import { WebSocketService } from './websocket.service';
import {
  Entity,
  EntityEvent,
  EntityQuery,
  PaginatedResponse,
} from '../../shared/models/entity.model';

/**
 * The entity-service REST API serialises a position as `{ lat, lon }` with
 * the altitude as a top-level field, while the WebSocket stream (and the
 * rest of the client) uses `{ latitude, longitude, altitude }`. Normalise
 * REST responses to the client shape.
 */
export function normalizeEntity<T extends Partial<Entity>>(raw: T): T {
  const anyRaw = raw as Record<string, unknown>;
  const position = anyRaw['position'] as Record<string, unknown> | null | undefined;
  if (!position || typeof position !== 'object') {
    return raw;
  }
  const hasRestShape = 'lat' in position || 'lon' in position;
  if (!hasRestShape && position['altitude'] !== undefined) {
    return raw;
  }
  const latitude = (hasRestShape ? position['lat'] : position['latitude']) as number | undefined;
  const longitude = (hasRestShape ? position['lon'] : position['longitude']) as number | undefined;
  const altitude = (anyRaw['altitude'] ?? position['altitude']) as number | null | undefined;
  return {
    ...raw,
    position: {
      latitude,
      longitude,
      ...(altitude !== null && altitude !== undefined ? { altitude } : {}),
    },
  };
}

@Injectable({ providedIn: 'root' })
export class EntityService implements OnDestroy {
  private static readonly STALE_THRESHOLD_MS = 5 * 60 * 1000; // 5 minutes
  private static readonly MAX_ENTITIES = 5000;
  private static readonly EVICTION_INTERVAL_MS = 30_000;

  private readonly apiUrl = '/api/v1/entities';
  private readonly searchUrl = '/api/v1/search';
  private readonly wsSubscription: Subscription;
  private readonly ageoutSubscription: Subscription;
  private readonly evictionSubscription: Subscription;

  private readonly entitiesSubject = new BehaviorSubject<Map<string, Entity>>(new Map());
  private readonly querySubject = new Subject<EntityQuery | undefined>();
  private readonly queryPipelineSubscription: Subscription;
  private readonly entityEvictionsSubject = new Subject<string[]>();

  /** Deduplicated entity query pipeline — each call cancels the previous in-flight request */
  private readonly entityQuery$: Observable<PaginatedResponse<Entity>>;

  /** Current entities map, merged from REST + WebSocket updates */
  readonly currentEntities$ = this.entitiesSubject.asObservable();

  /** Real-time entity updates from WebSocket */
  readonly entityUpdates$: Observable<EntityEvent>;

  /** Emits arrays of entity IDs that were evicted due to staleness */
  readonly entityEvictions$ = this.entityEvictionsSubject.asObservable();

  constructor(
    private readonly http: HttpClient,
    private readonly wsService: WebSocketService,
  ) {
    this.entityUpdates$ = this.wsService.entityUpdates$;

    // Subscribe to WebSocket entity updates and merge into state
    this.wsSubscription = this.wsService.entityUpdates$.subscribe((event) => {
      this.mergeEntityEvent(event);
    });

    // Subscribe to ageout events
    this.ageoutSubscription = this.wsService.ageoutEvents$.subscribe((event) => {
      this.handleAgeoutEvent(event);
    });

    // Deduplicated entity query pipeline
    this.entityQuery$ = this.querySubject.pipe(
      switchMap((query) => {
        const params = this.buildParams(query);
        return this.http.get<PaginatedResponse<Entity>>(this.apiUrl, { params });
      }),
      map((response) => ({ ...response, data: (response.data ?? []).map(normalizeEntity) })),
      tap((response) => {
        const entities = this.entitiesSubject.value;
        response.data.forEach((entity) => entities.set(entity.id, entity));
        this.entitiesSubject.next(entities);
      }),
      shareReplay({ bufferSize: 1, refCount: true }),
    );

    // Keep pipeline hot
    this.queryPipelineSubscription = this.entityQuery$.subscribe();

    // Periodic eviction of stale entities
    this.evictionSubscription = interval(EntityService.EVICTION_INTERVAL_MS)
      .subscribe(() => this.evictStaleEntities());
  }

  ngOnDestroy(): void {
    this.wsSubscription.unsubscribe();
    this.ageoutSubscription.unsubscribe();
    this.queryPipelineSubscription.unsubscribe();
    this.evictionSubscription.unsubscribe();
  }

  getEntities(query?: EntityQuery): Observable<PaginatedResponse<Entity>> {
    this.querySubject.next(query);
    return this.entityQuery$;
  }

  private buildParams(query?: EntityQuery): HttpParams {
    let params = new HttpParams();
    if (query) {
      // entity-service reads `types`, not `entityTypes`
      if (query.entityTypes?.length) params = params.set('types', query.entityTypes.join(','));
      if (query.sources?.length) params = params.set('sources', query.sources.join(','));
      if (query.classification) params = params.set('classification', query.classification);
      if (query.north !== undefined) params = params.set('north', query.north.toString());
      if (query.south !== undefined) params = params.set('south', query.south.toString());
      if (query.east !== undefined) params = params.set('east', query.east.toString());
      if (query.west !== undefined) params = params.set('west', query.west.toString());
      if (query.page !== undefined) params = params.set('page', query.page.toString());
      if (query.pageSize !== undefined) params = params.set('pageSize', query.pageSize.toString());
    }
    return params;
  }

  getEntity(id: string): Observable<Entity> {
    return this.http.get<Entity>(`${this.apiUrl}/${id}`).pipe(
      map(normalizeEntity),
      tap((entity) => {
        const map = this.entitiesSubject.value;
        map.set(entity.id, entity);
        this.entitiesSubject.next(map);
      }),
    );
  }

  createEntity(dto: Partial<Entity>): Observable<Entity> {
    return this.http.post<Entity>(this.apiUrl, dto).pipe(
      map(normalizeEntity),
      tap((entity) => {
        const map = this.entitiesSubject.value;
        map.set(entity.id, entity);
        this.entitiesSubject.next(map);
      }),
    );
  }

  updateEntity(id: string, dto: Partial<Entity>): Observable<Entity> {
    return this.http.patch<Entity>(`${this.apiUrl}/${id}`, dto).pipe(
      map(normalizeEntity),
      tap((entity) => {
        const map = this.entitiesSubject.value;
        map.set(entity.id, entity);
        this.entitiesSubject.next(map);
      }),
    );
  }

  searchEntities(query: string): Observable<PaginatedResponse<Entity>> {
    const params = new HttpParams().set('q', query);
    return this.http.get<PaginatedResponse<Entity>>(this.searchUrl, { params });
  }

  getEntityById(id: string): Entity | undefined {
    return this.entitiesSubject.value.get(id);
  }

  clearEntities(): void {
    this.entitiesSubject.next(new Map());
  }

  deleteAllEntities(): Observable<{ deleted: number }> {
    return this.http.delete<{ deleted: number }>(this.apiUrl).pipe(
      tap(() => this.clearEntities()),
    );
  }

  private evictStaleEntities(): void {
    const map = this.entitiesSubject.value;
    if (map.size === 0) return;

    const now = Date.now();
    const evictedIds: string[] = [];

    for (const [id, entity] of map) {
      if (entity.lastSeenAt) {
        const age = now - new Date(entity.lastSeenAt).getTime();
        if (age > EntityService.STALE_THRESHOLD_MS) {
          map.delete(id);
          evictedIds.push(id);
        }
      }
    }

    // If still over max, remove oldest by lastSeenAt
    if (map.size > EntityService.MAX_ENTITIES) {
      const sorted = [...map.entries()].sort((a, b) => {
        const aTime = a[1].lastSeenAt ? new Date(a[1].lastSeenAt).getTime() : 0;
        const bTime = b[1].lastSeenAt ? new Date(b[1].lastSeenAt).getTime() : 0;
        return aTime - bTime;
      });
      const excess = map.size - EntityService.MAX_ENTITIES;
      for (let i = 0; i < excess; i++) {
        map.delete(sorted[i][0]);
        evictedIds.push(sorted[i][0]);
      }
    }

    if (evictedIds.length > 0) {
      this.entitiesSubject.next(map);
      this.entityEvictionsSubject.next(evictedIds);
    }
  }

  private handleAgeoutEvent(event: { eventType: string; payload: any }): void {
    const map = this.entitiesSubject.value;
    const entityId = event.payload.entity_id;

    switch (event.eventType) {
      case 'stale': {
        const entity = map.get(entityId);
        if (entity) {
          entity.ageoutState = 'STALE';
          this.entitiesSubject.next(map);
        }
        break;
      }
      case 'agedout': {
        map.delete(entityId);
        this.entitiesSubject.next(map);
        this.entityEvictionsSubject.next([entityId]);
        break;
      }
      case 'restored': {
        const entity = map.get(entityId);
        if (entity) {
          entity.ageoutState = 'LIVE';
          this.entitiesSubject.next(map);
        }
        break;
      }
    }
  }

  private mergeEntityEvent(event: EntityEvent): void {
    const map = this.entitiesSubject.value;

    switch (event.type) {
      case 'created':
      case 'updated': {
        const prev = map.get(event.entity.id);
        const merged =
          prev && event.entity.affiliation == null && prev.affiliation != null
            ? { ...event.entity, affiliation: prev.affiliation }
            : event.entity;
        map.set(event.entity.id, merged);
        break;
      }
      case 'deleted':
        map.delete(event.entity.id);
        break;
    }

    this.entitiesSubject.next(map);
  }
}
