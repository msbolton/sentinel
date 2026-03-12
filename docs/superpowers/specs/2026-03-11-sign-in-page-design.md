# Sign-In Page Design

## Overview

A branded sign-in landing page that gates the Sentinel application. Unauthenticated users see a centered sign-in card over an animated dark background. Clicking "Sign In" triggers the existing Keycloak OIDC redirect flow.

## Motivation

Currently, unauthenticated users are redirected directly to Keycloak's default login form with no Sentinel branding. A dedicated landing page provides a professional first impression and makes the authentication flow feel intentional rather than abrupt.

## Design Decisions

- **Layout**: Centered card over full-bleed animated background
- **Auth mechanism**: Existing Keycloak redirect (no custom login form)
- **Theme**: Always uses default (normal dark blue) theme regardless of saved preference
- **Animations**: CSS-only (no additional dependencies) — keyframes for radar sweep, dot pulse, particles
- **No Keycloak theming**: Out of scope

## Routing Changes

### New route

```typescript
{
  path: 'login',
  loadComponent: () =>
    import('./features/login/login.component').then((m) => m.LoginComponent),
},
```

This route has no guard — it must be accessible to unauthenticated users.

### Modified guards

**`authGuard`** — redirect to `/login` with return URL:

```typescript
export const authGuard: CanActivateFn = (route, state) => {
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
```

**`roleGuard`** — only the unauthenticated branch changes (redirect to `/login` with return URL). The authenticated-but-unauthorized branch continues to redirect to `/map` as before:

```typescript
export function roleGuard(...requiredRoles: string[]): CanActivateFn {
  return (route, state) => {
    const authService = inject(AuthService);
    const router = inject(Router);

    if (!authService.isAuthenticated()) {
      router.navigate(['/login'], {
        queryParams: { returnUrl: state.url },
      });
      return false;
    }
    // ... existing role check unchanged
  };
}
```

### Return URL / deep link flow

The app uses hash location strategy (`/#/map`, `/#/search`, etc.). The full flow:

1. User visits `/#/alerts` → `authGuard` redirects to `/#/login?returnUrl=%2Falerts`
2. `LoginComponent` reads `returnUrl` from `ActivatedRoute.queryParams`
3. On "Sign In" click, calls `authService.login()` which invokes `keycloak.login({ redirectUri })`. The `redirectUri` must be the full URL including the hash fragment: `window.location.origin + window.location.pathname + '#/map'` (or `#/<returnUrl>`)
4. Keycloak authenticates and redirects back to the constructed URL
5. Angular activates the target route — guard passes because user is now authenticated

If no `returnUrl` is present, default to `/map`.

**Important**: The `redirectUri` passed to Keycloak must match a valid redirect URI in the Keycloak client configuration. The `sentinel-web` client should have a wildcard pattern (e.g., `http://localhost:4200/*`) or specific entries that cover hash-based routes.

### Auth flow diagram

```
User visits /#/alerts → authGuard checks authentication
  → Authenticated: allow
  → Not authenticated: redirect to /#/login?returnUrl=/alerts
    → LoginComponent renders sign-in page
    → User clicks "Sign In"
    → authService.login({ redirectUri: origin + '/#/alerts' })
    → Keycloak login page
    → Keycloak auth succeeds → redirect to /#/alerts
    → authGuard passes → AlertsComponent loads
```

## LoginComponent

Standalone Angular component at `apps/web/src/app/features/login/login.component.ts`.

### Visual elements

**Background:**
- Dark navy base (`#060e1f` to `#0e1e3d`)
- CSS grid lines (subtle, low opacity)
- Concentric radar circles centered on page
- 3-4 glowing entity dots (blue, cyan, purple) at fixed positions

**Animations (CSS-only, no extra dependencies):**
- Radar sweep: 360° rotation over ~8s (CSS `rotate` on a conic-gradient arc)
- Entity dots: slow pulse (`scale` + `opacity` keyframes, staggered `animation-delay`)
- 1-2 drifting particles via `translateY` keyframes

**Sign-in card (centered):**
- Semi-transparent dark background with subtle border (`rgba(59,130,246,0.25)`)
- Logo mark (blue gradient square with crosshair icon)
- "SENTINEL" in large tracked letters
- "Geospatial Intelligence Platform" subtitle
- Horizontal gradient divider
- Blue gradient "SIGN IN" button
- "Authorized personnel only" muted footer text

### Behavior

**Authentication check — subscribe to the observable, not the snapshot:**

The component must subscribe to `authService.isAuthenticated$` (the `BehaviorSubject`-backed observable) rather than calling the synchronous `isAuthenticated()` method. This handles the async `check-sso` case where Keycloak's silent SSO iframe resolves after the component has already rendered.

```typescript
ngOnInit() {
  this.authService.isAuthenticated$.pipe(
    filter(isAuth => isAuth),
    take(1),
  ).subscribe(() => {
    const returnUrl = this.route.snapshot.queryParams['returnUrl'] || '/map';
    this.router.navigateByUrl(returnUrl);
  });
}
```

**Sign-in button:**
- Reads `returnUrl` from `ActivatedRoute.queryParams`
- Constructs full redirect URI: `window.location.origin + window.location.pathname + '#' + (returnUrl || '/map')`
- Calls `authService.login()` (which needs to accept an optional `redirectUri` parameter — see auth service changes below)

### Dev mode behavior

`AuthService.init()` sets a dev profile (authenticated as `dev-operator`) when Keycloak is unavailable. This means `isAuthenticated$` emits `true` immediately and the `LoginComponent` auto-redirects to `/map`. The login page is effectively bypassed in dev mode, which is the correct behavior — developers don't need to authenticate.

To visually iterate on the login component during development, temporarily comment out the `isAuthenticated$` subscription redirect or navigate directly to `/#/login` before `init()` resolves.

## Auth Service Changes

`authService.login()` currently calls `this.keycloak?.login()` with no arguments. It needs to accept an optional `redirectUri`:

```typescript
async login(redirectUri?: string): Promise<void> {
  if (!this.keycloak) {
    console.warn('Keycloak not initialized');
    return;
  }
  await this.keycloak.login(redirectUri ? { redirectUri } : undefined);
}
```

## Files

| File | Action | Description |
|------|--------|-------------|
| `apps/web/src/app/features/login/login.component.ts` | Create | Sign-in page component (template + styles inline) |
| `apps/web/src/app/app.routes.ts` | Edit | Add `/login` route with `loadComponent` |
| `apps/web/src/app/core/guards/auth.guard.ts` | Edit | Redirect to `/login` with `returnUrl` query param |
| `apps/web/src/app/core/services/auth.service.ts` | Edit | Add optional `redirectUri` param to `login()` |

## Out of Scope

- Custom Keycloak login theme
- Username/password form (Keycloak handles authentication)
- Registration flow
- "Remember me" or additional sign-in options
- Theme switching on the login page
