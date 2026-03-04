import { Coordinate, PaginationRequest, PaginationResponse } from './common';
import { EntityType } from './entity';

// Canonical definition in @sentinel/proto-gen (proto/link.proto)
export enum LinkType {
  COMMUNICATION = 'COMMUNICATION',
  ASSOCIATION = 'ASSOCIATION',
  COLOCATION = 'COLOCATION',
  FINANCIAL = 'FINANCIAL',
  ORGANIZATIONAL = 'ORGANIZATIONAL',
  MOVEMENT_PATTERN = 'MOVEMENT_PATTERN',
  COMMAND_CONTROL = 'COMMAND_CONTROL',
  GEOGRAPHIC = 'GEOGRAPHIC',
  FAMILIAL = 'FAMILIAL',
  LOGISTIC = 'LOGISTIC',
  OPERATIONAL = 'OPERATIONAL',
  IDENTITY = 'IDENTITY',
}

export enum LinkEventType {
  CREATED = 'CREATED',
  UPDATED = 'UPDATED',
  DELETED = 'DELETED',
}

export interface Link {
  linkId: string;
  sourceEntityId: string;
  targetEntityId: string;
  linkType: LinkType;
  confidence: number;
  description?: string;
  evidence: string[];
  firstObserved?: string;
  lastObserved?: string;
  metadata: Record<string, string>;
}

export interface GraphNode {
  entityId: string;
  entityType: EntityType;
  name: string;
  position?: Coordinate;
}

export interface GraphEdge {
  link: Link;
}

export interface GetLinksRequest extends PaginationRequest {
  entityId: string;
  linkTypes?: LinkType[];
  minConfidence?: number;
  maxHops?: number;
}

export interface GetLinksResponse {
  links: Link[];
  pagination: PaginationResponse;
}

export interface GetGraphRequest {
  centerEntityId: string;
  maxDepth: number;
  linkTypes?: LinkType[];
  minConfidence?: number;
}

export interface GetGraphResponse {
  nodes: GraphNode[];
  edges: GraphEdge[];
}
