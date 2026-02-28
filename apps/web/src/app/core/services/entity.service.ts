import { Injectable, OnDestroy } from '@angular/core';
import { HttpClient, HttpParams } from '@angular/common/http';
import { BehaviorSubject, Observable, Subject, Subscription, shareReplay, switchMap, tap } from 'rxjs';
import { WebSocketService } from './websocket.service';
import {
  Entity,
  EntityEvent,
  EntityQuery,
  PaginatedResponse,
} from '../../shared/models/entity.model';

@Injectable({ providedIn: 'root' })
export class EntityService implements OnDestroy {
  private readonly apiUrl = '/api/v1/entities';
  private readonly searchUrl = '/api/v1/search';
  private readonly wsSubscription: Subscription;

  private readonly entitiesSubject = new BehaviorSubject<Map<string, Entity>>(new Map());
  private readonly querySubject = new Subject<EntityQuery | undefined>();
  private readonly queryPipelineSubscription: Subscription;

  /** Deduplicated entity query pipeline — each call cancels the previous in-flight request */
  private readonly entityQuery$: Observable<PaginatedResponse<Entity>>;

  /** Current entities map, merged from REST + WebSocket updates */
  readonly currentEntities$ = this.entitiesSubject.asObservable();

  /** Real-time entity updates from WebSocket */
  readonly entityUpdates$: Observable<EntityEvent>;

  constructor(
    private readonly http: HttpClient,
    private readonly wsService: WebSocketService,
  ) {
    this.entityUpdates$ = this.wsService.entityUpdates$;

    // Subscribe to WebSocket entity updates and merge into state
    this.wsSubscription = this.wsService.entityUpdates$.subscribe((event) => {
      this.mergeEntityEvent(event);
    });

    // Deduplicated entity query pipeline
    this.entityQuery$ = this.querySubject.pipe(
      switchMap((query) => {
        const params = this.buildParams(query);
        return this.http.get<PaginatedResponse<Entity>>(this.apiUrl, { params });
      }),
      tap((response) => {
        const map = this.entitiesSubject.value;
        response.data.forEach((entity) => map.set(entity.id, entity));
        this.entitiesSubject.next(map);
      }),
      shareReplay({ bufferSize: 1, refCount: true }),
    );

    // Keep pipeline hot
    this.queryPipelineSubscription = this.entityQuery$.subscribe();
  }

  ngOnDestroy(): void {
    this.wsSubscription.unsubscribe();
    this.queryPipelineSubscription.unsubscribe();
  }

  getEntities(query?: EntityQuery): Observable<PaginatedResponse<Entity>> {
    this.querySubject.next(query);
    return this.entityQuery$;
  }

  private buildParams(query?: EntityQuery): HttpParams {
    let params = new HttpParams();
    if (query) {
      if (query.entityType) params = params.set('entityType', query.entityType);
      if (query.source) params = params.set('source', query.source);
      if (query.classification) params = params.set('classification', query.classification);
      if (query.search) params = params.set('search', query.search);
      if (query.north !== undefined) params = params.set('north', query.north.toString());
      if (query.south !== undefined) params = params.set('south', query.south.toString());
      if (query.east !== undefined) params = params.set('east', query.east.toString());
      if (query.west !== undefined) params = params.set('west', query.west.toString());
      if (query.limit !== undefined) params = params.set('limit', query.limit.toString());
      if (query.offset !== undefined) params = params.set('offset', query.offset.toString());
    }
    return params;
  }

  getEntity(id: string): Observable<Entity> {
    return this.http.get<Entity>(`${this.apiUrl}/${id}`).pipe(
      tap((entity) => {
        const map = this.entitiesSubject.value;
        map.set(entity.id, entity);
        this.entitiesSubject.next(map);
      }),
    );
  }

  createEntity(dto: Partial<Entity>): Observable<Entity> {
    return this.http.post<Entity>(this.apiUrl, dto).pipe(
      tap((entity) => {
        const map = this.entitiesSubject.value;
        map.set(entity.id, entity);
        this.entitiesSubject.next(map);
      }),
    );
  }

  updateEntity(id: string, dto: Partial<Entity>): Observable<Entity> {
    return this.http.patch<Entity>(`${this.apiUrl}/${id}`, dto).pipe(
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

  private mergeEntityEvent(event: EntityEvent): void {
    const map = this.entitiesSubject.value;

    switch (event.type) {
      case 'created':
      case 'updated':
        map.set(event.entity.id, event.entity);
        break;
      case 'deleted':
        map.delete(event.entity.id);
        break;
    }

    this.entitiesSubject.next(map);
  }
}
