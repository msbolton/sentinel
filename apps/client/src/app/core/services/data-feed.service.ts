import { Injectable, inject } from '@angular/core';
import { HttpClient } from '@angular/common/http';
import { toSignal } from '@angular/core/rxjs-interop';
import { BehaviorSubject, Observable, tap, retry, timer } from 'rxjs';

export type FeedHealthLevel = 'healthy' | 'warn' | 'critical' | 'unknown';
export type FeedState = 'stopped' | 'starting' | 'running' | 'idle' | 'degraded' | 'error';

export interface FeedStatus {
  owner: string;
  state: FeedState;
  health: FeedHealthLevel;
  lastError?: string;
  lastMessageAt: string | null;
  lastSuccessAt: string | null;
  messagesTotal: number;
  entitiesLast: number;
  errorsTotal: number;
  appliedVersion: number;
  detail: Record<string, unknown>;
  updatedAt: string | null;
}

export interface DataFeed {
  id: string;
  name: string;
  description: string;
  driver: string;
  source: string;
  builtin: boolean;
  enabled: boolean;
  version: number;
  config: Record<string, unknown>;
  secretRefs: Record<string, string>;
  capabilities: string[];
  createdAt: string;
  updatedAt: string;
  status: FeedStatus;
}

export type StreamTransport = 'mqtt' | 'stomp' | 'tcp';
export type StreamFormat = 'json' | 'nmea' | 'cot' | 'ais' | 'adsb' | 'link16';

/** Request shape used by the add-feed form; mapped to a `stream` driver feed. */
export interface CreateFeedRequest {
  name: string;
  connector_type: StreamTransport;
  format: StreamFormat;
  config: Record<string, unknown>;
}

/** Maps the form's connector vocabulary onto the stream driver's config. */
export function toStreamFeed(request: CreateFeedRequest): Record<string, unknown> {
  const transport: Record<string, unknown> = {};
  switch (request.connector_type) {
    case 'mqtt':
      transport['kind'] = 'mqtt';
      transport['brokerUrl'] = request.config['broker_url'];
      transport['topics'] = request.config['topics'];
      transport['qos'] = request.config['qos'] ?? 1;
      break;
    case 'stomp':
      transport['kind'] = 'stomp';
      transport['brokerUrl'] = request.config['broker_url'];
      transport['queue'] = request.config['queue'];
      break;
    case 'tcp':
      transport['kind'] = 'tcpserver';
      transport['address'] = request.config['address'];
      break;
  }
  const decoder = request.format === 'adsb' ? 'sbs' : request.format;
  return {
    name: request.name,
    driver: 'stream',
    enabled: true,
    config: { transport, decoder: { name: decoder } },
  };
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

  /** Enables or disables a feed for everyone; the state is persisted server-side. */
  toggleFeed(id: string, enabled: boolean): Observable<DataFeed> {
    const action = enabled ? 'enable' : 'disable';
    return this.http.post<DataFeed>(`${this.apiUrl}/${id}/${action}`, {}).pipe(
      tap((updated) => {
        const feeds = this.feedsSubject.value.map((f) => (f.id === updated.id ? updated : f));
        this.feedsSubject.next(feeds);
      }),
    );
  }

  createFeed(request: CreateFeedRequest): Observable<DataFeed> {
    return this.http.post<DataFeed>(this.apiUrl, toStreamFeed(request)).pipe(
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
