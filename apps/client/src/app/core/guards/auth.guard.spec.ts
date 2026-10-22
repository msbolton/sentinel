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

describe('guards for a browser that was a demo guest', () => {
  let authService: { isAuthenticated: jest.Mock; getUserRoles: jest.Mock; ready: Promise<void> };
  let router: { navigate: jest.Mock };
  let finishInit: () => void;

  beforeEach(() => {
    localStorage.setItem('sentinel-demo-visitor', '1');
    authService = {
      isAuthenticated: jest.fn().mockReturnValue(false),
      getUserRoles: jest.fn().mockReturnValue(['sentinel-viewer', 'sentinel-guest']),
      ready: new Promise<void>((r) => (finishInit = r)),
    };
    router = { navigate: jest.fn() };
    TestBed.configureTestingModule({
      providers: [
        { provide: AuthService, useValue: authService },
        { provide: Router, useValue: router },
      ],
    });
  });
  afterEach(() => localStorage.clear());

  const run = (guard: ReturnType<typeof roleGuard>, url: string) =>
    TestBed.runInInjectionContext(() => guard({} as ActivatedRouteSnapshot, { url } as RouterStateSnapshot)) as Promise<boolean>;

  it('waits for the stored guest session to resume, then lets it in (a reload)', async () => {
    const result = run(authGuard, '/map');
    authService.isAuthenticated.mockReturnValue(true);
    finishInit();
    await expect(result).resolves.toBe(true);
    expect(router.navigate).not.toHaveBeenCalled();
  });

  it('sends a former guest with no session left to /demo, not /login', async () => {
    finishInit();
    await expect(run(authGuard, '/map')).resolves.toBe(false);
    expect(router.navigate).toHaveBeenCalledWith(['/demo']);
  });

  it('roleGuard does the same', async () => {
    finishInit();
    await expect(run(roleGuard('sentinel-analyst'), '/link-graph')).resolves.toBe(false);
    expect(router.navigate).toHaveBeenCalledWith(['/demo']);
  });
});
