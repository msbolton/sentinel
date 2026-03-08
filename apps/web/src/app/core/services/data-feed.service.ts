import { Injectable, inject } from '@angular/core';
import { HttpClient } from '@angular/common/http';
import { toSignal } from '@angular/core/rxjs-interop';
import { BehaviorSubject, Observable, tap, retry, timer } from 'rxjs';

export interface FeedHealth {
  lastSuccessAt: string;
  entitiesCount: number;
  errorCount: number;
  status: 'healthy' | 'warn' | 'critical' | 'unknown';
}

export interface DataFeed {
  id: string;
  name: string;
  sourceType: string;
  description: string;
  enabled: boolean;
  custom?: boolean;
  connectorType?: string;
  format?: string;
  health?: FeedHealth;
}

export interface CreateFeedRequest {
  name: string;
  connector_type: 'mqtt' | 'stomp' | 'tcp';
  format: 'json' | 'nmea' | 'cot' | 'ais' | 'adsb' | 'link16';
  config: Record<string, unknown>;
}

@Injectable({ providedIn: 'root' })
export class DataFeedService {
  private readonly http = inject(HttpClient);
  private readonly apiUrl = '/api/v1/feeds';
  private readonly feedsSubject = new BehaviorSubject<DataFeed[]>([]);
  private loaded = false;
  readonly feeds$ = this.feedsSubject.asObservable();
  readonly feeds = toSignal(this.feeds$, { initialValue: [] });

  loadFeeds(): void {
    this.http.get<DataFeed[]>(this.apiUrl).pipe(
      retry({ count: 3, delay: (_, retryIndex) => timer(retryIndex * 2000) }),
    ).subscribe({
      next: (feeds) => {
        this.loaded = true;
        this.feedsSubject.next(feeds);
      },
      error: () => {
        this.loaded = false;
      },
    });
  }

  get hasLoaded(): boolean {
    return this.loaded;
  }

  toggleFeed(id: string, enabled: boolean): Observable<DataFeed> {
    return this.http.put<DataFeed>(`${this.apiUrl}/${id}`, { enabled }).pipe(
      tap((updated) => {
        const feeds = this.feedsSubject.value.map((f) =>
          f.id === updated.id ? updated : f,
        );
        this.feedsSubject.next(feeds);
      }),
    );
  }

  createFeed(request: CreateFeedRequest): Observable<DataFeed> {
    return this.http.post<DataFeed>(this.apiUrl, request).pipe(
      tap(() => this.loadFeeds()),
    );
  }

  deleteFeed(id: string): Observable<void> {
    return this.http.delete<void>(`${this.apiUrl}/${id}`).pipe(
      tap(() => {
        const feeds = this.feedsSubject.value.filter(f => f.id !== id);
        this.feedsSubject.next(feeds);
      }),
    );
  }
}
