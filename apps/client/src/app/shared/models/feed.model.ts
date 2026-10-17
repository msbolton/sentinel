/**
 * Feed resources served by the ingest service through the gateway
 * (`GET /api/v1/feeds`, `GET /api/v1/drivers`).
 */

export type FeedHealthLevel = 'healthy' | 'warn' | 'critical' | 'unknown';
export type FeedState =
  | 'stopped'
  | 'starting'
  | 'running'
  | 'idle'
  | 'degraded'
  | 'error'
  | 'unassigned';

export interface FeedStatus {
  owner: string;
  state: FeedState;
  health: FeedHealthLevel;
  lastError?: string;
  lastMessageAt: string | null;
  lastSuccessAt: string | null;
  messagesTotal: number;
  entitiesLast: number;
  errorsTotal: number;
  appliedVersion: number;
  /** Driver-specific detail: idle, creditsRemaining, effectiveIntervalSec, leaseToken, connections… */
  detail: Record<string, unknown>;
  updatedAt: string | null;
}

export interface Feed {
  id: string;
  name: string;
  description: string;
  driver: string;
  /** EntitySource label the feed's observations carry (OPENSKY, ADS_B, AIS…). */
  source: string;
  builtin: boolean;
  enabled: boolean;
  version: number;
  config: Record<string, unknown>;
  /** Secret references by field, e.g. { clientSecret: 'env:OPENSKY_CLIENT_SECRET', clientCert: 'file:/etc/sentinel/tak/client.pem' }. */
  secretRefs: Record<string, string>;
  capabilities: string[];
  /** Effective direction: the driver's, narrowed by config.direction. Absent on older servers. */
  direction?: FeedDirection;
  createdBy?: string;
  createdAt: string;
  updatedAt: string;
  status: FeedStatus;
}

/** Body of POST /api/v1/feeds. */
export interface CreateFeedRequest {
  id?: string;
  name: string;
  description?: string;
  driver: string;
  config: Record<string, unknown>;
  secretRefs?: Record<string, string>;
  enabled?: boolean;
}

/** Body of PUT /api/v1/feeds/{id}: every mutable field. */
export interface ReplaceFeedRequest {
  name: string;
  description?: string;
  config: Record<string, unknown>;
  secretRefs: Record<string, string>;
  enabled: boolean;
}

/** The subset of JSON Schema the driver config schemas use. */
export interface JsonSchema {
  $schema?: string;
  $id?: string;
  type?: 'object' | 'string' | 'number' | 'integer' | 'boolean' | 'array';
  title?: string;
  description?: string;
  properties?: Record<string, JsonSchema>;
  required?: string[];
  items?: JsonSchema;
  enum?: Array<string | number>;
  const?: string | number | boolean;
  default?: unknown;
  minimum?: number;
  maximum?: number;
  exclusiveMinimum?: number;
  minLength?: number;
  minItems?: number;
  pattern?: string;
  format?: string;
  /** Marks a property whose value is a secret reference and belongs in secretRefs. */
  'x-secret'?: boolean;
  allOf?: JsonSchema[];
  if?: JsonSchema;
  then?: JsonSchema;
}

/** Which way a feed moves data: a source (in), a sink (out), or both over one connection. */
export type FeedDirection = 'in' | 'out' | 'both';

/** A lat/lon box; west > east wraps the antimeridian. */
export interface AreaBox {
  north: number;
  south: number;
  east: number;
  west: number;
}

/** Longest list of areas a policy may carry. */
export const POLICY_MAX_BOXES = 16;

export type AlertSeverity = 'INFO' | 'LOW' | 'MEDIUM' | 'HIGH' | 'CRITICAL';

/**
 * What a sending connection (`out`/`both`) is allowed to deliver:
 * `feed.config.policy`. Empty lists mean no restriction.
 */
export interface ConnectionPolicy {
  entityTypes?: string[];
  /** Feed ids whose entities may leave; empty = every feed. */
  feeds?: string[];
  boxes?: AreaBox[];
  classificationCeiling?: string;
  /** Whether entities that themselves arrived from another connection may go out again. */
  reexport?: boolean;
  alerts?: {
    enabled?: boolean;
    minSeverity?: AlertSeverity;
    types?: string[];
  };
}

/** Delivery tuning for a sending connection: `feed.config.outbound`. */
export interface OutboundTuning {
  queueMax?: number;
  perEntityMinSec?: number;
  maxEventsPerSec?: number;
  maxLagSec?: number;
  policyExitGraceSec?: number;
}

