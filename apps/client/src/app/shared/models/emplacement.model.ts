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

export type BearingReason = 'air_target' | 'no_ground_intersection' | 'terrain_unavailable' | 'no_origin_altitude';

/** One bearing line a camera site carries: a direction, not a position. Azimuth is from true north. */
export interface SiteBearing {
  trackId?: string;
  azimuthDeg: number;
  elevationDeg: number;
  sigmaAzDeg?: number;
  sigmaElDeg?: number;
  lengthM: number;
  reason: BearingReason;
  classification?: string;
  confidence?: number;
  band?: string;
  /** RFC3339. */
  observedAt: string;
}

/** What the server uses when a camera feed sets no bearingTtlSec. */
export const DEFAULT_BEARING_TTL_SEC = 10;

const BEARING_REASONS: BearingReason[] = ['air_target', 'no_ground_intersection', 'terrain_unavailable', 'no_origin_altitude'];

type FieldsCarrier = { platformData?: { generic?: { fields?: Record<string, string> } } } | null | undefined;

/** How long a site's bearings live, in seconds: its bearingTtlSec, else the default. */
export function bearingTtlOf(entity: FieldsCarrier): number {
  const ttl = Number(entity?.platformData?.generic?.fields?.['bearingTtlSec']);
  return Number.isFinite(ttl) && ttl > 0 ? ttl : DEFAULT_BEARING_TTL_SEC;
}

/** Whether a bearing observed at observedAt has outlived ttlSec by nowMs. An unreadable time is expired. */
export function bearingExpired(observedAt: string, ttlSec: number, nowMs: number): boolean {
  const t = Date.parse(observedAt);
  return Number.isNaN(t) || nowMs >= t + ttlSec * 1000;
}

function asBearing(x: unknown): SiteBearing | null {
  if (!x || typeof x !== 'object') return null;
  const b = x as SiteBearing;
  if (!Number.isFinite(b.azimuthDeg) || !Number.isFinite(b.elevationDeg) || !Number.isFinite(b.lengthM)) return null;
  if (!BEARING_REASONS.includes(b.reason) || typeof b.observedAt !== 'string' || Number.isNaN(Date.parse(b.observedAt))) return null;
  return b;
}

/**
 * The live bearings a camera site entity carries (platformData.generic.fields
 * "bearings", a JSON array), judged at nowMs, the server's clock. A bearing
 * past observedAt + bearingTtlSec is dropped here, since a site need not
 * republish at the moment one expires. Empty for an entity with none, bad
 * JSON, or entries that are not bearings.
 */
export function bearingsOf(entity: FieldsCarrier, nowMs: number): SiteBearing[] {
  const raw = entity?.platformData?.generic?.fields?.['bearings'];
  if (!raw) return [];
  let list: unknown;
  try {
    list = JSON.parse(raw);
  } catch {
    return [];
  }
  if (!Array.isArray(list)) return [];
  const ttlSec = bearingTtlOf(entity);
  const out: SiteBearing[] = [];
  for (const x of list) {
    const b = asBearing(x);
    if (b && !bearingExpired(b.observedAt, ttlSec, nowMs)) out.push(b);
  }
  return out;
}

const WGS84_A = 6378137;
const WGS84_F = 1 / 298.257223563;
const WGS84_E2 = WGS84_F * (2 - WGS84_F);
const RAD = Math.PI / 180;

/**
 * The far end of a bearing line: length along the azimuth (true north) and
 * elevation from the origin, in the origin's local east-north-up frame, taken
 * to geodetic coordinates on the WGS84 ellipsoid. The line is straight, so
 * over a long range its end rises above the ellipsoid by the earth's curvature.
 */
export function bearingEnd(
  origin: { lat: number; lon: number; alt: number },
  b: Pick<SiteBearing, 'azimuthDeg' | 'elevationDeg' | 'lengthM'>,
): { lat: number; lon: number; alt: number } {
  const lat = origin.lat * RAD;
  const lon = origin.lon * RAD;
  const az = b.azimuthDeg * RAD;
  const el = b.elevationDeg * RAD;
  const east = b.lengthM * Math.cos(el) * Math.sin(az);
  const north = b.lengthM * Math.cos(el) * Math.cos(az);
  const up = b.lengthM * Math.sin(el);

  const sinLat = Math.sin(lat);
  const cosLat = Math.cos(lat);
  const sinLon = Math.sin(lon);
  const cosLon = Math.cos(lon);
  const n = WGS84_A / Math.sqrt(1 - WGS84_E2 * sinLat * sinLat);
  const x = (n + origin.alt) * cosLat * cosLon + -sinLon * east - sinLat * cosLon * north + cosLat * cosLon * up;
  const y = (n + origin.alt) * cosLat * sinLon + cosLon * east - sinLat * sinLon * north + cosLat * sinLon * up;
  const z = (n * (1 - WGS84_E2) + origin.alt) * sinLat + cosLat * north + sinLat * up;

  // ECEF to geodetic by fixed-point iteration; it converges to well under a millimetre in a few steps.
  const p = Math.hypot(x, y);
  let phi = Math.atan2(z, p * (1 - WGS84_E2));
  let h = 0;
  for (let i = 0; i < 6; i++) {
    const s = Math.sin(phi);
    const nn = WGS84_A / Math.sqrt(1 - WGS84_E2 * s * s);
    h = Math.abs(phi) < Math.PI / 4 ? p / Math.cos(phi) - nn : z / s - nn * (1 - WGS84_E2); // the z form near the poles
    phi = Math.atan2(z, p * (1 - (WGS84_E2 * nn) / (nn + h)));
  }
  return { lat: phi / RAD, lon: Math.atan2(y, x) / RAD, alt: h };
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
