import { LinkType } from '@sentinel/proto-gen';
import { Classification, Entity, EntitySource, EntityType } from './entity.model';
export { LinkType };

/** The link types an analyst can pick: every LinkType but the proto's UNSPECIFIED. */
export const LINK_TYPES: LinkType[] = Object.values(LinkType).filter((t) => t !== LinkType.UNSPECIFIED);

/** A link row as the link-analysis service returns it. */
export interface Link {
  id: string;
  sourceEntityId: string;
  targetEntityId: string;
  linkType: LinkType;
  confidence: number;
  description?: string;
  evidence: string[];
  firstObserved?: string;
  lastObserved?: string;
  metadata: Record<string, string>;
  createdAt: string;
}

/** `POST /api/v1/links`. Confidence defaults to 0.5 on the server. */
export interface CreateLinkRequest {
  sourceEntityId: string;
  targetEntityId: string;
  linkType: LinkType;
  confidence?: number;
  description?: string;
}

/** `GET /api/v1/links/graph`: a breadth-first walk out from one entity. */
export interface GraphRequest {
  centerId: string;
  /** 1 to 10 hops; the server defaults to 3. */
  maxDepth?: number;
  types?: LinkType[];
  minConfidence?: number;
}

/** An entity on the graph. The position is `{ lat, lon }` like the search index's. */
export interface GraphNode {
  entityId: string;
  entityType: EntityType;
  name: string;
  classification: Classification;
  position?: { lat: number; lon: number };
}

export interface GraphEdge {
  id: string;
  sourceEntityId: string;
  targetEntityId: string;
  linkType: LinkType;
  confidence: number;
}

export interface GraphResult {
  nodes: GraphNode[];
  edges: GraphEdge[];
}

/** The graph node as entity-shaped code (selection, the map) expects it. */
export function nodeToEntity(node: GraphNode): Entity {
  return {
    id: node.entityId,
    name: node.name,
    entityType: node.entityType,
    classification: node.classification,
    source: EntitySource.UNSPECIFIED,
    metadata: {},
    affiliations: [],
    createdAt: '',
    updatedAt: '',
    ...(node.position ? { position: { latitude: node.position.lat, longitude: node.position.lon } } : {}),
  };
}
