import { TestBed } from '@angular/core/testing';
import { provideHttpClient } from '@angular/common/http';
import { HttpTestingController, provideHttpClientTesting } from '@angular/common/http/testing';
import { BehaviorSubject, Subject } from 'rxjs';
import { DemoService } from './demo.service';
import { AuthService } from './auth.service';
import { WebSocketService } from './websocket.service';
import { loadGuestSession, saveGuestSession } from './guest-session-store';

const sessionBody = {
  accessToken: 'at',
  refreshToken: 'rt',
  expiresIn: 300,
  guestId: '1a2b3c4d',
  expiresAt: '2026-10-22T12:00:00Z',
  visitorsFeedId: 'feed-visitors',
};

describe('DemoService', () => {
  let service: DemoService;
  let http: HttpTestingController;
  let sessionLost: Subject<void>;
  let guestEnded: Subject<void>;
  let auth: { adoptTokens: jest.Mock; sessionLost$: Subject<void>; userProfile$: BehaviorSubject<unknown>; isGuest: jest.Mock };
  let ws: { guestSessionEnded$: Subject<void>; reconnectNow: jest.Mock };

  beforeEach(() => {
    sessionLost = new Subject<void>();
    guestEnded = new Subject<void>();
    ws = { guestSessionEnded$: guestEnded, reconnectNow: jest.fn() };
    localStorage.clear();
    auth = { adoptTokens: jest.fn(), sessionLost$: sessionLost, userProfile$: new BehaviorSubject<unknown>(null), isGuest: jest.fn().mockReturnValue(false) };
    TestBed.configureTestingModule({
      providers: [
        provideHttpClient(),
        provideHttpClientTesting(),
        { provide: AuthService, useValue: auth },
        { provide: WebSocketService, useValue: ws },
      ],
    });
    service = TestBed.inject(DemoService);
    http = TestBed.inject(HttpTestingController);
  });
  afterEach(() => {
    http.verify();
    localStorage.clear();
  });

  async function start(): Promise<'ok' | 'full' | 'error'> {
    const result = service.startSession();
    return result;
  }

  it('startSession posts to /api/v1/demo/session, adopts tokens and returns ok', async () => {
    const result = start();
    const req = http.expectOne('/api/v1/demo/session');
    expect(req.request.method).toBe('POST');
    req.flush(sessionBody);

    await expect(result).resolves.toBe('ok');
    expect(auth.adoptTokens).toHaveBeenCalledWith({ accessToken: 'at', refreshToken: 'rt', expiresIn: 300 });
    expect(service.session()).toEqual({
      guestId: '1a2b3c4d',
      expiresAt: '2026-10-22T12:00:00Z',
      visitorsFeedId: 'feed-visitors',
    });
    expect(service.sessionEnded()).toBe(false);
    expect(ws.reconnectNow).toHaveBeenCalledTimes(1);
  });

  it('startSession returns full on 503 {error:"full"}', async () => {
    const result = start();
    http.expectOne('/api/v1/demo/session').flush({ error: 'full' }, { status: 503, statusText: 'Service Unavailable' });

    await expect(result).resolves.toBe('full');
    expect(auth.adoptTokens).not.toHaveBeenCalled();
    expect(ws.reconnectNow).not.toHaveBeenCalled();
    expect(service.session()).toBeNull();
  });

  it('startSession returns error with a starting-up message on any other 503', async () => {
    const result = start();
    http.expectOne('/api/v1/demo/session').flush({ error: 'starting' }, { status: 503, statusText: 'Service Unavailable' });

    await expect(result).resolves.toBe('error');
    expect(service.errorMessage()).toBe('The demo is starting up — try again in a moment.');
  });

  it('startSession returns error with a starting-up message on a 503 with no JSON body', async () => {
    const result = start();
    http.expectOne('/api/v1/demo/session').flush('upstream down', { status: 503, statusText: 'Service Unavailable' });

    await expect(result).resolves.toBe('error');
    expect(service.errorMessage()).toBe('The demo is starting up — try again in a moment.');
  });

  it('startSession returns error with a try-again-in-a-moment message on 429 (too many sessions from this network)', async () => {
    const result = start();
    http.expectOne('/api/v1/demo/session').flush({ error: 'busy' }, { status: 429, statusText: 'Too Many Requests' });

    await expect(result).resolves.toBe('error');
    expect(service.errorMessage()).toBe('Your network already has demo sessions open — try again in a moment.');
  });

  it.each([500, 0])('startSession returns error with a generic message on status %s', async (status) => {
    const result = start();
    const req = http.expectOne('/api/v1/demo/session');
    if (status === 0) {
      req.error(new ProgressEvent('error'));
    } else {
      req.flush({}, { status, statusText: 'x' });
    }

    await expect(result).resolves.toBe('error');
    expect(service.errorMessage()).toBe('Something went wrong starting the demo — try again in a moment.');
  });

  it('startSession returns error when the adopted token cannot be read', async () => {
    auth.adoptTokens.mockImplementation(() => {
      throw new Error('bad token');
    });
    const result = start();
    http.expectOne('/api/v1/demo/session').flush(sessionBody);

    await expect(result).resolves.toBe('error');
    expect(service.session()).toBeNull();
  });

  it('a successful start clears an earlier ended session and error', async () => {
    service.sessionEnded.set(true);
    const result = start();
    http.expectOne('/api/v1/demo/session').flush(sessionBody);
    await result;
    expect(service.sessionEnded()).toBe(false);
    expect(service.errorMessage()).toBeNull();
  });

  describe('reportPosition', () => {
    it("('denied') posts {denied:true} and stores the returned ip position", async () => {
      const result = service.reportPosition('denied');
      const req = http.expectOne('/api/v1/demo/position');
      expect(req.request.method).toBe('POST');
      expect(req.request.body).toEqual({ denied: true });
      req.flush({ lat: 51.5, lon: -0.1, source: 'ip', emplacementId: 'emp-1' });

      await expect(result).resolves.toEqual({ lat: 51.5, lon: -0.1, source: 'ip' });
      expect(service.ownEmplacementId()).toBe('emp-1');
      expect(service.ownPosition()).toEqual({ lat: 51.5, lon: -0.1, source: 'ip' });
    });

    it('posts a browser position blurred to its 0.1° cell centre, and keeps the precise fix for the own marker', async () => {
      const result = service.reportPosition({ lat: 51.5074, lon: -0.1278 });
      const req = http.expectOne('/api/v1/demo/position');
      expect(req.request.body.lat).toBeCloseTo(51.55, 9);
      expect(req.request.body.lon).toBeCloseTo(-0.15, 9);
      expect(Object.keys(req.request.body).sort()).toEqual(['lat', 'lon']);
      // The precise fix never leaves the browser.
      expect(JSON.stringify(req.request.body)).not.toContain('51.5074');
      req.flush({ lat: 51.55, lon: -0.15, source: 'browser', emplacementId: 'emp-2' });

      // The marker is drawn at the precise fix, not at the blurred echo.
      await expect(result).resolves.toEqual({ lat: 51.5074, lon: -0.1278, source: 'browser' });
      expect(service.ownPosition()).toEqual({ lat: 51.5074, lon: -0.1278, source: 'browser' });
      expect(service.ownEmplacementId()).toBe('emp-2');
    });

    it('returns null and stores nothing for {source:"none"}', async () => {
      const result = service.reportPosition('denied');
      http.expectOne('/api/v1/demo/position').flush({ source: 'none' });

      await expect(result).resolves.toBeNull();
      expect(service.ownEmplacementId()).toBeNull();
      expect(service.ownPosition()).toBeNull();
    });

    it('returns null when the request fails', async () => {
      const result = service.reportPosition({ lat: 1, lon: 2 });
      http.expectOne('/api/v1/demo/position').flush({}, { status: 403, statusText: 'Forbidden' });

      await expect(result).resolves.toBeNull();
      expect(service.ownPosition()).toBeNull();
    });
  });

  describe('the stored guest session (reloads and other tabs)', () => {
    it('startSession stores what the demo needs to pick up again', async () => {
      const result = start();
      http.expectOne('/api/v1/demo/session').flush(sessionBody);
      await result;
      expect(loadGuestSession()).toEqual(
        expect.objectContaining({ guestId: '1a2b3c4d', expiresAt: '2026-10-22T12:00:00Z', visitorsFeedId: 'feed-visitors' }),
      );
    });

    it('takes up the stored session once the user is a (resumed) guest', () => {
      saveGuestSession({ refreshToken: 'r', guestId: '1a2b3c4d', expiresAt: '2026-10-22T12:00:00Z', visitorsFeedId: 'feed-visitors', ownEmplacementId: 'emp-1' });
      expect(service.session()).toBeNull();

      auth.isGuest.mockReturnValue(true);
      auth.userProfile$.next({ username: 'guest-1a2b3c4d' });

      expect(service.session()).toEqual({ guestId: '1a2b3c4d', expiresAt: '2026-10-22T12:00:00Z', visitorsFeedId: 'feed-visitors' });
      expect(service.ownEmplacementId()).toBe('emp-1');
    });

    it('ignores a stored session for someone who is not a guest', () => {
      saveGuestSession({ refreshToken: 'r', guestId: 'g', expiresAt: 'x', visitorsFeedId: 'v' });
      auth.userProfile$.next({ username: 'analyst' });
      expect(service.session()).toBeNull();
    });

    it('reportPosition stores the own site id', async () => {
      const result = service.reportPosition('denied');
      http.expectOne('/api/v1/demo/position').flush({ lat: 51.55, lon: -0.15, source: 'ip', emplacementId: 'emp-9' });
      await result;
      expect(loadGuestSession()?.ownEmplacementId).toBe('emp-9');
    });

    it('an ended session clears the stored one', async () => {
      const result = start();
      http.expectOne('/api/v1/demo/session').flush(sessionBody);
      await result;
      expect(loadGuestSession()).not.toBeNull();
      guestEnded.next();
      expect(loadGuestSession()).toBeNull();
    });
  });

  describe('sessionEnded', () => {
    async function startedSession(): Promise<void> {
      const result = start();
      http.expectOne('/api/v1/demo/session').flush(sessionBody);
      await result;
    }

    it('turns true when the session is lost while a guest session is held', async () => {
      await startedSession();
      sessionLost.next();
      expect(service.sessionEnded()).toBe(true);
    });

    it('turns true when the gateway ends the guest on the entity stream', async () => {
      await startedSession();
      guestEnded.next();
      expect(service.sessionEnded()).toBe(true);
    });

    it('stays false when a session this service did not start is lost', () => {
      sessionLost.next();
      expect(service.sessionEnded()).toBe(false);
    });
  });

  it('a new session forgets the last one: welcome card, position, site and focus', async () => {
    service.welcomeDismissed.set(true);
    service.welcomeShown.set(true);
    service.ownPosition.set({ lat: 1, lon: 2, source: 'browser' });
    service.ownEmplacementId.set('old-site');
    service.focusBounds.set({ west: 1, south: 2, east: 3, north: 4 });

    const result = start();
    http.expectOne('/api/v1/demo/session').flush(sessionBody);
    await result;

    expect(service.welcomeDismissed()).toBe(false);
    expect(service.welcomeShown()).toBe(false);
    expect(service.ownPosition()).toBeNull();
    expect(service.ownEmplacementId()).toBeNull();
    expect(service.focusBounds()).toBeNull();
  });

  it('starts with the welcome card undismissed and no focus bounds', () => {
    expect(service.welcomeDismissed()).toBe(false);
    expect(service.welcomeShown()).toBe(false);
    expect(service.focusBounds()).toBeNull();
    expect(service.ownEmplacementId()).toBeNull();
    expect(service.ownPosition()).toBeNull();
  });
});

