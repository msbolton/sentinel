import { inject } from '@angular/core';
import { CanActivateFn, Router } from '@angular/router';
import { AuthService } from '../services/auth.service';
import { wasDemoVisitor } from '../services/guest-session-store';

/** How long a guard waits for a stored demo session to resume before it decides. */
const RESUME_WAIT_MS = 8_000;

/**
 * A browser that was a demo guest: it may be resuming its stored session
 * (AuthService.init), so wait for that, bounded, before deciding. Signed in
 * by then, it carries on; otherwise it goes back to /demo for a new guest,
 * never to the login form.
 */
async function resumeOrDemo(auth: AuthService, router: Router, allowed: () => boolean): Promise<boolean> {
  let timer: ReturnType<typeof setTimeout> | undefined;
  try {
    await Promise.race([auth.ready, new Promise<void>((resolve) => (timer = setTimeout(resolve, RESUME_WAIT_MS)))]);
  } finally {
    clearTimeout(timer);
  }
  if (auth.isAuthenticated()) {
    return allowed();
  }
  router.navigate(['/demo']);
  return false;
}

/**
 * Route guard that redirects unauthenticated users to the login page.
 * Passes the intended URL as a returnUrl query param so the login page
 * can redirect back after authentication. A former demo guest goes to /demo.
 */
export const authGuard: CanActivateFn = (_route, state) => {
  const authService = inject(AuthService);
  const router = inject(Router);

  if (authService.isAuthenticated()) {
    return true;
  }
  if (wasDemoVisitor()) {
    return resumeOrDemo(authService, router, () => true);
  }

  router.navigate(['/login'], {
    queryParams: { returnUrl: state.url },
  });
  return false;
};

/**
 * Route guard that checks whether the user has at least one of the required roles.
 * Redirects unauthenticated users to /login (a former demo guest to /demo);
 * authenticated users without the required role are sent to /map.
 */
export function roleGuard(...requiredRoles: string[]): CanActivateFn {
  return (_route, state) => {
    const authService = inject(AuthService);
    const router = inject(Router);

    const allowed = () => {
      const userRoles = authService.getUserRoles();
      if (!requiredRoles.some((role) => userRoles.includes(role))) {
        router.navigate(['/map']);
        return false;
      }
      return true;
    };

    if (!authService.isAuthenticated()) {
      if (wasDemoVisitor()) {
        return resumeOrDemo(authService, router, allowed);
      }
      router.navigate(['/login'], {
        queryParams: { returnUrl: state.url },
      });
      return false;
    }

    return allowed();
  };
}
