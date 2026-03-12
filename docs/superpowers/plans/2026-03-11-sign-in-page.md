# Sign-In Page Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a branded sign-in landing page that gates the app, redirecting unauthenticated users to `/login` instead of straight to Keycloak.

**Architecture:** A standalone `LoginComponent` at `/login` with CSS-only animations. Auth guards redirect to `/login` with a `returnUrl` query param. `AuthService.login()` accepts an optional `redirectUri` to preserve deep links through the Keycloak redirect.

**Tech Stack:** Angular 19 (standalone components, zoneless change detection), keycloak-js, RxJS

**Spec:** `docs/superpowers/specs/2026-03-11-sign-in-page-design.md`

---

## Chunk 1: Auth plumbing (guard + service changes)

### Task 1: Update AuthService.login() to accept redirectUri

**Files:**
- Modify: `apps/web/src/app/core/services/auth.service.ts:74-80`

- [ ] **Step 1: Write the failing test**

Create test file:

```typescript
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
      const warnSpy = jest.spyOn(console, 'warn').mockImplementation();

      await service.login();

      expect(warnSpy).toHaveBeenCalledWith('[Auth] No Keycloak instance available');
      warnSpy.mockRestore();
    });
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `npx nx test web --testFile=apps/web/src/app/core/services/auth.service.spec.ts`
Expected: FAIL — `login()` doesn't accept arguments yet.

- [ ] **Step 3: Update login() to accept optional redirectUri**

In `apps/web/src/app/core/services/auth.service.ts`, replace lines 74-80:

```typescript
  async login(redirectUri?: string): Promise<void> {
    if (this.keycloak) {
      await this.keycloak.login(redirectUri ? { redirectUri } : undefined);
    } else {
      console.warn('[Auth] No Keycloak instance available');
    }
  }
```

- [ ] **Step 4: Run test to verify it passes**

Run: `npx nx test web --testFile=apps/web/src/app/core/services/auth.service.spec.ts`
Expected: PASS (3 tests)

- [ ] **Step 5: Commit**

```bash
git add apps/web/src/app/core/services/auth.service.ts apps/web/src/app/core/services/auth.service.spec.ts
git commit -m "feat(auth): add redirectUri parameter to AuthService.login()"
```

---

### Task 2: Update auth guards to redirect to /login with returnUrl

**Files:**
- Modify: `apps/web/src/app/core/guards/auth.guard.ts`

- [ ] **Step 1: Write the failing tests**

Create test file. The guards are functional (not class-based), so we test them by invoking the function with mocked injectors.

```typescript
// apps/web/src/app/core/guards/auth.guard.spec.ts

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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `npx nx test web --testFile=apps/web/src/app/core/guards/auth.guard.spec.ts`
Expected: FAIL — guards don't pass `returnUrl` yet (and `authGuard` doesn't accept `route, state` params).

- [ ] **Step 3: Update the guards**

Replace the full contents of `apps/web/src/app/core/guards/auth.guard.ts`:

```typescript
import { inject } from '@angular/core';
import { CanActivateFn, Router } from '@angular/router';
import { AuthService } from '../services/auth.service';

/**
 * Route guard that redirects unauthenticated users to the login page.
 * Passes the intended URL as a returnUrl query param so the login page
 * can redirect back after authentication.
 */
export const authGuard: CanActivateFn = (_route, state) => {
  const authService = inject(AuthService);
  const router = inject(Router);

  if (authService.isAuthenticated()) {
    return true;
  }

  router.navigate(['/login'], {
    queryParams: { returnUrl: state.url },
  });
  return false;
};

/**
 * Route guard that checks whether the user has at least one of the required roles.
 * Redirects unauthenticated users to /login; authenticated users without
 * the required role are sent to /map.
 */
export function roleGuard(...requiredRoles: string[]): CanActivateFn {
  return (_route, state) => {
    const authService = inject(AuthService);
    const router = inject(Router);

    if (!authService.isAuthenticated()) {
      router.navigate(['/login'], {
        queryParams: { returnUrl: state.url },
      });
      return false;
    }

    const userRoles = authService.getUserRoles();
    const hasRole = requiredRoles.some((role) => userRoles.includes(role));

    if (!hasRole) {
      router.navigate(['/map']);
      return false;
    }

    return true;
  };
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `npx nx test web --testFile=apps/web/src/app/core/guards/auth.guard.spec.ts`
Expected: PASS (7 tests)

- [ ] **Step 5: Commit**

```bash
git add apps/web/src/app/core/guards/auth.guard.ts apps/web/src/app/core/guards/auth.guard.spec.ts
git commit -m "feat(auth): redirect unauthenticated users to /login with returnUrl"
```

---

## Chunk 2: LoginComponent + route wiring

### Task 3: Create LoginComponent

**Files:**
- Create: `apps/web/src/app/features/login/login.component.ts`

- [ ] **Step 1: Write the failing test**

```typescript
// apps/web/src/app/features/login/login.component.spec.ts