describe('DemoService with the real auth and entity stream', () => {
  const guestToken = [
    btoa(JSON.stringify({ alg: 'RS256' })),
    btoa(JSON.stringify({ preferred_username: 'guest-1a2b3c4d', realm_access: { roles: ['sentinel-guest'] } })),
    'sig',
  ].join('.');
  const originalWebSocket = (globalThis as any).WebSocket;
  let sockets: Array<{ url: string; onclose: ((e: CloseEvent) => void) | null; onopen: (() => void) | null }>;

  beforeEach(() => {
    jest.useFakeTimers();
    sockets = [];
    (globalThis as any).WebSocket = class {
      static readonly OPEN = 1;
      readyState = 0;
      onopen: (() => void) | null = null;
      onclose: ((e: CloseEvent) => void) | null = null;
      constructor(public url: string) {
        sockets.push(this);
      }
      close(): void {}
      send(): void {}
    };
    TestBed.configureTestingModule({ providers: [provideHttpClient(), provideHttpClientTesting()] });
  });
  afterEach(() => {
    TestBed.inject(WebSocketService).disconnect();
    TestBed.inject(AuthService).ngOnDestroy();
    (globalThis as any).WebSocket = originalWebSocket;
    jest.useRealTimers();
  });

  async function startGuest(): Promise<void> {
    const http = TestBed.inject(HttpTestingController);
    const result = TestBed.inject(DemoService).startSession();
    http.expectOne('/api/v1/demo/session').flush({
      accessToken: guestToken,
      refreshToken: 'rt',
      expiresIn: 300,
      guestId: '1a2b3c4d',
      expiresAt: '2026-10-22T12:00:00Z',
      visitorsFeedId: 'feed',
    });
    await expect(result).resolves.toBe('ok');
  }

  it('opens the entity stream with the guest token right after startSession, past a pending backoff', async () => {
    const ws = TestBed.inject(WebSocketService);
    ws.connect(); // the app connects before any token exists
    sockets[0].onclose?.({ code: 1006, reason: '' } as CloseEvent); // refused; backoff pending
    expect(sockets.length).toBe(1);
    expect(sockets[0].url).not.toContain('token=');

    await startGuest();

    expect(sockets.length).toBe(2);
    expect(sockets[1].url).toMatch(/\?token=/);
    expect(decodeURIComponent(sockets[1].url.split('token=')[1])).toBe(guestToken);
  });

  it('opens the stream again for a new session after the gateway ended the guest with 1008', async () => {
    const ws = TestBed.inject(WebSocketService);
    await startGuest();
    expect(sockets.length).toBe(1);
    sockets[0].onclose?.({ code: 1008, reason: 'expired' } as CloseEvent);
    jest.advanceTimersByTime(60_000);
    expect(sockets.length).toBe(1); // stopped, no reconnect loop
    expect(TestBed.inject(DemoService).sessionEnded()).toBe(true);

    await startGuest();

    expect(sockets.length).toBe(2);
    expect(TestBed.inject(DemoService).sessionEnded()).toBe(false);
    expect(ws).toBeTruthy();
  });
});
