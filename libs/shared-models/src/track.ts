import { Coordinate, TrackProcessingState } from './common';
import {
  GeodeticVelocity,
  GeodeticAcceleration,
  PositionCovariance,
  PositionVelocityCovariance,
  VelocityCovariance,
} from './kinematics';

export interface TrackPoint {
  id: string;
  entityId: string;
  position: Coordinate;
  heading?: number;
  speedKnots?: number;
  course?: number;
  source?: string;
  timestamp: string;
  altitude?: number;
  trackProcessingState?: TrackProcessingState;
  velocity?: GeodeticVelocity;
  acceleration?: GeodeticAcceleration;
  positionCovariance?: PositionCovariance;
  positionVelocityCovariance?: PositionVelocityCovariance;
  velocityCovariance?: VelocityCovariance;
  circularError?: number;
  altitudeError?: number;
  sensorId?: string;
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
