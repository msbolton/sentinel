import { Injectable, inject, signal, computed, DestroyRef } from '@angular/core';
import { HttpClient, HttpErrorResponse } from '@angular/common/http';
import { Observable, tap, retry, timer, Subscription, interval, startWith, switchMap, catchError, of, defer } from 'rxjs';
import {
  Feed,
  FeedDriver,
  CreateFeedRequest,
  ReplaceFeedRequest,
  FeedErrorResponse,
  FeedProblem,
  ListenPorts,
} from '../../shared/models/feed.model';

/**
 * Feeds and drivers as served by the ingest service through the gateway.
 * State lives in signals; nothing is cached in the browser. Enabling or
 * disabling a feed is a global operator action; what a user sees on the
 * map is the LayerVisibilityService's business.
 */
@Injectable({ providedIn: 'root' })
export class FeedService {
  private readonly http = inject(HttpClient);
  private readonly destroyRef = inject(DestroyRef);
  private readonly feedsUrl = '/api/v1/feeds';
  private readonly driversUrl = '/api/v1/drivers';
  private readonly portsUrl = '/api/v1/feeds/ports';

  private readonly feedsState = signal<Feed[]>([]);
  private readonly driversState = signal<FeedDriver[]>([]);
  private readonly listenPortsState = signal<ListenPorts | null>(null);
  private readonly loadingState = signal(false);
  private readonly errorState = signal<string | null>(null);
  private loaded = false;
  /** Counts the calls of `loadPorts`, so that only the latest one may answer. */
  private portsCalls = 0;
  private pollers = 0;
  private pollSub: Subscription | null = null;

  /** Every feed, built-ins first (server order). */
  readonly feeds = this.feedsState.asReadonly();
  readonly drivers = this.driversState.asReadonly();
  /** The answer to the latest `loadPorts`: null before it, while it waits, and when it failed. */
  readonly listenPorts = this.listenPortsState.asReadonly();
  readonly loading = this.loadingState.asReadonly();
  readonly error = this.errorState.asReadonly();
  readonly enabledFeeds = computed(() => this.feedsState().filter((f) => f.enabled));

  get hasLoaded(): boolean {
    return this.loaded;
  }

  /** Fetches the feed list once (retrying transient failures). */
  load(): Observable<Feed[]> {
    this.loadingState.set(true);
    return this.http.get<Feed[]>(this.feedsUrl).pipe(
      retry({ count: 3, delay: (_, n) => timer(n * 2000) }),
      tap({
        next: (feeds) => {
          this.loaded = true;
          this.errorState.set(null);
          this.feedsState.set(feeds);
          this.loadingState.set(false);
        },
        error: (err) => {
          this.errorState.set(messageOf(err));
          this.loadingState.set(false);
        },
      }),
    );
  }

  /** Fetches the driver catalog (schemas included). */
  loadDrivers(): Observable<FeedDriver[]> {
    return this.http.get<FeedDriver[]>(this.driversUrl).pipe(tap((d) => this.driversState.set(d)));
  }

  /**
   * Asks which ports listening connections hold, which Sentinel keeps for itself and which one to suggest.
   * `listenPorts` holds the answer to the latest call and to no other: asking again drops what it held, an
   * earlier call that is answered late is ignored (its own subscriber still gets the reply), and a failure
   * leaves none. A failure is the caller's to ignore, as the server checks the port when a connection is saved.
   */
  loadPorts(): Observable<ListenPorts> {
    return defer(() => {
      const call = ++this.portsCalls;
      this.listenPortsState.set(null);
      return this.http.get<ListenPorts>(this.portsUrl).pipe(
        tap((ports) => {
          if (call === this.portsCalls) {
            this.listenPortsState.set(ports);
          }
        }),
      );
    });
  }

  driver(id: string): FeedDriver | undefined {
    return this.driversState().find((d) => d.id === id);
  }

  feed(id: string): Feed | undefined {
    return this.feedsState().find((f) => f.id === id);
  }

  /**
   * Keeps the list fresh while at least one view wants it. Returns a
   * function that withdraws this view's interest; polling stops when the
   * last one withdraws.
   */
  poll(intervalMs = 10_000): () => void {
    this.pollers++;
    if (!this.pollSub) {
      this.pollSub = interval(intervalMs)
        .pipe(
          startWith(0),
          switchMap(() => this.load().pipe(catchError(() => of([] as Feed[])))),
        )
        .subscribe();
      this.destroyRef.onDestroy(() => this.pollSub?.unsubscribe());
    }
    let released = false;
    return () => {
      if (released) {
        return;
      }
      released = true;
      this.pollers--;
      if (this.pollers <= 0) {
        this.pollers = 0;
        this.pollSub?.unsubscribe();
        this.pollSub = null;
      }
    };
  }

  create(body: CreateFeedRequest): Observable<Feed> {
    return this.http.post<Feed>(this.feedsUrl, body).pipe(tap((f) => this.upsert(f)));
  }

  replace(id: string, body: ReplaceFeedRequest): Observable<Feed> {
    return this.http.put<Feed>(`${this.feedsUrl}/${encodeURIComponent(id)}`, body).pipe(tap((f) => this.upsert(f)));
  }

  /** Enables or disables a feed for everyone; persisted server-side. */
  setEnabled(id: string, enabled: boolean): Observable<Feed> {
    const action = enabled ? 'enable' : 'disable';
    return this.http
      .post<Feed>(`${this.feedsUrl}/${encodeURIComponent(id)}/${action}`, {})
      .pipe(tap((f) => this.upsert(f)));
  }

  delete(id: string): Observable<void> {
    return this.http
      .delete<void>(`${this.feedsUrl}/${encodeURIComponent(id)}`)
      .pipe(tap(() => this.feedsState.update((feeds) => feeds.filter((f) => f.id !== id))));
  }

  private upsert(feed: Feed): void {
    this.feedsState.update((feeds) => {
      const i = feeds.findIndex((f) => f.id === feed.id);
      if (i < 0) {
        return [...feeds, feed];
      }
      const next = feeds.slice();
      next[i] = feed;
      return next;
    });
  }
}

/** Extracts the server's message and validation problems from an HTTP error. */
export function problemsOf(err: unknown): { message: string; problems: FeedProblem[] } {
  if (err instanceof HttpErrorResponse) {
    const body = err.error as FeedErrorResponse | null;
    if (body && typeof body === 'object' && typeof body.error === 'string') {
      return { message: body.error, problems: body.details ?? [] };
    }
    return { message: err.message, problems: [] };
  }
  return { message: String(err), problems: [] };
}

function messageOf(err: unknown): string {
  return problemsOf(err).message;
}
