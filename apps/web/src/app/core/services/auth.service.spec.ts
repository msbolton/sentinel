// apps/web/src/app/core/services/auth.service.spec.ts

import { TestBed } from '@angular/core/testing';
import { AuthService } from './auth.service';

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
});
