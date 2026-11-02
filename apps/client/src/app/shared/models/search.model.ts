import { ViewportBounds } from '../../features/map/viewport-bounds';
import { Classification, Entity, EntitySource, EntityType } from './entity.model';

/** A request to `GET /api/v1/search`. Every filter is optional. */
export interface SearchRequest {
  q: string;
  types?: EntityType[];
  sources?: EntitySource[];
  classifications?: Classification[];
  /** Restrict hits to this box; omitted searches the whole globe. */
  bounds?: ViewportBounds;
  page?: number;
  pageSize?: number;
}

/**
 * One document from the search index, as the search service returns it.
 * The position uses OpenSearch's `{ lat, lon }`, not the client's
 * `{ latitude, longitude }`; `hitToEntity` converts.
 */
export interface SearchHit {
  id: string;
  name: string;
  entityType: EntityType;
  description?: string;
  source?: EntitySource;
  classification?: Classification;
  position?: { lat: number; lon: number };
  affiliations?: string[];
  metadata?: Record<string, string>;
  createdAt?: string;
  updatedAt?: string;
  lastSeenAt?: string;
  identifiers?: string[];
  exercise?: boolean;
}

export interface SearchFacets {
  entityTypes: Record<string, number>;
  sources: Record<string, number>;
  classifications: Record<string, number>;
}

export interface SearchResponse {
  total: number;
  page: number;
  pageSize: number;
  hits: SearchHit[];
  facets?: SearchFacets;
}

/** The index document as the rest of the client's entity-shaped code expects it. */
export function hitToEntity(hit: SearchHit): Entity {
  const { position, ...rest } = hit;
  return {
    ...rest,
    source: hit.source ?? EntitySource.UNSPECIFIED,
    classification: hit.classification ?? Classification.UNCLASSIFIED,
    metadata: hit.metadata ?? {},
    affiliations: hit.affiliations ?? [],
    createdAt: hit.createdAt ?? '',
    updatedAt: hit.updatedAt ?? hit.createdAt ?? '',
    ...(position ? { position: { latitude: position.lat, longitude: position.lon } } : {}),
  };
}
