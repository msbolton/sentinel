import { TestBed } from '@angular/core/testing';
import { Router, ActivatedRouteSnapshot, RouterStateSnapshot } from '@angular/router';
import { authGuard, roleGuard } from './auth.guard';
import { AuthService } from '../services/auth.service';

describe('authGuard', () => {
  let authService: { isAuthenticated: jest.Mock };
  let router: { navigate: jest.Mock };

  beforeEach(() => {
    authService = { isAuthenticated: jest.fn() };
    router = { navigate: jest.fn() };

    TestBed.configureTestingModule({
      providers: [
        { provide: AuthService, useValue: authService },
        { provide: Router, useValue: router },
      ],
    });
  });

  it('should allow access when authenticated', () => {
    authService.isAuthenticated.mockReturnValue(true);

    const result = TestBed.runInInjectionContext(() =>
      authGuard({} as ActivatedRouteSnapshot, { url: '/map' } as RouterStateSnapshot),
    );

    expect(result).toBe(true);
  });

  it('should redirect to /login with returnUrl when not authenticated', () => {
    authService.isAuthenticated.mockReturnValue(false);

    const result = TestBed.runInInjectionContext(() =>
      authGuard({} as ActivatedRouteSnapshot, { url: '/alerts' } as RouterStateSnapshot),
    );

    expect(result).toBe(false);
    expect(router.navigate).toHaveBeenCalledWith(['/login'], {
      queryParams: { returnUrl: '/alerts' },
    });
  });
});

describe('roleGuard', () => {
  let authService: { isAuthenticated: jest.Mock; getUserRoles: jest.Mock };
  let router: { navigate: jest.Mock };

  beforeEach(() => {
    authService = { isAuthenticated: jest.fn(), getUserRoles: jest.fn() };
    router = { navigate: jest.fn() };

    TestBed.configureTestingModule({
      providers: [
        { provide: AuthService, useValue: authService },
        { provide: Router, useValue: router },
      ],
    });
  });

  it('should redirect to /login with returnUrl when not authenticated', () => {
    authService.isAuthenticated.mockReturnValue(false);
    const guard = roleGuard('sentinel-admin');

    const result = TestBed.runInInjectionContext(() =>
      guard({} as ActivatedRouteSnapshot, { url: '/link-graph' } as RouterStateSnapshot),
    );

    expect(result).toBe(false);
    expect(router.navigate).toHaveBeenCalledWith(['/login'], {
      queryParams: { returnUrl: '/link-graph' },
    });
  });

  it('should redirect to /map when authenticated but missing required role', () => {
    authService.isAuthenticated.mockReturnValue(true);
    authService.getUserRoles.mockReturnValue(['sentinel-viewer']);
    const guard = roleGuard('sentinel-admin');

    const result = TestBed.runInInjectionContext(() =>
      guard({} as ActivatedRouteSnapshot, { url: '/link-graph' } as RouterStateSnapshot),
    );

    expect(result).toBe(false);
    expect(router.navigate).toHaveBeenCalledWith(['/map']);
  });

  it('should allow access when authenticated with required role', () => {
    authService.isAuthenticated.mockReturnValue(true);
    authService.getUserRoles.mockReturnValue(['sentinel-admin']);
    const guard = roleGuard('sentinel-admin');

    const result = TestBed.runInInjectionContext(() =>
      guard({} as ActivatedRouteSnapshot, { url: '/link-graph' } as RouterStateSnapshot),
    );

    expect(result).toBe(true);
  });

  it('should allow access when user has any one of multiple required roles', () => {
    authService.isAuthenticated.mockReturnValue(true);
    authService.getUserRoles.mockReturnValue(['sentinel-analyst']);
    const guard = roleGuard('sentinel-analyst', 'sentinel-admin');

    const result = TestBed.runInInjectionContext(() =>
      guard({} as ActivatedRouteSnapshot, { url: '/link-graph' } as RouterStateSnapshot),
    );

    expect(result).toBe(true);
  });

  it('should reject when user has none of multiple required roles', () => {
    authService.isAuthenticated.mockReturnValue(true);
    authService.getUserRoles.mockReturnValue(['sentinel-viewer']);
    const guard = roleGuard('sentinel-analyst', 'sentinel-admin');

    const result = TestBed.runInInjectionContext(() =>
      guard({} as ActivatedRouteSnapshot, { url: '/link-graph' } as RouterStateSnapshot),
    );

    expect(result).toBe(false);
    expect(router.navigate).toHaveBeenCalledWith(['/map']);
  });
});