import { ComponentFixture, TestBed } from '@angular/core/testing';
import { Router, ActivatedRoute } from '@angular/router';
import { BehaviorSubject } from 'rxjs';
import { LoginComponent } from './login.component';
import { AuthService } from '../../core/services/auth.service';

describe('LoginComponent', () => {
  let component: LoginComponent;
  let fixture: ComponentFixture<LoginComponent>;
  let authService: { isAuthenticated$: BehaviorSubject<boolean>; login: jest.Mock };
  let router: { navigateByUrl: jest.Mock };
  let queryParams: { returnUrl?: string };

  beforeEach(async () => {
    authService = {
      isAuthenticated$: new BehaviorSubject<boolean>(false),
      login: jest.fn().mockResolvedValue(undefined),
    };
    router = { navigateByUrl: jest.fn(), navigate: jest.fn() };
    queryParams = {};

    await TestBed.configureTestingModule({
      imports: [LoginComponent],
      providers: [
        { provide: AuthService, useValue: authService },
        { provide: Router, useValue: router },
        {
          provide: ActivatedRoute,
          useValue: { snapshot: { queryParams } },
        },
      ],
    }).compileComponents();

    fixture = TestBed.createComponent(LoginComponent);
    component = fixture.componentInstance;
  });

  it('should create', () => {
    fixture.detectChanges();
    expect(component).toBeTruthy();
  });

  it('should redirect to /map when already authenticated', () => {
    authService.isAuthenticated$.next(true);
    fixture.detectChanges();

    expect(router.navigateByUrl).toHaveBeenCalledWith('/map');
  });

  it('should redirect to returnUrl when already authenticated', () => {
    queryParams.returnUrl = '/alerts';
    authService.isAuthenticated$.next(true);
    fixture.detectChanges();

    expect(router.navigateByUrl).toHaveBeenCalledWith('/alerts');
  });

  it('should not redirect when not authenticated', () => {
    fixture.detectChanges();

    expect(router.navigateByUrl).not.toHaveBeenCalled();
  });

  it('should call authService.login with redirectUri on sign in', () => {
    fixture.detectChanges();

    component.onSignIn();

    const expectedUri = window.location.origin + window.location.pathname + '#/map';
    expect(authService.login).toHaveBeenCalledWith(expectedUri);
  });

  it('should use returnUrl in redirectUri on sign in', () => {
    queryParams.returnUrl = '/alerts';
    fixture.detectChanges();

    component.onSignIn();

    const expectedUri = window.location.origin + window.location.pathname + '#/alerts';
    expect(authService.login).toHaveBeenCalledWith(expectedUri);
  });

  it('should render the sign-in button', () => {
    fixture.detectChanges();
    const button = fixture.nativeElement.querySelector('.sign-in-btn');
    expect(button).toBeTruthy();
    expect(button.textContent).toContain('SIGN IN');
  });

  it('should render the SENTINEL title', () => {
    fixture.detectChanges();
    const title = fixture.nativeElement.querySelector('.app-title');
    expect(title.textContent).toContain('SENTINEL');
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `npx nx test web --testFile=apps/web/src/app/features/login/login.component.spec.ts`
Expected: FAIL — `LoginComponent` doesn't exist yet.

- [ ] **Step 3: Create LoginComponent**

Create `apps/web/src/app/features/login/login.component.ts`. This is a large file — the full component with inline template and styles follows. Key behavioral points:

- Subscribes to `authService.isAuthenticated$` to handle async SSO; auto-redirects if already authenticated
- `onSignIn()` constructs a full redirect URI including the hash fragment and passes it to `authService.login()`
- All animations are CSS-only (`@keyframes` in the component styles)
- Self-contained styles — does not depend on theme service or global CSS variables

```typescript
import { Component, OnInit, OnDestroy, inject } from '@angular/core';
import { Router, ActivatedRoute } from '@angular/router';
import { Subscription, filter, take } from 'rxjs';
import { AuthService } from '../../core/services/auth.service';

@Component({
  selector: 'app-login',
  standalone: true,
  template: `
    <div class="login-page">
      <!-- Animated background -->
      <div class="bg-grid"></div>
      <div class="radar-container">
        <div class="radar-circle radar-circle-1"></div>
        <div class="radar-circle radar-circle-2"></div>
        <div class="radar-circle radar-circle-3"></div>
        <div class="radar-sweep"></div>
      </div>
      <div class="entity-dot dot-1"></div>
      <div class="entity-dot dot-2"></div>
      <div class="entity-dot dot-3"></div>
      <div class="entity-dot dot-4"></div>
      <div class="particle particle-1"></div>
      <div class="particle particle-2"></div>

      <!-- Sign-in card -->
      <div class="login-card">
        <div class="logo-mark">
          <div class="crosshair">
            <div class="crosshair-dot"></div>
          </div>
        </div>
        <h1 class="app-title">SENTINEL</h1>
        <p class="app-subtitle">Geospatial Intelligence Platform</p>
        <div class="divider"></div>
        <button class="sign-in-btn" (click)="onSignIn()">SIGN IN</button>
        <p class="auth-notice">Authorized personnel only</p>
      </div>
    </div>
  `,
  styles: [`
    :host {
      display: block;
      width: 100vw;
      height: 100vh;
      overflow: hidden;
    }

    .login-page {
      position: relative;
      width: 100%;
      height: 100%;
      background: linear-gradient(135deg, #060e1f 0%, #0e1e3d 50%, #091428 100%);
      display: flex;
      align-items: center;
      justify-content: center;
    }

    /* Grid background */
    .bg-grid {
      position: absolute;
      inset: 0;
      background-image:
        linear-gradient(rgba(59, 130, 246, 0.07) 1px, transparent 1px),
        linear-gradient(90deg, rgba(59, 130, 246, 0.07) 1px, transparent 1px);
      background-size: 60px 60px;
    }

    /* Radar circles */
    .radar-container {
      position: absolute;
      top: 50%;
      left: 50%;
      transform: translate(-50%, -50%);
    }

    .radar-circle {
      position: absolute;
      border-radius: 50%;
      border: 1px solid rgba(59, 130, 246, 0.1);
      top: 50%;
      left: 50%;
      transform: translate(-50%, -50%);
    }

    .radar-circle-1 {
      width: 500px;
      height: 500px;
    }

    .radar-circle-2 {
      width: 350px;
      height: 350px;
      border-color: rgba(59, 130, 246, 0.15);
    }

    .radar-circle-3 {
      width: 200px;
      height: 200px;
    }

    /* Radar sweep */
    .radar-sweep {
      position: absolute;
      top: 50%;
      left: 50%;
      width: 250px;
      height: 250px;
      transform-origin: 0 0;
      background: conic-gradient(
        from 0deg,
        transparent 0deg,
        rgba(59, 130, 246, 0.08) 30deg,
        transparent 60deg
      );
      animation: sweep 8s linear infinite;
    }

    @keyframes sweep {
      from { transform: rotate(0deg); }
      to { transform: rotate(360deg); }
    }

    /* Entity dots */
    .entity-dot {
      position: absolute;
      border-radius: 50%;
      animation: pulse 3s ease-in-out infinite;
    }

    .dot-1 {
      top: 22%;
      left: 18%;
      width: 6px;
      height: 6px;
      background: #3b82f6;
      box-shadow: 0 0 12px 4px rgba(59, 130, 246, 0.4);
      animation-delay: 0s;
    }

    .dot-2 {
      top: 65%;
      left: 75%;
      width: 5px;
      height: 5px;
      background: #22d3ee;
      box-shadow: 0 0 12px 4px rgba(34, 211, 238, 0.4);
      animation-delay: 1s;
    }

    .dot-3 {
      top: 38%;
      left: 82%;
      width: 4px;
      height: 4px;
      background: #a78bfa;
      box-shadow: 0 0 10px 3px rgba(167, 139, 250, 0.4);
      animation-delay: 2s;
    }

    .dot-4 {
      top: 72%;
      left: 25%;
      width: 5px;
      height: 5px;
      background: #3b82f6;
      box-shadow: 0 0 12px 4px rgba(59, 130, 246, 0.4);
      animation-delay: 0.5s;
    }

    @keyframes pulse {
      0%, 100% { transform: scale(1); opacity: 0.6; }
      50% { transform: scale(1.8); opacity: 1; }
    }

    /* Floating particles */
    .particle {
      position: absolute;
      width: 2px;
      height: 2px;
      border-radius: 50%;
      background: rgba(59, 130, 246, 0.4);
      animation: drift 12s ease-in-out infinite;
    }

    .particle-1 {
      top: 80%;
      left: 40%;
      animation-delay: 0s;
    }

    .particle-2 {
      top: 70%;
      left: 60%;
      animation-delay: 6s;
    }

    @keyframes drift {
      0%, 100% { transform: translateY(0) translateX(0); opacity: 0; }
      10% { opacity: 0.6; }
      50% { transform: translateY(-120px) translateX(20px); opacity: 0.4; }
      90% { opacity: 0; }
    }

    /* Sign-in card */
    .login-card {
      position: relative;
      z-index: 10;
      width: 340px;
      padding: 40px 36px;
      background: rgba(8, 16, 38, 0.92);
      border: 1px solid rgba(59, 130, 246, 0.2);
      border-radius: 12px;
      text-align: center;
      box-shadow:
        0 0 60px rgba(59, 130, 246, 0.08),
        0 25px 80px rgba(0, 0, 0, 0.5);
      backdrop-filter: blur(12px);
      animation: cardAppear 0.6s ease-out;
    }

    @keyframes cardAppear {
      from {
        opacity: 0;
        transform: translateY(20px) scale(0.97);
      }
      to {
        opacity: 1;
        transform: translateY(0) scale(1);
      }
    }

    /* Logo mark */
    .logo-mark {
      width: 52px;
      height: 52px;
      margin: 0 auto 18px;
      border-radius: 10px;
      background: linear-gradient(135deg, #3b82f6, #2563eb);
      display: flex;
      align-items: center;
      justify-content: center;
      box-shadow: 0 0 24px rgba(59, 130, 246, 0.3);
    }

    .crosshair {
      width: 22px;
      height: 22px;
      border: 2.5px solid rgba(255, 255, 255, 0.9);
      border-radius: 50%;
      position: relative;
    }

    .crosshair-dot {
      position: absolute;
      top: 50%;
      left: 50%;
      transform: translate(-50%, -50%);
      width: 5px;
      height: 5px;
      background: white;
      border-radius: 50%;
    }

    .app-title {
      font-family: 'Inter', 'Segoe UI', system-ui, sans-serif;
      font-size: 28px;
      font-weight: 700;
      letter-spacing: 6px;
      color: rgba(255, 255, 255, 0.95);
      margin: 0;
    }

    .app-subtitle {
      font-family: 'Inter', 'Segoe UI', system-ui, sans-serif;
      font-size: 12px;
      color: rgba(255, 255, 255, 0.4);
      margin: 6px 0 0;
      letter-spacing: 1.5px;
    }

    .divider {
      width: 100%;
      height: 1px;
      background: linear-gradient(90deg, transparent, rgba(59, 130, 246, 0.3), transparent);
      margin: 28px 0;
    }

    .sign-in-btn {
      width: 100%;
      padding: 13px 24px;
      background: linear-gradient(135deg, #3b82f6, #2563eb);
      color: white;
      border: none;
      border-radius: 8px;
      font-family: 'Inter', 'Segoe UI', system-ui, sans-serif;
      font-size: 14px;
      font-weight: 600;
      letter-spacing: 2px;
      cursor: pointer;
      box-shadow: 0 4px 20px rgba(59, 130, 246, 0.3);
      transition: box-shadow 0.2s ease, transform 0.15s ease;
    }

    .sign-in-btn:hover {
      box-shadow: 0 6px 28px rgba(59, 130, 246, 0.45);
      transform: translateY(-1px);
    }

    .sign-in-btn:active {
      transform: translateY(0);
      box-shadow: 0 2px 12px rgba(59, 130, 246, 0.3);
    }

    .auth-notice {
      font-family: 'Inter', 'Segoe UI', system-ui, sans-serif;
      font-size: 11px;
      color: rgba(255, 255, 255, 0.25);
      margin: 16px 0 0;
      letter-spacing: 0.5px;
    }
  `],
})
export class LoginComponent implements OnInit, OnDestroy {
  private readonly authService = inject(AuthService);
  private readonly router = inject(Router);
  private readonly route = inject(ActivatedRoute);
  private authSub: Subscription | null = null;

  ngOnInit(): void {
    this.authSub = this.authService.isAuthenticated$.pipe(
      filter((isAuth) => isAuth),
      take(1),
    ).subscribe(() => {
      const returnUrl = this.route.snapshot.queryParams['returnUrl'] || '/map';
      this.router.navigateByUrl(returnUrl);
    });
  }

  ngOnDestroy(): void {
    this.authSub?.unsubscribe();
  }

  onSignIn(): void {
    const returnUrl = this.route.snapshot.queryParams['returnUrl'] || '/map';
    const redirectUri =
      window.location.origin + window.location.pathname + '#' + returnUrl;
    this.authService.login(redirectUri);
  }
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `npx nx test web --testFile=apps/web/src/app/features/login/login.component.spec.ts`
Expected: PASS (8 tests)

- [ ] **Step 5: Commit**

```bash
git add apps/web/src/app/features/login/login.component.ts apps/web/src/app/features/login/login.component.spec.ts
git commit -m "feat(auth): create branded LoginComponent with animated background"
```

---

### Task 4: Add /login route

**Files:**
- Modify: `apps/web/src/app/app.routes.ts`

- [ ] **Step 1: Add the login route to app.routes.ts**

No dedicated test for route wiring — this is a single-line declarative config. Coverage comes from the full `npx nx test web` run in Step 2 plus the smoke test in Task 5.

Add the `/login` route **before** the wildcard route. It must not have a guard. Insert after the `locations` route entry (before the `**` wildcard):

```typescript
  {
    path: 'login',
    loadComponent: () =>
      import('./features/login/login.component').then((m) => m.LoginComponent),
  },
```

The `**` wildcard must remain last.

- [ ] **Step 2: Run all web tests to verify nothing broke**

Run: `npx nx test web`
Expected: All existing tests + new tests pass.

- [ ] **Step 3: Commit**

```bash
git add apps/web/src/app/app.routes.ts
git commit -m "feat(auth): add /login route to app routing"
```

---

### Task 5: Manual smoke test

- [ ] **Step 1: Build and verify**

Run: `npx nx build web`
Expected: Build succeeds within bundle budgets (2 MB warning / 5 MB error). The login component is CSS-only with no extra dependencies, so it should add minimal bundle size.

- [ ] **Step 2: Start the dev server**

Run: `npm run start:web`

- [ ] **Step 3: Verify login page renders**

Open `http://localhost:4200/#/login` in a browser.
Expected: Animated sign-in page with radar sweep, pulsing dots, centered card with SENTINEL branding and SIGN IN button.

Note: In dev mode without Keycloak, the page will auto-redirect to `/map` because `AuthService` sets a dev profile. To see the login page, either start Keycloak infrastructure (`npm run docker:infra`) or temporarily add a short `delay(2000)` before the `filter` in the component's `ngOnInit` subscription during visual testing.

- [ ] **Step 4: Verify guard redirect works**

Clear localStorage and open `http://localhost:4200/#/alerts` (with Keycloak running).
Expected: Redirected to `/#/login?returnUrl=%2Falerts`. After clicking SIGN IN → Keycloak login → redirected back to `/#/alerts`.
