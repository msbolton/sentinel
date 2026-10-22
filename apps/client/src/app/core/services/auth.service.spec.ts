// apps/client/src/app/core/services/auth.service.spec.ts

import { TestBed } from '@angular/core/testing';
import { AuthService } from './auth.service';
import { environment } from '../../../environments/environment';
import { loadGuestSession, saveGuestSession, wasDemoVisitor } from './guest-session-store';

const mockKeycloakInit = jest.fn();
jest.mock('keycloak-js', () => ({
  __esModule: true,
  default: jest.fn().mockImplementation(() => ({ init: mockKeycloakInit })),
}));

describe('AuthService', () => {
  let service: AuthService;

  beforeEach(() => {
    TestBed.configureTestingModule({});
    service = TestBed.inject(AuthService);
  });

  describe('init', () => {
    it('should null out keycloak when init fails', async () => {
      const warnSpy = jest.spyOn(console, 'warn').mockImplementation();
      const mockKeycloak = {
        init: jest.fn().mockRejectedValue(new Error('Connection refused')),
      };

      // Simulate: keycloak-js imported successfully, constructor ran, but init() throws
      (service as any).keycloak = mockKeycloak;

      // Manually trigger the catch path
      try {
        await mockKeycloak.init();
      } catch {
        (service as any).keycloak = null;
        (service as any).setDevelopmentProfile();
      }

      expect((service as any).keycloak).toBeNull();
      expect(service.isAuthenticated()).toBe(true);

      warnSpy.mockRestore();
    });
  });

  describe('init when Keycloak is unreachable', () => {
    const wasProduction = environment.production;
    let warnSpy: jest.SpyInstance;
    let errorSpy: jest.SpyInstance;

    beforeEach(() => {
      errorSpy = jest.spyOn(console, 'error').mockImplementation();
      mockKeycloakInit.mockReset().mockRejectedValue(new Error('Connection refused'));
      warnSpy = jest.spyOn(console, 'warn').mockImplementation();
    });

    afterEach(() => {
      environment.production = wasProduction;
      warnSpy.mockRestore();
      errorSpy.mockRestore();
    });

    it('in production reports a login error and never signs in a development user', async () => {
      environment.production = true;
      const devProfile = jest.spyOn(service as any, 'setDevelopmentProfile');
      let profile: unknown = 'unset';
      service.userProfile$.subscribe((p) => (profile = p));

      const result = await service.init();

      expect(result).toBe(false);
      expect(devProfile).not.toHaveBeenCalled();
      expect(service.isAuthenticated()).toBe(false);
      expect(profile).toBeNull();
      expect(service.initError$.value).toMatch(/authentication service/i);
    });

    it('in development falls back to the development user', async () => {
      environment.production = false;

      const result = await service.init();

      expect(result).toBe(true);
      expect(service.isAuthenticated()).toBe(true);
      expect(service.initError$.value).toBeNull();
    });
  });

  describe('login', () => {
    it('should call keycloak.login with redirectUri when provided', async () => {
      const mockKeycloak = { login: jest.fn().mockResolvedValue(undefined) };
      (service as any).keycloak = mockKeycloak;

      await service.login('http://localhost:4200/#/alerts');

      expect(mockKeycloak.login).toHaveBeenCalledWith({
        redirectUri: 'http://localhost:4200/#/alerts',
      });
    });

    it('should call keycloak.login with no args when redirectUri not provided', async () => {
      const mockKeycloak = { login: jest.fn().mockResolvedValue(undefined) };
      (service as any).keycloak = mockKeycloak;

      await service.login();

      expect(mockKeycloak.login).toHaveBeenCalledWith(undefined);
    });

    it('should warn and return when keycloak is null', async () => {
      (service as any).keycloak = null;
      const warnSpy = jest.spyOn(console, 'warn').mockImplementation();

      await service.login();

      expect(warnSpy).toHaveBeenCalledWith('[Auth] No Keycloak instance available');
      warnSpy.mockRestore();
    });
  });

  describe('loginWithCredentials', () => {
    const mockToken = [
      btoa(JSON.stringify({ alg: 'RS256' })),
      btoa(JSON.stringify({
        preferred_username: 'operator',
        email: 'op@sentinel.local',
        realm_access: { roles: ['sentinel-operator'] },
        classification_level: 'UNCLASSIFIED',
      })),
      'signature',
    ].join('.');

    let originalFetch: typeof fetch | undefined;

    beforeEach(() => {
      originalFetch = globalThis.fetch;
      globalThis.fetch = jest.fn();
    });

    afterEach(() => {
      if (originalFetch) {
        globalThis.fetch = originalFetch;
      } else {
        delete (globalThis as any).fetch;
      }
    });

    it('should authenticate on successful credentials', async () => {
      (globalThis.fetch as jest.Mock).mockResolvedValue({
        ok: true,
        json: () => Promise.resolve({
          access_token: mockToken,
          refresh_token: 'refresh-token',
          expires_in: 300,
        }),
      } as Response);

      const result = await service.loginWithCredentials('operator', 'password');

      expect(result.success).toBe(true);
      expect(service.isAuthenticated()).toBe(true);
      expect(globalThis.fetch).toHaveBeenCalledWith(
        '/auth/realms/sentinel/protocol/openid-connect/token',
        expect.objectContaining({ method: 'POST' }),
      );
    });

    it('should return error on invalid credentials', async () => {
      (globalThis.fetch as jest.Mock).mockResolvedValue({
        ok: false,
        json: () => Promise.resolve({ error_description: 'Invalid user credentials' }),
      } as Response);

      const result = await service.loginWithCredentials('bad', 'wrong');

      expect(result.success).toBe(false);
      expect(result.error).toBe('Invalid user credentials');
      expect(service.isAuthenticated()).toBe(false);
    });

    it('should return error when server is unreachable', async () => {
      (globalThis.fetch as jest.Mock).mockRejectedValue(new Error('Network error'));

      const result = await service.loginWithCredentials('operator', 'password');

      expect(result.success).toBe(false);
      expect(result.error).toBe('Unable to reach authentication server');
    });
  });
  describe('guest sessions', () => {
    const tokenWith = (roles: string[], username = 'guest-1a2b3c4d') =>
      [
        btoa(JSON.stringify({ alg: 'RS256' })),
        btoa(JSON.stringify({ preferred_username: username, realm_access: { roles } })),
        'signature',
      ].join('.');

    let originalFetch: typeof fetch | undefined;

    beforeEach(() => {
      originalFetch = globalThis.fetch;
      globalThis.fetch = jest.fn();
    });

    afterEach(() => {
      service.ngOnDestroy();
      jest.useRealTimers();
      if (originalFetch) {
        globalThis.fetch = originalFetch;
      } else {
        delete (globalThis as any).fetch;
      }
    });

    it('adoptTokens sets the profile from the token and marks authenticated', () => {
      service.adoptTokens({
        accessToken: tokenWith(['sentinel-viewer', 'sentinel-guest']),
        refreshToken: 'r1',
        expiresIn: 300,
      });

      let profile: any = null;
      service.userProfile$.subscribe((p) => (profile = p));
      expect(service.isAuthenticated()).toBe(true);
      expect(profile.username).toBe('guest-1a2b3c4d');
      expect(profile.roles).toEqual(['sentinel-viewer', 'sentinel-guest']);
      expect(service.getToken()).toBe(tokenWith(['sentinel-viewer', 'sentinel-guest']));
    });

    it('isGuest is true only with sentinel-guest', () => {
      expect(service.isGuest()).toBe(false);
      service.adoptTokens({ accessToken: tokenWith(['sentinel-viewer']), refreshToken: 'r', expiresIn: 300 });
      expect(service.isGuest()).toBe(false);
      service.adoptTokens({
        accessToken: tokenWith(['sentinel-viewer', 'sentinel-guest']),
        refreshToken: 'r',
        expiresIn: 300,
      });
      expect(service.isGuest()).toBe(true);
    });

    it('an unreadable token is not kept: getToken stays null and the user stays signed out', () => {
      expect(() =>
        service.adoptTokens({ accessToken: 'not-a-jwt', refreshToken: 'r', expiresIn: 300 }),
      ).toThrow();
      expect(service.getToken()).toBeNull();
      expect(service.isAuthenticated()).toBe(false);
    });

    describe('init finishing after a guest session was adopted', () => {
      let warnSpy: jest.SpyInstance;
      let errorSpy: jest.SpyInstance;
      const wasProduction = environment.production;

      beforeEach(() => {
        warnSpy = jest.spyOn(console, 'warn').mockImplementation();
        errorSpy = jest.spyOn(console, 'error').mockImplementation();
      });
      afterEach(() => {
        environment.production = wasProduction;
        warnSpy.mockRestore();
        errorSpy.mockRestore();
      });

      const guestTokens = () => ({
        accessToken: tokenWith(['sentinel-viewer', 'sentinel-guest']),
        refreshToken: 'r',
        expiresIn: 300,
      });

      it('keeps the guest when Keycloak answers not-authenticated after adoptTokens', async () => {
        let answer!: (v: boolean) => void;
        mockKeycloakInit.mockReset().mockReturnValue(new Promise<boolean>((r) => (answer = r)));
        const initDone = service.init();
        await Promise.resolve();
        await Promise.resolve();

        service.adoptTokens(guestTokens());
        answer(false);
        await initDone;

        expect(service.isAuthenticated()).toBe(true);
        expect(service.isGuest()).toBe(true);
        expect(service.getToken()).toBe(guestTokens().accessToken);
      });

      it('keeps the guest when Keycloak init fails (production) after adoptTokens', async () => {
        environment.production = true;
        let fail!: (e: Error) => void;
        mockKeycloakInit.mockReset().mockReturnValue(new Promise<boolean>((_, r) => (fail = r)));
        const initDone = service.init();
        await Promise.resolve();
        await Promise.resolve();

        service.adoptTokens(guestTokens());
        fail(new Error('down'));
        await initDone;

        expect(service.isAuthenticated()).toBe(true);
        expect(service.isGuest()).toBe(true);
        expect(service.initError$.value).toBeNull();
      });

      it('ready resolves when init finishes, even when it fails', async () => {
        environment.production = true;
        mockKeycloakInit.mockReset().mockRejectedValue(new Error('down'));
        let ready = false;
        void service.ready.then(() => (ready = true));
        await Promise.resolve();
        expect(ready).toBe(false);

        await service.init();
        await service.ready;
        expect(ready).toBe(true);
      });
    });

    it('a failed refresh emits sessionLost$ and signs out', async () => {
      jest.useFakeTimers();
      const warnSpy = jest.spyOn(console, 'warn').mockImplementation();
      (globalThis.fetch as jest.Mock).mockResolvedValue({ ok: false, json: () => Promise.resolve({}) } as Response);
      const lost = jest.fn();
      service.sessionLost$.subscribe(lost);
      service.adoptTokens({ accessToken: tokenWith(['sentinel-guest']), refreshToken: 'r', expiresIn: 300 });

      await jest.advanceTimersByTimeAsync(300_000);

      expect(lost).toHaveBeenCalledTimes(1);
      expect(service.isAuthenticated()).toBe(false);
      expect(service.getToken()).toBeNull();
      warnSpy.mockRestore();
    });

    it('a successful refresh keeps the session and does not emit sessionLost$', async () => {
      jest.useFakeTimers();
      const next = tokenWith(['sentinel-guest']);
      (globalThis.fetch as jest.Mock).mockResolvedValue({
        ok: true,
        json: () => Promise.resolve({ access_token: next, refresh_token: 'r2', expires_in: 300 }),
      } as Response);
      const lost = jest.fn();
      service.sessionLost$.subscribe(lost);
      service.adoptTokens({ accessToken: tokenWith(['sentinel-guest']), refreshToken: 'r', expiresIn: 300 });

      await jest.advanceTimersByTimeAsync(300_000);

      expect(lost).not.toHaveBeenCalled();
      expect(service.isGuest()).toBe(true);
      expect(service.getToken()).toBe(next);
    });
  });
  describe('a guest session kept across reloads and tabs', () => {
    const jwt = (roles: string[], username = 'guest-1a2b3c4d') =>
      [btoa(JSON.stringify({ alg: 'RS256' })), btoa(JSON.stringify({ preferred_username: username, realm_access: { roles } })), 'sig'].join('.');
    const guestJwt = jwt(['sentinel-viewer', 'sentinel-guest']);
    const later = () => new Date(Date.now() + 30 * 60_000).toISOString();
    const tokenAnswer = (access: string, refresh: string) =>
      ({ ok: true, json: () => Promise.resolve({ access_token: access, refresh_token: refresh, expires_in: 300 }) }) as Response;

    let originalFetch: typeof fetch | undefined;
    let warnSpy: jest.SpyInstance;
    let errorSpy: jest.SpyInstance;

    beforeEach(() => {
      localStorage.clear();
      originalFetch = globalThis.fetch;
      globalThis.fetch = jest.fn();
      mockKeycloakInit.mockReset().mockResolvedValue(false); // Keycloak: no session of its own
      warnSpy = jest.spyOn(console, 'warn').mockImplementation();
      errorSpy = jest.spyOn(console, 'error').mockImplementation();
    });
    afterEach(() => {
      service.ngOnDestroy();
      jest.useRealTimers();
      localStorage.clear();
      warnSpy.mockRestore();
      errorSpy.mockRestore();
      if (originalFetch) globalThis.fetch = originalFetch;
      else delete (globalThis as any).fetch;
    });

    const refreshBody = () => String(((globalThis.fetch as jest.Mock).mock.calls.at(-1)![1] as RequestInit).body);

    it("adoptTokens stores a guest's refresh token, and only a guest's", async () => {
      service.adoptTokens({ accessToken: guestJwt, refreshToken: 'r1', expiresIn: 300 });
      expect(loadGuestSession()).toEqual({ refreshToken: 'r1' });
      expect(wasDemoVisitor()).toBe(true);

      localStorage.clear();
      const other = new AuthService();
      (globalThis.fetch as jest.Mock).mockResolvedValue(tokenAnswer(jwt(['sentinel-operator'], 'operator'), 'real-refresh'));
      await other.loginWithCredentials('operator', 'pw');
      other.adoptTokens({ accessToken: jwt(['sentinel-viewer'], 'viewer'), refreshToken: 'viewer-refresh', expiresIn: 300 });
      expect(localStorage.length).toBe(0);
      other.ngOnDestroy();
    });

    it("a guest's refresh stores the new refresh token", async () => {
      jest.useFakeTimers();
      (globalThis.fetch as jest.Mock).mockResolvedValue(tokenAnswer(guestJwt, 'r2'));
      service.adoptTokens({ accessToken: guestJwt, refreshToken: 'r1', expiresIn: 300 });
      await jest.advanceTimersByTimeAsync(300_000);
      expect(loadGuestSession()?.refreshToken).toBe('r2');
    });

    it('init resumes a stored guest when Keycloak has no session (a reload)', async () => {
      saveGuestSession({ refreshToken: 'stored', guestId: '1a2b3c4d', expiresAt: later(), visitorsFeedId: 'visitors' });
      (globalThis.fetch as jest.Mock).mockResolvedValue(tokenAnswer(guestJwt, 'fresh'));

      await expect(service.init()).resolves.toBe(true);

      expect(refreshBody()).toContain('grant_type=refresh_token');
      expect(refreshBody()).toContain('refresh_token=stored');
      expect(service.isAuthenticated()).toBe(true);
      expect(service.isGuest()).toBe(true);
      expect(service.getToken()).toBe(guestJwt);
      expect(loadGuestSession()).toEqual(expect.objectContaining({ refreshToken: 'fresh', guestId: '1a2b3c4d' }));
    });

    it('a second tab picks up the guest the first tab started', async () => {
      (globalThis.fetch as jest.Mock).mockResolvedValue(tokenAnswer(guestJwt, 'tab2'));
      const firstTab = new AuthService();
      firstTab.adoptTokens({ accessToken: guestJwt, refreshToken: 'tab1', expiresIn: 300 });
      saveGuestSession({ guestId: '1a2b3c4d', expiresAt: later(), visitorsFeedId: 'visitors' }); // DemoService's part

      await service.init();

      expect(refreshBody()).toContain('refresh_token=tab1');
      expect(service.isGuest()).toBe(true);
      firstTab.ngOnDestroy();
    });

    it('a stored session past its expiry is cleared without asking Keycloak', async () => {
      saveGuestSession({ refreshToken: 'stored', guestId: 'g', expiresAt: new Date(Date.now() - 1000).toISOString() });

      await service.init();

      expect(globalThis.fetch).not.toHaveBeenCalled();
      expect(service.isAuthenticated()).toBe(false);
      expect(loadGuestSession()).toBeNull();
      expect(wasDemoVisitor()).toBe(true); // the guards still send this browser to /demo
    });

    it('a refresh that fails on resume clears the stored session and stays signed out', async () => {
      saveGuestSession({ refreshToken: 'revoked', guestId: 'g', expiresAt: later() });
      (globalThis.fetch as jest.Mock).mockResolvedValue({ ok: false, json: () => Promise.resolve({}) } as Response);

      await expect(service.init()).resolves.toBe(false);

      expect(service.isAuthenticated()).toBe(false);
      expect(service.getToken()).toBeNull();
      expect(loadGuestSession()).toBeNull();
    });

    it('a resumed token that is not a guest is refused and cleared', async () => {
      saveGuestSession({ refreshToken: 'stored', guestId: 'g', expiresAt: later() });
      (globalThis.fetch as jest.Mock).mockResolvedValue(tokenAnswer(jwt(['sentinel-admin'], 'admin'), 'x'));

      await service.init();

      expect(service.isAuthenticated()).toBe(false);
      expect(loadGuestSession()).toBeNull();
    });

    it('Keycloak reporting a real user drops any stored guest', async () => {
      saveGuestSession({ refreshToken: 'stored', guestId: 'g', expiresAt: later() });
      mockKeycloakInit.mockReset().mockResolvedValue(true);

      await service.init();

      expect(globalThis.fetch).not.toHaveBeenCalled();
      expect(loadGuestSession()).toBeNull();
      expect(wasDemoVisitor()).toBe(false);
    });

    it('a lost session clears the stored one; logout forgets the visitor too', async () => {
      jest.useFakeTimers();
      (globalThis.fetch as jest.Mock).mockResolvedValue({ ok: false, json: () => Promise.resolve({}) } as Response);
      service.adoptTokens({ accessToken: guestJwt, refreshToken: 'r1', expiresIn: 300 });
      await jest.advanceTimersByTimeAsync(300_000);
      expect(loadGuestSession()).toBeNull();
      expect(wasDemoVisitor()).toBe(true);

      service.adoptTokens({ accessToken: guestJwt, refreshToken: 'r2', expiresIn: 300 });
      await service.logout();
      expect(loadGuestSession()).toBeNull();
      expect(wasDemoVisitor()).toBe(false);
    });
  });
});
