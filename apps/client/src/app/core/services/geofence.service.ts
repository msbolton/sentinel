import { Injectable, inject, signal } from '@angular/core';
import { HttpClient } from '@angular/common/http';
import { Observable, Subject, forkJoin, map, of, tap, catchError } from 'rxjs';
import { EntityService } from './entity.service';
import { Entity } from '../../shared/models/entity.model';
import {
  DEFAULT_GEOFENCE_COLOR,
  GeofenceConfig,
  GeofenceDraft,
  GeofenceRule,
  WATCHABLE_TYPES,
} from '../../shared/models/geofence.model';
import { formatArea, formatDistance, polygonAreaM2 } from '../../features/map/geofence-geometry';

const COLOR_KEY = 'sentinel-geofence-color';
const RULES_URL = '/api/v1/alerts/rules';

/**
 * Geofences are GEOFENCE alert rules. This service is the client's one view
 * of them: the list, the selection, the draft being drawn, and the anchor
 * entities range rings follow.
 */
@Injectable({ providedIn: 'root' })
export class GeofenceService {
  private readonly http = inject(HttpClient);
  private readonly entities = inject(EntityService);

  readonly zones = signal<GeofenceRule[]>([]);
  readonly selectedId = signal<string | null>(null);
  readonly draft = signal<GeofenceDraft | null>(null);
  /** Anchor entities of ring zones by id; null when the entity could not be fetched. */
  readonly anchors = signal<Map<string, Entity | null>>(new Map());
  readonly lastColor = signal<string>(localStorage.getItem(COLOR_KEY) ?? DEFAULT_GEOFENCE_COLOR);
  /** The panel's New button and the map control both ask the map to start drawing through this. */
  readonly startDrawingRequested = new Subject<void>();
  /** The zone whose shape is being redrawn, so Finish replaces it instead of making a draft. */
  readonly redrawing = signal<GeofenceRule | null>(null);

  load(): Observable<GeofenceRule[]> {
    return this.http.get<GeofenceRule[] | { data: GeofenceRule[] }>(RULES_URL).pipe(
      map((res) => (Array.isArray(res) ? res : res?.data ?? [])),
      map((rules) => rules.filter((r) => r.ruleType === 'GEOFENCE').map((r) => normalizeRule(r))),
      tap((zones) => {
        this.zones.set(zones);
        this.fetchAnchors(zones);
      }),
    );
  }

  create(draft: GeofenceDraft): Observable<GeofenceRule> {
    return this.http.post<GeofenceRule>(RULES_URL, draft).pipe(
      map((r) => normalizeRule(r)),
      tap((zone) => {
        this.zones.update((zs) => [...zs, zone]);
        this.fetchAnchors([zone]);
      }),
    );
  }

  update(id: string, patch: Partial<GeofenceDraft>): Observable<GeofenceRule> {
    return this.http.put<GeofenceRule>(`${RULES_URL}/${id}`, patch).pipe(
      map((r) => normalizeRule(r)),
      tap((zone) => {
        this.zones.update((zs) => zs.map((z) => (z.id === id ? zone : z)));
        this.fetchAnchors([zone]);
      }),
    );
  }

  setEnabled(id: string, enabled: boolean): Observable<GeofenceRule> {
    return this.update(id, { enabled });
  }

  delete(id: string): Observable<void> {
    return this.http.delete<void>(`${RULES_URL}/${id}`).pipe(
      tap(() => {
        this.zones.update((zs) => zs.filter((z) => z.id !== id));
        if (this.selectedId() === id) this.selectedId.set(null);
      }),
    );
  }

  select(id: string | null): void {
    this.selectedId.set(id);
  }

  setDraft(draft: GeofenceDraft | null): void {
    this.draft.set(draft);
  }

  rememberColor(hex: string): void {
    this.lastColor.set(hex);
    localStorage.setItem(COLOR_KEY, hex);
  }

  /** One line for the list: trigger, watched types, severity, size; ring state. */
  zoneSummary(zone: GeofenceRule): string {
    const c = zone.config;
    const types = zone.monitoredEntityTypes.length === 0
      ? 'everything'
      : zone.monitoredEntityTypes.map((t) => WATCHABLE_TYPES.find((w) => w.type === t)?.label.toLowerCase() ?? t.toLowerCase()).join(', ');
    if (c.shape === 'ring' && c.ring) {
      const parts = [`${types} within ${formatDistance(c.ring.radiusM)}`, zone.severity, 'follows track'];
      if (this.anchors().get(c.ring.entityId) === null) parts.push('track lost');
      return parts.join(' · ');
    }
    const trigger = c.triggerOn === 'EXIT' ? 'leave' : c.triggerOn === 'BOTH' ? 'enter or leave' : 'enter';
    const size = c.shape === 'circle' && c.circle ? formatDistance(c.circle.radiusM) : formatArea(polygonAreaM2(c.polygon ?? []));
    return [trigger, types, zone.severity, size].join(' · ');
  }

  /**
   * Fetch each ring's anchor; a failure records null so the list can say
   * "track lost", and a null is retried on the next load in case the track
   * is back.
   */
  private fetchAnchors(zones: GeofenceRule[]): void {
    const known = this.anchors();
    const ids = zones
      .map((z) => z.config.ring?.entityId)
      .filter((id): id is string => typeof id === 'string' && id !== '' && !known.get(id));
    if (ids.length === 0) return;
    forkJoin(
      ids.map((id) => this.entities.getEntity(id).pipe(
        map((e) => [id, e] as const),
        catchError(() => of([id, null] as const)),
      )),
    ).subscribe((pairs) => {
      this.anchors.update((m) => {
        const next = new Map(m);
        for (const [id, e] of pairs) next.set(id, e);
        return next;
      });
    });
  }
}

/** Rules made before the client existed carry no shape or colour; give them the defaults. */
function normalizeRule(raw: GeofenceRule): GeofenceRule {
  const config = (raw.config ?? {}) as Partial<GeofenceConfig>;
  return {
    ...raw,
    monitoredEntityTypes: raw.monitoredEntityTypes ?? [],
    config: {
      ...config,
      shape: config.shape ?? 'polygon',
      color: config.color ?? DEFAULT_GEOFENCE_COLOR,
      triggerOn: config.triggerOn ?? 'ENTRY',
    } as GeofenceConfig,
  };
}
