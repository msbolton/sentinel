import { Injectable, inject, signal } from '@angular/core';
import { HttpClient, HttpErrorResponse } from '@angular/common/http';
import { firstValueFrom } from 'rxjs';
import { AuthService } from './auth.service';
import { WebSocketService } from './websocket.service';
import { blurPosition } from '../../shared/blur';

/** What the gateway tells a new guest, minus the tokens (AuthService holds those). */
export interface DemoSession {
  guestId: string;
  /** ISO-8601 time the gateway ends the guest. */
  expiresAt: string;
  /** The connection the visitors' emplacements belong to. */
  visitorsFeedId: string;
}

export interface DemoPosition {
  lat: number;
  lon: number;
  source: 'browser' | 'ip';
}

export interface DemoBounds {
  west: number;
  south: number;
  east: number;
  north: number;
}

export type StartResult = 'ok' | 'full' | 'error';

interface SessionResponse extends DemoSession {
  accessToken: string;
  refreshToken: string;
  expiresIn: number;
}

interface PositionResponse {
  lat?: number;
  lon?: number;
  source: 'browser' | 'ip' | 'none';
  emplacementId?: string;
}

const STARTING_MESSAGE = 'The demo is starting up — try again in a moment.';
const GENERIC_MESSAGE = 'Something went wrong starting the demo — try again in a moment.';

/**
 * The public-demo visitor: signs them in as a guest through the gateway and
 * places their (blurred) emplacement. Holds nothing durable; the
 * tokens live in AuthService and a reload starts a new session.
 */
@Injectable({ providedIn: 'root' })
export class DemoService {
  private readonly http = inject(HttpClient);
  private readonly auth = inject(AuthService);
  private readonly ws = inject(WebSocketService);

  /** The guest session this service started; null until startSession succeeds. */
  readonly session = signal<DemoSession | null>(null);
  readonly ownEmplacementId = signal<string | null>(null);
  readonly ownPosition = signal<DemoPosition | null>(null);
  /** True once the guest was ended (expired, refresh failed or the gateway closed the stream). */
  readonly sessionEnded = signal(false);
  /** The guest closed the welcome card; kept in memory, so it stays closed for this session only. */
  readonly welcomeDismissed = signal(false);
  /**
   * True once the welcome card has been on screen (or was dismissed). The map
   * waits for it before asking for the visitor's location, so the browser's
   * permission prompt arrives after the explanation, not before it.
   */
  readonly welcomeShown = signal(false);
  /** A box the map should show, e.g. around the visitor; null for no request. */
  readonly focusBounds = signal<DemoBounds | null>(null);
  /** Why the last startSession gave 'error'; null otherwise. */
  readonly errorMessage = signal<string | null>(null);

  constructor() {
    const ended = () => {
      if (this.session() !== null) {
        this.sessionEnded.set(true);
      }
    };
    this.auth.sessionLost$.subscribe(ended);
    this.ws.guestSessionEnded$.subscribe(ended);
  }

  async startSession(): Promise<StartResult> {
    this.errorMessage.set(null);
    try {
      const res = await firstValueFrom(this.http.post<SessionResponse>('/api/v1/demo/session', {}));
      this.auth.adoptTokens({
        accessToken: res.accessToken,
        refreshToken: res.refreshToken,
        expiresIn: res.expiresIn,
      });
      this.session.set({ guestId: res.guestId, expiresAt: res.expiresAt, visitorsFeedId: res.visitorsFeedId });
      this.sessionEnded.set(false);
      // A new session starts from scratch: nothing of the last guest's welcome, place or view carries over.
      this.welcomeDismissed.set(false);
      this.welcomeShown.set(false);
      this.ownPosition.set(null);
      this.ownEmplacementId.set(null);
      this.focusBounds.set(null);
      // The stream opened before this token existed (or was stopped when the
      // last guest ended); give it the new token without waiting out a backoff.
      this.ws.reconnectNow();
      return 'ok';
    } catch (err) {
      if (err instanceof HttpErrorResponse && err.status === 503) {
        if (err.error?.error === 'full') {
          return 'full';
        }
        this.errorMessage.set(STARTING_MESSAGE);
        return 'error';
      }
      this.errorMessage.set(GENERIC_MESSAGE);
      return 'error';
    }
  }

  /**
   * Tells the gateway where the visitor is (or that they declined), and keeps
   * the answer. A browser fix is blurred here, before it is sent, so the
   * precise one never leaves the machine; it is kept only for the visitor's
   * own marker. Declined, the answer is the gateway's (blurred) IP location.
   * Null when the gateway could place nobody or the call failed.
   */
  async reportPosition(p: { lat: number; lon: number } | 'denied'): Promise<DemoPosition | null> {
    const body = p === 'denied' ? { denied: true } : blurPosition(p.lat, p.lon);
    try {
      const res = await firstValueFrom(this.http.post<PositionResponse>('/api/v1/demo/position', body));
      if (res.source === 'none' || res.lat === undefined || res.lon === undefined) {
        return null;
      }
      const position: DemoPosition =
        p === 'denied' ? { lat: res.lat, lon: res.lon, source: res.source } : { lat: p.lat, lon: p.lon, source: 'browser' };
      this.ownPosition.set(position);
      this.ownEmplacementId.set(res.emplacementId ?? null);
      return position;
    } catch {
      return null;
    }
  }
}
