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
}

export enum EntityType {
  UNKNOWN = 'UNKNOWN',
  PERSON = 'PERSON',
  VEHICLE = 'VEHICLE',
  VESSEL = 'VESSEL',
  AIRCRAFT = 'AIRCRAFT',
  FACILITY = 'FACILITY',
  EQUIPMENT = 'EQUIPMENT',
  UNIT = 'UNIT',
  SIGNAL = 'SIGNAL',
  CYBER = 'CYBER',
}

export enum EntitySource {
  HUMINT = 'HUMINT',
  SIGINT = 'SIGINT',
  GEOINT = 'GEOINT',
  OSINT = 'OSINT',
  MASINT = 'MASINT',
  CYBER = 'CYBER',
  MANUAL = 'MANUAL',
}

export enum Classification {
  UNCLASSIFIED = 'UNCLASSIFIED',
  CONFIDENTIAL = 'CONFIDENTIAL',
  SECRET = 'SECRET',
  TOP_SECRET = 'TOP_SECRET',
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
