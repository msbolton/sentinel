import { Injectable, OnDestroy } from '@angular/core';
import { HttpInterceptorFn } from '@angular/common/http';
import { BehaviorSubject, Observable } from 'rxjs';

export interface UserProfile {
  username: string;
  email?: string;
  roles: string[];
  classificationLevel: string;
}

@Injectable({ providedIn: 'root' })
export class AuthService implements OnDestroy {
  private keycloak: any = null;
  private tokenRefreshInterval: ReturnType<typeof setInterval> | null = null;

  private readonly authenticatedSubject = new BehaviorSubject<boolean>(false);
  private readonly userProfileSubject = new BehaviorSubject<UserProfile | null>(null);

  readonly isAuthenticated$: Observable<boolean> = this.authenticatedSubject.asObservable();
  readonly userProfile$: Observable<UserProfile | null> = this.userProfileSubject.asObservable();

  async init(): Promise<boolean> {
    try {
      // Dynamic import of keycloak-js to handle cases where it may not be installed
      const KeycloakModule = await import('keycloak-js').catch(() => null);

      if (!KeycloakModule) {
        console.warn('[Auth] Keycloak JS adapter not available, running in unauthenticated mode');
        this.setDevelopmentProfile();
        return true;
      }

      const Keycloak = KeycloakModule.default;
      this.keycloak = new Keycloak({
        url: '/auth',
        realm: 'sentinel',
        clientId: 'sentinel-web',
      });

      const authenticated = await this.keycloak.init({
        onLoad: 'check-sso',
        silentCheckSsoRedirectUri: window.location.origin + '/assets/silent-check-sso.html',
        pkceMethod: 'S256',
      });

      this.authenticatedSubject.next(authenticated);

      if (authenticated) {
        await this.loadUserProfile();
        this.setupTokenRefresh();
      }

      return authenticated;
    } catch (error) {
      console.warn('[Auth] Keycloak initialization failed, running in unauthenticated mode:', error);
      this.setDevelopmentProfile();
      return true;
    }
  }

  async login(): Promise<void> {
    if (this.keycloak) {
      await this.keycloak.login();
    } else {
      console.warn('[Auth] No Keycloak instance available');
    }
  }

  async logout(): Promise<void> {
    if (this.keycloak) {
      await this.keycloak.logout({ redirectUri: window.location.origin });
    }
    this.authenticatedSubject.next(false);
    this.userProfileSubject.next(null);
  }

  getToken(): string | null {
    return this.keycloak?.token ?? null;
  }

  isAuthenticated(): boolean {
    return this.authenticatedSubject.value;
  }

  private async loadUserProfile(): Promise<void> {
    if (!this.keycloak) return;

    try {
      const profile = await this.keycloak.loadUserProfile();
      const tokenParsed = this.keycloak.tokenParsed;

      this.userProfileSubject.next({
        username: profile.username ?? 'unknown',
        email: profile.email,
        roles: tokenParsed?.realm_access?.roles ?? [],
        classificationLevel: tokenParsed?.classification_level ?? 'UNCLASSIFIED',
      });
    } catch (error) {
      console.error('[Auth] Failed to load user profile:', error);
    }
  }

  ngOnDestroy(): void {
    if (this.tokenRefreshInterval) {
      clearInterval(this.tokenRefreshInterval);
      this.tokenRefreshInterval = null;
    }
  }

  private setupTokenRefresh(): void {
    if (!this.keycloak) return;

    // Clear existing interval before creating new one
    if (this.tokenRefreshInterval) {
      clearInterval(this.tokenRefreshInterval);
    }

    // Refresh token every 60 seconds
    this.tokenRefreshInterval = setInterval(async () => {
      try {
        const refreshed = await this.keycloak.updateToken(70);
        if (refreshed) {
          console.log('[Auth] Token refreshed');
        }
      } catch {
        console.warn('[Auth] Token refresh failed');
        this.authenticatedSubject.next(false);
      }
    }, 60000);
  }

  private setDevelopmentProfile(): void {
    this.authenticatedSubject.next(true);
    this.userProfileSubject.next({
      username: 'dev-operator',
      email: 'operator@sentinel.local',
      roles: ['analyst', 'operator'],
      classificationLevel: 'UNCLASSIFIED',
    });
  }
}

/**
 * HTTP interceptor that attaches the JWT token to outgoing requests.
 */
export const authInterceptor: HttpInterceptorFn = (req, next) => {
  // Only attach to API requests
  if (!req.url.startsWith('/api')) {
    return next(req);
  }

  // Inject AuthService manually since functional interceptors can't use constructor injection
  // We access the token from a singleton pattern
  const token = AuthService.prototype.getToken.call(
    (window as any).__sentinelAuthService,
  );

  if (token) {
    const cloned = req.clone({
      setHeaders: {
        Authorization: `Bearer ${token}`,
      },
    });
    return next(cloned);
  }

  return next(req);
};
