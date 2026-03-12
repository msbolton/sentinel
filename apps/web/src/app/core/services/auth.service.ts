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
  private directToken: string | null = null;
  private directRefreshToken: string | null = null;
  private tokenRefreshInterval: ReturnType<typeof setInterval> | null = null;
  private tokenExpiryTimeout: ReturnType<typeof setTimeout> | null = null;

  private readonly keycloakTokenUrl = '/auth/realms/sentinel/protocol/openid-connect/token';
  private readonly clientId = 'sentinel-web';

  private readonly authenticatedSubject = new BehaviorSubject<boolean>(false);
  private readonly userProfileSubject = new BehaviorSubject<UserProfile | null>(null);

  readonly isAuthenticated$: Observable<boolean> = this.authenticatedSubject.asObservable();
  readonly userProfile$: Observable<UserProfile | null> = this.userProfileSubject.asObservable();

  async init(): Promise<boolean> {
    try {
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

      // Listen for token expiry events
      this.keycloak.onTokenExpired = () => {
        console.warn('[Auth] Token expired, attempting refresh');
        this.refreshToken();
      };

      this.keycloak.onAuthLogout = () => {
        console.warn('[Auth] Session ended by Keycloak');
        this.authenticatedSubject.next(false);
        this.userProfileSubject.next(null);
      };

      return authenticated;
    } catch (error) {
      console.warn('[Auth] Keycloak initialization failed, running in unauthenticated mode:', error);
      this.keycloak = null;
      this.setDevelopmentProfile();
      return true;
    }
  }

  async login(redirectUri?: string): Promise<void> {
    if (this.keycloak) {
      await this.keycloak.login(redirectUri ? { redirectUri } : undefined);
    } else {
      console.warn('[Auth] No Keycloak instance available');
    }
  }

  async loginWithCredentials(username: string, password: string): Promise<{ success: boolean; error?: string }> {
    const body = new URLSearchParams({
      grant_type: 'password',
      client_id: this.clientId,
      username,
      password,
    });

    try {
      const response = await fetch(this.keycloakTokenUrl, {
        method: 'POST',
        headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
        body: body.toString(),
      });

      if (!response.ok) {
        const err = await response.json().catch(() => null);
        const message = err?.error_description || 'Invalid username or password';
        return { success: false, error: message };
      }

      const tokenResponse = await response.json();
      this.directToken = tokenResponse.access_token;
      this.directRefreshToken = tokenResponse.refresh_token;

      const parsed = this.parseJwt(tokenResponse.access_token);
      this.userProfileSubject.next({
        username: parsed.preferred_username ?? username,
        email: parsed.email,
        roles: parsed.realm_access?.roles ?? [],
        classificationLevel: parsed.classification_level ?? 'UNCLASSIFIED',
      });

      this.authenticatedSubject.next(true);
      this.setupDirectTokenRefresh(tokenResponse.expires_in);
      return { success: true };
    } catch {
      return { success: false, error: 'Unable to reach authentication server' };
    }
  }

  private parseJwt(token: string): any {
    const base64Url = token.split('.')[1];
    const base64 = base64Url.replace(/-/g, '+').replace(/_/g, '/');
    return JSON.parse(atob(base64));
  }

  private setupDirectTokenRefresh(expiresIn: number): void {
    this.clearTimers();

    // Refresh 30 seconds before expiry
    const refreshAt = Math.max((expiresIn - 30) * 1000, 10000);
    this.tokenRefreshInterval = setInterval(() => {
      this.refreshDirectToken();
    }, refreshAt);
  }

  private async refreshDirectToken(): Promise<void> {
    if (!this.directRefreshToken) return;

    const body = new URLSearchParams({
      grant_type: 'refresh_token',
      client_id: this.clientId,
      refresh_token: this.directRefreshToken,
    });

    try {
      const response = await fetch(this.keycloakTokenUrl, {
        method: 'POST',
        headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
        body: body.toString(),
      });

      if (!response.ok) {
        throw new Error('Refresh failed');
      }

      const tokenResponse = await response.json();
      this.directToken = tokenResponse.access_token;
      this.directRefreshToken = tokenResponse.refresh_token;

      const parsed = this.parseJwt(tokenResponse.access_token);
      this.userProfileSubject.next({
        username: parsed.preferred_username ?? 'unknown',
        email: parsed.email,
        roles: parsed.realm_access?.roles ?? [],
        classificationLevel: parsed.classification_level ?? 'UNCLASSIFIED',
      });
    } catch {
      console.warn('[Auth] Direct token refresh failed, session expired');
      this.directToken = null;
      this.directRefreshToken = null;
      this.authenticatedSubject.next(false);
      this.userProfileSubject.next(null);
    }
  }

  async logout(): Promise<void> {
    this.clearTimers();
    this.directToken = null;
    this.directRefreshToken = null;
    if (this.keycloak) {
      await this.keycloak.logout({ redirectUri: window.location.origin });
    }
    this.authenticatedSubject.next(false);
    this.userProfileSubject.next(null);
  }

  getToken(): string | null {
    return this.directToken ?? this.keycloak?.token ?? null;
  }

  isAuthenticated(): boolean {
    return this.authenticatedSubject.value;
  }

  getUserRoles(): string[] {
    return this.userProfileSubject.value?.roles ?? [];
  }

  hasRole(role: string): boolean {
    return this.getUserRoles().includes(role);
  }

  hasAnyRole(...roles: string[]): boolean {
    const userRoles = this.getUserRoles();
    return roles.some((role) => userRoles.includes(role));
  }

  getClassificationLevel(): string {
    return this.userProfileSubject.value?.classificationLevel ?? 'UNCLASSIFIED';
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
    this.clearTimers();
  }

  private clearTimers(): void {
    if (this.tokenRefreshInterval) {
      clearInterval(this.tokenRefreshInterval);
      this.tokenRefreshInterval = null;
    }
    if (this.tokenExpiryTimeout) {
      clearTimeout(this.tokenExpiryTimeout);
      this.tokenExpiryTimeout = null;
    }
  }

  private setupTokenRefresh(): void {
    if (!this.keycloak) return;

    this.clearTimers();

    // Refresh token every 60 seconds (with 70-second validity threshold)
    this.tokenRefreshInterval = setInterval(() => {
      this.refreshToken();
    }, 60000);

    // Schedule a warning before token expiry
    this.scheduleExpiryWarning();
  }

  private async refreshToken(): Promise<void> {
    if (!this.keycloak) return;

    try {
      const refreshed = await this.keycloak.updateToken(70);
      if (refreshed) {
        console.log('[Auth] Token refreshed');
        await this.loadUserProfile();
        this.scheduleExpiryWarning();
      }
    } catch {
      console.warn('[Auth] Token refresh failed, session expired');
      this.authenticatedSubject.next(false);
      this.userProfileSubject.next(null);
    }
  }

  private scheduleExpiryWarning(): void {
    if (!this.keycloak?.tokenParsed?.exp) return;

    if (this.tokenExpiryTimeout) {
      clearTimeout(this.tokenExpiryTimeout);
    }

    const expiresIn = this.keycloak.tokenParsed.exp * 1000 - Date.now();
    // Warn 2 minutes before expiry
    const warnAt = expiresIn - 120000;

    if (warnAt > 0) {
      this.tokenExpiryTimeout = setTimeout(() => {
        console.warn('[Auth] Token expiring soon, refreshing proactively');
        this.refreshToken();
      }, warnAt);
    }
  }

  private setDevelopmentProfile(): void {
    this.authenticatedSubject.next(true);
    this.userProfileSubject.next({
      username: 'dev-operator',
      email: 'operator@sentinel.local',
      roles: ['sentinel-analyst', 'sentinel-operator', 'sentinel-admin'],
      classificationLevel: 'UNCLASSIFIED',
    });
  }
}

/**
 * HTTP interceptor that attaches the JWT token to outgoing requests.
 */
export const authInterceptor: HttpInterceptorFn = (req, next) => {
  if (!req.url.startsWith('/api')) {
    return next(req);
  }

  const authService = (window as any).__sentinelAuthService;
  const token = authService ? authService.getToken() : null;

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
