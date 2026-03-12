// apps/web/src/app/core/services/auth.service.spec.ts

import { TestBed } from '@angular/core/testing';
import { AuthService } from './auth.service';

describe('AuthService', () => {
  let service: AuthService;

  beforeEach(() => {
    TestBed.configureTestingModule({});
    service = TestBed.inject(AuthService);
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
