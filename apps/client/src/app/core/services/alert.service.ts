import { Injectable, OnDestroy } from '@angular/core';
import { HttpClient, HttpParams } from '@angular/common/http';
import { BehaviorSubject, Observable, Subject, Subscription, map, merge } from 'rxjs';
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
  /** Alerts this client acknowledged, so live views drop them without waiting for the server. */
  private readonly localUpdates = new Subject<Alert>();

  /** Real-time alert stream: the WebSocket's alerts plus this client's acknowledgements. */
  readonly alertStream$: Observable<Alert>;

  /** Count of unacknowledged alerts */
  readonly unacknowledgedCount$: Observable<number> = this.unacknowledgedCountSubject.asObservable();

  constructor(
    private readonly http: HttpClient,
    private readonly wsService: WebSocketService,
  ) {
    this.alertStream$ = merge(this.wsService.alertStream$, this.localUpdates);

    // A new alert on the stream is one more to acknowledge. An acknowledged
    // one is an update (ours echoed back, or another operator's), and the
    // server knows the count.
    this.wsSubscription = this.wsService.alertStream$.subscribe((alert) => {
      if (alert.acknowledged) this.refreshUnacknowledgedCount();
      else this.unacknowledgedCountSubject.next(this.unacknowledgedCountSubject.value + 1);
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
      if (query.alertType) params = params.set('types', query.alertType);
      if (query.acknowledged !== undefined) params = params.set('acknowledged', query.acknowledged.toString());
      if (query.entityId) params = params.set('entityId', query.entityId);
      if (query.page !== undefined) params = params.set('page', query.page.toString());
      if (query.pageSize !== undefined) params = params.set('pageSize', query.pageSize.toString());
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
        // The server answers with the updated alert; mark it acknowledged either way.
        this.localUpdates.next({ ...(alert ?? {}), id, acknowledged: true } as Alert);
        return alert;
      }),
    );
  }

  refreshUnacknowledgedCount(): void {
    this.http
      .get<PaginatedResponse<Alert>>(this.apiUrl, {
        params: new HttpParams()
          .set('acknowledged', 'false')
          .set('pageSize', '1'),
      })
      .subscribe({
        next: (response) => this.unacknowledgedCountSubject.next(response.total ?? 0),
        error: () => {
          /* silently handle - server may not be running */
        },
      });
  }
}
