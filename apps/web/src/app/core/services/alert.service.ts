import { Injectable, OnDestroy } from '@angular/core';
import { HttpClient, HttpParams } from '@angular/common/http';
import { BehaviorSubject, Observable, Subscription, map, scan, startWith } from 'rxjs';
import { WebSocketService } from './websocket.service';
import {
  Alert,
  AlertQuery,
} from '../../shared/models/alert.model';
import { PaginatedResponse } from '../../shared/models/entity.model';

@Injectable({ providedIn: 'root' })
export class AlertService implements OnDestroy {
  private readonly apiUrl = '/api/v1/alerts';
  private readonly wsSubscription: Subscription;

  private readonly unacknowledgedCountSubject = new BehaviorSubject<number>(0);

  /** Real-time alert stream from WebSocket */
  readonly alertStream$: Observable<Alert>;

  /** Count of unacknowledged alerts */
  readonly unacknowledgedCount$: Observable<number> = this.unacknowledgedCountSubject.asObservable();

  constructor(
    private readonly http: HttpClient,
    private readonly wsService: WebSocketService,
  ) {
    this.alertStream$ = this.wsService.alertStream$;

    // Track unacknowledged count from real-time alerts
    this.wsSubscription = this.wsService.alertStream$.pipe(
      scan((count, alert) => alert.acknowledged ? count : count + 1, 0),
      startWith(0),
    ).subscribe((count) => {
      this.unacknowledgedCountSubject.next(
        this.unacknowledgedCountSubject.value + (count > 0 ? 1 : 0),
      );
    });

    // Also load initial count
    this.refreshUnacknowledgedCount();
  }

  ngOnDestroy(): void {
    this.wsSubscription.unsubscribe();
  }

  getAlerts(query?: AlertQuery): Observable<PaginatedResponse<Alert>> {
    let params = new HttpParams();
    if (query) {
      if (query.severity) params = params.set('severity', query.severity);
      if (query.alertType) params = params.set('alertType', query.alertType);
      if (query.acknowledged !== undefined) params = params.set('acknowledged', query.acknowledged.toString());
      if (query.entityId) params = params.set('entityId', query.entityId);
      if (query.limit !== undefined) params = params.set('limit', query.limit.toString());
      if (query.offset !== undefined) params = params.set('offset', query.offset.toString());
    }

    return this.http.get<PaginatedResponse<Alert>>(this.apiUrl, { params });
  }

  acknowledgeAlert(id: string): Observable<Alert> {
    return this.http.patch<Alert>(`${this.apiUrl}/${id}/acknowledge`, {}).pipe(
      map((alert) => {
        // Decrement unacknowledged count
        const current = this.unacknowledgedCountSubject.value;
        if (current > 0) {
          this.unacknowledgedCountSubject.next(current - 1);
        }
        return alert;
      }),
    );
  }

  refreshUnacknowledgedCount(): void {
    this.http
      .get<PaginatedResponse<Alert>>(this.apiUrl, {
        params: new HttpParams()
          .set('acknowledged', 'false')
          .set('limit', '0'),
      })
      .subscribe({
        next: (response) => this.unacknowledgedCountSubject.next(response.total ?? 0),
        error: () => {
          /* silently handle - server may not be running */
        },
      });
  }
}
