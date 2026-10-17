import { DestroyRef, Injectable, inject, signal } from '@angular/core';
import { HttpClient, HttpErrorResponse, HttpParams } from '@angular/common/http';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { Observable, catchError, filter, of, tap, throwError, timer } from 'rxjs';
import { WebSocketService } from './websocket.service';
import {
  AnalyticsStats,
  EntityBaseline,
  Finding,
  FindingQuery,
  FindingStatus,
} from '../../shared/models/finding.model';
import { PaginatedResponse } from '../../shared/models/entity.model';

/**
 * Behavioural analysis: findings (departures from an entity's baseline) and
 * the baselines themselves, served by the analytics service through the
 * gateway. The open-findings count feeds the sidebar badge and refreshes
 * on a timer, after a review, and when an alert that came from a finding
 * arrives on the stream.
 */
@Injectable({ providedIn: 'root' })
export class AnalyticsService {
  private readonly apiUrl = '/api/v1/analytics';
  private readonly http = inject(HttpClient);
  private readonly ws = inject(WebSocketService);
  private readonly destroyRef = inject(DestroyRef);

  /** Open findings visible to this user; null until the first count arrives. */
  readonly openCount = signal<number | null>(null);

  constructor() {
    this.refreshOpenCount();
    timer(60_000, 60_000)
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe(() => this.refreshOpenCount());
    this.ws.alertStream$
      .pipe(
        filter((alert) => !!alert?.metadata?.['findingId']),
        takeUntilDestroyed(this.destroyRef),
      )
      .subscribe(() => this.refreshOpenCount());
  }

  getFindings(query: FindingQuery = {}): Observable<PaginatedResponse<Finding>> {
    let params = new HttpParams();
    if (query.status) params = params.set('status', query.status);
    if (query.kinds?.length) params = params.set('kinds', query.kinds.join(','));
    if (query.severity) params = params.set('severity', query.severity);
    if (query.entityId) params = params.set('entityId', query.entityId);
    if (query.since) params = params.set('since', query.since);
    if (query.page) params = params.set('page', String(query.page));
    if (query.pageSize) params = params.set('pageSize', String(query.pageSize));
    return this.http.get<PaginatedResponse<Finding>>(`${this.apiUrl}/findings`, { params });
  }

  getFinding(id: string): Observable<Finding> {
    return this.http.get<Finding>(`${this.apiUrl}/findings/${id}`);
  }

  /** Confirm or dismiss a finding (analyst or admin). */
  reviewFinding(id: string, status: Extract<FindingStatus, 'CONFIRMED' | 'DISMISSED'>): Observable<Finding> {
    return this.http
      .patch<Finding>(`${this.apiUrl}/findings/${id}`, { status })
      .pipe(tap(() => this.refreshOpenCount()));
  }

  getEntityFindings(entityId: string, query: FindingQuery = {}): Observable<PaginatedResponse<Finding>> {
    let params = new HttpParams();
    if (query.status) params = params.set('status', query.status);
    if (query.pageSize) params = params.set('pageSize', String(query.pageSize));
    return this.http.get<PaginatedResponse<Finding>>(`${this.apiUrl}/entities/${entityId}/findings`, { params });
  }

  /** The entity's baseline, or null when the service has none for it yet. */
  getBaseline(entityId: string): Observable<EntityBaseline | null> {
    return this.http.get<EntityBaseline>(`${this.apiUrl}/entities/${entityId}/baseline`).pipe(
      catchError((err: HttpErrorResponse) => (err.status === 404 ? of(null) : throwError(() => err))),
    );
  }

  getStats(): Observable<AnalyticsStats> {
    return this.http.get<AnalyticsStats>(`${this.apiUrl}/stats`);
  }

  refreshOpenCount(): void {
    this.getFindings({ status: 'OPEN', pageSize: 1 }).subscribe({
      next: (res) => this.openCount.set(res.total ?? 0),
      error: () => {
        /* the service may be down; keep the last count */
      },
    });
  }
}
