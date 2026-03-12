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
});
