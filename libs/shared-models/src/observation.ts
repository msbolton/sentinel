import { Coordinate } from './common';
import { TrackProcessingState } from './common';
import {
  GeodeticVelocity,
  GeodeticAcceleration,
  MeasurementUncertainty,
  PositionCovariance,
  PositionVelocityCovariance,
  VelocityCovariance,
} from './kinematics';

export interface Observation {
  id: string;
  entityId: string;

  // Who observed
  sensorId?: string;
  feedId?: string;
  source?: string;

  // What was observed (position)
  position?: Coordinate;
  altitude?: number;

  // Kinematics at time of observation
  heading?: number;
  speedKnots?: number;
  course?: number;
  velocity?: GeodeticVelocity;
  acceleration?: GeodeticAcceleration;

  // Measurement uncertainty (UC2 DetectionPointRecordType)
  uncertainty?: MeasurementUncertainty;

  // Full covariance
  positionCovariance?: PositionCovariance;
  positionVelocityCovariance?: PositionVelocityCovariance;
  velocityCovariance?: VelocityCovariance;

  // Detection metadata
  detectionConfidence?: number;
  trackProcessingState?: TrackProcessingState;

  // Sensor-relative measurements (UC2 DetectionPointRecordType)
  azimuth?: number;
  elevation?: number;
  range?: number;
  azimuthError?: number;
  elevationError?: number;
  rangeError?: number;

  // Raw data
  rawData?: Record<string, unknown>;

  // Timestamp
  timestamp: string;
}
