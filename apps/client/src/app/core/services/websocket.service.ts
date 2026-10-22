import { Injectable, OnDestroy, inject } from '@angular/core';
import { BehaviorSubject, Observable, Subject, Subscription } from 'rxjs';
import { EntityEvent } from '../../shared/models/entity.model';
import { Alert } from '../../shared/models/alert.model';
import { DensityGrid } from '../../shared/models/density.model';
import { AuthService } from './auth.service';

export type ConnectionStatus = 'connected' | 'disconnected' | 'reconnecting';

export interface ViewportBounds {
  north: number;
  south: number;
  east: number;
  west: number;
}

/** Wire frame exchanged with the gateway's /ws/entities endpoint. */
interface Envelope {
  type: string;
  data?: unknown;
  ts?: string;
}

const WS_PATH = '/ws/entities';
const RECONNECT_MIN_MS = 1000;
const RECONNECT_MAX_MS = 10000;
const RECONNECT_JITTER_MS = 250;
/** Close code the gateway uses when it ends an expired demo guest. */
const CLOSE_POLICY_VIOLATION = 1008;

/**
 * Real-time entity stream over a native WebSocket.
 *
 * The gateway sends JSON envelopes `{ type, data, ts }`. Entity updates arrive
 * batched as `entity:batch`; ageout and alert events are broadcast to every
 * client. The service reconnects with exponential backoff and replays the last
 * viewport and entity subscriptions after every (re)connect.
 */
@Injectable({ providedIn: 'root' })
export class WebSocketService implements OnDestroy {
  private readonly auth = inject(AuthService);

  private socket: WebSocket | null = null;
  private intentionalClose = false;
  private reconnectAttempt = 0;
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null;
  private lastViewport: ViewportBounds | null = null;
  private readonly subscribedEntities = new Set<string>();
  /** Server clock minus local clock, from connection:ack; 0 until known. */
  private clockSkewMs = 0;

  private readonly entityUpdatesSubject = new Subject<EntityEvent>();
  private readonly alertSubject = new Subject<Alert>();
  private readonly connectionStatusSubject = new BehaviorSubject<ConnectionStatus>('disconnected');
  private readonly ageoutSubject = new Subject<{ eventType: string; payload: any }>();
  private readonly densitySubject = new Subject<DensityGrid>();

  readonly entityUpdates$: Observable<EntityEvent> = this.entityUpdatesSubject.asObservable();
  readonly alertStream$: Observable<Alert> = this.alertSubject.asObservable();
  readonly connectionStatus$: Observable<ConnectionStatus> = this.connectionStatusSubject.asObservable();
  readonly ageoutEvents$ = this.ageoutSubject.asObservable();
  /** Per-cell object counts a feed sends while views are too large to show the objects. */
  readonly density$: Observable<DensityGrid> = this.densitySubject.asObservable();

  private readonly guestSessionEndedSubject = new Subject<void>();
  /** The gateway ended this demo guest (closed the socket with 1008); the service stops reconnecting. */
  readonly guestSessionEnded$: Observable<void> = this.guestSessionEndedSubject.asObservable();

  /** Watches for a lost session while a socket is wanted (connect to disconnect). */
  private sessionLostSub: Subscription | null = null;

  connect(): void {
    if (this.socket || this.reconnectTimer) {
      return; // Already connected or reconnecting
    }
    this.intentionalClose = false;
    this.sessionLostSub ??= this.auth.sessionLost$.subscribe(() => {
      // A guest whose session can no longer be refreshed has nothing to reconnect with.
      if (this.auth.isGuest()) {
        this.disconnect();
      }
    });
    this.open();
  }

  /**
   * Drops any socket or pending backoff and opens a fresh one at once, with
   * the current token. For when the token just changed (a demo guest signed
   * in) or the stream was stopped for a guest whose session ended.
   */
  reconnectNow(): void {
    this.disconnect();
    this.reconnectAttempt = 0;
    this.connect();
  }

  /**
   * Current time on the server's clock, in ms. Entity timestamps come from
   * the server, so extrapolation must measure elapsed time against it rather
   * than a possibly skewed local clock.
   */
  serverNow(): number {
    return Date.now() + this.clockSkewMs;
  }

  sendViewportUpdate(bounds: ViewportBounds): void {
    this.lastViewport = { ...bounds };
    this.send('viewport:update', bounds);
  }

  subscribeToEntity(entityId: string): void {
    this.subscribedEntities.add(entityId);
    this.send('entity:subscribe', { entityId });
  }

  unsubscribeFromEntity(entityId: string): void {
    this.subscribedEntities.delete(entityId);
    this.send('entity:unsubscribe', { entityId });
  }

