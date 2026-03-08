export interface Coordinate {
  latitude: number;
  longitude: number;
  altitudeMeters?: number;
}

export interface BoundingBox {
  north: number;
  south: number;
  east: number;
  west: number;
}

export interface PaginationRequest {
  page: number;
  pageSize: number;
}

export interface PaginationResponse {
  page: number;
  pageSize: number;
  total: number;
  totalPages: number;
}

// Canonical definition in @sentinel/proto-gen (proto/common.proto)
export enum Classification {
  UNCLASSIFIED = 'UNCLASSIFIED',
  CONFIDENTIAL = 'CONFIDENTIAL',
  SECRET = 'SECRET',
  TOP_SECRET = 'TOP_SECRET',
}

export const CLASSIFICATION_LEVELS: Record<Classification, number> = {
  [Classification.UNCLASSIFIED]: 0,
  [Classification.CONFIDENTIAL]: 1,
  [Classification.SECRET]: 2,
  [Classification.TOP_SECRET]: 3,
};

export enum TrackEnvironment {
  AIR = 'AIR',
  SEA_SURFACE = 'SEA_SURFACE',
  SUBSURFACE = 'SUBSURFACE',
  GROUND = 'GROUND',
  SPACE = 'SPACE',
  UNKNOWN = 'UNKNOWN',
}

export enum TrackProcessingState {
  LIVE = 'LIVE',
  PREDICTED = 'PREDICTED',
  DEAD_RECKONED = 'DEAD_RECKONED',
  HYPOTHESIZED = 'HYPOTHESIZED',
  HISTORICAL = 'HISTORICAL',
}

export enum NavigationalStatus {
  UNDER_WAY_USING_ENGINE = 'UNDER_WAY_USING_ENGINE',
  AT_ANCHOR = 'AT_ANCHOR',
  NOT_UNDER_COMMAND = 'NOT_UNDER_COMMAND',
  RESTRICTED_MANOEUVRABILITY = 'RESTRICTED_MANOEUVRABILITY',
  CONSTRAINED_BY_DRAUGHT = 'CONSTRAINED_BY_DRAUGHT',
  MOORED = 'MOORED',
  AGROUND = 'AGROUND',
  ENGAGED_IN_FISHING = 'ENGAGED_IN_FISHING',
  UNDER_WAY_SAILING = 'UNDER_WAY_SAILING',
  AIS_SART = 'AIS_SART',
  UNKNOWN = 'UNKNOWN',
}

export enum OperationalStatus {
  OPERATIONAL = 'OPERATIONAL',
  DEGRADED = 'DEGRADED',
  DAMAGED = 'DAMAGED',
  DESTROYED = 'DESTROYED',
  INACTIVE = 'INACTIVE',
  UNKNOWN = 'UNKNOWN',
}

export enum DamageAssessment {
  NONE = 'NONE',
  LIGHT = 'LIGHT',
  MODERATE = 'MODERATE',
  HEAVY = 'HEAVY',
  DESTROYED = 'DESTROYED',
  UNKNOWN = 'UNKNOWN',
}
