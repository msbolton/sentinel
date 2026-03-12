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
