import { Classification, EntityType } from './entity.model';

export interface TrackPoint {
  timestamp: string;
  latitude: number;
  longitude: number;
  altitude?: number;
  heading?: number;
  speedKnots?: number;
  course?: number;
  source?: string;
  metadata?: Record<string, string>;
}

export interface TrackSegment {
  entityId: string;
  entityName: string;
  entityType: string;
  points: TrackPoint[];
  startTime: string;
  endTime: string;
}

export interface TrackQuery {
  entityId: string;
  startTime: string;
  endTime: string;
  maxPoints?: number;
}

export interface TimelineEvent {
  timestamp: string;
  count: number;
  entityTypes: Record<string, number>;
}

/** One sample of a replayed track: [epochMs, lon, lat, altitudeM, headingDeg]. */
export type ReplayPoint = [
  epochMs: number,
  lon: number,
  lat: number,
  altitudeM: number | null,
  headingDeg: number | null,
];

export interface ReplayEntity {
  entityId: string;
  entityType: EntityType;
  name: string;
  classification: Classification;
  points: ReplayPoint[];
}

/** The busiest tracks in a box over a window; `truncated` when the entity cap cut some off. */
export interface ReplayResponse {
  startTime: string;
  endTime: string;
  entities: ReplayEntity[];
  truncated: boolean;
}

export interface TimeWindow {
  start: Date;
  end: Date;
}