/** One line for a sending connection's policy: "AIRCRAFT, VESSEL · 2 areas · ≤ SECRET". */
export function policySummaryOf(policy: ConnectionPolicy | null | undefined): string {
  const p = policy ?? {};
  const types = (p.entityTypes ?? []).filter(Boolean);
  const boxes = p.boxes ?? [];
  const parts = [
    types.length === 0 ? 'all types' : types.join(', '),
    boxes.length === 0 ? 'everywhere' : `${boxes.length} area${boxes.length === 1 ? '' : 's'}`,
    `≤ ${p.classificationCeiling || 'UNCLASSIFIED'}`,
  ];
  if ((p.feeds ?? []).length > 0) {
    parts.push(`${p.feeds!.length} feed${p.feeds!.length === 1 ? '' : 's'}`);
  }
  if (p.alerts?.enabled) {
    parts.push('alerts');
  }
  return parts.join(' · ');
}

/** The sink-side numbers in `status.detail` (the outbound pump and the driver). */
export interface SinkDetail {
  delivered?: number;
  deliveryErrors?: number;
  denied?: number;
  queueDepth?: number;
  tracked?: number;
  lastDeliveredAt?: string;
  remotePeer?: string;
  tls?: boolean;
}

/**
 * Picks the delivery figures out of a connection's status detail. The
 * outbound pump nests its counters under `outbound`; the driver puts
 * `remotePeer` and `tls` at the top level.
 */
export function sinkDetailOf(detail: Record<string, unknown> | null | undefined): SinkDetail {
  const out: SinkDetail = {};
  if (!detail || typeof detail !== 'object') {
    return out;
  }
  const outbound = isRecord(detail['outbound']) ? detail['outbound'] : {};
  const num = (key: string): number | undefined => {
    const v = outbound[key] ?? detail[key];
    return typeof v === 'number' && Number.isFinite(v) ? v : undefined;
  };
  const str = (key: string): string | undefined => {
    const v = outbound[key] ?? detail[key];
    return typeof v === 'string' && v !== '' ? v : undefined;
  };
  const delivered = num('delivered');
  const deliveryErrors = num('deliveryErrors');
  const denied = num('denied');
  const queueDepth = num('queueDepth');
  const tracked = num('tracked');
  const lastDeliveredAt = str('lastDeliveredAt');
  const remotePeer = str('remotePeer');
  if (delivered !== undefined) out.delivered = delivered;
  if (deliveryErrors !== undefined) out.deliveryErrors = deliveryErrors;
  if (denied !== undefined) out.denied = denied;
  if (queueDepth !== undefined) out.queueDepth = queueDepth;
  if (tracked !== undefined) out.tracked = tracked;
  if (lastDeliveredAt !== undefined) out.lastDeliveredAt = lastDeliveredAt;
  if (remotePeer !== undefined) out.remotePeer = remotePeer;
  if (typeof detail['tls'] === 'boolean') out.tls = detail['tls'];
  return out;
}

function isRecord(v: unknown): v is Record<string, unknown> {
  return typeof v === 'object' && v !== null && !Array.isArray(v);
}

export interface FeedDriver {
  id: string;
  name: string;
  description: string;
  source: string;
  /** Absent on older servers; treat as 'in'. */
  direction?: FeedDirection;
  capabilities: string[];
  configSchema: JsonSchema;
  /** secretRefs keys the driver expects (clientId, clientSecret, password…). */
  secretFields: string[];
}

/** One validation problem from a 422 response. */
export interface FeedProblem {
  path: string;
  message: string;
}

export interface FeedErrorResponse {
  error: string;
  details?: FeedProblem[];
}

/** A feed's direction, with older servers (no field) treated as a source. */
export function directionOf(feed: { direction?: FeedDirection } | null | undefined): FeedDirection {
  return feed?.direction ?? 'in';
}

/** True when the connection sends entities out (out or both). */
export function isSending(feed: { direction?: FeedDirection } | null | undefined): boolean {
  const d = directionOf(feed);
  return d === 'out' || d === 'both';
}

/** The key a feed's entities are grouped under on the map: the feed id, else the legacy source. */
export function layerKeyOf(entity: { feedId?: string | null; source?: string | null }): string {
  return entity.feedId || entity.source || 'unknown';
}
