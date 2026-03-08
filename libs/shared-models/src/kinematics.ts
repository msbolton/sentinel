/** 3-axis geodetic velocity in m/s (North-East-Up frame) */
export interface GeodeticVelocity {
  north: number;
  east: number;
  up: number;
}

/** 3-axis geodetic acceleration in m/s² (North-East-Up frame) */
export interface GeodeticAcceleration {
  north: number;
  east: number;
  up: number;
}

/**
 * 3x3 position covariance matrix.
 * Units: meters². Symmetric matrix stored as upper triangle.
 * Axes: Pn=north, Pe=east, Pu=up.
 */
export interface PositionCovariance {
  pnPn: number;
  pnPe: number;
  pnPu: number;
  pePe: number;
  pePu: number;
  puPu: number;
}

/**
 * 3x3 position-velocity cross-covariance.
 * Units: m·(m/s). Full 3x3 matrix (not symmetric).
 */
export interface PositionVelocityCovariance {
  pnVn: number; pnVe: number; pnVu: number;
  peVn: number; peVe: number; peVu: number;
  puVn: number; puVe: number; puVu: number;
}

/**
 * 3x3 velocity covariance matrix.
 * Units: (m/s)². Symmetric matrix stored as upper triangle.
 */
export interface VelocityCovariance {
  vnVn: number;
  vnVe: number;
  vnVu: number;
  veVe: number;
  veVu: number;
  vuVu: number;
}

/** Full kinematic state vector */
export interface KinematicState {
  velocity?: GeodeticVelocity;
  acceleration?: GeodeticAcceleration;
  positionCovariance?: PositionCovariance;
  positionVelocityCovariance?: PositionVelocityCovariance;
  velocityCovariance?: VelocityCovariance;
}

/** ECEF-based orientation */
export interface Orientation {
  yaw: number;
  pitch: number;
  roll: number;
}

/** Measurement uncertainty for observations */
export interface MeasurementUncertainty {
  circularError?: number;
  semiMajor?: number;
  semiMinor?: number;
  orientation?: number;
  altitudeError?: number;
  confidence?: number;
}
