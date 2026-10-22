import { Injectable, OnDestroy } from '@angular/core';
import { HttpInterceptorFn } from '@angular/common/http';
import { BehaviorSubject, Observable, Subject } from 'rxjs';
import { environment } from '../../../environments/environment';

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

  private readonly sessionLostSubject = new Subject<void>();

  /**
   * Emitted when a direct-token session (password grant or guest) can no longer
   * be refreshed. It fires before the session state is cleared, so a listener
   * can still see who the user was (for example via isGuest()).
   */
  readonly sessionLost$: Observable<void> = this.sessionLostSubject.asObservable();

  /** Set when sign-in is unavailable because Keycloak could not be initialised (production only). */
  readonly initError$ = new BehaviorSubject<string | null>(null);

  private markReady!: () => void;

  /**
   * Resolves once init() has finished (whatever its outcome). Anything that
   * decides what to do from the signed-in state, such as the /demo entry,
   * waits for it so it never reads the state before Keycloak has answered.
   */
  readonly ready: Promise<void> = new Promise<void>((resolve) => (this.markReady = resolve));

  async init(): Promise<boolean> {
    try {
      return await this.initKeycloak();
    } finally {
      this.markReady();
    }
  }

  private async initKeycloak(): Promise<boolean> {
    try {
      const KeycloakModule = await import('keycloak-js').catch(() => null);

      if (!KeycloakModule) {
        return this.handleInitFailure('Keycloak JS adapter not available');
      }

      const Keycloak = KeycloakModule.default;
      this.keycloak = new Keycloak({
        url: '/auth',
        realm: 'sentinel',
        clientId: 'sentinel-web',
      });

      const authenticated = await this.keycloak.init({
        pkceMethod: 'S256',
      });

      if (this.directToken) {
        // A direct session (a demo guest) was adopted while Keycloak was still
        // answering; it must not be reset by this result.
        return this.isAuthenticated();
      }

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
      this.keycloak = null;
      return this.handleInitFailure('Keycloak initialization failed', error);
    }
  }

  /**
   * Development falls back to a local operator so the UI works without Keycloak.
   * A production build must never do that: it stays signed out and surfaces an
   * error on the login page.
   */
  private handleInitFailure(reason: string, error?: unknown): boolean {
    if (this.directToken) {
      console.warn(`[Auth] ${reason}; keeping the direct session`, error ?? '');
      return this.isAuthenticated();
    }
    if (environment.production) {
      console.error(`[Auth] ${reason}; sign-in is unavailable`, error ?? '');
      this.authenticatedSubject.next(false);
      this.userProfileSubject.next(null);
      this.initError$.next('The authentication service is unavailable. Try again shortly or contact an administrator.');
      return false;
    }
    console.warn(`[Auth] ${reason}, running in unauthenticated mode`, error ?? '');
    this.setDevelopmentProfile();
    return true;
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
      this.applyTokenResponse(tokenResponse.access_token, tokenResponse.refresh_token, username);
      this.authenticatedSubject.next(true);
      this.setupDirectTokenRefresh(tokenResponse.expires_in);
      return { success: true };
    } catch {
      return { success: false, error: 'Unable to reach authentication server' };
    }
  }

  /**
   * Adopts tokens issued by the gateway (a demo guest session) as a direct
   * session: same in-memory tokens, profile and refresh as loginWithCredentials.
   */
  adoptTokens(t: { accessToken: string; refreshToken: string; expiresIn: number }): void {
    this.applyTokenResponse(t.accessToken, t.refreshToken, 'unknown');
    this.authenticatedSubject.next(true);
    this.setupDirectTokenRefresh(t.expiresIn);
  }

  /** Keeps the tokens in memory and derives the user profile from the access token. */
  private applyTokenResponse(accessToken: string, refreshToken: string, fallbackUsername: string): void {
    // Parse first: a token that cannot be read must not become the current one.
    const parsed = this.parseJwt(accessToken);
    this.directToken = accessToken;
    this.directRefreshToken = refreshToken;
    this.userProfileSubject.next({
      username: parsed.preferred_username ?? fallbackUsername,
      email: parsed.email,
      roles: parsed.realm_access?.roles ?? [],
      classificationLevel: parsed.classification_level ?? 'UNCLASSIFIED',
    });
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
      this.applyTokenResponse(tokenResponse.access_token, tokenResponse.refresh_token, 'unknown');
    } catch {
      console.warn('[Auth] Direct token refresh failed, session expired');
      this.sessionLostSubject.next();
      this.clearTimers();
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

  /** True for a demo visitor: a read-only guest session. */
  isGuest(): boolean {
    return this.hasAnyRole('sentinel-guest');
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
