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

/** The key a feed's entities are grouped under on the map: the feed id, else the legacy source. */
export function layerKeyOf(entity: { feedId?: string | null; source?: string | null }): string {
  return entity.feedId || entity.source || 'unknown';
}
