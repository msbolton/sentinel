import { TestBed } from '@angular/core/testing';
import { Router } from '@angular/router';
import { authGuard, roleGuard } from './auth.guard';
import { AuthService } from '../services/auth.service';
import {
  ActivatedRouteSnapshot,
  RouterStateSnapshot,
} from '@angular/router';

describe('authGuard', () => {
  let authService: jest.Mocked<Pick<AuthService, 'isAuthenticated' | 'login' | 'getUserRoles'>>;
  let router: jest.Mocked<Pick<Router, 'navigate'>>;

  const mockRoute = {} as ActivatedRouteSnapshot;
  const mockState = {} as RouterStateSnapshot;

  beforeEach(() => {
    TestBed.configureTestingModule({
      providers: [
        {
          provide: AuthService,
          useValue: {
            isAuthenticated: jest.fn(),
            login: jest.fn(),
            getUserRoles: jest.fn(),
          },
        },
        {
          provide: Router,
          useValue: {
            navigate: jest.fn(),
          },
        },
      ],
    });

    authService = TestBed.inject(AuthService) as any;
    router = TestBed.inject(Router) as any;
  });

  describe('authGuard', () => {
    it('should return true when user is authenticated', () => {
      (authService.isAuthenticated as jest.Mock).mockReturnValue(true);

      const result = TestBed.runInInjectionContext(() =>
        authGuard(mockRoute, mockState),
      );

      expect(result).toBe(true);
    });

    it('should call login and return false when user is not authenticated', () => {
      (authService.isAuthenticated as jest.Mock).mockReturnValue(false);

      const result = TestBed.runInInjectionContext(() =>
        authGuard(mockRoute, mockState),
      );

      expect(result).toBe(false);
      expect(authService.login).toHaveBeenCalled();
    });

    it('should not call login when user is authenticated', () => {
      (authService.isAuthenticated as jest.Mock).mockReturnValue(true);

      TestBed.runInInjectionContext(() => authGuard(mockRoute, mockState));

      expect(authService.login).not.toHaveBeenCalled();
    });
  });

  describe('roleGuard', () => {
    it('should return true when user is authenticated and has a required role', () => {
      (authService.isAuthenticated as jest.Mock).mockReturnValue(true);
      (authService.getUserRoles as jest.Mock).mockReturnValue([
        'analyst',
        'viewer',
      ]);

      const guard = roleGuard('analyst', 'admin');
      const result = TestBed.runInInjectionContext(() =>
        guard(mockRoute, mockState),
      );

      expect(result).toBe(true);
    });

    it('should redirect to /map and return false when user lacks required roles', () => {
      (authService.isAuthenticated as jest.Mock).mockReturnValue(true);
      (authService.getUserRoles as jest.Mock).mockReturnValue(['viewer']);

      const guard = roleGuard('admin', 'operator');
      const result = TestBed.runInInjectionContext(() =>
        guard(mockRoute, mockState),
      );

      expect(result).toBe(false);
      expect(router.navigate).toHaveBeenCalledWith(['/map']);
    });

    it('should call login and return false when user is not authenticated', () => {
      (authService.isAuthenticated as jest.Mock).mockReturnValue(false);

      const guard = roleGuard('admin');
      const result = TestBed.runInInjectionContext(() =>
        guard(mockRoute, mockState),
      );

      expect(result).toBe(false);
      expect(authService.login).toHaveBeenCalled();
    });

    it('should not navigate to /map when user is not authenticated (login takes priority)', () => {
      (authService.isAuthenticated as jest.Mock).mockReturnValue(false);

      const guard = roleGuard('admin');
      TestBed.runInInjectionContext(() => guard(mockRoute, mockState));

      expect(router.navigate).not.toHaveBeenCalled();
    });

    it('should return true when user has at least one of multiple required roles', () => {
      (authService.isAuthenticated as jest.Mock).mockReturnValue(true);
      (authService.getUserRoles as jest.Mock).mockReturnValue(['operator']);

      const guard = roleGuard('analyst', 'operator', 'admin');
      const result = TestBed.runInInjectionContext(() =>
        guard(mockRoute, mockState),
      );

      expect(result).toBe(true);
    });

    it('should deny access when user has no roles at all', () => {
      (authService.isAuthenticated as jest.Mock).mockReturnValue(true);
      (authService.getUserRoles as jest.Mock).mockReturnValue([]);

      const guard = roleGuard('admin');
      const result = TestBed.runInInjectionContext(() =>
        guard(mockRoute, mockState),
      );

      expect(result).toBe(false);
      expect(router.navigate).toHaveBeenCalledWith(['/map']);
    });
  });
});
