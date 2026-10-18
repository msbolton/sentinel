/**
 * How AIS vessel detail reads to an operator: ship type categories, the
 * navigational status in words, and the voyage ETA.
 */

export type VesselCategory = 'cargo' | 'tanker' | 'passenger' | 'fishing' | 'highSpeed' | 'pleasure' | 'special' | 'other';

/** Pin colours per vessel category, the convention of common AIS plotters. */
export const VESSEL_CATEGORY_COLORS: Record<VesselCategory, string> = {
  cargo: '#22c55e',
  tanker: '#ef4444',
  passenger: '#3b82f6',
  fishing: '#f97316',
  highSpeed: '#eab308',
  pleasure: '#a855f7',
  special: '#14b8a6',
  other: '#94a3b8',
};

export const VESSEL_CATEGORY_LABELS: Record<VesselCategory, string> = {
  cargo: 'Cargo',
  tanker: 'Tanker',
  passenger: 'Passenger',
  fishing: 'Fishing',
  highSpeed: 'High speed',
  pleasure: 'Pleasure / sailing',
  special: 'Service / special',
  other: 'Other',
};

/** The category of an AIS ship type code (ITU-R M.1371 table 53). */
export function vesselCategory(shipType: number | null | undefined): VesselCategory {
  const t = shipType ?? 0;
  if (t >= 70 && t <= 79) return 'cargo';
  if (t >= 80 && t <= 89) return 'tanker';
  if (t >= 60 && t <= 69) return 'passenger';
  if (t === 30) return 'fishing';
  if (t >= 40 && t <= 49) return 'highSpeed';
  if (t === 36 || t === 37) return 'pleasure';
  if ((t >= 31 && t <= 35) || (t >= 50 && t <= 59)) return 'special';
  return 'other';
}

const NAV_STATUS_LABELS: Record<string, string> = {
  UNDER_WAY_USING_ENGINE: 'Under way (engine)',
  AT_ANCHOR: 'At anchor',
  NOT_UNDER_COMMAND: 'Not under command',
  RESTRICTED_MANOEUVRABILITY: 'Restricted manoeuvrability',
  CONSTRAINED_BY_DRAUGHT: 'Constrained by draught',
  MOORED: 'Moored',
  AGROUND: 'Aground',
  ENGAGED_IN_FISHING: 'Fishing',
  UNDER_WAY_SAILING: 'Under way (sailing)',
  AIS_SART: 'AIS-SART (distress)',
};

/** The navigational status in words, or null when it is unknown. */
export function navStatusLabel(status: string | null | undefined): string | null {
  if (!status || status === 'UNKNOWN') return null;
  return NAV_STATUS_LABELS[status] ?? status.toLowerCase().replace(/_/g, ' ');
}

const MONTHS = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];

/**
 * The voyage ETA as "18 Oct 14:30 UTC". AIS carries month, day, hour and
 * minute in UTC with no year ("MM-DDTHH:MM"); a full timestamp from
 * another source is shown the same way.
 */
export function formatAisEta(eta: string | null | undefined): string | null {
  if (!eta) return null;
  const m = /^(\d{2})-(\d{2})T(\d{2}):(\d{2})$/.exec(eta);
  if (m) {
    const month = Number(m[1]);
    if (month < 1 || month > 12) return null;
    return `${Number(m[2])} ${MONTHS[month - 1]} ${m[3]}:${m[4]} UTC`;
  }
  const t = new Date(eta);
  if (Number.isNaN(t.getTime())) return eta;
  const pad = (n: number) => String(n).padStart(2, '0');
  return `${t.getUTCDate()} ${MONTHS[t.getUTCMonth()]} ${pad(t.getUTCHours())}:${pad(t.getUTCMinutes())} UTC`;
}
