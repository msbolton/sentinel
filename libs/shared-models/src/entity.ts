import { BoundingBox, Classification, Coordinate, PaginationRequest, PaginationResponse } from './common';

// Canonical definitions in @sentinel/proto-gen (proto/entity.proto)
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
  SENSOR = 'SENSOR',
  SATELLITE = 'SATELLITE',
  DRONE = 'DRONE',
}

export enum EntitySource {
  HUMINT = 'HUMINT',
  SIGINT = 'SIGINT',
  GEOINT = 'GEOINT',
  OSINT = 'OSINT',
  MASINT = 'MASINT',
  CYBER = 'CYBER',
  MANUAL = 'MANUAL',
  AIS = 'AIS',
  ADS_B = 'ADS_B',
  LINK16 = 'LINK16',
  GPS = 'GPS',
  RADAR = 'RADAR',
  CELESTRAK = 'CELESTRAK',
  OPENSKY = 'OPENSKY',
  ADSB_LOL = 'ADSB_LOL',
}

export enum Affiliation {
  FRIENDLY = 'FRIENDLY',
  HOSTILE = 'HOSTILE',
  NEUTRAL = 'NEUTRAL',
  UNKNOWN = 'UNKNOWN',
  ASSUMED_FRIENDLY = 'ASSUMED_FRIENDLY',
  SUSPECT = 'SUSPECT',
  PENDING = 'PENDING',
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
