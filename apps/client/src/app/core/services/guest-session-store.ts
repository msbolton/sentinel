/**
 * A demo guest's session, kept in localStorage so a reload, a tab the phone
 * evicted or a second tab carries on as the same guest rather than landing
 * on the login form (and starting another guest from /demo). Only ever a
 * guest's: the throwaway, read-only refresh token and what the demo needs to
 * pick up again. A signed-in user's tokens are never stored. Every access is
 * guarded, since storage can be missing or throw (private mode, blocked site
 * data); without it a reload simply starts a new guest.
 */
export interface StoredGuestSession {
  refreshToken?: string;
  guestId?: string;
  /** ISO-8601 time the gateway ends the guest. */
  expiresAt?: string;
  visitorsFeedId?: string;
  ownEmplacementId?: string;
}

const SESSION_KEY = 'sentinel-demo-session';
/** Set while this browser has been a demo visitor, so the guards send it back to /demo, not /login. */
const VISITOR_KEY = 'sentinel-demo-visitor';

const FIELDS: (keyof StoredGuestSession)[] = ['refreshToken', 'guestId', 'expiresAt', 'visitorsFeedId', 'ownEmplacementId'];

export function loadGuestSession(): StoredGuestSession | null {
  try {
    const raw = localStorage.getItem(SESSION_KEY);
    if (!raw) return null;
    const parsed: unknown = JSON.parse(raw);
    if (!parsed || typeof parsed !== 'object') return null;
    const out: StoredGuestSession = {};
    for (const k of FIELDS) {
      const v = (parsed as Record<string, unknown>)[k];
      if (typeof v === 'string' && v !== '') out[k] = v;
    }
    return out;
  } catch {
    return null;
  }
}

/** Merges `patch` into the stored session (or, with replace, starts it afresh) and marks the browser a demo visitor. */
export function saveGuestSession(patch: StoredGuestSession, opts: { replace?: boolean } = {}): void {
  try {
    const next = { ...(opts.replace ? {} : (loadGuestSession() ?? {})), ...patch };
    localStorage.setItem(SESSION_KEY, JSON.stringify(next));
    localStorage.setItem(VISITOR_KEY, '1');
  } catch {
    // No storage: the session lives in this tab only.
  }
}

/** Drops the stored session; the browser is still remembered as a demo visitor. */
export function clearGuestSession(): void {
  try {
    localStorage.removeItem(SESSION_KEY);
  } catch {
    // Nothing to clear.
  }
}

export function wasDemoVisitor(): boolean {
  try {
    return localStorage.getItem(VISITOR_KEY) === '1';
  } catch {
    return false;
  }
}

/** Drops the stored session and the visitor mark (a sign-out, or a real user signing in). */
export function forgetDemoVisitor(): void {
  try {
    localStorage.removeItem(SESSION_KEY);
    localStorage.removeItem(VISITOR_KEY);
  } catch {
    // Nothing to forget.
  }
}
