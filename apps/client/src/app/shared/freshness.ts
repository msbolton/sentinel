export type FreshnessState = 'fresh' | 'aging' | 'stale';

export interface Freshness {
  state: FreshnessState;
  /** "12 s ago", "4 min ago", "2 h ago", "3 d ago" or "aged out". */
  label: string;
}

/** Under a minute is fresh, under ten minutes is aging, beyond that stale. */
export const FRESH_UNDER_SEC = 60;
export const AGING_UNDER_SEC = 600;

interface Timed {
  lastSeenAt?: string;
  updatedAt: string;
  ageoutState?: 'LIVE' | 'STALE' | 'AGED_OUT';
}

/**
 * How current an entity's last fix is, judged at `nowMs`. The age-out state
 * from the track service wins over the clock, so a feed that has declared a
 * track stale reads as stale however recent its timestamp.
 */
export function freshnessOf(entity: Timed, nowMs: number): Freshness {
  const seenMs = Date.parse(entity.lastSeenAt ?? entity.updatedAt);
  const ageSec = Number.isFinite(seenMs) ? Math.max(0, Math.floor((nowMs - seenMs) / 1000)) : Infinity;

  if (entity.ageoutState === 'AGED_OUT') {
    return { state: 'stale', label: 'aged out' };
  }

  const state: FreshnessState =
    entity.ageoutState === 'STALE' || ageSec >= AGING_UNDER_SEC ? 'stale' : ageSec >= FRESH_UNDER_SEC ? 'aging' : 'fresh';

  return { state, label: ageLabel(ageSec) };
}

function ageLabel(ageSec: number): string {
  if (!Number.isFinite(ageSec)) return 'unknown';
  if (ageSec < 60) return `${ageSec} s ago`;
  if (ageSec < 3600) return `${Math.floor(ageSec / 60)} min ago`;
  if (ageSec < 86400) return `${Math.floor(ageSec / 3600)} h ago`;
  return `${Math.floor(ageSec / 86400)} d ago`;
}
