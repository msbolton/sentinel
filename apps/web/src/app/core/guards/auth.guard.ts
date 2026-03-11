import { inject } from '@angular/core';
import { CanActivateFn, Router } from '@angular/router';
import { AuthService } from '../services/auth.service';

/**
 * Route guard that redirects unauthenticated users to the login flow.
 * In dev mode (Keycloak unavailable), all users are treated as authenticated.
 */
export const authGuard: CanActivateFn = () => {
  const authService = inject(AuthService);
  const router = inject(Router);

  if (authService.isAuthenticated()) {
    return true;
  }

  // Trigger Keycloak login redirect
  authService.login();
  return false;
};

/**
 * Route guard that checks whether the user has at least one of the required roles.
 * Usage: `canActivate: [roleGuard('analyst', 'admin')]`
 */
export function roleGuard(...requiredRoles: string[]): CanActivateFn {
  return () => {
    const authService = inject(AuthService);
    const router = inject(Router);

    if (!authService.isAuthenticated()) {
      authService.login();
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
