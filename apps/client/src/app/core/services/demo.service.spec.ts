import { TestBed } from '@angular/core/testing';
import { provideHttpClient } from '@angular/common/http';
import { HttpTestingController, provideHttpClientTesting } from '@angular/common/http/testing';
import { Subject } from 'rxjs';
import { DemoService } from './demo.service';
import { AuthService } from './auth.service';
import { WebSocketService } from './websocket.service';

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
  let auth: { adoptTokens: jest.Mock; sessionLost$: Subject<void> };

  beforeEach(() => {
    sessionLost = new Subject<void>();
    guestEnded = new Subject<void>();
    auth = { adoptTokens: jest.fn(), sessionLost$: sessionLost };
    TestBed.configureTestingModule({
      providers: [
        provideHttpClient(),
        provideHttpClientTesting(),
        { provide: AuthService, useValue: auth },
        { provide: WebSocketService, useValue: { guestSessionEnded$: guestEnded } },
      ],
    });
    service = TestBed.inject(DemoService);
    http = TestBed.inject(HttpTestingController);
  });
  afterEach(() => http.verify());

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
  });

  it('startSession returns full on 503 {error:"full"}', async () => {
    const result = start();
    http.expectOne('/api/v1/demo/session').flush({ error: 'full' }, { status: 503, statusText: 'Service Unavailable' });

    await expect(result).resolves.toBe('full');
    expect(auth.adoptTokens).not.toHaveBeenCalled();
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

  it.each([429, 500, 0])('startSession returns error with a generic message on status %s', async (status) => {
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

    it('posts a browser position as {lat,lon}', async () => {
      const result = service.reportPosition({ lat: 51.5074, lon: -0.1278 });
      const req = http.expectOne('/api/v1/demo/position');
      expect(req.request.body).toEqual({ lat: 51.5074, lon: -0.1278 });
      req.flush({ lat: 51.5074, lon: -0.1278, source: 'browser', emplacementId: 'emp-2' });

      await expect(result).resolves.toEqual({ lat: 51.5074, lon: -0.1278, source: 'browser' });
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

  it('starts with the welcome card undismissed and no focus bounds', () => {
    expect(service.welcomeDismissed()).toBe(false);
    expect(service.focusBounds()).toBeNull();
    expect(service.ownEmplacementId()).toBeNull();
    expect(service.ownPosition()).toBeNull();
  });
});
