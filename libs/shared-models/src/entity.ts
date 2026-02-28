import { BoundingBox, Classification, Coordinate, PaginationRequest, PaginationResponse } from './common';

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

export interface Entity {
  id: string;
  entityType: EntityType;
  name: string;
  description?: string;
  source: EntitySource;
  classification: Classification;
  position?: Coordinate;
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

export enum EntityEventType {
  CREATED = 'CREATED',
  UPDATED = 'UPDATED',
  DELETED = 'DELETED',
  POSITION_UPDATE = 'POSITION_UPDATE',
}

export interface EntityEvent {
  eventId: string;
  entity: Entity;
  eventType: EntityEventType;
  timestamp: string;
}

export interface CreateEntityRequest {
  entityType: EntityType;
  name: string;
  description?: string;
  source: EntitySource;
  classification?: Classification;
  position?: Coordinate;
  heading?: number;
  speedKnots?: number;
  course?: number;
  milStd2525dSymbol?: string;
  metadata?: Record<string, string>;
  affiliations?: string[];
}

export interface UpdateEntityRequest {
  name?: string;
  description?: string;
  source?: EntitySource;
  classification?: Classification;
  position?: Coordinate;
  heading?: number;
  speedKnots?: number;
  course?: number;
  milStd2525dSymbol?: string;
  metadata?: Record<string, string>;
  affiliations?: string[];
}

export interface QueryEntitiesRequest extends PaginationRequest {
  boundingBox?: BoundingBox;
  entityTypes?: EntityType[];
  sources?: EntitySource[];
  classificationMax?: Classification;
  textQuery?: string;
}

export interface QueryEntitiesResponse {
  entities: Entity[];
  pagination: PaginationResponse;
}
