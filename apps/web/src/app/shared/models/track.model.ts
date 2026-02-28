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

export interface PlaybackState {
  playing: boolean;
  currentTime: Date;
  startTime: Date;
  endTime: Date;
  speed: number;
}
