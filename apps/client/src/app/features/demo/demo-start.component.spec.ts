import { ComponentFixture, TestBed } from '@angular/core/testing';
import { Router, provideRouter } from '@angular/router';
import { signal } from '@angular/core';
import { DemoStartComponent } from './demo-start.component';
import { DemoService } from '../../core/services/demo.service';
import { AuthService } from '../../core/services/auth.service';
import { provideHttpClient } from '@angular/common/http';
import { HttpTestingController, provideHttpClientTesting } from '@angular/common/http/testing';
import { saveGuestSession } from '../../core/services/guest-session-store';

const mockKeycloakInit = jest.fn();
jest.mock('keycloak-js', () => ({
  __esModule: true,
  default: jest.fn().mockImplementation(() => ({ init: mockKeycloakInit })),
}));

describe('DemoStartComponent', () => {
  let fixture: ComponentFixture<DemoStartComponent>;
  let router: Router;
  let navigate: jest.SpyInstance;
  let demo: {
    startSession: jest.Mock;
    sessionEnded: ReturnType<typeof signal<boolean>>;
    errorMessage: ReturnType<typeof signal<string | null>>;
  };
  let auth: { isAuthenticated: jest.Mock; isGuest: jest.Mock; ready: Promise<void> };

  async function setup(): Promise<void> {
    demo = {
      startSession: jest.fn().mockResolvedValue('ok'),
      sessionEnded: signal(false),
      errorMessage: signal<string | null>(null),
    };
    auth = {
      isAuthenticated: jest.fn().mockReturnValue(false),
      isGuest: jest.fn().mockReturnValue(false),
      ready: Promise.resolve(),
    };
    await TestBed.configureTestingModule({
      imports: [DemoStartComponent],
      providers: [
        provideRouter([]),
        { provide: DemoService, useValue: demo },
        { provide: AuthService, useValue: auth },
      ],
    }).compileComponents();
    router = TestBed.inject(Router);
    navigate = jest.spyOn(router, 'navigateByUrl').mockResolvedValue(true);
  }

  /** ngOnInit awaits auth.ready, which whenStable does not track without zone.js. */
  const settle = async () => {
    await fixture.whenStable();
    await new Promise((r) => setTimeout(r));
  };

  async function render(): Promise<HTMLElement> {
    fixture = TestBed.createComponent(DemoStartComponent);
    fixture.detectChanges();
    await settle();
    fixture.detectChanges();
    return fixture.nativeElement as HTMLElement;
  }

  beforeEach(setup);

  it('navigates to /map on ok', async () => {
    await render();
    expect(demo.startSession).toHaveBeenCalledTimes(1);
    expect(navigate).toHaveBeenCalledWith('/map', expect.anything());
  });

  it('shows the full copy on full and does not navigate', async () => {
    demo.startSession.mockResolvedValue('full');
    const el = await render();
    expect(el.textContent).toContain('The demo is full right now — try again in a few minutes.');
    expect(navigate).not.toHaveBeenCalled();
  });

  it('shows the service error message on error and offers a retry', async () => {
    demo.startSession.mockResolvedValue('error');
    demo.errorMessage.set('The demo is starting up — try again in a moment.');
    const el = await render();
    expect(el.textContent).toContain('The demo is starting up — try again in a moment.');
    expect(navigate).not.toHaveBeenCalled();

    demo.startSession.mockResolvedValue('ok');
    (el.querySelector('button.retry') as HTMLButtonElement).click();
    await settle();
    expect(demo.startSession).toHaveBeenCalledTimes(2);
    expect(navigate).toHaveBeenCalledWith('/map', expect.anything());
  });

  it('waits for auth init before deciding, so a restored real session is not replaced', async () => {
    let finishInit!: () => void;
    auth.ready = new Promise<void>((r) => (finishInit = r));
    fixture = TestBed.createComponent(DemoStartComponent);
    fixture.detectChanges();
    await Promise.resolve();
    expect(demo.startSession).not.toHaveBeenCalled();
    expect(navigate).not.toHaveBeenCalled();

    auth.isAuthenticated.mockReturnValue(true); // Keycloak restored a real user
    finishInit();
    await settle();

    expect(demo.startSession).not.toHaveBeenCalled();
    expect(navigate).toHaveBeenCalledWith('/map', expect.anything());
  });

  it('stops waiting for auth init after 8 s and carries on as not signed in', async () => {
    jest.useFakeTimers();
    try {
      auth.ready = new Promise<void>(() => undefined); // Keycloak init hangs
      fixture = TestBed.createComponent(DemoStartComponent);
      fixture.detectChanges();

      await jest.advanceTimersByTimeAsync(7_999);
      expect(demo.startSession).not.toHaveBeenCalled();

      await jest.advanceTimersByTimeAsync(1);
      expect(demo.startSession).toHaveBeenCalledTimes(1);
      expect(navigate).toHaveBeenCalledWith('/map', expect.anything());
    } finally {
      jest.useRealTimers();
    }
  });

  it('leaves no timer behind when auth init answers in time', async () => {
    jest.useFakeTimers();
    try {
      fixture = TestBed.createComponent(DemoStartComponent);
      fixture.detectChanges();
      await jest.advanceTimersByTimeAsync(0);
      expect(jest.getTimerCount()).toBe(0);
    } finally {
      jest.useRealTimers();
    }
  });

  it('reuses a signed-in guest session and goes straight to /map', async () => {
    auth.isAuthenticated.mockReturnValue(true);
    auth.isGuest.mockReturnValue(true);
    await render();
    expect(demo.startSession).not.toHaveBeenCalled();
    expect(navigate).toHaveBeenCalledWith('/map', expect.anything());
  });

  it('starts a fresh session when the signed-in guest session has ended', async () => {
    auth.isAuthenticated.mockReturnValue(true);
    auth.isGuest.mockReturnValue(true);
    demo.sessionEnded.set(true);
    await render();
    expect(demo.startSession).toHaveBeenCalledTimes(1);
  });

  it('never replaces a real user with a guest', async () => {
    auth.isAuthenticated.mockReturnValue(true);
    auth.isGuest.mockReturnValue(false);
    await render();
    expect(demo.startSession).not.toHaveBeenCalled();
    expect(navigate).toHaveBeenCalledWith('/map', expect.anything());
  });
});

