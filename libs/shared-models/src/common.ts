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
