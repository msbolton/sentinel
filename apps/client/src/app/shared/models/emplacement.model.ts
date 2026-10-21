/** Whether an emplacement stays where it was surveyed. */
export type Mobility = 'FIXED' | 'MOBILE';
export type SensorType = 'RADAR' | 'EO_IR' | 'RF' | 'ACOUSTIC' | 'AIS_RECEIVER' | 'ADSB_RECEIVER' | 'HUMAN' | 'OTHER';
export type AzimuthReference = 'TRUE_NORTH' | 'PLATFORM';

export const SENSOR_TYPES: SensorType[] = ['RADAR', 'EO_IR', 'RF', 'ACOUSTIC', 'AIS_RECEIVER', 'ADSB_RECEIVER', 'HUMAN', 'OTHER'];

export const SENSOR_TYPE_LABELS: Record<SensorType, string> = {
  RADAR: 'Radar', EO_IR: 'EO/IR', RF: 'RF', ACOUSTIC: 'Acoustic',
  AIS_RECEIVER: 'AIS receiver', ADSB_RECEIVER: 'ADS-B receiver', HUMAN: 'Observer', OTHER: 'Other',
};

export interface EmplacementPoint { lat: number; lon: number }

/** Around the boresight; azimuthWidthDeg 360 is omni; all zeros is no coverage. */
export interface EmplacementCoverage {
  minRangeM: number;
  maxRangeM: number;
  azimuthWidthDeg: number;
  elevationMinDeg?: number;
  elevationMaxDeg?: number;
}

export interface EmplacementSigma { rangeM?: number; azimuthDeg?: number; elevationDeg?: number }

/** One emplacement as /api/v1/emplacements returns it. */
export interface Emplacement {
  id: string;
  name: string;
  description: string;
  mobility: Mobility;
  sensorType: SensorType;
  position: EmplacementPoint | null;
  altitude: number | null;
  /** How far above the ground the sensor is mounted (m); a camera with no altitude is placed at the terrain plus this. */
  heightAboveGroundM?: number | null;
  boresightDeg: number;
  azimuthReference: AzimuthReference;
  coverage: EmplacementCoverage;
  defaultSigma: EmplacementSigma;
  feedId: string | null;
  classification: string;
  exercise: boolean;
  active: boolean;
  lastReportAt: string | null;
  /** The interval the emplacement last declared, as its row recorded it. */
  heartbeatIntervalSec?: number | null;
  /** When its row last recorded a heartbeat (lags the owning replica). */
  lastHeartbeatAt?: string | null;
  /** Its heartbeat state per the owning replica; absent when its connection does not heartbeat it. */
  heartbeat?: HeartbeatState;
  /** While offline, when it went silent. */
  silentSince?: string | null;
  createdBy: string;
  createdAt: string;
  updatedAt: string;
  version: number;
}

/** A create or PATCH body: what is left out is left as it is; feedId '' unbinds. */
export interface EmplacementInput {
  name?: string;
  description?: string;
  mobility?: Mobility;
  sensorType?: SensorType;
  position?: EmplacementPoint;
  altitude?: number;
  heightAboveGroundM?: number;
  boresightDeg?: number;
  azimuthReference?: AzimuthReference;
  coverage?: EmplacementCoverage;
  defaultSigma?: EmplacementSigma;
  feedId?: string;
  classification?: string;
  exercise?: boolean;
  active?: boolean;
}

/** The form's state; id and version are null for a new emplacement. */
export interface EmplacementDraft {
  id: string | null;
  /** The version the user opened: a save claims it in If-Match, so a newer one on the server answers 412. */
  version: number | null;
  name: string;
  description: string;
  mobility: Mobility;
  sensorType: SensorType;
  position: EmplacementPoint | null;
  altitude: number | null;
  heightAboveGroundM: number | null;
  boresightDeg: number;
  azimuthReference: AzimuthReference;
  coverage: EmplacementCoverage;
  defaultSigma: EmplacementSigma;
  feedId: string | null;
  classification: string;
  exercise: boolean;
  active: boolean;
}

export function newDraft(): EmplacementDraft {
  return {
    id: null, version: null, name: '', description: '', mobility: 'FIXED', sensorType: 'RADAR', position: null, altitude: null, heightAboveGroundM: null,
    boresightDeg: 0, azimuthReference: 'TRUE_NORTH', coverage: { minRangeM: 0, maxRangeM: 50_000, azimuthWidthDeg: 120 },
    defaultSigma: {}, feedId: null, classification: 'UNCLASSIFIED', exercise: false, active: true,
  };
}