  disconnect(): void {
    this.intentionalClose = true;
    this.sessionLostSub?.unsubscribe();
    this.sessionLostSub = null;
    if (this.reconnectTimer) {
      clearTimeout(this.reconnectTimer);
      this.reconnectTimer = null;
    }
    const socket = this.socket;
    this.socket = null;
    if (socket) {
      socket.onopen = null;
      socket.onmessage = null;
      socket.onerror = null;
      socket.onclose = null;
      socket.close();
    }
    this.connectionStatusSubject.next('disconnected');
  }

  ngOnDestroy(): void {
    this.disconnect();
  }

  private open(): void {
    const socket = new WebSocket(this.buildUrl());
    this.socket = socket;

    socket.onopen = () => {
      console.log('[WS] Connected to entity stream');
      this.reconnectAttempt = 0;
      this.connectionStatusSubject.next('connected');
      this.replayState();
    };

    socket.onmessage = (event: MessageEvent) => {
      this.handleFrame(typeof event.data === 'string' ? event.data : '');
    };

    socket.onerror = () => {
      // The close event that follows drives reconnection.
    };

    socket.onclose = (event: CloseEvent) => {
      if (this.socket === socket) {
        this.socket = null;
      }
      if (this.intentionalClose) {
        this.connectionStatusSubject.next('disconnected');
        return;
      }
      console.log('[WS] Disconnected:', event?.reason || event?.code || 'connection closed');
      if (event?.code === CLOSE_POLICY_VIOLATION && this.auth.isGuest()) {
        // The gateway ended this guest; reconnecting would only be refused again.
        this.disconnect();
        this.guestSessionEndedSubject.next();
        return;
      }
      this.scheduleReconnect();
    };
  }

  private buildUrl(): string {
    const protocol = window.location.protocol === 'https:' ? 'wss' : 'ws';
    let url = `${protocol}://${window.location.host}${WS_PATH}`;
    const token = this.auth.getToken();
    if (token) {
      url += `?token=${encodeURIComponent(token)}`;
    }
    return url;
  }

  private scheduleReconnect(): void {
    this.connectionStatusSubject.next('reconnecting');
    const backoff = Math.min(RECONNECT_MIN_MS * 2 ** this.reconnectAttempt, RECONNECT_MAX_MS);
    const delay = backoff + Math.random() * RECONNECT_JITTER_MS;
    this.reconnectAttempt++;
    this.reconnectTimer = setTimeout(() => {
      this.reconnectTimer = null;
      if (!this.intentionalClose) {
        this.open();
      }
    }, delay);
  }

  /** Re-send viewport and subscriptions so the server-side state matches. */
  private replayState(): void {
    if (this.lastViewport) {
      this.send('viewport:update', this.lastViewport);
    }
    for (const entityId of this.subscribedEntities) {
      this.send('entity:subscribe', { entityId });
    }
  }

  private send(type: string, data: unknown): void {
    if (this.socket?.readyState === WebSocket.OPEN) {
      this.socket.send(JSON.stringify({ type, data }));
    }
  }

  /**
   * Ageout frames carry one payload or, since the gateway coalesces them per
   * flush, an array of payloads. Emit one event per payload either way.
   */
  private emitAgeout(eventType: 'stale' | 'agedout' | 'restored', data: unknown): void {
    const payloads = Array.isArray(data) ? data : [data];
    for (const payload of payloads) {
      if (payload && typeof payload === 'object') {
        this.ageoutSubject.next({ eventType, payload });
      }
    }
  }

  private handleFrame(raw: string): void {
    let envelope: Envelope;
    try {
      envelope = JSON.parse(raw) as Envelope;
    } catch {
      console.warn('[WS] Ignoring malformed frame');
      return;
    }

    switch (envelope.type) {
      case 'entity:batch': {
        const events = (envelope.data as EntityEvent[]) ?? [];
        for (const event of events) {
          this.entityUpdatesSubject.next(event);
        }
        break;
      }
      case 'entity:created':
      case 'entity:updated':
      case 'entity:deleted':
        this.entityUpdatesSubject.next({
          ...(envelope.data as EntityEvent),
          type: envelope.type.split(':')[1] as EntityEvent['type'],
        });
        break;
      case 'events.entity.stale':
        this.emitAgeout('stale', envelope.data);
        break;
      case 'events.entity.agedout':
        this.emitAgeout('agedout', envelope.data);
        break;
      case 'events.entity.restored':
        this.emitAgeout('restored', envelope.data);
        break;
      case 'alert:new':
        this.alertSubject.next(envelope.data as Alert);
        break;
      case 'density:grid':
        if (envelope.data) this.densitySubject.next(envelope.data as DensityGrid);
        break;
      case 'error':
        console.warn('[WS] Server error:', envelope.data);
        break;
      case 'connection:ack': {
        const serverTime = Date.parse((envelope.data as { serverTime?: string })?.serverTime ?? '');
        if (Number.isFinite(serverTime)) {
          this.clockSkewMs = serverTime - Date.now();
        }
        break;
      }
      default:
        // viewport:updated, entity:subscribed, pong: nothing to do
        break;
    }
  }
}
