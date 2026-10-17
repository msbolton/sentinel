/**
 * Behavioural analysis: what is normal for an entity (its baseline) and
 * where it departs from it (findings), as the analytics service serves them
 * under /api/v1/analytics.
 */

export type FindingKind =
  | 'UNUSUAL_PLACE'
  | 'UNUSUAL_TIME'
  | 'UNUSUAL_MOVEMENT'
  | 'WENT_DARK'
  | 'REAPPEARED';

export const FINDING_KINDS: FindingKind[] = [
  'UNUSUAL_PLACE',
  'UNUSUAL_TIME',
  'UNUSUAL_MOVEMENT',
  'WENT_DARK',
  'REAPPEARED',
];

export const FINDING_KIND_LABELS: Record<FindingKind, string> = {
  UNUSUAL_PLACE: 'Unusual place',
  UNUSUAL_TIME: 'Unusual time',
  UNUSUAL_MOVEMENT: 'Unusual movement',
  WENT_DARK: 'Went dark',
  REAPPEARED: 'Reappeared',
};

export type FindingStatus = 'OPEN' | 'CONFIRMED' | 'DISMISSED' | 'CLOSED';

export const FINDING_STATUSES: FindingStatus[] = ['OPEN', 'CONFIRMED', 'DISMISSED', 'CLOSED'];

export type FindingSeverity = 'INFO' | 'LOW' | 'MEDIUM' | 'HIGH' | 'CRITICAL';

export const FINDING_SEVERITIES: FindingSeverity[] = ['CRITICAL', 'HIGH', 'MEDIUM', 'LOW', 'INFO'];

export interface Finding {
  id: string;
  entityId: string;
  kind: FindingKind;
  severity: FindingSeverity;
  score: number;
  summary: string;
  expected: Record<string, unknown>;
  actual: Record<string, unknown>;
  thresholds: Record<string, unknown>;
  position?: { lat: number; lon: number };
  evidenceStart: string;
  evidenceEnd: string;
  status: FindingStatus;
  reviewedBy?: string;
  reviewedAt?: string;
  alertId?: string;
  classification: string;
  hits: number;
  firstAt: string;
  lastAt: string;
  closedAt?: string;
}

export interface FindingQuery {
  status?: FindingStatus;
  kinds?: FindingKind[];
  severity?: FindingSeverity;
  entityId?: string;
  since?: string;
  page?: number;
  pageSize?: number;
}

export interface EntityPlace {
  clusterIndex: number;
  name?: string;
  locationId?: string;
  lat: number;
  lon: number;
  radiusM: number;
  visits: number;
  medianDwellSec: number;
  pointShare: number;
  firstSeen: string;
  lastSeen: string;
}

export interface BaselineDist {
  mean: number;
  stddev: number;
  p95: number;
  max: number;
  n: number;
}

export interface EntityBaseline {
  entityId: string;
  entityType: string;
  computedAt: string;
  windowStart: string;
  windowEnd: string;
  pointCount: number;
  days: number;
  mature: boolean;
  stats: {
    range: { p50M: number; p95M: number };
    speed: BaselineDist;
    altitude: BaselineDist;
    gap: { p50Sec: number; p95Sec: number };
    params: { solarOffsetHours: number; windowDays: number; [key: string]: unknown };
  };
  /** 168 hour-of-week slots (Sunday 00:00 first), local solar time. */
  hours: number[];
  places: EntityPlace[];
}

export interface AnalyticsStats {
  positions: number;
  findingsOpened: number;
  alertsPublished: number;
  baselines?: number;
  findings?: {
    open: number;
    openBySeverity: Record<string, number>;
    openByKind: Record<string, number>;
    openedLast24h: number;
  };
  [key: string]: unknown;
}

/** A human label for a finding kind. */
export function findingKindLabel(kind: string): string {
  return FINDING_KIND_LABELS[kind as FindingKind] ?? kind;
}

/** Metres as "850 m" or "12.3 km". */
export function formatMetres(m: number | undefined | null): string {
  if (m == null || !Number.isFinite(m)) return '–';
  return m >= 1000 ? `${(m / 1000).toFixed(1)} km` : `${Math.round(m)} m`;
}

/** Seconds as "45 s", "12 min", "3.5 h" or "2.1 d". */
export function formatDuration(sec: number | undefined | null): string {
  if (sec == null || !Number.isFinite(sec)) return '–';
  if (sec < 60) return `${Math.round(sec)} s`;
  if (sec < 3600) return `${Math.round(sec / 60)} min`;
  if (sec < 86400) return `${(sec / 3600).toFixed(1)} h`;
  return `${(sec / 86400).toFixed(1)} d`;
}

/**
 * The expected/actual maps as label-value pairs for display, skipping the
 * nested baseline summary and raw positions.
 */
export function evidencePairs(m: Record<string, unknown> | undefined): { label: string; value: string }[] {
  if (!m) return [];
  const out: { label: string; value: string }[] = [];
  for (const [key, raw] of Object.entries(m)) {
    if (key === 'baseline' || raw == null) continue;
    let value: string;
    if (typeof raw === 'number') {
      if (/M$/.test(key)) value = formatMetres(raw);
      else if (/Sec$/.test(key)) value = formatDuration(raw);
      else if (/[Ss]hare$/.test(key)) value = `${(raw * 100).toFixed(1)}%`;
      else value = Number.isInteger(raw) ? String(raw) : raw.toFixed(2);
    } else if (typeof raw === 'object') {
      const o = raw as Record<string, unknown>;
      if (typeof o['lat'] === 'number' && typeof o['lon'] === 'number') {
        value = `${(o['lat'] as number).toFixed(4)}, ${(o['lon'] as number).toFixed(4)}${o['name'] ? ` (${o['name']})` : ''}`;
      } else {
        value = JSON.stringify(raw);
      }
    } else {
      value = String(raw);
    }
    out.push({ label: key.replace(/([A-Z])/g, ' $1').replace(/^./, (c) => c.toUpperCase()).replace(/ M$/, ''), value });
  }
  return out;
}