export function draftOf(e: Emplacement): EmplacementDraft {
  return {
    id: e.id, version: e.version, name: e.name, description: e.description, mobility: e.mobility, sensorType: e.sensorType,
    position: e.position ? { ...e.position } : null, altitude: e.altitude, heightAboveGroundM: e.heightAboveGroundM ?? null, boresightDeg: e.boresightDeg,
    azimuthReference: e.azimuthReference, coverage: { ...e.coverage }, defaultSigma: { ...e.defaultSigma },
    feedId: e.feedId, classification: e.classification, exercise: e.exercise, active: e.active,
  };
}

/** The body the form saves. A coverage with no reach is sent as no coverage. */
export function inputOf(d: EmplacementDraft): EmplacementInput {
  const coverage = d.coverage.maxRangeM > 0 ? d.coverage : { minRangeM: 0, maxRangeM: 0, azimuthWidthDeg: 0 };
  const input: EmplacementInput = {
    name: d.name, description: d.description, mobility: d.mobility, sensorType: d.sensorType,
    boresightDeg: d.boresightDeg, azimuthReference: d.azimuthReference, coverage, defaultSigma: d.defaultSigma,
    feedId: d.feedId ?? '', classification: d.classification, exercise: d.exercise, active: d.active,
  };
  if (d.position) input.position = d.position;
  if (d.altitude != null) input.altitude = d.altitude;
  if (d.heightAboveGroundM != null) input.heightAboveGroundM = d.heightAboveGroundM;
  return input;
}

/** The emplacement an entity stands for (its EMPLACEMENT identity), or null. */
export function emplacementIdOf(entity: { identities?: { type: string; value: string }[] | null }): string | null {
  return entity.identities?.find((i) => i?.type === 'EMPLACEMENT')?.value ?? null;
}

export function coverageSummary(c: EmplacementCoverage): string {
  if (!(c.maxRangeM > 0)) return 'no coverage';
  const reach = c.maxRangeM >= 1000 ? `${Math.round(c.maxRangeM / 100) / 10} km` : `${Math.round(c.maxRangeM)} m`;
  return `${c.azimuthWidthDeg >= 360 ? 'omni' : `${Math.round(c.azimuthWidthDeg)}°`} · ${reach}`;
}

/** What an emplacement's heartbeats say of it (the ingest service's states). */
export type HeartbeatState = 'online' | 'offline' | 'never' | 'pending';

/** Missed intervals after which an emplacement is offline. */
export const MISSED_HEARTBEATS = 3;
/** The interval of a heartbeat that declares none. */
export const DEFAULT_HEARTBEAT_INTERVAL_SEC = 10;

/** A site's heartbeat as the map and the panels show it. */
export interface SiteHeartbeat {
  state: HeartbeatState;
  intervalSec: number;
  lastHeartbeatAt: string | null;
  /** When it went silent (its last heartbeat); null while online, pending or never heard from. */
  silentSince: string | null;
}

/** One entry of a connection's status detail.emplacements. */
export interface EmplacementLiveness {
  id: string;
  name: string;
  classification: string;
  state: HeartbeatState;
  lastHeartbeatAt?: string;
  intervalSec: number;
}

export interface HeartbeatBadge {
  label: string;
  tone: 'online' | 'offline' | 'pending';
}

const HEARTBEAT_STATES: HeartbeatState[] = ['online', 'offline', 'never', 'pending'];

function lapsed(lastIso: string, intervalSec: number, nowMs: number): boolean {
  const t = Date.parse(lastIso);
  return Number.isNaN(t) || nowMs >= t + MISSED_HEARTBEATS * intervalSec * 1000;
}

/**
 * The heartbeat a site entity carries (platformData.generic.fields, which its
 * connection writes on every publish), judged at nowMs, the server's clock. A
 * site published online whose last heartbeat is three intervals old is
 * offline now, before any publish says so. Null for an entity that carries
 * none: a site whose connection does not heartbeat it.
 */
