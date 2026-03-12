import { EntityType, EntitySource, Classification } from '@sentinel/proto-gen';
import {
  Affiliation,
  DamageAssessment,
  KinematicState,
  OperationalStatus,
  Orientation,
  PlatformData,
  TrackEnvironment,
} from './platform-data.model';
export { EntityType, EntitySource, Classification };
export { Affiliation, DamageAssessment, OperationalStatus, TrackEnvironment };

export interface Entity {
  id: string;
  entityType: EntityType;
  name: string;
  description?: string;
  source: EntitySource;
  classification: Classification;
  position?: {
    latitude: number;
    longitude: number;
    altitude?: number;
  };
  heading?: number;
  speedKnots?: number;
  course?: number;
  milStd2525dSymbol?: string;
  metadata: Record<string, string>;
  affiliations: string[];
  createdAt: string;
  updatedAt: string;
  lastSeenAt?: string;

  // Identity
  affiliation?: Affiliation;
  sourceEntityId?: string;
  countryOfOrigin?: string;

  // Track context
  trackEnvironment?: TrackEnvironment;

  // Operational status
  operationalStatus?: OperationalStatus;
  damageAssessment?: DamageAssessment;

  // Physical dimensions
  dimensions?: {
    length?: number;
    width?: number;
    height?: number;
  };

  // Kinematics
  orientation?: Orientation;
  kinematics?: KinematicState;

  // Protocol-specific data
  platformData?: PlatformData;

  // Measurement quality
  circularError?: number;

  // Federation metadata (set for entities received from peer instances)
  sourceInstanceId?: string;
  sourceInstanceName?: string;
}

export interface EntityEvent {
  type: 'created' | 'updated' | 'deleted';
  entity: Entity;
  timestamp: string;
}

export interface EntityQuery {
  entityType?: EntityType;
  source?: EntitySource;
  classification?: Classification;
  search?: string;
  north?: number;
  south?: number;
  east?: number;
  west?: number;
  limit?: number;
  offset?: number;
}

export interface PaginatedResponse<T> {
  data: T[];
  total: number;
  limit: number;
  offset: number;
}
