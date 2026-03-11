# Sign-In Page Design

## Overview

A branded sign-in landing page that gates the Sentinel application. Unauthenticated users see a centered sign-in card over an animated dark background. Clicking "Sign In" triggers the existing Keycloak OIDC redirect flow.

## Motivation

Currently, unauthenticated users are redirected directly to Keycloak's default login form with no Sentinel branding. A dedicated landing page provides a professional first impression and makes the authentication flow feel intentional rather than abrupt.

## Design Decisions

- **Layout**: Centered card over full-bleed animated background
- **Auth mechanism**: Existing Keycloak redirect (no custom login form)
- **Theme**: Always uses default (normal dark blue) theme regardless of saved preference
- **No Keycloak theming**: Out of scope

## Routing Changes

### New route
- `/login` — unguarded, loads `LoginComponent`

### Modified behavior
- `authGuard` redirects to `/login` instead of calling `authService.login()` directly
- After Keycloak auth completes, user lands on the originally requested route (or `/map` by default)
- Wildcard `**` continues to redirect to `/map`, which is guarded and bounces unauthenticated users to `/login`

### Auth flow
```
User visits any route → authGuard checks authentication
  → Authenticated: allow
  → Not authenticated: redirect to /login
    → User clicks "Sign In" → authService.login() → Keycloak redirect
      → Keycloak auth succeeds → redirect back to app → authGuard passes
```

## LoginComponent

Standalone Angular component at `apps/web/src/app/features/login/login.component.ts`.

### Visual elements

**Background:**
- Dark navy base (`#060e1f` to `#0e1e3d`)
- CSS grid lines (subtle, low opacity)
- Concentric radar circles centered on page
- 3-4 glowing entity dots (blue, cyan, purple) at fixed positions

**Animations:**
- Radar sweep: 360° rotation over ~8 seconds (CSS `rotate` on a gradient arc)
- Entity dots: slow pulse (`scale` + `opacity` keyframes, staggered timing)
- Optional: 1-2 drifting particles for depth

**Sign-in card (centered):**
- Semi-transparent dark background with subtle border (`rgba(59,130,246,0.25)`)
- Logo mark (blue gradient square with crosshair icon)
- "SENTINEL" in large tracked letters
- "Geospatial Intelligence Platform" subtitle
- Horizontal gradient divider
- Blue gradient "SIGN IN" button
- "Authorized personnel only" muted footer text

### Behavior
- `ngOnInit`: check `authService.isAuthenticated()` — if already authenticated, redirect to `/map` via `Router`
- Sign-in button click: call `authService.login()`
- No theme service interaction — styles are self-contained

## Auth Guard Changes

File: `apps/web/src/app/core/guards/auth.guard.ts`

Current `authGuard` behavior:
```typescript
// Currently calls authService.login() directly (Keycloak redirect)
if (!authService.isAuthenticated()) {
  authService.login();
  return false;
}
```

New behavior:
```typescript
// Redirect to /login route instead
if (!authService.isAuthenticated()) {
  router.navigate(['/login']);
  return false;
}
```

The `roleGuard` should also redirect to `/login` instead of calling `login()` when the user is not authenticated.

## Files

| File | Action | Description |
|------|--------|-------------|
| `apps/web/src/app/features/login/login.component.ts` | Create | Sign-in page component |
| `apps/web/src/app/app.routes.ts` | Edit | Add `/login` route |
| `apps/web/src/app/core/guards/auth.guard.ts` | Edit | Redirect to `/login` instead of direct Keycloak call |

## Out of Scope

- Custom Keycloak login theme
- Username/password form (Keycloak handles authentication)
- Registration flow
- "Remember me" or additional sign-in options
- Theme switching on the login page