export function siteHeartbeatOf(
  entity: { platformData?: { generic?: { fields?: Record<string, string> } } } | null | undefined,
  nowMs: number,
): SiteHeartbeat | null {
  const f = entity?.platformData?.generic?.fields;
  if (!f || f['heartbeatIntervalSec'] === undefined) return null;
  const intervalSec = Number(f['heartbeatIntervalSec']) || DEFAULT_HEARTBEAT_INTERVAL_SEC;
  const last = f['lastHeartbeatAt'] || null;
  const said = HEARTBEAT_STATES.includes(f['heartbeat'] as HeartbeatState) ? (f['heartbeat'] as HeartbeatState) : null;
  if (last && !lapsed(last, intervalSec, nowMs)) return { state: 'online', intervalSec, lastHeartbeatAt: last, silentSince: null };
  if (said === 'pending') return { state: 'pending', intervalSec, lastHeartbeatAt: last, silentSince: null };
  if (last || f['silentSince']) return { state: 'offline', intervalSec, lastHeartbeatAt: last, silentSince: f['silentSince'] || last };
  return { state: 'never', intervalSec, lastHeartbeatAt: null, silentSince: null };
}

/** A site's heartbeat from its REST row, before its entity is on the stream; null for a row with none. */
export function rowHeartbeatOf(e: Pick<Emplacement, 'heartbeat' | 'silentSince' | 'heartbeatIntervalSec' | 'lastHeartbeatAt'>): SiteHeartbeat | null {
  if (!e.heartbeat) return null;
  return {
    state: e.heartbeat,
    intervalSec: e.heartbeatIntervalSec ?? DEFAULT_HEARTBEAT_INTERVAL_SEC,
    lastHeartbeatAt: e.lastHeartbeatAt ?? null,
    silentSince: e.heartbeat === 'offline' ? (e.silentSince ?? e.lastHeartbeatAt ?? null) : null,
  };
}

/** A connection's detail.emplacements, by emplacement id. */
export function livenessOf(detail: Record<string, unknown> | null | undefined): Map<string, EmplacementLiveness> {
  const out = new Map<string, EmplacementLiveness>();
  const list = detail?.['emplacements'];
  if (!Array.isArray(list)) return out;
  for (const x of list) {
    if (x && typeof x === 'object' && typeof (x as EmplacementLiveness).id === 'string') out.set((x as EmplacementLiveness).id, x as EmplacementLiveness);
  }
  return out;
}

/** "2 of 3 sites online" for a connection that reports its emplacements' heartbeats, else null. */
export function sitesSummaryOf(detail: Record<string, unknown> | null | undefined): string | null {
  const live = [...livenessOf(detail).values()];
  if (live.length === 0) return null;
  const online = live.filter((l) => l.state === 'online').length;
  return `${online} of ${live.length} site${live.length === 1 ? '' : 's'} online`;
}

/**
 * An emplacement's badge in its connection's Emplacements section. It is
 * taken from the connection's status (the owning replica), else from the row.
 * Null when neither has a heartbeat: the connection does not heartbeat its sites.
 */
export function heartbeatBadgeOf(
  e: Pick<Emplacement, 'heartbeat' | 'silentSince' | 'lastHeartbeatAt'>,
  live: EmplacementLiveness | undefined,
  nowMs: number,
): HeartbeatBadge | null {
  const state = live?.state ?? e.heartbeat;
  const last = live ? (live.lastHeartbeatAt ?? null) : (e.silentSince ?? e.lastHeartbeatAt ?? null);
  switch (state) {
    case 'online':
      return { label: 'online', tone: 'online' };
    case 'offline':
      return { label: last ? `silent since ${formatZulu(last, nowMs)}` : 'silent', tone: 'offline' };
    case 'pending':
      return { label: 'awaiting first heartbeat', tone: 'pending' };
    case 'never':
      return { label: 'awaiting first heartbeat', tone: 'offline' };
    default:
      return null;
  }
}

/** "14:02Z" for a time today (UTC), "10-19 14:02Z" for another day; '' for a time that does not parse. */
export function formatZulu(iso: string, nowMs: number = Date.now()): string {
  const t = new Date(iso);
  if (Number.isNaN(t.getTime())) return '';
  const pad = (n: number) => String(n).padStart(2, '0');
  const hm = `${pad(t.getUTCHours())}:${pad(t.getUTCMinutes())}Z`;
  const now = new Date(nowMs);
  const sameDay = t.getUTCFullYear() === now.getUTCFullYear() && t.getUTCMonth() === now.getUTCMonth() && t.getUTCDate() === now.getUTCDate();
  return sameDay ? hm : `${pad(t.getUTCMonth() + 1)}-${pad(t.getUTCDate())} ${hm}`;
}