describe('DemoStartComponent with the stored guest session (a reload, a second tab)', () => {
  const guestJwt = [
    btoa(JSON.stringify({ alg: 'RS256' })),
    btoa(JSON.stringify({ preferred_username: 'guest-1a2b3c4d', realm_access: { roles: ['sentinel-viewer', 'sentinel-guest'] } })),
    'sig',
  ].join('.');
  let originalFetch: typeof fetch | undefined;
  let navigate: jest.SpyInstance;
  let warnSpy: jest.SpyInstance;

  beforeEach(async () => {
    localStorage.clear();
    saveGuestSession({
      refreshToken: 'stored',
      guestId: '1a2b3c4d',
      expiresAt: new Date(Date.now() + 30 * 60_000).toISOString(),
      visitorsFeedId: 'visitors',
    });
    mockKeycloakInit.mockReset().mockResolvedValue(false);
    originalFetch = globalThis.fetch;
    globalThis.fetch = jest.fn();
    warnSpy = jest.spyOn(console, 'warn').mockImplementation();
    await TestBed.configureTestingModule({
      imports: [DemoStartComponent],
      providers: [provideRouter([]), provideHttpClient(), provideHttpClientTesting()],
    }).compileComponents();
    navigate = jest.spyOn(TestBed.inject(Router), 'navigateByUrl').mockResolvedValue(true);
  });
  afterEach(() => {
    TestBed.inject(AuthService).ngOnDestroy();
    localStorage.clear();
    warnSpy.mockRestore();
    if (originalFetch) globalThis.fetch = originalFetch;
    else delete (globalThis as any).fetch;
  });

  async function open(): Promise<void> {
    void TestBed.inject(AuthService).init(); // as the app shell does
    const fixture = TestBed.createComponent(DemoStartComponent);
    fixture.detectChanges();
    await fixture.whenStable();
    for (let i = 0; i < 5; i++) await new Promise((r) => setTimeout(r));
  }

  it('carries on as the stored guest instead of starting another', async () => {
    (globalThis.fetch as jest.Mock).mockResolvedValue({
      ok: true,
      json: () => Promise.resolve({ access_token: guestJwt, refresh_token: 'next', expires_in: 300 }),
    } as Response);

    await open();

    TestBed.inject(HttpTestingController).expectNone('/api/v1/demo/session');
    expect(navigate).toHaveBeenCalledWith('/map', expect.anything());
    expect(TestBed.inject(DemoService).session()).toEqual(expect.objectContaining({ guestId: '1a2b3c4d', visitorsFeedId: 'visitors' }));
  });

  it('starts a new guest when the stored one can no longer be resumed', async () => {
    (globalThis.fetch as jest.Mock).mockResolvedValue({ ok: false, json: () => Promise.resolve({}) } as Response);

    await open();

    TestBed.inject(HttpTestingController).expectOne('/api/v1/demo/session');
  });
});
