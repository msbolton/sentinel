export const FEDERATION_PROTOCOL_VERSION = 1;
export const FEDERATION_PORT_DEFAULT = 3100;

export const FederationMessageType = {
  HANDSHAKE: 'fed:handshake',
  HEARTBEAT: 'fed:heartbeat',
  ENTITY_BATCH: 'fed:entity:batch',
  PRESENCE_UPDATE: 'fed:presence:update',
  PRESENCE_REMOVE: 'fed:presence:remove',
} as const;

export type FederationMessageTypeValue = typeof FederationMessageType[keyof typeof FederationMessageType];

export interface FederationMessage {
  type: FederationMessageTypeValue;
  sourceInstanceId: string;
  classificationLevel: string;
  payload: unknown;
}

export interface HandshakePayload {
  instanceId: string;
  displayName: string;
  classificationLevel: string;
  protocolVersion: number;
}

export interface FederatedEntity {
  entityId: string;
  entityType: string;
  latitude: number;
  longitude: number;
  altitude?: number;
  heading?: number;
  speed?: number;
  classification: string;
  source: string;
  timestamp: string;
  metadata?: Record<string, unknown>;
  sourceInstanceId: string;
  sourceInstanceName: string;
}

export interface EntityBatchPayload {
  entities: FederatedEntity[];
}

export interface PresenceUpdatePayload {
  users: PresenceEntry[];
}

export interface PresenceEntry {
  userId: string;
  displayName: string;
  cameraCenter: { lat: number; lon: number };
  zoom: number;
  timestamp: number;
}

export interface PresenceRemovePayload {
  userIds: string[];
}

export const FederationCloseReason = {
  VERSION_MISMATCH: 'version-mismatch',
  POLICY_VIOLATION: 'policy-violation',
  AUTH_FAILURE: 'auth-failure',
  SHUTDOWN: 'shutdown',
} as const;

export type PeerConnectionState = 'connecting' | 'handshaking' | 'connected' | 'stale' | 'disconnected';
