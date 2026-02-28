import { Coordinate } from './common';

export interface TrackPoint {
  id: string;
  entityId: string;
  position: Coordinate;
  heading?: number;
  speedKnots?: number;
  course?: number;
  source?: string;
  timestamp: string;
}

export interface TrackSegment {
  segmentId: string;
  entityId: string;
  points: TrackPoint[];
  startTime: string;
  endTime: string;
}

export interface GetTrackHistoryRequest {
  entityId: string;
  startTime: string;
  endTime: string;
  maxPoints?: number;
  simplify?: boolean;
}

export interface GetTrackHistoryResponse {
  segments: TrackSegment[];
  totalPoints: number;
}

export interface TrackReplayRequest {
  entityId: string;
  startTime: string;
  endTime: string;
  speedMultiplier: number;
}

export interface TrackReplayEvent {
  entityId: string;
  point: TrackPoint;
  replayTimestamp: string;
}
